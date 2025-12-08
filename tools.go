package fstools

import (
	"bytes"
	"io"
	"os"
	slashpath "path"

	"github.com/absfs/absfs"
)

// Exists checks if a path exists in the filesystem.
func Exists(fs absfs.Filer, path string) bool {
	_, err := fs.Stat(path)
	if os.IsNotExist(err) {
		return false
	}
	return true
}

// Size calculates the total size in bytes of a file or directory tree.
// For directories, it recursively sums the size of all contained files.
func Size(fs absfs.Filer, path string) (int64, error) {
	info, err := fs.Stat(path)
	if err != nil {
		return 0, err
	}

	if !info.IsDir() {
		return info.Size(), nil
	}

	var total int64
	err = Walk(fs, path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// CountResult contains the results of a Count operation.
type CountResult struct {
	Files int64
	Dirs  int64
}

// Total returns the total number of files and directories.
func (c CountResult) Total() int64 {
	return c.Files + c.Dirs
}

// Count counts the number of files and directories under a path.
// The path itself is included in the count.
func Count(fs absfs.Filer, path string) (CountResult, error) {
	var result CountResult
	err := Walk(fs, path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			result.Dirs++
		} else {
			result.Files++
		}
		return nil
	})
	return result, err
}

// Equal compares two files or directory trees for identical content.
// For files, it compares the content byte-by-byte.
// For directories, it recursively compares all files and subdirectories.
func Equal(fs1 absfs.FileSystem, path1 string, fs2 absfs.FileSystem, path2 string) (bool, error) {
	info1, err := fs1.Stat(path1)
	if err != nil {
		return false, err
	}

	info2, err := fs2.Stat(path2)
	if err != nil {
		return false, err
	}

	if info1.IsDir() != info2.IsDir() {
		return false, nil
	}

	if !info1.IsDir() {
		return equalFiles(fs1, path1, fs2, path2)
	}

	return equalDirs(fs1, path1, fs2, path2)
}

func equalFiles(fs1 absfs.FileSystem, path1 string, fs2 absfs.FileSystem, path2 string) (bool, error) {
	f1, err := fs1.OpenFile(path1, os.O_RDONLY, 0)
	if err != nil {
		return false, err
	}
	defer f1.Close()

	f2, err := fs2.OpenFile(path2, os.O_RDONLY, 0)
	if err != nil {
		return false, err
	}
	defer f2.Close()

	const bufSize = 32 * 1024
	buf1 := make([]byte, bufSize)
	buf2 := make([]byte, bufSize)

	for {
		n1, err1 := f1.Read(buf1)
		n2, err2 := f2.Read(buf2)

		if n1 != n2 || !bytes.Equal(buf1[:n1], buf2[:n2]) {
			return false, nil
		}

		if err1 == io.EOF && err2 == io.EOF {
			return true, nil
		}

		if err1 != nil && err1 != io.EOF {
			return false, err1
		}
		if err2 != nil && err2 != io.EOF {
			return false, err2
		}

		if (err1 == io.EOF) != (err2 == io.EOF) {
			return false, nil
		}
	}
}

func equalDirs(fs1 absfs.FileSystem, path1 string, fs2 absfs.FileSystem, path2 string) (bool, error) {
	f1, err := fs1.OpenFile(path1, os.O_RDONLY, 0)
	if err != nil {
		return false, err
	}
	defer f1.Close()

	entries1, err := f1.Readdir(-1)
	if err != nil {
		return false, err
	}

	f2, err := fs2.OpenFile(path2, os.O_RDONLY, 0)
	if err != nil {
		return false, err
	}
	defer f2.Close()

	entries2, err := f2.Readdir(-1)
	if err != nil {
		return false, err
	}

	if len(entries1) != len(entries2) {
		return false, nil
	}

	nameMap := make(map[string]os.FileInfo)
	for _, e := range entries2 {
		if e.Name() == "." || e.Name() == ".." {
			continue
		}
		nameMap[e.Name()] = e
	}

	for _, e1 := range entries1 {
		if e1.Name() == "." || e1.Name() == ".." {
			continue
		}

		e2, found := nameMap[e1.Name()]
		if !found {
			return false, nil
		}

		if e1.IsDir() != e2.IsDir() {
			return false, nil
		}

		childPath1 := slashpath.Join(path1, e1.Name())
		childPath2 := slashpath.Join(path2, e2.Name())

		equal, err := Equal(fs1, childPath1, fs2, childPath2)
		if err != nil || !equal {
			return equal, err
		}
	}

	return true, nil
}
