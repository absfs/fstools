# Phase 1: Foundation Implementation Plan

**Status**: Ready for Implementation
**Depends On**: None
**Enables**: Phase 2, Phase 3

---

## Overview

Phase 1 establishes the core abstractions that all subsequent features build upon:

1. **Entry** - Unified representation of filesystem entries
2. **EntryStream** - Lazy, pull-based iteration
3. **Predicate** - Composable filters with combinators
4. **Stream Transformers** - Filter, Take, Skip, etc.
5. **Stream Consumers** - ForEach, Collect, Count, First
6. **Find Builder** - Fluent API for complex searches
7. **Context Support** - Cancellation throughout

---

## Part 1: Entry Type

### File: `entry.go`

The Entry type provides a unified representation of filesystem entries, carrying all commonly needed metadata.

```go
package fstools

import (
    "io/fs"
    "os"
    "path/filepath"
    "time"
)

// Entry represents a filesystem entry with metadata.
// It carries all commonly needed information to avoid repeated Stat calls.
type Entry struct {
    // Path is the absolute path to the entry
    Path string

    // RelPath is the path relative to the walk root (empty if not from a walk)
    RelPath string

    // Depth is the directory depth from the walk root (0 = root itself)
    Depth int

    // Info contains file metadata. May be nil if there was an error.
    Info os.FileInfo

    // DirEntry is the underlying directory entry if available (from WalkDir/FastWalk)
    DirEntry fs.DirEntry

    // Err holds any error encountered accessing this entry
    Err error
}

// Name returns the base name of the entry
func (e *Entry) Name() string {
    if e.Info != nil {
        return e.Info.Name()
    }
    return filepath.Base(e.Path)
}

// Size returns the file size, or 0 for directories/errors
func (e *Entry) Size() int64 {
    if e.Info != nil {
        return e.Info.Size()
    }
    return 0
}

// Mode returns the file mode
func (e *Entry) Mode() os.FileMode {
    if e.Info != nil {
        return e.Info.Mode()
    }
    return 0
}

// ModTime returns the modification time
func (e *Entry) ModTime() time.Time {
    if e.Info != nil {
        return e.Info.ModTime()
    }
    return time.Time{}
}

// IsDir returns true if entry is a directory
func (e *Entry) IsDir() bool {
    if e.Info != nil {
        return e.Info.IsDir()
    }
    if e.DirEntry != nil {
        return e.DirEntry.IsDir()
    }
    return false
}

// IsRegular returns true if entry is a regular file
func (e *Entry) IsRegular() bool {
    return e.Mode().IsRegular()
}

// IsSymlink returns true if entry is a symbolic link
func (e *Entry) IsSymlink() bool {
    return e.Mode()&os.ModeSymlink != 0
}

// Extension returns the file extension including the dot
func (e *Entry) Extension() string {
    return filepath.Ext(e.Name())
}
```

### Tests: `entry_test.go`

```go
func TestEntry_Properties(t *testing.T) {
    // Test with FileInfo
    // Test with DirEntry only
    // Test with neither (error case)
    // Test IsDir, IsRegular, IsSymlink
    // Test Extension
}
```

---

## Part 2: EntryStream Interface

### File: `stream.go`

EntryStream provides lazy, pull-based iteration over filesystem entries.

