package fstools_test

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/absfs/fstools"
	"github.com/absfs/memfs"
)

func TestFastWalk(t *testing.T) {
	// Create test filesystem
	mfs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	// Create test structure
	dirs := []string{
		"/dir1",
		"/dir1/subdir1",
		"/dir1/subdir2",
		"/dir2",
	}
	files := []string{
		"/file1.txt",
		"/dir1/file2.txt",
		"/dir1/subdir1/file3.txt",
		"/dir1/subdir2/file4.txt",
		"/dir2/file5.txt",
	}

	for _, dir := range dirs {
		if err := mfs.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	for _, file := range files {
		f, err := mfs.Create(file)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte("test"))
		f.Close()
	}

	// Walk and collect all paths
	var walked []string
	var mu sync.Mutex
	err = fstools.FastWalk(mfs, "/", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != "/" {
			mu.Lock()
			walked = append(walked, path)
			mu.Unlock()
		}
		return nil
	})

	if err != nil {
		t.Fatal(err)
	}

	// Sort for consistent comparison
	sort.Strings(walked)

	// Verify we found all expected items
	expected := append([]string{}, dirs...)
	expected = append(expected, files...)
	sort.Strings(expected)

	if len(walked) != len(expected) {
		t.Errorf("Expected %d items, got %d", len(expected), len(walked))
		t.Logf("Expected: %v", expected)
		t.Logf("Got: %v", walked)
	}

	// Check each expected item exists
	walkedMap := make(map[string]bool)
	for _, p := range walked {
		walkedMap[p] = true
	}

	for _, exp := range expected {
		if !walkedMap[exp] {
			t.Errorf("Expected to find %s but didn't", exp)
		}
	}
}

func TestFastWalkSkipDir(t *testing.T) {
	mfs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	// Create test structure
	if err := mfs.MkdirAll("/skip/nested", 0755); err != nil {
		t.Fatal(err)
	}
	if err := mfs.Mkdir("/keep", 0755); err != nil {
		t.Fatal(err)
	}

	f, _ := mfs.Create("/skip/file.txt")
	f.Write([]byte("test"))
	f.Close()

	f, _ = mfs.Create("/keep/file.txt")
	f.Write([]byte("test"))
	f.Close()

	// Walk and skip "skip" directory
	var walked []string
	var mu sync.Mutex
	err = fstools.FastWalk(mfs, "/", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != "/" {
			mu.Lock()
			walked = append(walked, path)
			mu.Unlock()
		}
		if d.IsDir() && d.Name() == "skip" {
			return filepath.SkipDir
		}
		return nil
	})

	if err != nil {
		t.Fatal(err)
	}

	// Verify "skip" directory contents were skipped
	for _, p := range walked {
		if strings.Contains(p, "skip/") {
			t.Errorf("Should have skipped contents of skip: %s", p)
		}
	}

	// Verify "keep" directory was walked
	foundKeep := false
	for _, p := range walked {
		if strings.Contains(p, "keep") {
			foundKeep = true
			break
		}
	}
	if !foundKeep {
		t.Error("Should have found 'keep' directory")
	}
}

func TestFastWalkConfig(t *testing.T) {
	mfs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	// Create a simple structure
	if err := mfs.Mkdir("/dir", 0755); err != nil {
		t.Fatal(err)
	}
	f, _ := mfs.Create("/file.txt")
	f.Write([]byte("test"))
	f.Close()

	// Test with custom worker count
	config := &fstools.FastWalkConfig{
		NumWorkers: 2,
	}

	count := 0
	err = fstools.FastWalkWithConfig(mfs, "/", config, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		count++
		return nil
	})

	if err != nil {
		t.Fatal(err)
	}

	if count == 0 {
		t.Error("Should have walked at least some files")
	}
}

func TestFastWalkEmptyDir(t *testing.T) {
	mfs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	// Create empty directory
	if err := mfs.Mkdir("/empty", 0755); err != nil {
		t.Fatal(err)
	}

	count := 0
	err = fstools.FastWalk(mfs, "/empty", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		count++
		return nil
	})

	if err != nil {
		t.Fatal(err)
	}

	// Should have walked just the root directory
	if count != 1 {
		t.Errorf("Expected 1 item (the directory itself), got %d", count)
	}
}

func TestFastWalkSingleFile(t *testing.T) {
	mfs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	// Create single file
	f, _ := mfs.Create("/single.txt")
	f.Write([]byte("test"))
	f.Close()

	var walked []string
	err = fstools.FastWalk(mfs, "/single.txt", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		walked = append(walked, path)
		return nil
	})

	if err != nil {
		t.Fatal(err)
	}

	if len(walked) != 1 || walked[0] != "/single.txt" {
		t.Errorf("Expected [/single.txt], got %v", walked)
	}
}
