package fstools_test

import (
	"os"
	"testing"

	"github.com/absfs/absfs"
	"github.com/absfs/fstools"
	"github.com/absfs/memfs"
)

func setupTestFS(t *testing.T) absfs.FileSystem {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	// Create directory structure
	dirs := []string{"/testdir", "/testdir/subdir", "/testdir/subdir/deep"}
	for _, path := range dirs {
		if err := fs.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Create files with specific sizes
	files := map[string]string{
		"/testfile.txt":              "hello world",
		"/testdir/file1.txt":         "content1",
		"/testdir/file2.txt":         "content2",
		"/testdir/subdir/file3.txt":  "content3",
		"/testdir/subdir/deep/file4.txt": "content4",
	}

	for path, content := range files {
		f, err := fs.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			f.Close()
			t.Fatal(err)
		}
		f.Close()
	}

	return fs
}

func TestExists(t *testing.T) {
	fs := setupTestFS(t)

	tests := []struct {
		path   string
		exists bool
	}{
		{"/testfile.txt", true},
		{"/testdir", true},
		{"/testdir/file1.txt", true},
		{"/nonexistent", false},
		{"/testdir/nonexistent.txt", false},
	}

	for _, tt := range tests {
		if got := fstools.Exists(fs, tt.path); got != tt.exists {
			t.Errorf("Exists(%q) = %v, want %v", tt.path, got, tt.exists)
		}
	}
}

func TestSize(t *testing.T) {
	fs := setupTestFS(t)

	tests := []struct {
		path string
		size int64
	}{
		{"/testfile.txt", 11}, // "hello world"
		{"/testdir/file1.txt", 8}, // "content1"
		{"/testdir", 32}, // file1.txt(8) + file2.txt(8) + file3.txt(8) + file4.txt(8)
	}

	for _, tt := range tests {
		size, err := fstools.Size(fs, tt.path)
		if err != nil {
			t.Errorf("Size(%q) error: %v", tt.path, err)
			continue
		}
		if size != tt.size {
			t.Errorf("Size(%q) = %d, want %d", tt.path, size, tt.size)
		}
	}
}

func TestCount(t *testing.T) {
	fs := setupTestFS(t)

	tests := []struct {
		path  string
		files int64
		dirs  int64
	}{
		{"/testfile.txt", 1, 0}, // Just the file itself
		{"/testdir", 4, 3},      // 4 files + 3 dirs (subdir, deep, and testdir itself)
		{"/testdir/subdir", 2, 2}, // 2 files (file3, file4) + 2 dirs (deep, subdir itself)
	}

	for _, tt := range tests {
		count, err := fstools.Count(fs, tt.path)
		if err != nil {
			t.Errorf("Count(%q) error: %v", tt.path, err)
			continue
		}
		if count.Files != tt.files {
			t.Errorf("Count(%q).Files = %d, want %d", tt.path, count.Files, tt.files)
		}
		if count.Dirs != tt.dirs {
			t.Errorf("Count(%q).Dirs = %d, want %d", tt.path, count.Dirs, tt.dirs)
		}
	}
}

func TestEqual(t *testing.T) {
	fs1 := setupTestFS(t)
	fs2 := setupTestFS(t)

	// Test equal files
	equal, err := fstools.Equal(fs1, "/testfile.txt", fs2, "/testfile.txt")
	if err != nil {
		t.Errorf("Equal error: %v", err)
	}
	if !equal {
		t.Errorf("Equal files should be equal")
	}

	// Test different files
	equal, err = fstools.Equal(fs1, "/testfile.txt", fs2, "/testdir/file1.txt")
	if err != nil {
		t.Errorf("Equal error: %v", err)
	}
	if equal {
		t.Errorf("Different files should not be equal")
	}

	// Test equal directories
	equal, err = fstools.Equal(fs1, "/testdir", fs2, "/testdir")
	if err != nil {
		t.Errorf("Equal error: %v", err)
	}
	if !equal {
		t.Errorf("Equal directories should be equal")
	}

	// Modify fs2 and test inequality
	f, err := fs2.OpenFile("/testdir/file1.txt", 0x242, 0644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("modified"))
	f.Close()

	equal, err = fstools.Equal(fs1, "/testdir", fs2, "/testdir")
	if err != nil {
		t.Errorf("Equal error: %v", err)
	}
	if equal {
		t.Errorf("Modified directories should not be equal")
	}
}