```go
package fstools

import (
    "context"
    "io"

    "github.com/absfs/absfs"
)

// EntryStream provides lazy iteration over filesystem entries.
// Streams must be closed when done to release resources.
type EntryStream interface {
    // Next returns the next entry, or io.EOF when done.
    // Returns ctx.Err() if context is cancelled.
    Next(ctx context.Context) (*Entry, error)

    // Close releases resources. Must be called when done.
    Close() error
}

// walkStream wraps Walk to provide EntryStream interface
type walkStream struct {
    fs      absfs.Filer
    root    string
    opts    *Options
    entries chan *Entry
    err     error
    done    chan struct{}
    closed  bool
}

// WalkStream returns an EntryStream that walks the filesystem.
func WalkStream(fs absfs.Filer, root string, opts *Options) EntryStream {
    if opts == nil {
        opts = &Options{}
    }

    s := &walkStream{
        fs:      fs,
        root:    root,
        opts:    opts,
        entries: make(chan *Entry, 100), // Buffer for efficiency
        done:    make(chan struct{}),
    }

    go s.walk()
    return s
}

func (s *walkStream) walk() {
    defer close(s.entries)

    walkFn := func(path string, info os.FileInfo, err error) error {
        select {
        case <-s.done:
            return filepath.SkipAll
        default:
        }

        relPath, _ := filepath.Rel(s.root, path)
        depth := len(strings.Split(relPath, string(filepath.Separator)))
        if relPath == "." {
            depth = 0
        }

        entry := &Entry{
            Path:    path,
            RelPath: relPath,
            Depth:   depth,
            Info:    info,
            Err:     err,
        }

        select {
        case s.entries <- entry:
            return nil
        case <-s.done:
            return filepath.SkipAll
        }
    }

    if s.opts.Fast {
        s.err = FastWalk(s.fs, s.root, func(path string, d fs.DirEntry, err error) error {
            info, infoErr := d.Info()
            if infoErr != nil && err == nil {
                err = infoErr
            }
            return walkFn(path, info, err)
        })
    } else {
        s.err = Walk(s.fs, s.root, walkFn)
    }
}

func (s *walkStream) Next(ctx context.Context) (*Entry, error) {
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    case entry, ok := <-s.entries:
        if !ok {
            if s.err != nil {
                return nil, s.err
            }
            return nil, io.EOF
        }
        return entry, nil
    }
}

func (s *walkStream) Close() error {
    if !s.closed {
        s.closed = true
        close(s.done)
    }
    return nil
}

// FromSlice creates an EntryStream from a slice of entries
func FromSlice(entries []*Entry) EntryStream {
    return &sliceStream{entries: entries}
}

type sliceStream struct {
    entries []*Entry
    pos     int
}

func (s *sliceStream) Next(ctx context.Context) (*Entry, error) {
    if ctx.Err() != nil {
        return nil, ctx.Err()
    }
    if s.pos >= len(s.entries) {
        return nil, io.EOF
    }
    entry := s.entries[s.pos]
    s.pos++
    return entry, nil
}

func (s *sliceStream) Close() error {
    return nil
}

// Merge combines multiple streams into one
func Merge(streams ...EntryStream) EntryStream {
    return &mergeStream{streams: streams}
}

type mergeStream struct {
    streams []EntryStream
    current int
}

func (m *mergeStream) Next(ctx context.Context) (*Entry, error) {
    for m.current < len(m.streams) {
        entry, err := m.streams[m.current].Next(ctx)
        if err == io.EOF {
            m.current++
            continue
        }
        return entry, err
    }
    return nil, io.EOF
}

func (m *mergeStream) Close() error {
    var lastErr error
    for _, s := range m.streams {
        if err := s.Close(); err != nil {
            lastErr = err
        }
    }
    return lastErr
}
```

### Tests: `stream_test.go`

```go
func TestWalkStream(t *testing.T) {
    // Create memfs with test structure
    // Walk and collect entries
    // Verify all entries found
    // Test early close (cancellation)
}

func TestFromSlice(t *testing.T) {
    // Create slice of entries
    // Iterate through stream
    // Verify order preserved
}

func TestMerge(t *testing.T) {
    // Create multiple streams
    // Merge and iterate
    // Verify all entries from all streams
}

func TestWalkStream_Cancellation(t *testing.T) {
    // Start walk
    // Cancel context mid-walk
    // Verify returns ctx.Err()
}
```

---

## Part 3: Predicates

### File: `predicate.go`

Predicates are functions that test entries. Combinators allow building complex filters.

