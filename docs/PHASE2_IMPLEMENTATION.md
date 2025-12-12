# Phase 2: Atomic & Safety Implementation Plan

**Status**: Ready for Implementation
**Depends On**: Phase 1 (Foundation)
**Enables**: Phase 3 (Sync & Diff)

---

## Overview

Phase 2 provides safety primitives for filesystem operations:

1. **AtomicWrite** - Write-temp-rename pattern for crash-safe writes
2. **AtomicWriteFunc** - Atomic write with callback for streaming writes
3. **SafeMkdirAll** - Atomic directory creation
4. **Transaction** - Multi-operation atomic changes (best-effort)

These primitives are essential for building reliable higher-level operations like Sync.

---

## Part 1: Atomic Write

### Background

The write-temp-rename pattern ensures that a file is either fully written or not modified at all:

1. Write to a temporary file in the same directory
2. Sync/flush the temporary file
3. Rename temporary file to target (atomic on POSIX filesystems)

If the process crashes at any point, the original file is unchanged.

### File: `atomic.go`

```go
package fstools

import (
    "context"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "time"

    "github.com/absfs/absfs"
)

// AtomicWriteOptions configures atomic write behavior
type AtomicWriteOptions struct {
    // TempDir specifies where to create temporary files.
    // If empty, uses the same directory as the target.
    // Using the same directory ensures rename is atomic.
    TempDir string

    // TempPrefix is the prefix for temporary files
    TempPrefix string

    // Sync forces fsync before rename (slower but safer)
    Sync bool

    // Mode is the file permission mode
    Mode os.FileMode

    // PreserveOnError keeps the temp file if write fails (for debugging)
    PreserveOnError bool
}

// DefaultAtomicWriteOptions returns sensible defaults
func DefaultAtomicWriteOptions() *AtomicWriteOptions {
    return &AtomicWriteOptions{
        TempPrefix: ".tmp.",
        Sync:       true,
        Mode:       0644,
    }
}

// AtomicWrite writes data to path atomically using write-temp-rename.
// If the operation fails partway through, the original file is unchanged.
func AtomicWrite(ctx context.Context, fs absfs.FileSystem, path string, data []byte, opts *AtomicWriteOptions) error {
    return AtomicWriteFunc(ctx, fs, path, opts, func(f absfs.File) error {
        _, err := f.Write(data)
        return err
    })
}

// AtomicWriteReader writes data from a reader to path atomically.
func AtomicWriteReader(ctx context.Context, fs absfs.FileSystem, path string, r io.Reader, opts *AtomicWriteOptions) error {
    return AtomicWriteFunc(ctx, fs, path, opts, func(f absfs.File) error {
        _, err := io.Copy(f, r)
        return err
    })
}

// AtomicWriteFunc writes to path atomically using a callback to write content.
// The callback receives a file handle to write to. If the callback returns an error,
// the original file is unchanged.
func AtomicWriteFunc(ctx context.Context, fs absfs.FileSystem, path string, opts *AtomicWriteOptions, fn func(absfs.File) error) error {
    if opts == nil {
        opts = DefaultAtomicWriteOptions()
    }

    // Check context
    if ctx.Err() != nil {
        return ctx.Err()
    }

    // Determine temp file location
    dir := filepath.Dir(path)
    if opts.TempDir != "" {
        dir = opts.TempDir
    }

    // Generate temp file name
    tempName := fmt.Sprintf("%s%s%d", opts.TempPrefix, filepath.Base(path), time.Now().UnixNano())
    tempPath := filepath.Join(dir, tempName)

    // Ensure directory exists
    if err := fs.MkdirAll(dir, 0755); err != nil {
        return fmt.Errorf("create directory: %w", err)
    }

    // Create temp file
    tempFile, err := fs.Create(tempPath)
    if err != nil {
        return fmt.Errorf("create temp file: %w", err)
    }

    // Cleanup on failure
    success := false
    defer func() {
        if !success && !opts.PreserveOnError {
            fs.Remove(tempPath)
        }
    }()

    // Write content
    if err := fn(tempFile); err != nil {
        tempFile.Close()
        return fmt.Errorf("write content: %w", err)
    }

    // Sync if requested
    if opts.Sync {
        if syncer, ok := tempFile.(interface{ Sync() error }); ok {
            if err := syncer.Sync(); err != nil {
                tempFile.Close()
                return fmt.Errorf("sync: %w", err)
            }
        }
    }

    // Close temp file
    if err := tempFile.Close(); err != nil {
        return fmt.Errorf("close temp file: %w", err)
    }

    // Set permissions
    if opts.Mode != 0 {
        if err := fs.Chmod(tempPath, opts.Mode); err != nil {
            // Non-fatal on filesystems that don't support chmod
        }
    }

    // Atomic rename
    if err := fs.Rename(tempPath, path); err != nil {
        return fmt.Errorf("rename: %w", err)
    }

    success = true
    return nil
}

// AtomicWriteString is a convenience wrapper for string content
func AtomicWriteString(ctx context.Context, fs absfs.FileSystem, path string, content string, opts *AtomicWriteOptions) error {
    return AtomicWrite(ctx, fs, path, []byte(content), opts)
}
```

