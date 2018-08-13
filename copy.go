package fstools

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"

	"github.com/xtgo/set"

	"github.com/absfs/absfs"
)

type CopyFunc func(io.Writer, io.Reader) (int64, error)
type CopierFunc func(io.Writer, io.Reader) (int64, error)

func (f CopyFunc) Copy(w io.Writer, r io.Reader) (int64, error) {
	return f(w, r)
}

func (f CopierFunc) Copier(info os.FileInfo) CopyHandler {
	return CopyFunc(f)
}

type CopyHandler interface {
	Copy(io.Writer, io.Reader) (int64, error)
}

type Copier interface {
	Copier(os.FileInfo) CopyHandler
}

func Copy(target, source absfs.SymlinkFileSystem, c Copier) error {
	var targetpaths, sourcepaths sort.StringSlice
	tmap := make(map[string]os.FileInfo)
	f, err := os.Create("/tmp/archive_copier.txt")
	if err != nil {
		return err
	}
	defer f.Close()

	// Get list of existing files
	err = Walk(target, string(filepath.Separator), func(path string, info os.FileInfo, err error) error {
		_, err = f.WriteString(path + "\n")
		if err != nil {
			return err
		}

		if info.Mode()&os.ModeSymlink != 0 {
			log.Warnf("Symlink: %q", path)
			return filepath.SkipDir
		}

		tmap[path] = info
		return nil
	})
	if err != nil {
		return err
	}

	timemap := make(map[string][]time.Time)
	err = Walk(source, string(filepath.Separator), func(path string, info os.FileInfo, err error) (out error) {
		if err != nil {
			log.Warn(err)
			return filepath.SkipDir
		}
		sourcepaths = append(sourcepaths, path)
		if info.Mode()&os.ModeSymlink != 0 {
			log.Error("Symlink: %q", path)
			return filepath.SkipDir
		}

		tinfo := tmap[path]
		if tinfo != nil {
			if info.Size() == tinfo.Size() && info.ModTime().Equal(tinfo.ModTime()) {
				return nil
			}
		}
		if info.IsDir() {
			err = target.Mkdir(path, info.Mode())
			if err != nil && !os.IsExist(err) {
				// if strings.Contains(err.Error(), "file name too long") {
				// 	return filepath.SkipDir
				// }

				return err
			}
			timemap[path] = []time.Time{time.Now(), info.ModTime()}
			return nil
		}

		src, err := source.OpenFile(path, os.O_RDONLY, 0400)
		if err != nil {
			if strings.Contains(err.Error(), "file name too long") {
				panic(err)
			}
			return err
		}

		dst, err := target.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			if strings.Contains(err.Error(), "file name too long") {
				panic(err)
			}
			src.Close()
			return err
		}

		_, err = c.Copier(info).Copy(dst, src)
		if err != nil {
			dst.Close()
			src.Close()
			return err
		}

		err = dst.Close()
		if err != nil {
			return err
		}

		err = src.Close()
		if err != nil {
			return err
		}
		err = target.Chtimes(path, time.Now(), info.ModTime())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Remove files from target that are not in source
	targetpaths = make(sort.StringSlice, 0, len(tmap))
	for k := range tmap {
		targetpaths = append(targetpaths, k)
	}
	removepaths := append(targetpaths, sourcepaths...)
	n := set.Diff(removepaths, len(targetpaths))
	removepaths = removepaths[:n]

	for _, path := range removepaths {
		err := target.RemoveAll(path) // RemoveAll(target, path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	for path, times := range timemap {
		err = target.Chtimes(path, times[0], times[1])
		if err != nil {
			return err
		}
	}

	return nil
}

// func Copier(target, source absfs.Filer, fn func(w io.Writer, r io.Reader) (int64, error)) error {
// 	var targetpaths, sourcepaths sort.StringSlice
// 	tmap := make(map[string]os.FileInfo)

// 	// Get list of existing files
// 	err := Walk(target, string(filepath.Separator), func(path string, info os.FileInfo, err error) error {
// 		tmap[path] = info
// 		return nil
// 	})
// 	if err != nil {
// 		return err
// 	}

// 	err = Walk(source, string(filepath.Separator), func(path string, info os.FileInfo, err error) (out error) {
// 		sourcepaths = append(sourcepaths, path)
// 		if info.Mode()&os.ModeSymlink != 0 {
// 			log.Warnf("Symlink: %q", path)
// 			return filepath.SkipDir
// 		}
// 		tinfo := tmap[path]
// 		if tinfo != nil {
// 			if info.Size() == tinfo.Size() && info.ModTime().Equal(tinfo.ModTime()) {
// 				return nil
// 			}
// 		}

// 		if info.IsDir() {
// 			err = target.Mkdir(path, info.Mode())
// 			if os.IsExist(err) {
// 				return nil
// 			}
// 			return err
// 		}

// 		src, err := source.OpenFile(path, os.O_RDONLY, 0400)
// 		if err != nil {
// 			return err
// 		}
// 		defer func() {
// 			out = src.Close()
// 		}()

// 		dst, err := target.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
// 		if err != nil {
// 			return err
// 		}
// 		defer func() {
// 			out = dst.Close()
// 		}()

// 		_, err = fn(dst, src)
// 		if err != nil {
// 			return err
// 		}
// 		// TODO: Set dst ModTime to match source
// 		return nil
// 	})
// 	if err != nil {
// 		return err
// 	}
// 	// Remove files from target that are not in source
// 	targetpaths = make(sort.StringSlice, 0, len(tmap))
// 	for k := range tmap {
// 		targetpaths = append(targetpaths, k)
// 	}
// 	removepaths := append(targetpaths, sourcepaths...)
// 	n := set.Diff(removepaths, len(targetpaths))
// 	removepaths = removepaths[:n]

// 	for _, path := range removepaths {
// 		err = RemoveAll(target, path)
// 		if err != nil && !os.IsNotExist(err) {
// 			log.Error(err)
// 			continue
// 		}
// 	}

// 	return nil
// }

// func RemoveAll(filer absfs.Filer, path string) (err error) {

// 	// open the file
// 	var f fs.File
// 	f, err = filer.OpenFile(path, os.O_RDONLY, 0700)
// 	if os.IsNotExist(err) {
// 		return nil
// 	}
// 	if err != nil {
// 		if strings.Contains(err.Error(), "file name too long") {
// 			panic(err)
// 		}
// 		return err
// 	}

// 	// get FileInfo
// 	info, err := f.Stat()
// 	if err != nil {
// 		f.Close()
// 		return err
// 	}

// 	// if it's not a directory remove it and return
// 	if !info.IsDir() {
// 		f.Close()
// 		err = filer.Remove(path)
// 		if err != nil {
// 			log.Warning(err)

// 			return err
// 		}
// 		return nil
// 	}

// 	// get and loop through each directory entry calling remove all recursively.
// 	infos, err := f.Readdir(0)
// 	f.Close()
// 	if err != nil {

// 		log.Warning(err)
// 		return err
// 	}
// 	for _, info := range infos {
// 		path = filepath.Join(path, info.Name())
// 		err = RemoveAll(filer, path)
// 		if err != nil {
// 			log.Warning(err)
// 			return err
// 		}
// 	}

// 	return nil
// }

// func Copy(target, source absfs.Filer) error {
// 	return Copier(target, source, io.Copy)
// }