```go
package fstools

import (
    "os"
    "path/filepath"
    "regexp"
    "strings"
    "time"
)

// Predicate tests an entry and returns true if it matches
type Predicate func(*Entry) bool

// --- Combinators ---

// And returns a predicate that matches if ALL predicates match
func And(predicates ...Predicate) Predicate {
    return func(e *Entry) bool {
        for _, p := range predicates {
            if !p(e) {
                return false
            }
        }
        return true
    }
}

// Or returns a predicate that matches if ANY predicate matches
func Or(predicates ...Predicate) Predicate {
    return func(e *Entry) bool {
        for _, p := range predicates {
            if p(e) {
                return true
            }
        }
        return false
    }
}

// Not returns a predicate that inverts the given predicate
func Not(predicate Predicate) Predicate {
    return func(e *Entry) bool {
        return !predicate(e)
    }
}

// --- Type Predicates ---

// IsFile matches regular files
func IsFile() Predicate {
    return func(e *Entry) bool {
        return e.IsRegular()
    }
}

// IsDir matches directories
func IsDir() Predicate {
    return func(e *Entry) bool {
        return e.IsDir()
    }
}

// IsSymlink matches symbolic links
func IsSymlink() Predicate {
    return func(e *Entry) bool {
        return e.IsSymlink()
    }
}

// --- Name Predicates ---

// MatchGlob matches entries whose name matches the glob pattern
func MatchGlob(pattern string) Predicate {
    return func(e *Entry) bool {
        matched, _ := filepath.Match(pattern, e.Name())
        return matched
    }
}

// MatchGlobPath matches entries whose full path matches the glob pattern
func MatchGlobPath(pattern string) Predicate {
    return func(e *Entry) bool {
        matched, _ := filepath.Match(pattern, e.Path)
        return matched
    }
}

// MatchRegex matches entries whose name matches the regex
func MatchRegex(re *regexp.Regexp) Predicate {
    return func(e *Entry) bool {
        return re.MatchString(e.Name())
    }
}

// MatchRegexPath matches entries whose full path matches the regex
func MatchRegexPath(re *regexp.Regexp) Predicate {
    return func(e *Entry) bool {
        return re.MatchString(e.Path)
    }
}

// HasExtension matches files with any of the given extensions
// Extensions should include the dot (e.g., ".go", ".txt")
func HasExtension(exts ...string) Predicate {
    extSet := make(map[string]bool)
    for _, ext := range exts {
        extSet[strings.ToLower(ext)] = true
    }
    return func(e *Entry) bool {
        return extSet[strings.ToLower(e.Extension())]
    }
}

// NameContains matches entries whose name contains the substring
func NameContains(substr string) Predicate {
    return func(e *Entry) bool {
        return strings.Contains(e.Name(), substr)
    }
}

// NamePrefix matches entries whose name starts with the prefix
func NamePrefix(prefix string) Predicate {
    return func(e *Entry) bool {
        return strings.HasPrefix(e.Name(), prefix)
    }
}

// NameSuffix matches entries whose name ends with the suffix
func NameSuffix(suffix string) Predicate {
    return func(e *Entry) bool {
        return strings.HasSuffix(e.Name(), suffix)
    }
}

// --- Size Predicates ---

// SizeGreaterThan matches files larger than the given size
func SizeGreaterThan(size int64) Predicate {
    return func(e *Entry) bool {
        return e.Size() > size
    }
}

// SizeLessThan matches files smaller than the given size
func SizeLessThan(size int64) Predicate {
    return func(e *Entry) bool {
        return e.Size() < size
    }
}

// SizeBetween matches files with size in the range [min, max]
func SizeBetween(min, max int64) Predicate {
    return func(e *Entry) bool {
        s := e.Size()
        return s >= min && s <= max
    }
}

// SizeEquals matches files with exactly the given size
func SizeEquals(size int64) Predicate {
    return func(e *Entry) bool {
        return e.Size() == size
    }
}

// --- Time Predicates ---

// ModifiedAfter matches entries modified after the given time
func ModifiedAfter(t time.Time) Predicate {
    return func(e *Entry) bool {
        return e.ModTime().After(t)
    }
}

// ModifiedBefore matches entries modified before the given time
func ModifiedBefore(t time.Time) Predicate {
    return func(e *Entry) bool {
        return e.ModTime().Before(t)
    }
}

// ModifiedWithin matches entries modified within the given duration
func ModifiedWithin(d time.Duration) Predicate {
    return func(e *Entry) bool {
        return time.Since(e.ModTime()) <= d
    }
}

// ModifiedBetween matches entries modified between two times
func ModifiedBetween(start, end time.Time) Predicate {
    return func(e *Entry) bool {
        mt := e.ModTime()
        return mt.After(start) && mt.Before(end)
    }
}

// --- Depth Predicates ---

// DepthEquals matches entries at exactly the given depth
func DepthEquals(depth int) Predicate {
    return func(e *Entry) bool {
        return e.Depth == depth
    }
}

// DepthLessThan matches entries above the given depth
func DepthLessThan(depth int) Predicate {
    return func(e *Entry) bool {
        return e.Depth < depth
    }
}

// DepthGreaterThan matches entries below the given depth
func DepthGreaterThan(depth int) Predicate {
    return func(e *Entry) bool {
        return e.Depth > depth
    }
}

// MaxDepth matches entries at or above the given depth
func MaxDepth(depth int) Predicate {
    return func(e *Entry) bool {
        return e.Depth <= depth
    }
}

// --- Permission Predicates ---

// IsExecutable matches files that are executable
func IsExecutable() Predicate {
    return func(e *Entry) bool {
        return e.Mode()&0111 != 0
    }
}

// IsReadable matches files that are readable
func IsReadable() Predicate {
    return func(e *Entry) bool {
        return e.Mode()&0444 != 0
    }
}

// IsWritable matches files that are writable
func IsWritable() Predicate {
    return func(e *Entry) bool {
        return e.Mode()&0222 != 0
    }
}

// HasMode matches files with all the given mode bits set
func HasMode(mode os.FileMode) Predicate {
    return func(e *Entry) bool {
        return e.Mode()&mode == mode
    }
}

// --- Error Predicates ---

// HasError matches entries that have an error
func HasError() Predicate {
    return func(e *Entry) bool {
        return e.Err != nil
    }
}

// NoError matches entries without errors
func NoError() Predicate {
    return func(e *Entry) bool {
        return e.Err == nil
    }
}

// --- Special Predicates ---

// Always returns a predicate that always matches
func Always() Predicate {
    return func(e *Entry) bool {
        return true
    }
}

// Never returns a predicate that never matches
func Never() Predicate {
    return func(e *Entry) bool {
        return false
    }
}

// Hidden matches hidden files (starting with .)
func Hidden() Predicate {
    return func(e *Entry) bool {
        return strings.HasPrefix(e.Name(), ".")
    }
}
```

