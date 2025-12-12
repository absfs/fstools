# Phase 3: Sync & Diff Implementation Plan

**Status**: Ready for Implementation
**Depends On**: Phase 1 (Foundation), Phase 2 (Atomic & Safety)
**Enables**: Phase 4 (c4 Integration)

---

## Overview

Phase 3 provides filesystem comparison and synchronization:

1. **DiffResult** - Represents differences between filesystems
2. **Diff** - Compare two filesystem trees
3. **SyncOptions** - Configuration for sync behavior
4. **Sync** - Synchronize filesystems with multiple strategies
5. **Progress** - Observable progress for long operations

These operations work generically with any absfs.Filer. Content-addressable optimization comes in Phase 4.

---

## Part 1: Diff Types and Result

### File: `diff.go`

```go
package fstools

import (
    "context"
    "os"
    "time"

    "github.com/absfs/absfs"
)

// DiffType indicates the type of difference
type DiffType int

const (
    DiffAdded    DiffType = iota // Exists in source, not in destination
    DiffRemoved                   // Exists in destination, not in source
    DiffModified                  // Exists in both but different
    DiffTypeChanged               // File became directory or vice versa
)

func (d DiffType) String() string {
    switch d {
    case DiffAdded:
        return "added"
    case DiffRemoved:
        return "removed"
    case DiffModified:
        return "modified"
    case DiffTypeChanged:
        return "type_changed"
    default:
        return "unknown"
    }
}

// DiffEntry represents a single difference between filesystems
type DiffEntry struct {
    Type    DiffType
    Path    string      // Relative path
    SrcInfo os.FileInfo // Source file info (nil if removed)
    DstInfo os.FileInfo // Destination file info (nil if added)
}

// DiffResult contains all differences between two filesystem trees
type DiffResult struct {
    Added       []*DiffEntry // In source, not in destination
    Removed     []*DiffEntry // In destination, not in source
    Modified    []*DiffEntry // In both, but different
    TypeChanged []*DiffEntry // Type changed (file <-> directory)
    Unchanged   int64        // Count of unchanged entries

    // Statistics
    SrcFiles  int64
    SrcDirs   int64
    SrcBytes  int64
    DstFiles  int64
    DstDirs   int64
    DstBytes  int64
}

// Summary returns a summary of the diff
func (d *DiffResult) Summary() string {
    return fmt.Sprintf(
        "Added: %d, Removed: %d, Modified: %d, TypeChanged: %d, Unchanged: %d",
        len(d.Added), len(d.Removed), len(d.Modified), len(d.TypeChanged), d.Unchanged,
    )
}

// HasChanges returns true if there are any differences
func (d *DiffResult) HasChanges() bool {
    return len(d.Added) > 0 || len(d.Removed) > 0 ||
           len(d.Modified) > 0 || len(d.TypeChanged) > 0
}

// AllChanges returns all changes as a single slice
func (d *DiffResult) AllChanges() []*DiffEntry {
    total := len(d.Added) + len(d.Removed) + len(d.Modified) + len(d.TypeChanged)
    result := make([]*DiffEntry, 0, total)
    result = append(result, d.Added...)
    result = append(result, d.Removed...)
    result = append(result, d.Modified...)
    result = append(result, d.TypeChanged...)
    return result
}
```

---

## Part 2: Diff Options and Comparison

### File: `diff.go` (continued)

