# FsTools - Tools for file systems.

[![Go Reference](https://pkg.go.dev/badge/github.com/absfs/fstools.svg)](https://pkg.go.dev/github.com/absfs/fstools)
[![Go Report Card](https://goreportcard.com/badge/github.com/absfs/fstools)](https://goreportcard.com/report/github.com/absfs/fstools)
[![CI](https://github.com/absfs/fstools/actions/workflows/ci.yml/badge.svg)](https://github.com/absfs/fstools/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Utilities for traversing and manipulating abstract file systems that implement
the [absfs.Filer](https://github.com/absfs/absfs) interface.

## Implemented Features

- **Walk** - Traverses an absfs.Filer similar to filepath.Walk from the standard
  library, using PreOrder (depth-first) traversal by default.

- **WalkWithOptions** - Configurable traversal with options for sorting,
  custom comparison functions, and traversal order selection.

- **PreOrder** - Depth-first traversal visiting parent directories before
  their children (standard traversal order).

- **PostOrder** - Depth-first traversal visiting children before their parent
  directories, useful for operations like recursive deletion.

- **BreadthOrder** - Level-by-level traversal (breadth-first search), visiting
  all entries at each depth before descending.

- **KeyOrder** - Traversal that visits only files, skipping directories entirely.

- **Exists** - Simple utility to check if a path exists in a filesystem.

- **Size** - Calculate total size of a file or directory tree in bytes.

- **Count** - Count the number of files and directories under a path.

- **Equal** - Compare two files or directory trees for identical content.

- **Copy** - Copy files and directory trees between filesystems with options for:
  - Parallel file copying (for thread-safe filesystems)
  - Metadata preservation (permissions, timestamps)
  - Filtering via callbacks (skip files/directories)
  - Error handling callbacks for partial copy recovery

## Installation

```bash
go get github.com/absfs/fstools
```

## Usage

```go
import (
    "fmt"
    "os"
    "path/filepath"

    "github.com/absfs/fstools"
    "github.com/absfs/memfs"
)

func main() {
    fs, _ := memfs.NewFS()

    // Walk with default PreOrder traversal
    fstools.Walk(fs, "/", func(path string, info os.FileInfo, err error) error {
        fmt.Println(path)
        return nil
    })

    // Walk with custom options
    opts := &fstools.Options{
        Less: func(a, b os.FileInfo) bool {
            return a.Name() < b.Name()
        },
        Traversal: fstools.BreadthTraversal,
    }
    fstools.WalkWithOptions(fs, opts, "/", func(path string, info os.FileInfo, err error) error {
        fmt.Println(path)
        return nil
    })
}
```


