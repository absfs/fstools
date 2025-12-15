package fstools_test

import (
	"os"
	"sync/atomic"
	"testing"

	"github.com/absfs/absfs"
	"github.com/absfs/fstools"
	"github.com/absfs/lockfs"
	"github.com/absfs/memfs"
)

// newThreadSafeMemFS creates a memfs wrapped with lockfs for thread-safe concurrent access.
// This is necessary because memfs is not thread-safe and parallel copy uses multiple workers.
func newThreadSafeMemFS(t *testing.T) absfs.FileSystem {
	t.Helper()
	mfs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	lfs, err := lockfs.NewFS(mfs)
	if err != nil {
		t.Fatal(err)
	}
	return lfs
}

func TestParallelCopy(t *testing.T) {
	// Create source filesystem with test data (doesn't need locking - read-only during copy)
	srcFS, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	// Create directory structure
	dirs := []string{"/dir1", "/dir1/subdir", "/dir2"}
	for _, dir := range dirs {
		if err := srcFS.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Create files with content
	files := map[string]string{
		"/file1.txt":             "content1",
		"/dir1/file2.txt":        "content2",
		"/dir1/subdir/file3.txt": "content3",
		"/dir2/file4.txt":        "content4",
	}
	for path, content := range files {
		f, err := srcFS.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(content))
		f.Close()
	}

	// Create destination filesystem with lockfs for thread-safe concurrent writes
	dstFS := newThreadSafeMemFS(t)

	// Perform parallel copy
	stats, err := fstools.ParallelCopy(srcFS, dstFS, "/", "/backup", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify stats
	if stats.FilesCopied != int64(len(files)) {
		t.Errorf("Expected %d files copied, got %d", len(files), stats.FilesCopied)
	}
	if stats.DirsCopied != int64(len(dirs)+1) { // +1 for root
		t.Errorf("Expected %d dirs copied, got %d", len(dirs)+1, stats.DirsCopied)
	}

	// Verify all files exist with correct content
	for srcPath, expectedContent := range files {
		dstPath := "/backup" + srcPath

		info, err := dstFS.Stat(dstPath)
		if err != nil {
			t.Errorf("File %s not found: %v", dstPath, err)
			continue
		}
		if info.IsDir() {
			t.Errorf("Expected file at %s, got directory", dstPath)
			continue
		}

		// Read and verify content
		f, err := dstFS.OpenFile(dstPath, os.O_RDONLY, 0)
		if err != nil {
			t.Errorf("Failed to open %s: %v", dstPath, err)
			continue
		}
		buf := make([]byte, 100)
		n, _ := f.Read(buf)
		f.Close()

		if string(buf[:n]) != expectedContent {
			t.Errorf("Content mismatch for %s: expected %q, got %q", dstPath, expectedContent, string(buf[:n]))
		}
	}

	// Verify all directories exist
	for _, dir := range dirs {
		dstPath := "/backup" + dir
		info, err := dstFS.Stat(dstPath)
		if err != nil {
			t.Errorf("Directory %s not found: %v", dstPath, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("Expected directory at %s, got file", dstPath)
		}
	}
}

func TestParallelCopyWithFilter(t *testing.T) {
	srcFS, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	// Create structure with files to filter
	if err := srcFS.Mkdir("/include", 0755); err != nil {
		t.Fatal(err)
	}
	if err := srcFS.Mkdir("/exclude", 0755); err != nil {
		t.Fatal(err)
	}

	f, _ := srcFS.Create("/include/file.txt")
	f.Write([]byte("included"))
	f.Close()

	f, _ = srcFS.Create("/exclude/file.txt")
	f.Write([]byte("excluded"))
	f.Close()

	dstFS := newThreadSafeMemFS(t)

	// Copy with filter that excludes /exclude directory
	opts := &fstools.ParallelCopyOptions{
		Filter: func(path string, info os.FileInfo) bool {
			return info.Name() != "exclude"
		},
	}

	_, err = fstools.ParallelCopy(srcFS, dstFS, "/", "/backup", opts)
	if err != nil {
		t.Fatal(err)
	}

	// Verify included file exists
	if _, err := dstFS.Stat("/backup/include/file.txt"); err != nil {
		t.Error("Expected /backup/include/file.txt to exist")
	}

	// Verify excluded directory doesn't exist
	if _, err := dstFS.Stat("/backup/exclude"); err == nil {
		t.Error("Expected /backup/exclude to be filtered out")
	}
}

func TestParallelCopyWithProgress(t *testing.T) {
	srcFS, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	// Create test files
	for i := 0; i < 5; i++ {
		f, _ := srcFS.Create(string(rune('a'+i)) + ".txt")
		f.Write([]byte("content"))
		f.Close()
	}

	dstFS := newThreadSafeMemFS(t)

	var progressCount int64
	opts := &fstools.ParallelCopyOptions{
		OnProgress: func(path string, bytesWritten int64) {
			atomic.AddInt64(&progressCount, 1)
		},
	}

	stats, err := fstools.ParallelCopy(srcFS, dstFS, "/", "/backup", opts)
	if err != nil {
		t.Fatal(err)
	}

	// Verify progress was called for each file
	if atomic.LoadInt64(&progressCount) != stats.FilesCopied {
		t.Errorf("Expected %d progress callbacks, got %d", stats.FilesCopied, progressCount)
	}
}

func TestParallelCopyWorkerCount(t *testing.T) {
	srcFS, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	// Create some files
	for i := 0; i < 10; i++ {
		f, _ := srcFS.Create(string(rune('a'+i)) + ".txt")
		f.Write([]byte("content"))
		f.Close()
	}

	dstFS := newThreadSafeMemFS(t)

	// Test with specific worker count
	opts := &fstools.ParallelCopyOptions{
		NumWorkers: 2,
	}

	stats, err := fstools.ParallelCopy(srcFS, dstFS, "/", "/backup", opts)
	if err != nil {
		t.Fatal(err)
	}

	if stats.FilesCopied != 10 {
		t.Errorf("Expected 10 files copied, got %d", stats.FilesCopied)
	}
}

func TestParallelCopySingleFile(t *testing.T) {
	srcFS, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	// Create single file
	f, _ := srcFS.Create("/single.txt")
	f.Write([]byte("single file content"))
	f.Close()

	// Single file copy doesn't use parallel workers but use lockfs for consistency
	dstFS := newThreadSafeMemFS(t)

	stats, err := fstools.ParallelCopy(srcFS, dstFS, "/single.txt", "/copied.txt", nil)
	if err != nil {
		t.Fatal(err)
	}

	if stats.FilesCopied != 1 {
		t.Errorf("Expected 1 file copied, got %d", stats.FilesCopied)
	}

	// Verify content
	f, err = dstFS.OpenFile("/copied.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 100)
	n, _ := f.Read(buf)
	f.Close()

	if string(buf[:n]) != "single file content" {
		t.Errorf("Content mismatch: expected %q, got %q", "single file content", string(buf[:n]))
	}
}
