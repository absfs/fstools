package fstools

import (
	"errors"
	"io"
	"io/fs"
	"os"
	slashpath "path"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/absfs/absfs"
)

// ParallelCopyOptions configures the behavior of parallel copy operations.
type ParallelCopyOptions struct {
	// NumWorkers sets the number of concurrent copy workers.
	// If 0 or negative, defaults to max(4, runtime.NumCPU()).
	NumWorkers int

	// BufferSize sets the size of the copy buffer per worker.
	// If 0, defaults to 32KB.
	BufferSize int

	// PreserveMode preserves file permissions.
	PreserveMode bool

	// PreserveTimes preserves modification times.
	PreserveTimes bool

	// Filter is called for each file/directory to determine if it should be copied.
	// Return false to skip the item. Must be safe for concurrent use.
	Filter func(path string, info os.FileInfo) bool

	// OnError is called when an error occurs during copy.
	// Return nil to continue, or an error to stop. Must be safe for concurrent use.
	OnError func(path string, err error) error

	// OnProgress is called after each file is copied with the number of bytes copied.
	// Must be safe for concurrent use.
	OnProgress func(path string, bytesWritten int64)
}

var defaultParallelCopyOptions = &ParallelCopyOptions{
	NumWorkers:    0, // will default to max(4, NumCPU)
	BufferSize:    32 * 1024,
	PreserveMode:  true,
	PreserveTimes: true,
}

// ParallelCopyStats contains statistics from a parallel copy operation.
type ParallelCopyStats struct {
	FilesCopied int64
	DirsCopied  int64
	BytesCopied int64
	Errors      int64
}

// ParallelCopy copies files and directories from src to dst using concurrent workers.
// It is significantly faster than Copy for large directory trees with many files.
func ParallelCopy(src, dst absfs.FileSystem, srcPath, dstPath string, opts *ParallelCopyOptions) (*ParallelCopyStats, error) {
	if opts == nil {
		opts = defaultParallelCopyOptions
	}

	numWorkers := opts.NumWorkers
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
		if numWorkers < 4 {
			numWorkers = 4
		}
	}

	bufSize := opts.BufferSize
	if bufSize <= 0 {
		bufSize = 32 * 1024
	}

	srcInfo, err := src.Stat(srcPath)
	if err != nil {
		if opts.OnError != nil {
			return nil, opts.OnError(srcPath, err)
		}
		return nil, err
	}

	// For single file, just copy it directly
	if !srcInfo.IsDir() {
		stats := &ParallelCopyStats{}
		buf := make([]byte, bufSize)
		n, err := parallelCopyFile(src, dst, srcPath, dstPath, srcInfo, opts, buf)
		if err != nil {
			return stats, err
		}
		stats.FilesCopied = 1
		stats.BytesCopied = n
		return stats, nil
	}

	// Create copier with worker pool
	c := &parallelCopier{
		src:        src,
		dst:        dst,
		opts:       opts,
		bufSize:    bufSize,
		numWorkers: numWorkers,
		workc:      make(chan copyWork, numWorkers*2),
		donec:      make(chan struct{}),
		errc:       make(chan error, 1),
	}

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go c.worker(&wg)
	}

	// Walk source tree and enqueue copy work
	// First create all directories, then copy files
	var dirs []copyWork
	var dirsMu sync.Mutex

	err = FastWalk(src, srcPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if opts.OnError != nil {
				return opts.OnError(path, err)
			}
			return err
		}

		// Calculate relative path
		relPath := ""
		if len(path) > len(srcPath) {
			relPath = path[len(srcPath):]
			if len(relPath) > 0 && relPath[0] == '/' {
				relPath = relPath[1:]
			}
		}
		targetPath := dstPath
		if relPath != "" {
			targetPath = slashpath.Join(dstPath, relPath)
		}

		// Get full info for filter and mode preservation
		info, err := src.Stat(path)
		if err != nil {
			if opts.OnError != nil {
				return opts.OnError(path, err)
			}
			return err
		}

		// Apply filter
		if opts.Filter != nil && !opts.Filter(path, info) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			// Collect directories to create them in order
			dirsMu.Lock()
			dirs = append(dirs, copyWork{
				srcPath: path,
				dstPath: targetPath,
				info:    info,
				isDir:   true,
			})
			dirsMu.Unlock()
			return nil
		}

		// Enqueue file copy work
		select {
		case c.workc <- copyWork{
			srcPath: path,
			dstPath: targetPath,
			info:    info,
			isDir:   false,
		}:
		case <-c.donec:
			return errors.New("copy cancelled")
		case err := <-c.errc:
			return err
		}

		return nil
	})

	// Close work channel to signal workers to finish
	close(c.workc)

	// Wait for workers
	wg.Wait()

	// Check for errors
	select {
	case err := <-c.errc:
		if err != nil {
			return &c.stats, err
		}
	default:
	}

	if err != nil {
		return &c.stats, err
	}

	// Create directories (in order discovered, which is breadth-first from FastWalk)
	for _, d := range dirs {
		mode := d.info.Mode()
		if !opts.PreserveMode {
			mode = 0755
		}
		if err := dst.MkdirAll(d.dstPath, mode); err != nil && !os.IsExist(err) {
			if opts.OnError != nil {
				if oerr := opts.OnError(d.dstPath, err); oerr != nil {
					return &c.stats, oerr
				}
			} else {
				return &c.stats, err
			}
		} else {
			atomic.AddInt64(&c.stats.DirsCopied, 1)
		}

		// Preserve times after all children are copied
		if opts.PreserveTimes {
			dst.Chtimes(d.dstPath, d.info.ModTime(), d.info.ModTime())
		}
	}

	return &c.stats, nil
}