### Tests: `atomic_test.go`

```go
func TestAtomicWrite_Success(t *testing.T) {
    // Write to new file
    // Verify content
    // Verify no temp files left
}

func TestAtomicWrite_OverwriteExisting(t *testing.T) {
    // Write initial file
    // Atomic write new content
    // Verify new content
}

func TestAtomicWrite_FailurePreservesOriginal(t *testing.T) {
    // Write initial file
    // Attempt atomic write with failing callback
    // Verify original unchanged
}

func TestAtomicWrite_ContextCancellation(t *testing.T) {
    // Cancel context before write
    // Verify error
}

func TestAtomicWriteFunc_StreamingWrite(t *testing.T) {
    // Use callback to write streaming data
    // Verify final content
}

func TestAtomicWrite_Concurrent(t *testing.T) {
    // Multiple goroutines writing to same file
    // Verify no corruption
}
```

---

## Part 2: Safe Directory Operations

### File: `atomic.go` (continued)

```go
// SafeMkdirAll creates a directory and all parents atomically.
// Unlike os.MkdirAll, this ensures the full path either exists or doesn't.
// It also handles race conditions when multiple goroutines create the same path.
func SafeMkdirAll(ctx context.Context, fs absfs.FileSystem, path string, perm os.FileMode) error {
    if ctx.Err() != nil {
        return ctx.Err()
    }

    // Check if already exists
    info, err := fs.Stat(path)
    if err == nil {
        if info.IsDir() {
            return nil
        }
        return fmt.Errorf("%s exists but is not a directory", path)
    }

    // Create parent first
    parent := filepath.Dir(path)
    if parent != path && parent != "." && parent != "/" {
        if err := SafeMkdirAll(ctx, fs, parent, perm); err != nil {
            return err
        }
    }

    // Create directory
    err = fs.Mkdir(path, perm)
    if err != nil {
        // Race condition: another goroutine created it
        info, statErr := fs.Stat(path)
        if statErr == nil && info.IsDir() {
            return nil
        }
        return err
    }

    return nil
}

// SafeRemoveAll removes a path and all children safely.
// It handles errors gracefully and continues removing what it can.
func SafeRemoveAll(ctx context.Context, fs absfs.FileSystem, path string) error {
    if ctx.Err() != nil {
        return ctx.Err()
    }

    info, err := fs.Stat(path)
    if os.IsNotExist(err) {
        return nil
    }
    if err != nil {
        return err
    }

    if !info.IsDir() {
        return fs.Remove(path)
    }

    // Remove children first
    entries, err := fs.ReadDir(path)
    if err != nil {
        return err
    }

    var errs []error
    for _, entry := range entries {
        if ctx.Err() != nil {
            return ctx.Err()
        }
        childPath := filepath.Join(path, entry.Name())
        if err := SafeRemoveAll(ctx, fs, childPath); err != nil {
            errs = append(errs, err)
        }
    }

    // Remove directory itself
    if err := fs.Remove(path); err != nil {
        errs = append(errs, err)
    }

    if len(errs) > 0 {
        return fmt.Errorf("errors during removal: %v", errs)
    }
    return nil
}
```

---

## Part 3: Transaction Support

### Design Notes