### Tests: `predicate_test.go`

```go
func TestPredicateCombinators(t *testing.T) {
    // Test And with multiple predicates
    // Test Or with multiple predicates
    // Test Not
    // Test nested combinations
}

func TestTypePredicates(t *testing.T) {
    // Test IsFile, IsDir, IsSymlink
}

func TestNamePredicates(t *testing.T) {
    // Test MatchGlob
    // Test MatchRegex
    // Test HasExtension
    // Test NameContains, NamePrefix, NameSuffix
}

func TestSizePredicates(t *testing.T) {
    // Test SizeGreaterThan, SizeLessThan
    // Test SizeBetween, SizeEquals
}

func TestTimePredicates(t *testing.T) {
    // Test ModifiedAfter, ModifiedBefore
    // Test ModifiedWithin
}

func TestDepthPredicates(t *testing.T) {
    // Test DepthEquals, MaxDepth
}
```

---

## Part 4: Stream Transformers

### File: `stream.go` (continued)

```go
// --- Stream Transformers ---

// Filter returns a stream that only yields entries matching the predicate
func Filter(stream EntryStream, predicate Predicate) EntryStream {
    return &filterStream{
        stream:    stream,
        predicate: predicate,
    }
}

type filterStream struct {
    stream    EntryStream
    predicate Predicate
}

func (f *filterStream) Next(ctx context.Context) (*Entry, error) {
    for {
        entry, err := f.stream.Next(ctx)
        if err != nil {
            return nil, err
        }
        if f.predicate(entry) {
            return entry, nil
        }
    }
}

func (f *filterStream) Close() error {
    return f.stream.Close()
}

// Exclude returns a stream that excludes entries matching the predicate
func Exclude(stream EntryStream, predicate Predicate) EntryStream {
    return Filter(stream, Not(predicate))
}

// Take returns a stream that yields at most n entries
func Take(stream EntryStream, n int) EntryStream {
    return &takeStream{
        stream: stream,
        limit:  n,
    }
}

type takeStream struct {
    stream EntryStream
    limit  int
    count  int
}

func (t *takeStream) Next(ctx context.Context) (*Entry, error) {
    if t.count >= t.limit {
        return nil, io.EOF
    }
    entry, err := t.stream.Next(ctx)
    if err != nil {
        return nil, err
    }
    t.count++
    return entry, nil
}

func (t *takeStream) Close() error {
    return t.stream.Close()
}

// Skip returns a stream that skips the first n entries
func Skip(stream EntryStream, n int) EntryStream {
    return &skipStream{
        stream:   stream,
        toSkip:   n,
        skipped:  0,
    }
}

type skipStream struct {
    stream  EntryStream
    toSkip  int
    skipped int
}

func (s *skipStream) Next(ctx context.Context) (*Entry, error) {
    for s.skipped < s.toSkip {
        _, err := s.stream.Next(ctx)
        if err != nil {
            return nil, err
        }
        s.skipped++
    }
    return s.stream.Next(ctx)
}

func (s *skipStream) Close() error {
    return s.stream.Close()
}

// FilesOnly returns a stream of only regular files
func FilesOnly(stream EntryStream) EntryStream {
    return Filter(stream, IsFile())
}

// DirsOnly returns a stream of only directories
func DirsOnly(stream EntryStream) EntryStream {
    return Filter(stream, IsDir())
}

// WithMaxDepth returns a stream limited to entries at or above the given depth
func WithMaxDepth(stream EntryStream, depth int) EntryStream {
    return Filter(stream, MaxDepth(depth))
}

// WithoutErrors returns a stream excluding entries with errors
func WithoutErrors(stream EntryStream) EntryStream {
    return Filter(stream, NoError())
}

// WithoutHidden returns a stream excluding hidden files
func WithoutHidden(stream EntryStream) EntryStream {
    return Exclude(stream, Hidden())
}
```