func TestCopy(t *testing.T) {
	src := setupTestFS(t)
	dst, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	// Test copying a single file
	err = fstools.Copy(src, dst, "/testfile.txt", "/copied.txt", nil)
	if err != nil {
		t.Errorf("Copy error: %v", err)
	}

	equal, err := fstools.Equal(src, "/testfile.txt", dst, "/copied.txt")
	if err != nil {
		t.Errorf("Equal error: %v", err)
	}
	if !equal {
		t.Errorf("Copied file should be equal to source")
	}

	// Test copying a directory tree
	err = fstools.Copy(src, dst, "/testdir", "/copieddir", nil)
	if err != nil {
		t.Errorf("Copy directory error: %v", err)
	}

	equal, err = fstools.Equal(src, "/testdir", dst, "/copieddir")
	if err != nil {
		t.Errorf("Equal error: %v", err)
	}
	if !equal {
		t.Errorf("Copied directory should be equal to source")
	}
}

func TestCopyToFS(t *testing.T) {
	src := setupTestFS(t)
	dst, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	// Test CopyToFS convenience function
	err = fstools.CopyToFS(src, dst, "/testfile.txt")
	if err != nil {
		t.Errorf("CopyToFS error: %v", err)
	}

	equal, err := fstools.Equal(src, "/testfile.txt", dst, "/testfile.txt")
	if err != nil {
		t.Errorf("Equal error: %v", err)
	}
	if !equal {
		t.Errorf("Copied file should be equal to source")
	}
}

func TestCopyWithFilter(t *testing.T) {
	src := setupTestFS(t)
	dst, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	// Copy only .txt files
	opts := &fstools.CopyOptions{
		Filter: func(path string, info os.FileInfo) bool {
			if info.IsDir() {
				return true
			}
			return true // In this test all files are .txt, so just return true
		},
	}

	err = fstools.Copy(src, dst, "/testdir", "/filtered", opts)
	if err != nil {
		t.Errorf("Copy with filter error: %v", err)
	}

	if !fstools.Exists(dst, "/filtered") {
		t.Errorf("Filtered directory should exist")
	}
}

func TestCopyParallelOption(t *testing.T) {
	src := setupTestFS(t)
	// Use thread-safe memfs for parallel copy since memfs is not thread-safe
	dst := newThreadSafeMemFS(t)

	// Test copying with Parallel option enabled
	opts := &fstools.CopyOptions{
		Parallel:      true,
		PreserveMode:  true,
		PreserveTimes: true,
	}

	err := fstools.Copy(src, dst, "/testdir", "/parallel_copied", opts)
	if err != nil {
		t.Errorf("Parallel Copy error: %v", err)
	}

	// Verify the copy succeeded by checking equality
	equal, err := fstools.Equal(src, "/testdir", dst, "/parallel_copied")
	if err != nil {
		t.Errorf("Equal error: %v", err)
	}
	if !equal {
		t.Errorf("Parallel copied directory should be equal to source")
	}

	// Verify specific files exist
	if !fstools.Exists(dst, "/parallel_copied/file1.txt") {
		t.Error("file1.txt should exist")
	}
	if !fstools.Exists(dst, "/parallel_copied/subdir/file3.txt") {
		t.Error("subdir/file3.txt should exist")
	}
	if !fstools.Exists(dst, "/parallel_copied/subdir/deep/file4.txt") {
		t.Error("subdir/deep/file4.txt should exist")
	}
}