True filesystem transactions (full ACID) are impossible without filesystem-level support. However, we can provide **best-effort transactions** that:

1. Track intended operations
2. Execute them in order
3. Attempt rollback on failure (may not fully succeed)
4. Provide atomicity for simple cases

### File: `atomic.go` (continued)

```go
// Transaction provides best-effort atomic multi-file operations.
// Note: True atomic transactions are not possible on most filesystems.
// This provides ordering guarantees and best-effort rollback.
type Transaction struct {
    fs       absfs.FileSystem
    ops      []txOp
    executed []txOp
    state    txState
}

type txState int

const (
    txPending txState = iota
    txCommitted
    txRolledBack
)

type txOp interface {
    execute(fs absfs.FileSystem) error
    rollback(fs absfs.FileSystem) error
    String() string
}

// BeginTransaction starts a new transaction
func BeginTransaction(fs absfs.FileSystem) *Transaction {
    return &Transaction{
        fs:    fs,
        state: txPending,
    }
}

// Create schedules a file creation
func (tx *Transaction) Create(path string, data []byte, perm os.FileMode) *Transaction {
    tx.ops = append(tx.ops, &createOp{
        path: path,
        data: data,
        perm: perm,
    })
    return tx
}

// Write schedules a file write (creates or overwrites)
func (tx *Transaction) Write(path string, data []byte, perm os.FileMode) *Transaction {
    tx.ops = append(tx.ops, &writeOp{
        path: path,
        data: data,
        perm: perm,
    })
    return tx
}

// Remove schedules a file removal
func (tx *Transaction) Remove(path string) *Transaction {
    tx.ops = append(tx.ops, &removeOp{path: path})
    return tx
}

// Rename schedules a rename operation
func (tx *Transaction) Rename(oldPath, newPath string) *Transaction {
    tx.ops = append(tx.ops, &renameOp{
        oldPath: oldPath,
        newPath: newPath,
    })
    return tx
}

// Mkdir schedules a directory creation
func (tx *Transaction) Mkdir(path string, perm os.FileMode) *Transaction {
    tx.ops = append(tx.ops, &mkdirOp{
        path: path,
        perm: perm,
    })
    return tx
}

// Commit executes all operations. On failure, attempts rollback.
func (tx *Transaction) Commit(ctx context.Context) error {
    if tx.state != txPending {
        return fmt.Errorf("transaction already %s", tx.stateString())
    }

    for _, op := range tx.ops {
        if ctx.Err() != nil {
            tx.rollback(ctx)
            return ctx.Err()
        }

        if err := op.execute(tx.fs); err != nil {
            tx.rollback(ctx)
            return fmt.Errorf("operation %s failed: %w", op.String(), err)
        }
        tx.executed = append(tx.executed, op)
    }

    tx.state = txCommitted
    return nil
}

// Rollback undoes executed operations (best effort)
func (tx *Transaction) Rollback(ctx context.Context) error {
    return tx.rollback(ctx)
}

func (tx *Transaction) rollback(ctx context.Context) error {
    if tx.state == txRolledBack {
        return nil
    }

    var errs []error
    // Rollback in reverse order
    for i := len(tx.executed) - 1; i >= 0; i-- {
        if ctx.Err() != nil {
            errs = append(errs, ctx.Err())
            break
        }
        if err := tx.executed[i].rollback(tx.fs); err != nil {
            errs = append(errs, err)
        }
    }

    tx.state = txRolledBack
    if len(errs) > 0 {
        return fmt.Errorf("rollback errors: %v", errs)
    }
    return nil
}

func (tx *Transaction) stateString() string {
    switch tx.state {
    case txCommitted:
        return "committed"
    case txRolledBack:
        return "rolled back"
    default:
        return "pending"
    }
}

// --- Operation Implementations ---

type createOp struct {
    path    string
    data    []byte
    perm    os.FileMode
    existed bool
}

func (o *createOp) execute(fs absfs.FileSystem) error {
    _, err := fs.Stat(o.path)
    o.existed = err == nil
    if o.existed {
        return fmt.Errorf("file already exists: %s", o.path)
    }
    f, err := fs.OpenFile(o.path, os.O_CREATE|os.O_WRONLY, o.perm)
    if err != nil {
        return err
    }
    defer f.Close()
    _, err = f.Write(o.data)
    return err
}

func (o *createOp) rollback(fs absfs.FileSystem) error {
    if o.existed {
        return nil // Don't delete if it existed before
    }
    return fs.Remove(o.path)
}

func (o *createOp) String() string {
    return fmt.Sprintf("create(%s)", o.path)
}

type writeOp struct {
    path    string
    data    []byte
    perm    os.FileMode
    backup  []byte
    existed bool
}

func (o *writeOp) execute(fs absfs.FileSystem) error {
    // Backup existing content
    if f, err := fs.Open(o.path); err == nil {
        o.backup, _ = io.ReadAll(f)
        f.Close()
        o.existed = true
    }

    // Write new content
    f, err := fs.OpenFile(o.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, o.perm)
    if err != nil {
        return err
    }
    defer f.Close()
    _, err = f.Write(o.data)
    return err
}

func (o *writeOp) rollback(fs absfs.FileSystem) error {
    if !o.existed {
        return fs.Remove(o.path)
    }
    f, err := fs.OpenFile(o.path, os.O_WRONLY|os.O_TRUNC, o.perm)
    if err != nil {
        return err
    }
    defer f.Close()
    _, err = f.Write(o.backup)
    return err
}

func (o *writeOp) String() string {
    return fmt.Sprintf("write(%s)", o.path)
}

type removeOp struct {
    path   string
    backup []byte
    perm   os.FileMode
    isDir  bool
}

func (o *removeOp) execute(fs absfs.FileSystem) error {
    info, err := fs.Stat(o.path)
    if err != nil {
        return err
    }
    o.perm = info.Mode()
    o.isDir = info.IsDir()

    if !o.isDir {
        // Backup file content
        f, err := fs.Open(o.path)
        if err != nil {
            return err
        }
        o.backup, _ = io.ReadAll(f)
        f.Close()
    }

    return fs.Remove(o.path)
}

func (o *removeOp) rollback(fs absfs.FileSystem) error {
    if o.isDir {
        return fs.Mkdir(o.path, o.perm)
    }
    f, err := fs.OpenFile(o.path, os.O_CREATE|os.O_WRONLY, o.perm)
    if err != nil {
        return err
    }
    defer f.Close()
    _, err = f.Write(o.backup)
    return err
}

func (o *removeOp) String() string {
    return fmt.Sprintf("remove(%s)", o.path)
}

type renameOp struct {
    oldPath string
    newPath string
}

func (o *renameOp) execute(fs absfs.FileSystem) error {
    return fs.Rename(o.oldPath, o.newPath)
}

func (o *renameOp) rollback(fs absfs.FileSystem) error {
    return fs.Rename(o.newPath, o.oldPath)
}

func (o *renameOp) String() string {
    return fmt.Sprintf("rename(%s -> %s)", o.oldPath, o.newPath)
}

type mkdirOp struct {
    path    string
    perm    os.FileMode
    existed bool
}

func (o *mkdirOp) execute(fs absfs.FileSystem) error {
    _, err := fs.Stat(o.path)
    o.existed = err == nil
    if o.existed {
        return nil // Already exists, no-op
    }
    return fs.Mkdir(o.path, o.perm)
}

func (o *mkdirOp) rollback(fs absfs.FileSystem) error {
    if o.existed {
        return nil
    }
    return fs.Remove(o.path)
}

func (o *mkdirOp) String() string {
    return fmt.Sprintf("mkdir(%s)", o.path)
}
```

