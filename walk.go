package fstools

import (
	"os"
	"path/filepath"
	"strings"

	log "github.com/Sirupsen/logrus"

	"github.com/absfs/absfs"
)

func walk(filer absfs.Filer, path string, fn filepath.WalkFunc) error {

	info, err := filer.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return nil
	}

	if info.Mode()&os.ModeSymlink == os.ModeSymlink {
		if info.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}

	var infos []os.FileInfo
	if info.IsDir() {

		f, err := filer.OpenFile(path, os.O_RDONLY, 0500)
		if err != nil {
			if strings.Contains(err.Error(), "file name too long") {
				panic(err)
			}

			log.Error(err)
			return nil
		}
		infos, err = f.Readdir(0)
		f.Close()
	}

	err = fn(path, info, err)
	if err != nil {
		if err == filepath.SkipDir {
			return nil
		}
		return err
	}

	if info == nil || !info.IsDir() {
		return nil
	}

	for _, info := range infos {
		if info.Name() == "." || info.Name() == ".." || strings.HasSuffix(info.Name(), ".c4") {
			continue
		}
		p := filepath.Join(path, info.Name())
		err := walk(filer, p, fn)
		if err != nil {
			return err
		}
	}
	return nil
}

func Walk(filer absfs.Filer, path string, fn filepath.WalkFunc) error {
	return walk(filer, path, fn)
}
