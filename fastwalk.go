package fstools

import (
	"errors"
	"io/fs"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/absfs/absfs"
)

// FastWalkFunc is the callback function type for FastWalk.
// It receives fs.DirEntry instead of os.FileInfo for better performance
// (avoids stat calls when only type information is needed).
type FastWalkFunc func(path string, d fs.DirEntry, err error) error

// TraverseLink is a sentinel error that can be returned from FastWalkFunc
// to indicate that a symlink should be traversed as a directory.
// The caller is responsible for cycle detection.
var TraverseLink = errors.New("traverse symlink, assuming target is a directory")

// FastWalkConfig holds configuration options for FastWalk.
type FastWalkConfig struct {
	// NumWorkers sets the number of concurrent workers.
	// If 0 or negative, defaults to max(4, runtime.NumCPU()).
	NumWorkers int

	// FollowSymlinks determines whether symlinks should be followed.
	// When true, symlinks to directories will be traversed.
	// The caller must handle potential cycles.
	FollowSymlinks bool
}

// FastWalk walks the file tree rooted at root concurrently, calling fn for each file.
// It is significantly faster than Walk because:
//   - Runs multiple goroutines concurrently to traverse directories in parallel
//   - Avoids unnecessary stat calls by using DirEntry type information
//
// When the underlying filesystem is osfs, the platform-optimized ReadDir
// implementation is used automatically (e.g., getdents with d_type on Linux).
//
// The fn must be safe for concurrent use as it may be called from multiple goroutines.
// If fn returns filepath.SkipDir, the directory is skipped.
// If fn returns TraverseLink for a symlink, it will be traversed as a directory.
func FastWalk(filer absfs.Filer, root string, fn FastWalkFunc) error {
	return FastWalkWithConfig(filer, root, nil, fn)
}

// FastWalkWithConfig is like FastWalk but accepts a configuration.
func FastWalkWithConfig(filer absfs.Filer, root string, config *FastWalkConfig, fn FastWalkFunc) error {
	return genericFastWalk(filer, root, config, fn)
}

// genericFastWalk implements concurrent directory walking for any absfs.Filer.
func genericFastWalk(filer absfs.Filer, root string, config *FastWalkConfig, fn FastWalkFunc) error {
	numWorkers := 4
	if config != nil && config.NumWorkers > 0 {
		numWorkers = config.NumWorkers
	} else if n := runtime.NumCPU(); n > numWorkers {
		numWorkers = n
	}

	// Wait for all workers to finish
	var wg sync.WaitGroup
	defer wg.Wait()

	w := &genericWalker{
		filer:    filer,
		fn:       fn,
		enqueuec: make(chan fastWalkItem, numWorkers),
		workc:    make(chan fastWalkItem, numWorkers),
		donec:    make(chan struct{}),
		resc:     make(chan error, numWorkers),
	}
	defer close(w.donec)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go w.doWork(&wg)
	}

	todo := []fastWalkItem{{dir: root}}
	out := 0

	for {
		workc := w.workc
		var workItem fastWalkItem
		if len(todo) == 0 {
			workc = nil
		} else {
			workItem = todo[len(todo)-1]
		}

		select {
		case workc <- workItem:
			todo = todo[:len(todo)-1]
			out++
		case it := <-w.enqueuec:
			todo = append(todo, it)
		case err := <-w.resc:
			out--
			if err != nil {
				return err
			}
			if out == 0 && len(todo) == 0 {
				// Safe to quit, but check for race with enqueue
				select {
				case it := <-w.enqueuec:
					todo = append(todo, it)
				default:
					return nil
				}
			}
		}
	}
}

type genericWalker struct {
	filer    absfs.Filer
	fn       FastWalkFunc
	donec    chan struct{}
	workc    chan fastWalkItem
	enqueuec chan fastWalkItem
	resc     chan error
}

type fastWalkItem struct {
	dir          string
	callbackDone bool
}

func (w *genericWalker) enqueue(it fastWalkItem) {
	select {
	case w.enqueuec <- it:
	case <-w.donec:
	}
}

func (w *genericWalker) doWork(wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-w.donec:
			return
		case it := <-w.workc:
			select {
			case <-w.donec:
				return
			case w.resc <- w.walk(it.dir, !it.callbackDone):
			}
		}
	}
}

func (w *genericWalker) walk(root string, runUserCallback bool) error {
	// Check if root is a file or directory
	info, err := w.filer.Stat(root)
	if err != nil {
		return w.fn(root, nil, err)
	}

	if runUserCallback {
		err := w.fn(root, &fakeDirEntry{name: filepath.Base(root), isDir: info.IsDir(), info: info}, nil)
		if err == filepath.SkipDir {
			return nil
		}
		if err != nil {
			return err
		}
	}

	// If it's a file, we're done
	if !info.IsDir() {
		return nil
	}

	return w.readDir(root)
}

func (w *genericWalker) readDir(dirName string) error {
	// Use Filer.ReadDir for optimized directory reading
	entries, err := w.filer.ReadDir(dirName)
	if err != nil {
		// Handle permission errors gracefully
		if isPermissionError(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		// Skip . and ..
		if entry.Name() == "." || entry.Name() == ".." {
			continue
		}

		// Join path correctly (avoid "//file" when dirName is "/")
		var joined string
		if dirName == "/" {
			joined = "/" + entry.Name()
		} else {
			joined = dirName + "/" + entry.Name()
		}

		if entry.IsDir() {
			// Call callback for directory first
			err := w.fn(joined, entry, nil)
			if err == filepath.SkipDir {
				continue
			}
			if err != nil {
				return err
			}
			// Enqueue directory for traversal
			w.enqueue(fastWalkItem{dir: joined, callbackDone: true})
			continue
		}

		err := w.fn(joined, entry, nil)
		if entry.Type()&fs.ModeSymlink != 0 {
			if err == TraverseLink {
				w.enqueue(fastWalkItem{dir: joined, callbackDone: true})
				continue
			}
			if err == filepath.SkipDir {
				continue
			}
		}
		if err != nil {
			return err
		}
	}

	return nil
}

// isPermissionError checks if an error is a permission denied error
func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	return err.Error() == "permission denied" || err.Error() == "access is denied"
}

// fakeDirEntry is a minimal implementation of fs.DirEntry for directory callbacks
type fakeDirEntry struct {
	name  string
	isDir bool
	info  fs.FileInfo // Optional: full FileInfo if available
}

func (f *fakeDirEntry) Name() string      { return f.name }
func (f *fakeDirEntry) IsDir() bool       { return f.isDir }
func (f *fakeDirEntry) Type() fs.FileMode {
	if f.isDir {
		return fs.ModeDir
	}
	return 0
}
func (f *fakeDirEntry) Info() (fs.FileInfo, error) {
	if f.info != nil {
		return f.info, nil
	}
	return nil, errors.New("FileInfo not available")
}
