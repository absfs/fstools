package fstools

import (
	"io"
	"os"
	slashpath "path"

	"github.com/absfs/absfs"
)

// CopyOptions configures the behavior of copy operations.
type CopyOptions struct {
	// Parallel enables concurrent copying for thread-safe filesystems
	Parallel bool

	// PreserveMode preserves file permissions
	PreserveMode bool

	// PreserveTimes preserves modification times
	PreserveTimes bool

	// Filter is called for each file/directory to determine if it should be copied
	// Return false to skip the item
	Filter func(path string, info os.FileInfo) bool

	// OnError is called when an error occurs during copy
	// Return nil to continue, or an error to stop
	OnError func(path string, err error) error
}

var defaultCopyOptions = &CopyOptions{
	Parallel:      false,
	PreserveMode:  true,
	PreserveTimes: true,
	Filter:        nil,
	OnError:       nil,
}

// Copy copies files and directories from src filesystem to dst filesystem.
// It recursively copies the entire directory tree starting at srcPath to dstPath.
// If opts.Parallel is true, uses concurrent copying for better performance on
// thread-safe filesystems.
func Copy(src, dst absfs.FileSystem, srcPath, dstPath string, opts *CopyOptions) error {
	if opts == nil {
		opts = defaultCopyOptions
	}

	// If Parallel is enabled, delegate to ParallelCopy
	if opts.Parallel {
		parallelOpts := &ParallelCopyOptions{
			NumWorkers:    0, // Use default (CPU count)
			PreserveMode:  opts.PreserveMode,
			PreserveTimes: opts.PreserveTimes,
			Filter:        opts.Filter,
		}
		if opts.OnError != nil {
			parallelOpts.OnError = func(path string, err error) error {
				return opts.OnError(path, err)
			}
		}
		_, err := ParallelCopy(src, dst, srcPath, dstPath, parallelOpts)
		return err
	}

	srcInfo, err := src.Stat(srcPath)
	if err != nil {
		if opts.OnError != nil {
			return opts.OnError(srcPath, err)
		}
		return err
	}

	if opts.Filter != nil && !opts.Filter(srcPath, srcInfo) {
		return nil
	}

	if srcInfo.IsDir() {
		return copyDir(src, dst, srcPath, dstPath, srcInfo, opts)
	}
	return copyFile(src, dst, srcPath, dstPath, srcInfo, opts)
}

// CopyToFS is a convenience wrapper that copies from src to dst filesystem
// using the same path for both source and destination.
func CopyToFS(src, dst absfs.FileSystem, path string) error {
	return Copy(src, dst, path, path, nil)
}

func copyDir(src, dst absfs.FileSystem, srcPath, dstPath string, srcInfo os.FileInfo, opts *CopyOptions) error {
	mode := srcInfo.Mode()
	if !opts.PreserveMode {
		mode = 0755
	}

	if err := dst.Mkdir(dstPath, mode); err != nil && !os.IsExist(err) {
		if opts.OnError != nil {
			return opts.OnError(dstPath, err)
		}
		return err
	}

	f, err := src.OpenFile(srcPath, os.O_RDONLY, 0)
	if err != nil {
		if opts.OnError != nil {
			return opts.OnError(srcPath, err)
		}
		return err
	}
	defer f.Close()

	entries, err := f.Readdir(-1)
	if err != nil {
		if opts.OnError != nil {
			return opts.OnError(srcPath, err)
		}
		return err
	}

	for _, entry := range entries {
		if entry.Name() == "." || entry.Name() == ".." {
			continue
		}

		childSrc := slashpath.Join(srcPath, entry.Name())
		childDst := slashpath.Join(dstPath, entry.Name())

		if err := Copy(src, dst, childSrc, childDst, opts); err != nil {
			return err
		}
	}

	if opts.PreserveTimes {
		if err := dst.Chtimes(dstPath, srcInfo.ModTime(), srcInfo.ModTime()); err != nil {
			if opts.OnError != nil {
				return opts.OnError(dstPath, err)
			}
		}
	}

	return nil
}

func copyFile(src, dst absfs.FileSystem, srcPath, dstPath string, srcInfo os.FileInfo, opts *CopyOptions) error {
	srcFile, err := src.OpenFile(srcPath, os.O_RDONLY, 0)
	if err != nil {
		if opts.OnError != nil {
			return opts.OnError(srcPath, err)
		}
		return err
	}
	defer srcFile.Close()

	mode := srcInfo.Mode()
	if !opts.PreserveMode {
		mode = 0644
	}

	dstFile, err := dst.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		if opts.OnError != nil {
			return opts.OnError(dstPath, err)
		}
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		if opts.OnError != nil {
			return opts.OnError(dstPath, err)
		}
		return err
	}

	if opts.PreserveTimes {
		if err := dst.Chtimes(dstPath, srcInfo.ModTime(), srcInfo.ModTime()); err != nil {
			if opts.OnError != nil {
				return opts.OnError(dstPath, err)
			}
		}
	}

	return nil
}
