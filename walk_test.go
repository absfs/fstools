package fstools

import (
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/absfs/absfs"
	"github.com/absfs/memfs"
)

func setup() (absfs.FileSystem, error) {
	fs, err := memfs.NewFS()
	if err != nil {
		return nil, err
	}

	dir := []string{"/F", "/F/B", "/F/B/D", "/F/G", "/F/G/I"}
	for _, path := range dir {
		err := fs.Mkdir(path, 0755)
		if err != nil {
			return nil, err
		}
	}

	files := []string{"/F/B/A", "/F/B/D/C", "/F/B/D/E", "/F/G/I/H"}
	for _, path := range files {
		f, err := fs.Create(path)
		if err != nil {
			return nil, err
		}
		f.Close()
	}
	return fs, nil
}

func TestWalkWithOptions(t *testing.T) {
	fs, err := setup()
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{"/", "/F", "/F/B", "/F/B/A", "/F/B/D", "/F/B/D/C", "/F/B/D/E", "/F/G", "/F/G/I", "/F/G/I/H"}
	var values []string

	i := 0
	err = WalkWithOptions(fs, nil, "/", func(path string, info os.FileInfo, err error) error {
		t.Logf("%d: %s", i, path)
		values = append(values, path)
		i++
		if i > 10 {
			return errors.New("too many files")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(values) != len(expected) {
		t.Errorf("wrong length %d expected %d", len(values), len(expected))
	}

	for i, path := range values {
		if path != expected[i] {
			t.Errorf("wrong path order got %q expected %q", path, expected[i])
		}
	}
}

func TestPreOrderWalk(t *testing.T) {
	fs, err := setup()
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{"/", "/F", "/F/B", "/F/B/A", "/F/B/D", "/F/B/D/C", "/F/B/D/E", "/F/G", "/F/G/I", "/F/G/I/H"}
	var values []string

	i := 0
	err = PreOrder(fs, nil, "/", func(path string, info os.FileInfo, err error) error {
		t.Logf("%d: %s", i, path)
		values = append(values, path)
		i++
		if i > 10 {
			return errors.New("too many paths")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(values) != len(expected) {
		t.Errorf("wrong length %d expected %d", len(values), len(expected))
	}

	for i, path := range values {
		if path != expected[i] {
			t.Errorf("wrong path order got %q expected %q", path, expected[i])
		}
	}
}

func TestPostOrderWalk(t *testing.T) {
	fs, err := setup()
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"/F/B/A", "/F/B/D/C", "/F/B/D/E", "/F/B/D", "/F/B", "/F/G/I/H", "/F/G/I", "/F/G", "/F", "/"}

	var values []string

	i := 0
	err = PostOrder(fs, nil, "/", func(path string, info os.FileInfo, err error) error {
		t.Logf("%d: %s", i, path)
		values = append(values, path)
		i++
		if i > 10 {
			return errors.New("too many paths")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(values) != len(expected) {
		t.Errorf("wrong length %d expected %d", len(values), len(expected))
	}

	for i, path := range values {
		if path != expected[i] {
			t.Errorf("wrong path order got %q expected %q", path, expected[i])
		}
	}
}

func TestBreadthOrderWalk(t *testing.T) {
	fs, err := setup()
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"/", "/F", "/F/G", "/F/B", "/F/G/I", "/F/B/D", "/F/B/A", "/F/G/I/H", "/F/B/D/E", "/F/B/D/C"}
	var values []string

	i := 0
	err = BreadthOrder(fs, nil, "/", func(path string, info os.FileInfo, err error) error {
		t.Logf("%d: %s", i, path)
		values = append(values, path)
		i++
		if i > 10 {
			return errors.New("too many paths")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(values) != len(expected) {
		t.Errorf("wrong length %d expected %d", len(values), len(expected))
	}

	for i, path := range values {
		if path != expected[i] {
			t.Errorf("wrong path order got %q expected %q", path, expected[i])
		}
	}
}

func TestKeyOrderWalk(t *testing.T) {
	fs, err := setup()
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"/F/B/A", "/F/B/D/C", "/F/B/D/E", "/F/G/I/H"}
	var values []string

	i := 0
	err = KeyOrder(fs, nil, "/", func(path string, info os.FileInfo, err error) error {
		t.Logf("%d: %s", i, path)
		values = append(values, path)
		i++
		if i > 10 {
			return errors.New("too many paths")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(values) != len(expected) {
		t.Errorf("wrong length %d expected %d", len(values), len(expected))
	}

	for i, path := range values {
		if path != expected[i] {
			t.Errorf("wrong path order got %q expected %q", path, expected[i])
		}
	}
}

func TestFastWalkOption(t *testing.T) {
	fs, err := setup()
	if err != nil {
		t.Fatal(err)
	}

	// Expected paths (order may vary due to concurrent traversal)
	expected := map[string]bool{
		"/":          true,
		"/F":         true,
		"/F/B":       true,
		"/F/B/A":     true,
		"/F/B/D":     true,
		"/F/B/D/C":   true,
		"/F/B/D/E":   true,
		"/F/G":       true,
		"/F/G/I":     true,
		"/F/G/I/H":   true,
	}

	var mu sync.Mutex
	visited := make(map[string]bool)
	opts := &Options{Fast: true}

	err = WalkWithOptions(fs, opts, "/", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		mu.Lock()
		visited[path] = true
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Check all expected paths were visited
	for path := range expected {
		if !visited[path] {
			t.Errorf("Expected to visit %s but didn't", path)
		}
	}

	// Check no unexpected paths
	for path := range visited {
		if !expected[path] {
			t.Errorf("Visited unexpected path: %s", path)
		}
	}
}