```go
// CompareMode specifies how to compare files
type CompareMode int

const (
    // CompareSizeMtime compares size and modification time (fast)
    CompareSizeMtime CompareMode = iota

    // CompareSize compares size only (faster, less accurate)
    CompareSize

    // CompareContent compares actual file contents (slow, accurate)
    CompareContent

    // CompareChecksum compares by computing checksums (moderate)
    CompareChecksum
)

// DiffOptions configures diff behavior
type DiffOptions struct {
    // CompareMode determines how files are compared
    CompareMode CompareMode

    // Filter limits which entries are compared
    Filter Predicate

    // ExcludeFilter excludes matching entries
    ExcludeFilter Predicate

    // MaxDepth limits traversal depth (-1 for unlimited)
    MaxDepth int

    // FollowSymlinks follows symbolic links
    FollowSymlinks bool

    // IncludeUnchanged includes unchanged entries in result
    IncludeUnchanged bool

    // Progress callback for progress reporting
    Progress func(DiffProgress)

    // Workers for parallel comparison (0 = sequential)
    Workers int
}

// DefaultDiffOptions returns sensible defaults
func DefaultDiffOptions() *DiffOptions {
    return &DiffOptions{
        CompareMode: CompareSizeMtime,
        MaxDepth:    -1,
    }
}

// DiffProgress reports diff operation progress
type DiffProgress struct {
    Phase       string // "scanning_source", "scanning_dest", "comparing"
    Current     int64  // Current entry count
    Total       int64  // Total entries (0 if unknown)
    CurrentPath string // Current path being processed
}

// Diff compares two filesystem trees and returns their differences
func Diff(ctx context.Context, srcFS, dstFS absfs.Filer, srcRoot, dstRoot string, opts *DiffOptions) (*DiffResult, error) {
    if opts == nil {
        opts = DefaultDiffOptions()
    }

    result := &DiffResult{}

    // Collect source entries
    srcEntries, err := collectEntries(ctx, srcFS, srcRoot, opts, "scanning_source")
    if err != nil {
        return nil, fmt.Errorf("scan source: %w", err)
    }

    // Collect destination entries
    dstEntries, err := collectEntries(ctx, dstFS, dstRoot, opts, "scanning_dest")
    if err != nil {
        return nil, fmt.Errorf("scan destination: %w", err)
    }

    // Calculate statistics
    for _, e := range srcEntries {
        if e.Info.IsDir() {
            result.SrcDirs++
        } else {
            result.SrcFiles++
            result.SrcBytes += e.Info.Size()
        }
    }
    for _, e := range dstEntries {
        if e.Info.IsDir() {
            result.DstDirs++
        } else {
            result.DstFiles++
            result.DstBytes += e.Info.Size()
        }
    }

    // Compare entries
    progress := int64(0)
    total := int64(len(srcEntries))

    for relPath, srcEntry := range srcEntries {
        if ctx.Err() != nil {
            return nil, ctx.Err()
        }

        progress++
        if opts.Progress != nil {
            opts.Progress(DiffProgress{
                Phase:       "comparing",
                Current:     progress,
                Total:       total,
                CurrentPath: relPath,
            })
        }

        dstEntry, existsInDst := dstEntries[relPath]

        if !existsInDst {
            // Added (in source, not in destination)
            result.Added = append(result.Added, &DiffEntry{
                Type:    DiffAdded,
                Path:    relPath,
                SrcInfo: srcEntry.Info,
            })
            continue
        }

        // Both exist - compare
        diff, err := compareEntries(ctx, srcFS, dstFS, srcEntry, dstEntry, opts)
        if err != nil {
            return nil, fmt.Errorf("compare %s: %w", relPath, err)
        }

        if diff != nil {
            switch diff.Type {
            case DiffModified:
                result.Modified = append(result.Modified, diff)
            case DiffTypeChanged:
                result.TypeChanged = append(result.TypeChanged, diff)
            }
        } else {
            result.Unchanged++
        }

        // Remove from dst map (what remains is "removed")
        delete(dstEntries, relPath)
    }

    // Remaining dst entries are "removed" (not in source)
    for relPath, dstEntry := range dstEntries {
        result.Removed = append(result.Removed, &DiffEntry{
            Type:    DiffRemoved,
            Path:    relPath,
            DstInfo: dstEntry.Info,
        })
    }

    return result, nil
}

// collectEntries walks a filesystem and collects entries into a map
func collectEntries(ctx context.Context, fs absfs.Filer, root string, opts *DiffOptions, phase string) (map[string]*Entry, error) {
    entries := make(map[string]*Entry)
    progress := int64(0)

    walkOpts := &Options{
        MaxDepth: opts.MaxDepth,
    }

    err := Walk(fs, root, func(path string, info os.FileInfo, err error) error {
        if ctx.Err() != nil {
            return ctx.Err()
        }

        if err != nil {
            return err
        }

        relPath, _ := filepath.Rel(root, path)
        if relPath == "." {
            return nil // Skip root itself
        }

        entry := &Entry{
            Path:    path,
            RelPath: relPath,
            Info:    info,
        }

        // Apply filters
        if opts.Filter != nil && !opts.Filter(entry) {
            if info.IsDir() {
                return filepath.SkipDir
            }
            return nil
        }

        if opts.ExcludeFilter != nil && opts.ExcludeFilter(entry) {
            if info.IsDir() {
                return filepath.SkipDir
            }
            return nil
        }

        entries[relPath] = entry

        progress++
        if opts.Progress != nil {
            opts.Progress(DiffProgress{
                Phase:       phase,
                Current:     progress,
                Total:       0, // Unknown during scan
                CurrentPath: relPath,
            })
        }

        return nil
    })

    return entries, err
}

// compareEntries compares two entries and returns a DiffEntry if different
func compareEntries(ctx context.Context, srcFS, dstFS absfs.Filer, src, dst *Entry, opts *DiffOptions) (*DiffEntry, error) {
    srcInfo := src.Info
    dstInfo := dst.Info

    // Type changed?
    if srcInfo.IsDir() != dstInfo.IsDir() {
        return &DiffEntry{
            Type:    DiffTypeChanged,
            Path:    src.RelPath,
            SrcInfo: srcInfo,
            DstInfo: dstInfo,
        }, nil
    }

    // Directories are considered equal if both are directories
    if srcInfo.IsDir() {
        return nil, nil
    }

    // Compare files based on mode
    var different bool

    switch opts.CompareMode {
    case CompareSize:
        different = srcInfo.Size() != dstInfo.Size()

    case CompareSizeMtime:
        different = srcInfo.Size() != dstInfo.Size() ||
                   !srcInfo.ModTime().Equal(dstInfo.ModTime())

    case CompareContent:
        eq, err := compareContent(ctx, srcFS, dstFS, src.Path, dst.Path)
        if err != nil {
            return nil, err
        }
        different = !eq

    case CompareChecksum:
        eq, err := compareChecksum(ctx, srcFS, dstFS, src.Path, dst.Path)
        if err != nil {
            return nil, err
        }
        different = !eq
    }

    if different {
        return &DiffEntry{
            Type:    DiffModified,
            Path:    src.RelPath,
            SrcInfo: srcInfo,
            DstInfo: dstInfo,
        }, nil
    }

    return nil, nil
}

// compareContent compares file contents byte-by-byte
func compareContent(ctx context.Context, srcFS, dstFS absfs.Filer, srcPath, dstPath string) (bool, error) {
    srcFile, err := srcFS.Open(srcPath)
    if err != nil {
        return false, err
    }
    defer srcFile.Close()

    dstFile, err := dstFS.Open(dstPath)
    if err != nil {
        return false, err
    }
    defer dstFile.Close()

    const bufSize = 32 * 1024
    srcBuf := make([]byte, bufSize)
    dstBuf := make([]byte, bufSize)

    for {
        if ctx.Err() != nil {
            return false, ctx.Err()
        }

        srcN, srcErr := srcFile.Read(srcBuf)
        dstN, dstErr := dstFile.Read(dstBuf)

        if srcN != dstN {
            return false, nil
        }

        if srcN > 0 && !bytes.Equal(srcBuf[:srcN], dstBuf[:dstN]) {
            return false, nil
        }

        if srcErr == io.EOF && dstErr == io.EOF {
            return true, nil
        }

        if srcErr != nil && srcErr != io.EOF {
            return false, srcErr
        }
        if dstErr != nil && dstErr != io.EOF {
            return false, dstErr
        }
    }
}

// compareChecksum compares file checksums
func compareChecksum(ctx context.Context, srcFS, dstFS absfs.Filer, srcPath, dstPath string) (bool, error) {
    srcHash, err := hashFile(ctx, srcFS, srcPath)
    if err != nil {
        return false, err
    }

    dstHash, err := hashFile(ctx, dstFS, dstPath)
    if err != nil {
        return false, err
    }

    return bytes.Equal(srcHash, dstHash), nil
}

// hashFile computes SHA-256 hash of a file
func hashFile(ctx context.Context, fs absfs.Filer, path string) ([]byte, error) {
    f, err := fs.Open(path)
    if err != nil {
        return nil, err
    }
    defer f.Close()

    h := sha256.New()
    buf := make([]byte, 32*1024)

    for {
        if ctx.Err() != nil {
            return nil, ctx.Err()
        }

        n, err := f.Read(buf)
        if n > 0 {
            h.Write(buf[:n])
        }
        if err == io.EOF {
            break
        }
        if err != nil {
            return nil, err
        }
    }

    return h.Sum(nil), nil
}
```