---

## Part 5: Stream Consumers

### File: `stream.go` (continued)

```go
// --- Stream Consumers ---

// ForEach calls fn for each entry in the stream
func ForEach(ctx context.Context, stream EntryStream, fn func(*Entry) error) error {
    defer stream.Close()
    for {
        entry, err := stream.Next(ctx)
        if err == io.EOF {
            return nil
        }
        if err != nil {
            return err
        }
        if err := fn(entry); err != nil {
            return err
        }
    }
}

// Collect gathers all entries into a slice
func Collect(ctx context.Context, stream EntryStream) ([]*Entry, error) {
    defer stream.Close()
    var entries []*Entry
    for {
        entry, err := stream.Next(ctx)
        if err == io.EOF {
            return entries, nil
        }
        if err != nil {
            return entries, err
        }
        entries = append(entries, entry)
    }
}

// Count returns the number of entries in the stream
func StreamCount(ctx context.Context, stream EntryStream) (int64, error) {
    defer stream.Close()
    var count int64
    for {
        _, err := stream.Next(ctx)
        if err == io.EOF {
            return count, nil
        }
        if err != nil {
            return count, err
        }
        count++
    }
}

// First returns the first entry matching the predicate, or nil if none
func First(ctx context.Context, stream EntryStream, predicate Predicate) (*Entry, error) {
    defer stream.Close()
    filtered := Filter(stream, predicate)
    entry, err := filtered.Next(ctx)
    if err == io.EOF {
        return nil, nil
    }
    return entry, err
}

// Any returns true if any entry matches the predicate
func Any(ctx context.Context, stream EntryStream, predicate Predicate) (bool, error) {
    entry, err := First(ctx, stream, predicate)
    if err != nil {
        return false, err
    }
    return entry != nil, nil
}

// All returns true if all entries match the predicate
func All(ctx context.Context, stream EntryStream, predicate Predicate) (bool, error) {
    defer stream.Close()
    for {
        entry, err := stream.Next(ctx)
        if err == io.EOF {
            return true, nil
        }
        if err != nil {
            return false, err
        }
        if !predicate(entry) {
            return false, nil
        }
    }
}

// Reduce applies a function to each entry, accumulating a result
func Reduce[T any](ctx context.Context, stream EntryStream, initial T, fn func(T, *Entry) T) (T, error) {
    defer stream.Close()
    result := initial
    for {
        entry, err := stream.Next(ctx)
        if err == io.EOF {
            return result, nil
        }
        if err != nil {
            return result, err
        }
        result = fn(result, entry)
    }
}

// TotalSize returns the sum of all file sizes in the stream
func TotalSize(ctx context.Context, stream EntryStream) (int64, error) {
    return Reduce(ctx, stream, int64(0), func(total int64, e *Entry) int64 {
        return total + e.Size()
    })
}
```

