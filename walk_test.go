package fstools

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/absfs/absfs"
	"github.com/absfs/memfs"
)

func setup(t *testing.T) (absfs.FileSystem, error) {
	fs, err := memfs.NewFS()
	if err != nil {
		return nil, err
	}

	dirs := []string{"/dir20", "/dir20/dir21", "/dir10", "/dir10/dir11"}
	for _, path := range dirs {
		err = fs.Mkdir(path, 0700)
		if err != nil {
			return nil, err
		}
	}

	files := []string{"/dir20/foo.bar", "/dir20/dir21/baz.bat"}

	for i, path := range files {
		f, err := fs.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			return nil, err
		}

		_, err = f.Write([]byte(fmt.Sprintf("%d: this is a test\n", i)))
		if err != nil {
			return nil, err
		}
		err = f.Close()
		if err != nil {
			return nil, err
		}
	}

	return fs, nil
}

func TestWalkWithOptions(t *testing.T) {
	fs, err := setup(t)
	if err != nil {
		t.Fatal(err)
	}

	i := 0
	var list []string
	err = WalkWithOptions(fs, nil, "/", func(path string, info os.FileInfo, err error) error {
		list = append(list, path)
		i++
		if i > 10 {
			return errors.New("too many files")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []string{"/", "/dir10", "/dir10/dir11", "/dir20", "/dir20/dir21",
		"/dir20/dir21/baz.bat", "/dir20/foo.bar"}
	for i, test := range tests {
		if list[i] != test {
			t.Fatalf("results out of order %q != %q", list[i], test)
		}
	}

	if len(list) != len(tests) {
		t.Fatalf("output size different %d expected %d", len(list), len(tests))
	}
}