---

## Part 3: Sync Options and Strategies

### File: `sync.go`

```go
package fstools

import (
    "context"
    "fmt"
    "os"

    "github.com/absfs/absfs"
)

// SyncStrategy defines how sync handles differences
type SyncStrategy int

const (
    // SyncMirror makes destination identical to source (add, update, delete)
    SyncMirror SyncStrategy = iota

    // SyncUpdate only adds and updates, never deletes
    SyncUpdate

    // SyncAddOnly only adds new files, never updates or deletes
    SyncAddOnly
)

// SyncOptions configures sync behavior
type SyncOptions struct {
    // Strategy determines how differences are handled
    Strategy SyncStrategy

    // CompareMode determines how files are compared
    CompareMode CompareMode

    // DryRun reports what would happen without making changes
    DryRun bool

    // Filter limits which entries are synced
    Filter Predicate

    // ExcludeFilter excludes matching entries
    ExcludeFilter Predicate

    // MaxDepth limits traversal depth (-1 for unlimited)
    MaxDepth int

    // PreserveTimestamps preserves modification times
    PreserveTimestamps bool

    // PreservePermissions preserves file permissions
    PreservePermissions bool

    // Atomic uses atomic writes for file updates
    Atomic bool

    // Progress callback for progress reporting
    Progress func(SyncProgress)

    // OnError is called for each error (return nil to continue)
    OnError func(error) error

    // Workers for parallel operations (0 = sequential)
    Workers int
}

// DefaultSyncOptions returns sensible defaults
func DefaultSyncOptions() *SyncOptions {
    return &SyncOptions{
        Strategy:            SyncMirror,
        CompareMode:         CompareSizeMtime,
        MaxDepth:            -1,
        PreserveTimestamps:  true,
        PreservePermissions: true,
        Atomic:              true,
    }
}

// SyncProgress reports sync operation progress
type SyncProgress struct {
    Phase        string     // "diff", "sync"
    Operation    string     // "copy", "delete", "mkdir"
    CurrentPath  string     // Current path being processed
    BytesCopied  int64      // Bytes copied so far
    BytesTotal   int64      // Total bytes to copy
    FilesCopied  int64      // Files copied so far
    FilesTotal   int64      // Total files to copy
    FilesDeleted int64      // Files deleted
    DirsCreated  int64      // Directories created
}

// SyncResult contains sync operation results
type SyncResult struct {
    Added       int64   // Files added
    Updated     int64   // Files updated
    Deleted     int64   // Files deleted
    DirsCreated int64   // Directories created
    DirsDeleted int64   // Directories deleted
    BytesCopied int64   // Total bytes copied
    Errors      []error // Non-fatal errors
    DryRun      bool    // Was this a dry run
}

// Summary returns a summary of the sync
func (r *SyncResult) Summary() string {
    prefix := ""
    if r.DryRun {
        prefix = "[DRY RUN] "
    }
    return fmt.Sprintf(
        "%sAdded: %d, Updated: %d, Deleted: %d, Bytes: %d",
        prefix, r.Added, r.Updated, r.Deleted, r.BytesCopied,
    )
}
```