---

## Part 6: Find Builder

### File: `find.go`

The Find Builder provides a fluent API for building complex file searches.

```go
package fstools

import (
    "context"
    "regexp"
    "time"

    "github.com/absfs/absfs"
)

// Finder provides a fluent API for finding files
type Finder struct {
    fs         absfs.Filer
    roots      []string
    predicates []Predicate
    limit      int
    skip       int
    fast       bool
}

// Find creates a new Finder for the given filesystem and roots
func Find(fs absfs.Filer, roots ...string) *Finder {
    if len(roots) == 0 {
        roots = []string{"/"}
    }
    return &Finder{
        fs:    fs,
        roots: roots,
        limit: -1,  // No limit
    }
}

// --- Chainable Filter Methods ---

// Name matches files whose name matches the glob pattern
func (f *Finder) Name(pattern string) *Finder {
    f.predicates = append(f.predicates, MatchGlob(pattern))
    return f
}

// NameRegex matches files whose name matches the regex
func (f *Finder) NameRegex(pattern string) *Finder {
    re := regexp.MustCompile(pattern)
    f.predicates = append(f.predicates, MatchRegex(re))
    return f
}

// Extension matches files with any of the given extensions
func (f *Finder) Extension(exts ...string) *Finder {
    f.predicates = append(f.predicates, HasExtension(exts...))
    return f
}

// Type filters by file type
func (f *Finder) Type(fileType FileType) *Finder {
    switch fileType {
    case TypeFile:
        f.predicates = append(f.predicates, IsFile())
    case TypeDir:
        f.predicates = append(f.predicates, IsDir())
    case TypeSymlink:
        f.predicates = append(f.predicates, IsSymlink())
    }
    return f
}

// FileType constants
type FileType int

const (
    TypeFile FileType = iota
    TypeDir
    TypeSymlink
)

// Files filters to only regular files
func (f *Finder) Files() *Finder {
    return f.Type(TypeFile)
}

// Dirs filters to only directories
func (f *Finder) Dirs() *Finder {
    return f.Type(TypeDir)
}

// MinSize matches files at least the given size
func (f *Finder) MinSize(size int64) *Finder {
    f.predicates = append(f.predicates, SizeGreaterThan(size-1))
    return f
}

// MaxSize matches files at most the given size
func (f *Finder) MaxSize(size int64) *Finder {
    f.predicates = append(f.predicates, SizeLessThan(size+1))
    return f
}

// SizeBetween matches files with size in range
func (f *Finder) SizeBetween(min, max int64) *Finder {
    f.predicates = append(f.predicates, SizeBetween(min, max))
    return f
}

// ModifiedAfter matches files modified after the time
func (f *Finder) ModifiedAfter(t time.Time) *Finder {
    f.predicates = append(f.predicates, ModifiedAfter(t))
    return f
}

// ModifiedBefore matches files modified before the time
func (f *Finder) ModifiedBefore(t time.Time) *Finder {
    f.predicates = append(f.predicates, ModifiedBefore(t))
    return f
}

// ModifiedWithin matches files modified within the duration
func (f *Finder) ModifiedWithin(d time.Duration) *Finder {
    f.predicates = append(f.predicates, ModifiedWithin(d))
    return f
}

// Depth limits to entries at exactly this depth
func (f *Finder) Depth(depth int) *Finder {
    f.predicates = append(f.predicates, DepthEquals(depth))
    return f
}

// MaxDepth limits how deep to search
func (f *Finder) MaxDepth(depth int) *Finder {
    f.predicates = append(f.predicates, MaxDepth(depth))
    return f
}

// IncludeHidden includes hidden files (excluded by default)
func (f *Finder) IncludeHidden() *Finder {
    // Remove hidden filter if present (this is a no-op since hidden isn't excluded by default)
    return f
}

// ExcludeHidden excludes hidden files
func (f *Finder) ExcludeHidden() *Finder {
    f.predicates = append(f.predicates, Not(Hidden()))
    return f
}

// Where adds a custom predicate
func (f *Finder) Where(predicate Predicate) *Finder {
    f.predicates = append(f.predicates, predicate)
    return f
}

// Limit limits the number of results
func (f *Finder) Limit(n int) *Finder {
    f.limit = n
    return f
}

// Skip skips the first n results
func (f *Finder) Skip(n int) *Finder {
    f.skip = n
    return f
}

// Fast uses parallel walking
func (f *Finder) Fast() *Finder {
    f.fast = true
    return f
}

// --- Terminal Methods ---

// Stream returns an EntryStream of matching entries
func (f *Finder) Stream() EntryStream {
    var streams []EntryStream
    for _, root := range f.roots {
        s := WalkStream(f.fs, root, &Options{Fast: f.fast})
        streams = append(streams, s)
    }

    stream := streams[0]
    if len(streams) > 1 {
        stream = Merge(streams...)
    }

    // Apply predicates
    if len(f.predicates) > 0 {
        stream = Filter(stream, And(f.predicates...))
    }

    // Apply skip
    if f.skip > 0 {
        stream = Skip(stream, f.skip)
    }

    // Apply limit
    if f.limit >= 0 {
        stream = Take(stream, f.limit)
    }

    return stream
}

// All returns all matching entries
func (f *Finder) All(ctx context.Context) ([]*Entry, error) {
    return Collect(ctx, f.Stream())
}

// First returns the first matching entry, or nil
func (f *Finder) First(ctx context.Context) (*Entry, error) {
    stream := f.Stream()
    defer stream.Close()
    entry, err := stream.Next(ctx)
    if err == io.EOF {
        return nil, nil
    }
    return entry, err
}

// Count returns the number of matching entries
func (f *Finder) Count(ctx context.Context) (int64, error) {
    return StreamCount(ctx, f.Stream())
}

// Exists returns true if any matching entry exists
func (f *Finder) Exists(ctx context.Context) (bool, error) {
    entry, err := f.First(ctx)
    return entry != nil, err
}

// Do calls fn for each matching entry
func (f *Finder) Do(ctx context.Context, fn func(*Entry) error) error {
    return ForEach(ctx, f.Stream(), fn)
}

// Paths returns just the paths of matching entries
func (f *Finder) Paths(ctx context.Context) ([]string, error) {
    entries, err := f.All(ctx)
    if err != nil {
        return nil, err
    }
    paths := make([]string, len(entries))
    for i, e := range entries {
        paths[i] = e.Path
    }
    return paths, nil
}
```