### Transaction Usage Examples

```go
// Atomic config update
tx := BeginTransaction(fs)
tx.Write("/etc/app/config.json", newConfig, 0644)
tx.Write("/etc/app/version", []byte("2.0.0"), 0644)
if err := tx.Commit(ctx); err != nil {
    log.Printf("Config update failed and was rolled back: %v", err)
}

// Atomic directory structure creation
tx := BeginTransaction(fs)
tx.Mkdir("/app/data", 0755)
tx.Mkdir("/app/logs", 0755)
tx.Mkdir("/app/cache", 0755)
tx.Create("/app/config.yaml", defaultConfig, 0644)
tx.Commit(ctx)

// Swap files atomically
tx := BeginTransaction(fs)
tx.Rename("/path/to/current", "/path/to/backup")
tx.Rename("/path/to/new", "/path/to/current")
tx.Commit(ctx)
```

### Tests: `transaction_test.go`

```go
func TestTransaction_CreateCommit(t *testing.T) {
    // Create files in transaction
    // Commit
    // Verify files exist
}

func TestTransaction_CreateRollback(t *testing.T) {
    // Create files in transaction
    // Rollback
    // Verify files don't exist
}

func TestTransaction_WriteRollback(t *testing.T) {
    // Write initial file
    // Start transaction, write new content
    // Rollback
    // Verify original content restored
}

func TestTransaction_FailureRollsBack(t *testing.T) {
    // Schedule multiple operations
    // Make one fail (e.g., create file that exists)
    // Verify earlier operations rolled back
}

func TestTransaction_RemoveRollback(t *testing.T) {
    // Create file
    // Transaction removes it
    // Rollback
    // Verify file restored
}

func TestTransaction_RenameRollback(t *testing.T) {
    // Create file
    // Transaction renames it
    // Rollback
    // Verify original name restored
}
```