---

## Part 4: Sync Implementation

### File: `sync.go` (continued)

```go
// Sync synchronizes destination to match source according to options
func Sync(ctx context.Context, srcFS absfs.Filer, dstFS absfs.FileSystem, srcRoot, dstRoot string, opts *SyncOptions) (*SyncResult, error) {
    if opts == nil {
        opts = DefaultSyncOptions()
    }

    result := &SyncResult{DryRun: opts.DryRun}

    // Phase 1: Diff
    if opts.Progress != nil {
        opts.Progress(SyncProgress{Phase: "diff"})
    }

    diffOpts := &DiffOptions{
        CompareMode:   opts.CompareMode,
        Filter:        opts.Filter,
        ExcludeFilter: opts.ExcludeFilter,
        MaxDepth:      opts.MaxDepth,
    }

    diff, err := Diff(ctx, srcFS, dstFS, srcRoot, dstRoot, diffOpts)
    if err != nil {
        return nil, fmt.Errorf("diff: %w", err)
    }

    // Calculate total bytes to copy
    var totalBytes int64
    var totalFiles int64

    for _, entry := range diff.Added {
        if entry.SrcInfo != nil && !entry.SrcInfo.IsDir() {
            totalBytes += entry.SrcInfo.Size()
            totalFiles++
        }
    }
    for _, entry := range diff.Modified {
        if entry.SrcInfo != nil && !entry.SrcInfo.IsDir() {
            totalBytes += entry.SrcInfo.Size()
            totalFiles++
        }
    }

    // Phase 2: Sync
    if opts.Progress != nil {
        opts.Progress(SyncProgress{
            Phase:      "sync",
            BytesTotal: totalBytes,
            FilesTotal: totalFiles,
        })
    }

    // Create directories first (in order of depth)
    sortedAdded := sortByDepth(diff.Added, true) // Shallow first
    for _, entry := range sortedAdded {
        if ctx.Err() != nil {
            return result, ctx.Err()
        }

        if entry.SrcInfo != nil && entry.SrcInfo.IsDir() {
            dstPath := filepath.Join(dstRoot, entry.Path)

            if opts.Progress != nil {
                opts.Progress(SyncProgress{
                    Phase:       "sync",
                    Operation:   "mkdir",
                    CurrentPath: entry.Path,
                })
            }

            if !opts.DryRun {
                perm := os.FileMode(0755)
                if opts.PreservePermissions {
                    perm = entry.SrcInfo.Mode().Perm()
                }

                if err := dstFS.MkdirAll(dstPath, perm); err != nil {
                    if handleErr := handleSyncError(opts, err); handleErr != nil {
                        return result, handleErr
                    }
                    result.Errors = append(result.Errors, err)
                    continue
                }
            }

            result.DirsCreated++
        }
    }

    // Copy added files
    for _, entry := range diff.Added {
        if ctx.Err() != nil {
            return result, ctx.Err()
        }

        if entry.SrcInfo == nil || entry.SrcInfo.IsDir() {
            continue
        }

        srcPath := filepath.Join(srcRoot, entry.Path)
        dstPath := filepath.Join(dstRoot, entry.Path)

        if opts.Progress != nil {
            opts.Progress(SyncProgress{
                Phase:       "sync",
                Operation:   "copy",
                CurrentPath: entry.Path,
                FilesCopied: result.Added,
                FilesTotal:  totalFiles,
                BytesCopied: result.BytesCopied,
                BytesTotal:  totalBytes,
            })
        }

        if !opts.DryRun {
            if err := syncFile(ctx, srcFS, dstFS, srcPath, dstPath, entry.SrcInfo, opts); err != nil {
                if handleErr := handleSyncError(opts, err); handleErr != nil {
                    return result, handleErr
                }
                result.Errors = append(result.Errors, err)
                continue
            }
        }

        result.Added++
        result.BytesCopied += entry.SrcInfo.Size()
    }

    // Update modified files (unless AddOnly strategy)
    if opts.Strategy != SyncAddOnly {
        for _, entry := range diff.Modified {
            if ctx.Err() != nil {
                return result, ctx.Err()
            }

            if entry.SrcInfo == nil || entry.SrcInfo.IsDir() {
                continue
            }

            srcPath := filepath.Join(srcRoot, entry.Path)
            dstPath := filepath.Join(dstRoot, entry.Path)

            if opts.Progress != nil {
                opts.Progress(SyncProgress{
                    Phase:       "sync",
                    Operation:   "update",
                    CurrentPath: entry.Path,
                    FilesCopied: result.Added + result.Updated,
                    FilesTotal:  totalFiles,
                    BytesCopied: result.BytesCopied,
                    BytesTotal:  totalBytes,
                })
            }

            if !opts.DryRun {
                if err := syncFile(ctx, srcFS, dstFS, srcPath, dstPath, entry.SrcInfo, opts); err != nil {
                    if handleErr := handleSyncError(opts, err); handleErr != nil {
                        return result, handleErr
                    }
                    result.Errors = append(result.Errors, err)
                    continue
                }
            }

            result.Updated++
            result.BytesCopied += entry.SrcInfo.Size()
        }

        // Handle type changes (delete old, create new)
        for _, entry := range diff.TypeChanged {
            if ctx.Err() != nil {
                return result, ctx.Err()
            }

            dstPath := filepath.Join(dstRoot, entry.Path)
            srcPath := filepath.Join(srcRoot, entry.Path)

            if !opts.DryRun {
                // Remove old
                if entry.DstInfo.IsDir() {
                    if err := SafeRemoveAll(ctx, dstFS, dstPath); err != nil {
                        if handleErr := handleSyncError(opts, err); handleErr != nil {
                            return result, handleErr
                        }
                        result.Errors = append(result.Errors, err)
                        continue
                    }
                    result.DirsDeleted++
                } else {
                    if err := dstFS.Remove(dstPath); err != nil {
                        if handleErr := handleSyncError(opts, err); handleErr != nil {
                            return result, handleErr
                        }
                        result.Errors = append(result.Errors, err)
                        continue
                    }
                    result.Deleted++
                }

                // Create new
                if entry.SrcInfo.IsDir() {
                    if err := dstFS.MkdirAll(dstPath, entry.SrcInfo.Mode().Perm()); err != nil {
                        result.Errors = append(result.Errors, err)
                        continue
                    }
                    result.DirsCreated++
                } else {
                    if err := syncFile(ctx, srcFS, dstFS, srcPath, dstPath, entry.SrcInfo, opts); err != nil {
                        result.Errors = append(result.Errors, err)
                        continue
                    }
                    result.Added++
                    result.BytesCopied += entry.SrcInfo.Size()
                }
            }
        }
    }

    // Delete removed files (only for Mirror strategy)
    if opts.Strategy == SyncMirror {
        // Delete files first, then directories (deepest first)
        sortedRemoved := sortByDepth(diff.Removed, false) // Deep first

        for _, entry := range sortedRemoved {
            if ctx.Err() != nil {
                return result, ctx.Err()
            }

            dstPath := filepath.Join(dstRoot, entry.Path)

            if opts.Progress != nil {
                opts.Progress(SyncProgress{
                    Phase:        "sync",
                    Operation:    "delete",
                    CurrentPath:  entry.Path,
                    FilesDeleted: result.Deleted + result.DirsDeleted,
                })
            }

            if !opts.DryRun {
                if entry.DstInfo.IsDir() {
                    if err := dstFS.Remove(dstPath); err != nil {
                        if handleErr := handleSyncError(opts, err); handleErr != nil {
                            return result, handleErr
                        }
                        result.Errors = append(result.Errors, err)
                        continue
                    }
                    result.DirsDeleted++
                } else {
                    if err := dstFS.Remove(dstPath); err != nil {
                        if handleErr := handleSyncError(opts, err); handleErr != nil {
                            return result, handleErr
                        }
                        result.Errors = append(result.Errors, err)
                        continue
                    }
                    result.Deleted++
                }
            }
        }
    }

    return result, nil
}

// syncFile copies a single file with options
func syncFile(ctx context.Context, srcFS absfs.Filer, dstFS absfs.FileSystem, srcPath, dstPath string, srcInfo os.FileInfo, opts *SyncOptions) error {
    // Ensure parent directory exists
    if err := dstFS.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
        return fmt.Errorf("create parent: %w", err)
    }

    // Determine permissions
    perm := os.FileMode(0644)
    if opts.PreservePermissions {
        perm = srcInfo.Mode().Perm()
    }

    // Open source
    srcFile, err := srcFS.Open(srcPath)
    if err != nil {
        return fmt.Errorf("open source: %w", err)
    }
    defer srcFile.Close()

    // Write to destination
    if opts.Atomic {
        err = AtomicWriteFunc(ctx, dstFS, dstPath, &AtomicWriteOptions{
            Mode: perm,
            Sync: true,
        }, func(f absfs.File) error {
            _, err := io.Copy(f, srcFile)
            return err
        })
    } else {
        dstFile, err := dstFS.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
        if err != nil {
            return fmt.Errorf("create destination: %w", err)
        }
        _, err = io.Copy(dstFile, srcFile)
        closeErr := dstFile.Close()
        if err != nil {
            return fmt.Errorf("copy: %w", err)
        }
        if closeErr != nil {
            return fmt.Errorf("close: %w", closeErr)
        }
    }

    if err != nil {
        return err
    }

    // Preserve timestamps
    if opts.PreserveTimestamps {
        if chtimes, ok := dstFS.(interface{ Chtimes(string, time.Time, time.Time) error }); ok {
            chtimes(dstPath, srcInfo.ModTime(), srcInfo.ModTime())
        }
    }

    return nil
}

// handleSyncError processes an error according to options
func handleSyncError(opts *SyncOptions, err error) error {
    if opts.OnError != nil {
        return opts.OnError(err)
    }
    return err // Default: stop on first error
}

// sortByDepth sorts entries by path depth
func sortByDepth(entries []*DiffEntry, ascending bool) []*DiffEntry {
    sorted := make([]*DiffEntry, len(entries))
    copy(sorted, entries)

    sort.Slice(sorted, func(i, j int) bool {
        depthI := strings.Count(sorted[i].Path, string(filepath.Separator))
        depthJ := strings.Count(sorted[j].Path, string(filepath.Separator))
        if ascending {
            return depthI < depthJ
        }
        return depthI > depthJ
    })

    return sorted
}
```