type parallelCopier struct {
	src        absfs.FileSystem
	dst        absfs.FileSystem
	opts       *ParallelCopyOptions
	bufSize    int
	numWorkers int
	workc      chan copyWork
	donec      chan struct{}
	errc       chan error
	stats      ParallelCopyStats
}

type copyWork struct {
	srcPath string
	dstPath string
	info    os.FileInfo
	isDir   bool
}

func (c *parallelCopier) worker(wg *sync.WaitGroup) {
	defer wg.Done()

	buf := make([]byte, c.bufSize)

	for work := range c.workc {
		if work.isDir {
			continue // Directories handled separately
		}

		n, err := parallelCopyFile(c.src, c.dst, work.srcPath, work.dstPath, work.info, c.opts, buf)
		if err != nil {
			select {
			case c.errc <- err:
				close(c.donec)
			default:
			}
			return
		}

		atomic.AddInt64(&c.stats.FilesCopied, 1)
		atomic.AddInt64(&c.stats.BytesCopied, n)

		if c.opts.OnProgress != nil {
			c.opts.OnProgress(work.dstPath, n)
		}
	}
}

func parallelCopyFile(src, dst absfs.FileSystem, srcPath, dstPath string, srcInfo os.FileInfo, opts *ParallelCopyOptions, buf []byte) (int64, error) {
	// Ensure parent directory exists
	parentDir := slashpath.Dir(dstPath)
	if parentDir != "" && parentDir != "." && parentDir != "/" {
		if err := dst.MkdirAll(parentDir, 0755); err != nil && !os.IsExist(err) {
			return 0, err
		}
	}

	srcFile, err := src.OpenFile(srcPath, os.O_RDONLY, 0)
	if err != nil {
		if opts.OnError != nil {
			return 0, opts.OnError(srcPath, err)
		}
		return 0, err
	}
	defer srcFile.Close()

	mode := srcInfo.Mode()
	if !opts.PreserveMode {
		mode = 0644
	}

	dstFile, err := dst.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		if opts.OnError != nil {
			return 0, opts.OnError(dstPath, err)
		}
		return 0, err
	}
	defer dstFile.Close()

	n, err := io.CopyBuffer(dstFile, srcFile, buf)
	if err != nil {
		if opts.OnError != nil {
			return n, opts.OnError(dstPath, err)
		}
		return n, err
	}

	if opts.PreserveTimes {
		if err := dst.Chtimes(dstPath, srcInfo.ModTime(), srcInfo.ModTime()); err != nil {
			if opts.OnError != nil {
				opts.OnError(dstPath, err)
			}
		}
	}

	return n, nil
}

// Ensure imports are used
var (
	_ = fs.ModeDir
	_ = filepath.SkipDir
)