### Usage Examples

```go
// Find all Go files
entries, _ := Find(fs, "/src").Extension(".go").All(ctx)

// Find large log files modified recently
entries, _ := Find(fs, "/var/log").
    Extension(".log").
    MinSize(10 * 1024 * 1024).  // 10MB
    ModifiedWithin(24 * time.Hour).
    All(ctx)

// Find first matching config file
entry, _ := Find(fs, "/etc", "/home/user").
    Name("config.*").
    Files().
    First(ctx)

// Count directories
count, _ := Find(fs, "/project").
    Dirs().
    ExcludeHidden().
    Count(ctx)

// Process files as stream (memory efficient)
Find(fs, "/data").
    Files().
    MaxDepth(3).
    Do(ctx, func(e *Entry) error {
        // Process each file
        return nil
    })
```

### Tests: `find_test.go`

```go
func TestFind_Basic(t *testing.T) {
    // Create filesystem with known structure
    // Find all files
    // Verify correct results
}

func TestFind_Filters(t *testing.T) {
    // Test each filter method
    // Test combined filters
}

func TestFind_Limit(t *testing.T) {
    // Test Limit
    // Test Skip
    // Test Skip + Limit
}

func TestFind_MultipleRoots(t *testing.T) {
    // Test with multiple root directories
}

func TestFind_Cancellation(t *testing.T) {
    // Test context cancellation
}
```