---

## Part 5: Convenience Functions

### File: `sync.go` (continued)

```go
// Mirror is a convenience function for mirroring source to destination
func Mirror(ctx context.Context, srcFS absfs.Filer, dstFS absfs.FileSystem, srcRoot, dstRoot string, progress func(SyncProgress)) (*SyncResult, error) {
    return Sync(ctx, srcFS, dstFS, srcRoot, dstRoot, &SyncOptions{
        Strategy:            SyncMirror,
        CompareMode:         CompareSizeMtime,
        PreserveTimestamps:  true,
        PreservePermissions: true,
        Atomic:              true,
        Progress:            progress,
    })
}

// Update is a convenience function for updating destination with new/changed files
func Update(ctx context.Context, srcFS absfs.Filer, dstFS absfs.FileSystem, srcRoot, dstRoot string, progress func(SyncProgress)) (*SyncResult, error) {
    return Sync(ctx, srcFS, dstFS, srcRoot, dstRoot, &SyncOptions{
        Strategy:            SyncUpdate,
        CompareMode:         CompareSizeMtime,
        PreserveTimestamps:  true,
        PreservePermissions: true,
        Atomic:              true,
        Progress:            progress,
    })
}

// SyncDryRun performs a dry run and returns what would happen
func SyncDryRun(ctx context.Context, srcFS, dstFS absfs.Filer, srcRoot, dstRoot string, opts *SyncOptions) (*SyncResult, error) {
    if opts == nil {
        opts = DefaultSyncOptions()
    }
    optsCopy := *opts
    optsCopy.DryRun = true

    // For dry run, destination doesn't need to be writable
    // Cast to FileSystem interface (will panic if not, but dry run doesn't write)
    dstFSWriter, ok := dstFS.(absfs.FileSystem)
    if !ok {
        // Create a no-op wrapper for dry run
        dstFSWriter = &dryRunFS{dstFS}
    }

    return Sync(ctx, srcFS, dstFSWriter, srcRoot, dstRoot, &optsCopy)
}

// dryRunFS wraps a Filer to satisfy FileSystem interface for dry runs
type dryRunFS struct {
    absfs.Filer
}

func (d *dryRunFS) Create(name string) (absfs.File, error) {
    return nil, fmt.Errorf("dry run: create not supported")
}

func (d *dryRunFS) Mkdir(name string, perm os.FileMode) error {
    return fmt.Errorf("dry run: mkdir not supported")
}

func (d *dryRunFS) MkdirAll(path string, perm os.FileMode) error {
    return fmt.Errorf("dry run: mkdirall not supported")
}

func (d *dryRunFS) OpenFile(name string, flag int, perm os.FileMode) (absfs.File, error) {
    return nil, fmt.Errorf("dry run: openfile not supported")
}

func (d *dryRunFS) Remove(name string) error {
    return fmt.Errorf("dry run: remove not supported")
}

func (d *dryRunFS) RemoveAll(path string) error {
    return fmt.Errorf("dry run: removeall not supported")
}

func (d *dryRunFS) Rename(oldpath, newpath string) error {
    return fmt.Errorf("dry run: rename not supported")
}

func (d *dryRunFS) Chmod(name string, mode os.FileMode) error {
    return fmt.Errorf("dry run: chmod not supported")
}

func (d *dryRunFS) Chtimes(name string, atime time.Time, mtime time.Time) error {
    return fmt.Errorf("dry run: chtimes not supported")
}

func (d *dryRunFS) Chown(name string, uid, gid int) error {
    return fmt.Errorf("dry run: chown not supported")
}
```