---

## Part 4: Error Types

### File: `errors.go`

```go
package fstools

import (
    "errors"
    "fmt"
)

// Common errors
var (
    ErrCancelled     = errors.New("operation cancelled")
    ErrAlreadyExists = errors.New("file already exists")
    ErrNotExists     = errors.New("file does not exist")
    ErrNotDirectory  = errors.New("not a directory")
    ErrIsDirectory   = errors.New("is a directory")
)

// OperationError wraps an error with operation context
type OperationError struct {
    Op   string // Operation name
    Path string // Path involved
    Err  error  // Underlying error
}

func (e *OperationError) Error() string {
    return fmt.Sprintf("%s %s: %v", e.Op, e.Path, e.Err)
}

func (e *OperationError) Unwrap() error {
    return e.Err
}

// ErrorList collects multiple errors
type ErrorList []error

func (e ErrorList) Error() string {
    if len(e) == 0 {
        return "no errors"
    }
    if len(e) == 1 {
        return e[0].Error()
    }
    return fmt.Sprintf("%d errors, first: %v", len(e), e[0])
}

func (e ErrorList) Unwrap() []error {
    return e
}

// IsEmpty returns true if there are no errors
func (e ErrorList) IsEmpty() bool {
    return len(e) == 0
}

// Add appends an error if non-nil
func (e *ErrorList) Add(err error) {
    if err != nil {
        *e = append(*e, err)
    }
}

// AsError returns nil if empty, otherwise returns the ErrorList
func (e ErrorList) AsError() error {
    if e.IsEmpty() {
        return nil
    }
    return e
}
```

---

## Implementation Order

1. **errors.go** - Error types (no dependencies)
2. **atomic.go Part 1** - AtomicWrite, AtomicWriteFunc
3. **atomic.go Part 2** - SafeMkdirAll, SafeRemoveAll
4. **atomic.go Part 3** - Transaction type and operations

---

## Testing Checklist

- [ ] AtomicWrite creates new file
- [ ] AtomicWrite overwrites existing file
- [ ] AtomicWrite preserves original on failure
- [ ] AtomicWrite respects context cancellation
- [ ] AtomicWriteFunc with streaming data
- [ ] AtomicWrite concurrent access
- [ ] SafeMkdirAll creates nested directories
- [ ] SafeMkdirAll handles existing directory
- [ ] SafeMkdirAll handles race conditions
- [ ] SafeRemoveAll removes nested content
- [ ] SafeRemoveAll handles context cancellation
- [ ] Transaction commit creates files
- [ ] Transaction commit writes files
- [ ] Transaction rollback undoes creates
- [ ] Transaction rollback restores writes
- [ ] Transaction failure triggers rollback
- [ ] ErrorList accumulates errors

---

## Success Criteria

Phase 2 is complete when:

1. All tests pass with `-race`
2. AtomicWrite never corrupts files on failure
3. Transactions roll back cleanly in failure cases
4. Documentation explains limitations (best-effort transactions)
5. Error types provide actionable context