---

## Part 7: Context Support

### Updates to Existing Functions

Add context support to existing Walk, Copy functions:

```go
// walk.go additions

// WalkContext is Walk with context support
func WalkContext(ctx context.Context, fs absfs.Filer, root string, fn WalkFunc) error {
    return walkDir(ctx, fs, root, func(path string, info os.FileInfo, err error) error {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
            return fn(path, info, err)
        }
    })
}

// copy.go additions

// CopyContext is Copy with context support
func CopyContext(ctx context.Context, src, dst absfs.FileSystem, srcPath, dstPath string, opts *CopyOptions) error {
    // Check context before each operation
    if ctx.Err() != nil {
        return ctx.Err()
    }
    // ... existing copy logic with context checks
}
```

---

## Implementation Order

1. **entry.go** - Entry type (no dependencies)
2. **predicate.go** - Predicates (depends on Entry)
3. **stream.go** - EntryStream + transformers + consumers (depends on Entry, Predicate)
4. **find.go** - Find builder (depends on all above)
5. **Context updates** - Add to existing functions

---

## Testing Checklist

- [ ] Entry type with all properties
- [ ] WalkStream basic operation
- [ ] WalkStream cancellation
- [ ] FromSlice and Merge streams
- [ ] All predicate types
- [ ] Predicate combinators (And, Or, Not)
- [ ] Filter, Take, Skip transformers
- [ ] ForEach, Collect, Count, First consumers
- [ ] Find builder with all filter methods
- [ ] Find with multiple roots
- [ ] Context cancellation throughout
- [ ] Benchmarks for stream vs callback walk

---

## Success Criteria

Phase 1 is complete when:

1. All tests pass with `-race`
2. Benchmarks show stream overhead < 10% vs direct walk
3. Memory usage is bounded (no full materialization for streams)
4. Context cancellation works at all levels
5. Find builder can express common search patterns fluently