---

## Part 6: Progress Reporting Infrastructure

### File: `progress.go`

```go
package fstools

import (
    "sync"
    "time"
)

// ProgressReporter provides aggregated progress reporting
type ProgressReporter struct {
    mu       sync.Mutex
    callback func(Progress)
    interval time.Duration
    last     time.Time
    progress Progress
}

// Progress is a generic progress report
type Progress struct {
    Phase       string
    Operation   string
    CurrentPath string
    Current     int64
    Total       int64
    BytesDone   int64
    BytesTotal  int64
}

// NewProgressReporter creates a progress reporter that throttles updates
func NewProgressReporter(callback func(Progress), interval time.Duration) *ProgressReporter {
    return &ProgressReporter{
        callback: callback,
        interval: interval,
    }
}

// Update sends a progress update (throttled)
func (p *ProgressReporter) Update(progress Progress) {
    p.mu.Lock()
    defer p.mu.Unlock()

    p.progress = progress

    now := time.Now()
    if now.Sub(p.last) >= p.interval {
        p.last = now
        if p.callback != nil {
            p.callback(progress)
        }
    }
}

// Flush forces sending the current progress
func (p *ProgressReporter) Flush() {
    p.mu.Lock()
    defer p.mu.Unlock()

    if p.callback != nil {
        p.callback(p.progress)
    }
}

// ProgressBar is a simple text-based progress indicator
type ProgressBar struct {
    Total   int64
    Current int64
    Width   int
    Label   string
}

// String returns a string representation of the progress bar
func (p *ProgressBar) String() string {
    if p.Total <= 0 {
        return fmt.Sprintf("%s: %d", p.Label, p.Current)
    }

    percent := float64(p.Current) / float64(p.Total)
    filled := int(percent * float64(p.Width))

    bar := strings.Repeat("=", filled) + strings.Repeat(" ", p.Width-filled)
    return fmt.Sprintf("%s: [%s] %.1f%%", p.Label, bar, percent*100)
}
```

