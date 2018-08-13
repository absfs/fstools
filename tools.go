package fstools

import (
	"os"

	"github.com/absfs/absfs"
)

func Exists(fs absfs.Filer, path string) bool {
	_, err := fs.Stat(path)
	if os.IsNotExist(err) {
		return false
	}
	return true

}
