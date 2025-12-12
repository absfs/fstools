package fstools_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/absfs/fstools"
	"github.com/absfs/memfs"
	"github.com/absfs/osfs"
)

// setupMemFS creates an in-memory filesystem with the specified structure.
func setupMemFS(b *testing.B, numDirs, filesPerDir int) *memfs.FileSystem {
	b.Helper()

	mfs, err := memfs.NewFS()
	if err != nil {
		b.Fatal(err)
	}

	for i := 0; i < numDirs; i++ {
		dirPath := fmt.Sprintf("/dir%d", i)
		if err := mfs.Mkdir(dirPath, 0755); err != nil {
			b.Fatal(err)
		}

		for j := 0; j < filesPerDir; j++ {
			filePath := fmt.Sprintf("%s/file%d.txt", dirPath, j)
			f, err := mfs.Create(filePath)
			if err != nil {
				b.Fatal(err)
			}
			f.Write([]byte("test content"))
			f.Close()
		}
	}

	return mfs
}

// setupOSFS creates a temp directory with the specified structure.
func setupOSFS(b *testing.B, numDirs, filesPerDir int) (*osfs.FileSystem, string, func()) {
	b.Helper()

	tmpDir, err := os.MkdirTemp("", "fstools-bench-*")
	if err != nil {
		b.Fatal(err)
	}

	for i := 0; i < numDirs; i++ {
		dirPath := filepath.Join(tmpDir, fmt.Sprintf("dir%d", i))
		if err := os.Mkdir(dirPath, 0755); err != nil {
			b.Fatal(err)
		}

		for j := 0; j < filesPerDir; j++ {
			filePath := filepath.Join(dirPath, fmt.Sprintf("file%d.txt", j))
			if err := os.WriteFile(filePath, []byte("test content"), 0644); err != nil {
				b.Fatal(err)
			}
		}
	}

	ofs, err := osfs.NewFS()
	if err != nil {
		os.RemoveAll(tmpDir)
		b.Fatal(err)
	}

	// Convert to Unix-style path
	unixPath := osfs.FromNative(tmpDir)

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return ofs, unixPath, cleanup
}

// BenchmarkWalk benchmarks the standard Walk function.
func BenchmarkWalk(b *testing.B) {
	sizes := []struct {
		name         string
		numDirs      int
		filesPerDir  int
	}{
		{"Small_10x10", 10, 10},
		{"Medium_50x20", 50, 20},
		{"Large_100x50", 100, 50},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			b.Run("MemFS_Walk", func(b *testing.B) {
				mfs := setupMemFS(b, size.numDirs, size.filesPerDir)
				b.ReportAllocs()
				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					count := 0
					err := fstools.Walk(mfs, "/", func(path string, info os.FileInfo, err error) error {
						if err != nil {
							return err
						}
						count++
						return nil
					})
					if err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run("MemFS_FastWalk", func(b *testing.B) {
				mfs := setupMemFS(b, size.numDirs, size.filesPerDir)
				b.ReportAllocs()
				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					count := 0
					err := fstools.FastWalk(mfs, "/", func(path string, d fs.DirEntry, err error) error {
						if err != nil {
							return err
						}
						count++
						return nil
					})
					if err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run("OSFS_Walk", func(b *testing.B) {
				ofs, path, cleanup := setupOSFS(b, size.numDirs, size.filesPerDir)
				defer cleanup()
				b.ReportAllocs()
				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					count := 0
					err := fstools.Walk(ofs, path, func(p string, info os.FileInfo, err error) error {
						if err != nil {
							return err
						}
						count++
						return nil
					})
					if err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run("OSFS_FastWalk", func(b *testing.B) {
				ofs, path, cleanup := setupOSFS(b, size.numDirs, size.filesPerDir)
				defer cleanup()
				b.ReportAllocs()
				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					count := 0
					err := fstools.FastWalk(ofs, path, func(p string, d fs.DirEntry, err error) error {
						if err != nil {
							return err
						}
						count++
						return nil
					})
					if err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run("Stdlib_filepath.Walk", func(b *testing.B) {
				_, path, cleanup := setupOSFS(b, size.numDirs, size.filesPerDir)
				defer cleanup()
				nativePath := osfs.ToNative(path)
				b.ReportAllocs()
				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					count := 0
					err := filepath.Walk(nativePath, func(p string, info os.FileInfo, err error) error {
						if err != nil {
							return err
						}
						count++
						return nil
					})
					if err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run("Stdlib_filepath.WalkDir", func(b *testing.B) {
				_, path, cleanup := setupOSFS(b, size.numDirs, size.filesPerDir)
				defer cleanup()
				nativePath := osfs.ToNative(path)
				b.ReportAllocs()
				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					count := 0
					err := filepath.WalkDir(nativePath, func(p string, d fs.DirEntry, err error) error {
						if err != nil {
							return err
						}
						count++
						return nil
					})
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

// BenchmarkCopy benchmarks copy operations.
func BenchmarkCopy(b *testing.B) {
	sizes := []struct {
		name         string
		numDirs      int
		filesPerDir  int
	}{
		{"Small_10x10", 10, 10},
		{"Medium_50x20", 50, 20},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			b.Run("MemFS_Copy", func(b *testing.B) {
				b.ReportAllocs()

				for i := 0; i < b.N; i++ {
					b.StopTimer()
					srcFS := setupMemFS(b, size.numDirs, size.filesPerDir)
					dstFS, _ := memfs.NewFS()
					b.StartTimer()

					err := fstools.Copy(srcFS, dstFS, "/", "/backup", nil)
					if err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run("MemFS_ParallelCopy", func(b *testing.B) {
				b.ReportAllocs()

				for i := 0; i < b.N; i++ {
					b.StopTimer()
					srcFS := setupMemFS(b, size.numDirs, size.filesPerDir)
					dstFS, _ := memfs.NewFS()
					b.StartTimer()

					_, err := fstools.ParallelCopy(srcFS, dstFS, "/", "/backup", nil)
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

// BenchmarkSize benchmarks the Size function.
func BenchmarkSize(b *testing.B) {
	sizes := []struct {
		name         string
		numDirs      int
		filesPerDir  int
	}{
		{"Small_10x10", 10, 10},
		{"Medium_50x20", 50, 20},
		{"Large_100x50", 100, 50},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			mfs := setupMemFS(b, size.numDirs, size.filesPerDir)
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_, err := fstools.Size(mfs, "/")
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkCount benchmarks the Count function.
func BenchmarkCount(b *testing.B) {
	sizes := []struct {
		name         string
		numDirs      int
		filesPerDir  int
	}{
		{"Small_10x10", 10, 10},
		{"Medium_50x20", 50, 20},
		{"Large_100x50", 100, 50},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			mfs := setupMemFS(b, size.numDirs, size.filesPerDir)
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_, err := fstools.Count(mfs, "/")
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkFastWalkWorkers tests performance with different worker counts.
func BenchmarkFastWalkWorkers(b *testing.B) {
	ofs, path, cleanup := setupOSFS(b, 100, 50)
	defer cleanup()

	workerCounts := []int{1, 2, 4, 8, 16}

	for _, workers := range workerCounts {
		b.Run(fmt.Sprintf("Workers_%d", workers), func(b *testing.B) {
			config := &fstools.FastWalkConfig{
				NumWorkers: workers,
			}
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				count := 0
				err := fstools.FastWalkWithConfig(ofs, path, config, func(p string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					count++
					return nil
				})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