---

## Usage Examples

```go
// Simple mirror
result, err := Mirror(ctx, srcFS, dstFS, "/source", "/backup", nil)
fmt.Println(result.Summary())

// Sync with progress
result, err := Sync(ctx, srcFS, dstFS, "/source", "/backup", &SyncOptions{
    Strategy: SyncUpdate,
    Progress: func(p SyncProgress) {
        fmt.Printf("\r%s: %s (%.1f%%)", p.Phase, p.CurrentPath,
            float64(p.BytesCopied)/float64(p.BytesTotal)*100)
    },
})

// Dry run to preview changes
result, err := SyncDryRun(ctx, srcFS, dstFS, "/source", "/backup", nil)
fmt.Printf("Would add %d, update %d, delete %d files\n",
    result.Added, result.Updated, result.Deleted)

// Diff only
diff, err := Diff(ctx, srcFS, dstFS, "/source", "/backup", nil)
for _, entry := range diff.Added {
    fmt.Printf("+ %s\n", entry.Path)
}
for _, entry := range diff.Removed {
    fmt.Printf("- %s\n", entry.Path)
}
for _, entry := range diff.Modified {
    fmt.Printf("M %s\n", entry.Path)
}

// Sync with filter (only Go files)
result, err := Sync(ctx, srcFS, dstFS, "/src", "/backup", &SyncOptions{
    Filter: HasExtension(".go"),
})

// Sync with error handling (continue on error)
result, err := Sync(ctx, srcFS, dstFS, "/source", "/backup", &SyncOptions{
    OnError: func(err error) error {
        log.Printf("Warning: %v", err)
        return nil // Continue
    },
})
```

---

## Testing Checklist

### Diff Tests
- [ ] Diff detects added files
- [ ] Diff detects removed files
- [ ] Diff detects modified files (size change)
- [ ] Diff detects modified files (mtime change)
- [ ] Diff detects type changes (file <-> directory)
- [ ] Diff with CompareContent mode
- [ ] Diff with CompareChecksum mode
- [ ] Diff with filters
- [ ] Diff handles empty directories
- [ ] Diff handles symbolic links
- [ ] Diff respects context cancellation

### Sync Tests
- [ ] Sync creates new files
- [ ] Sync updates modified files
- [ ] Sync deletes removed files (Mirror)
- [ ] Sync preserves existing files (Update)
- [ ] Sync preserves timestamps
- [ ] Sync preserves permissions
- [ ] Sync with atomic writes
- [ ] Sync with dry run
- [ ] Sync with progress callback
- [ ] Sync with error callback
- [ ] Sync handles type changes
- [ ] Sync creates parent directories
- [ ] Sync respects context cancellation

### Integration Tests
- [ ] Sync between memfs instances
- [ ] Sync from osfs to memfs
- [ ] Sync from memfs to osfs
- [ ] Large directory sync (10k+ files)
- [ ] Sync with deep directory structures

---

## Implementation Order

1. **diff.go Part 1** - DiffType, DiffEntry, DiffResult
2. **diff.go Part 2** - DiffOptions, Diff function
3. **diff.go Part 3** - Compare helpers (content, checksum)
4. **sync.go Part 1** - SyncStrategy, SyncOptions, SyncResult
5. **sync.go Part 2** - Sync function
6. **sync.go Part 3** - Convenience functions
7. **progress.go** - Progress reporting infrastructure

---

## Success Criteria

Phase 3 is complete when:

1. All tests pass with `-race`
2. Diff correctly identifies all types of changes
3. Sync correctly applies changes according to strategy
4. Progress reporting works for long operations
5. Atomic writes prevent corruption during sync
6. Context cancellation stops operations cleanly
7. Large syncs (10k+ files) complete without excessive memory
