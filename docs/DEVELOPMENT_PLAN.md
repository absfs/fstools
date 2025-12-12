# fstools Development Plan

**Status**: Active Development
**Last Updated**: December 2024

---

## Vision

Transform fstools from a basic utility package into a comprehensive, composable filesystem utility library. The goal is to provide the "missing standard library" for filesystem operations across the absfs ecosystem.

### Design Principles

1. **Composability**: Operations chain together naturally (like Unix pipes)
2. **Memory Efficiency**: Handle million-file directories without excessive memory
3. **Cancellation**: All operations respect `context.Context`
4. **Observability**: Progress reporting for long operations
5. **Testability**: Works with any absfs.Filer including memfs
6. **Safety**: Atomic operations prevent corruption
7. **Simplicity**: Don't over-engineer; keep APIs focused

---

## Current State

fstools currently provides:

| Feature | File | Status |
|---------|------|--------|
| Walk | walk.go | Stable |
| FastWalk (parallel) | fastwalk.go | Stable |
| Copy | copy.go | Stable |
| ParallelCopy | parallel_copy.go | Stable |
| Size, Count, Exists, Equal | tools.go | Stable |

Recent improvements:
- Added `Options.Fast` flag to Walk for FastWalk behavior
- Added `CopyOptions.Parallel` flag for parallel copy
- Fixed race conditions in parallel operations

---

## Development Phases

### Phase 1: Foundation
**Focus**: Core abstractions and composable building blocks

- EntryStream lazy iterator pattern
- Composable Predicates with combinators
- Find Builder fluent API
- Context support throughout existing functions

**Why First**: These are foundational patterns that all subsequent features build upon.

### Phase 2: Atomic & Safety
**Focus**: Safe filesystem operations

- Atomic write (write-temp-rename pattern)
- Basic transaction support
- Safe directory operations

**Why Second**: Safety primitives are needed before building complex operations.

### Phase 3: Sync & Diff (Generic)
**Focus**: Comparing and synchronizing filesystems

- Generic Diff (size+mtime, optional byte comparison)
- Generic Sync with multiple strategies
- Patch application

**Why Third**: These are high-value operations that work without content IDs.

### Phase 4: c4 Integration (Deferred)
**Focus**: Content-addressable enhancements

- ManifestStore-aware operations
- Content-ID-enhanced Sync/Diff
- Smart cross-filesystem operations

**Why Deferred**: Depends on c4 ecosystem work (see PROPOSAL-absfs-integration.md in c4 repo).

---

## Phase Details

### Phase 1: Foundation

See: [PHASE1_IMPLEMENTATION.md](./PHASE1_IMPLEMENTATION.md)

**Deliverables**:
1. `Entry` type - Unified filesystem entry representation
2. `EntryStream` interface - Lazy, pull-based iteration
3. `Predicate` type with combinators (And, Or, Not)
4. Stream transformers (Filter, Take, Skip, FilesOnly, DirsOnly)
5. Stream consumers (ForEach, Collect, Count, First, Any)
6. Find Builder API
7. Context support in Walk, FastWalk, Copy

**Non-Goals**:
- Database backends (Phase 4)
- Network operations (Phase 4)
- Platform-specific code

### Phase 2: Atomic & Safety

See: [PHASE2_IMPLEMENTATION.md](./PHASE2_IMPLEMENTATION.md)

**Deliverables**:
1. `AtomicWrite` - Write-temp-rename pattern
2. `AtomicWriteFunc` - Atomic write with callback
3. `SafeMkdirAll` - Atomic directory creation
4. Basic `Transaction` type for multi-file operations

**Non-Goals**:
- Full ACID transactions (filesystem limitations)
- Journaling (too complex for this phase)

### Phase 3: Sync & Diff

See: [PHASE3_IMPLEMENTATION.md](./PHASE3_IMPLEMENTATION.md)

**Deliverables**:
1. `DiffResult` type
2. `Diff` function - Compare two filesystem trees
3. `SyncOptions` with strategies (Mirror, Update, Merge)
4. `Sync` function - Synchronize filesystems
5. `Patch` function - Apply diff results
6. Progress reporting for all operations

**Non-Goals**:
- rsync delta algorithm (requires chunking infrastructure)
- Content-ID optimization (Phase 4)

---

## Future Improvements

These features are documented for future consideration but not scheduled:

### Watch (Change Notification)

```go
type Watcher interface {
    Watch(path string, recursive bool) (<-chan Event, error)
    Close() error
}

type Event struct {
    Type EventType  // Create, Modify, Delete, Rename
    Path string
    Err  error
}
```

**Considerations**:
- Polling fallback works everywhere but is inefficient
- Platform-specific (FSEvents, inotify, ReadDirectoryChangesW) is efficient but complex
- Could be separate package (fswatcher) that integrates with fstools

### Rate Limiting

```go
type RateLimiter interface {
    Wait(ctx context.Context, bytes int64) error
}

// Usage
fstools.Copy(src, dst, srcPath, dstPath, &CopyOptions{
    RateLimiter: NewBandwidthLimiter(10 * MB / time.Second),
})
```

**Use Cases**:
- Network filesystem bandwidth control
- Preventing I/O storms on shared storage
- Background operations that shouldn't impact foreground

### Checkpointing (Resume)

```go
type Checkpoint struct {
    Operation   string    // "copy", "sync"
    Source      string
    Dest        string
    Progress    int64     // Bytes or files completed
    LastPath    string    // Resume point
    StartedAt   time.Time
}

// Resume interrupted operation
fstools.ResumeSync(ctx, checkpoint, src, dst)
```

**Use Cases**:
- Large copy/sync operations that may be interrupted
- Network filesystem operations over unreliable connections
- Long-running background tasks

### Tree Transformations

```go
// Flatten directory structure
fstools.Flatten(fs, "/deep/nested/structure", "/flat")

// Batch rename with pattern
fstools.BatchRename(fs, "/photos", func(path string) string {
    // Transform path
    return newPath
})

// Mirror directory structure without content
fstools.MirrorStructure(src, dst, "/source", "/dest")
```

### Eventually Consistent / Offline Workflows

For filesystems that don't provide strong consistency, or for offline/airgapped sync:

```go
// Export filesystem state as portable manifest
manifest := fstools.ExportState(fs, "/root")
manifest.WriteTo("state.manifest")

// On another system, import and diff
remoteManifest := fstools.ImportState("state.manifest")
diff := fstools.DiffManifests(localManifest, remoteManifest)

// Generate transfer package
fstools.ExportDiff(fs, diff, "transfer.tar")

// On target system, apply transfer
fstools.ImportDiff(fs, "transfer.tar")
```

**Use Cases**:
- Air-gapped security environments
- Intermittent/unreliable connectivity
- Bandwidth-constrained synchronization
- Audit trails and change tracking

This pattern works by:
1. Manifests capture filesystem state (paths, sizes, mtimes, optional hashes)
2. Manifests can be compared offline
3. Only changed content needs to transfer
4. Transfer packages are self-contained

**Note**: Full content-addressable support (c4 integration) makes this much more powerful - see Phase 4 and c4 PROPOSAL-absfs-integration.md.

---

## API Stability

### Stable (won't break)
- Walk, WalkDir, FastWalk signatures
- Copy, ParallelCopy signatures
- Size, Count, Exists, Equal signatures

### Experimental (may change)
- New Phase 1-3 features until v1.0
- Options structs may gain fields
- Internal implementation details

### Guidelines for Contributors
- New features should follow existing patterns
- All public functions need tests
- All operations should respect context cancellation
- Progress reporting should be optional (nil callback = no reporting)

---

## Testing Strategy

### Unit Tests
- Each function has corresponding `_test.go`
- Use memfs for fast, isolated tests
- Test error conditions, not just happy path

### Integration Tests
- Test with osfs on real filesystem
- Test cross-filesystem operations (memfs -> osfs)
- Test large directory handling (10k+ files)

### Benchmarks
- Existing benchmarks in `benchmark_test.go`
- Compare with stdlib filepath.Walk, filepath.WalkDir
- Measure memory allocations
- Test scaling with directory size

### Race Detection
- All tests run with `-race` in CI
- Parallel operations must be race-free

---

## Dependencies

### Current
- `github.com/absfs/absfs` - Core filesystem interface
- `github.com/absfs/memfs` - Testing
- `github.com/absfs/osfs` - Testing

### Phase 4 (Future)
- `github.com/absfs/c4` - Content identification
- `github.com/absfs/c4/c4m` - Manifest operations

### Avoided
- No CGO dependencies
- No platform-specific code in core (wrappers ok)
- Minimal external dependencies

---

## File Organization

Current:
```
fstools/
├── walk.go           # Walk, WalkDir, walkDir
├── fastwalk.go       # FastWalk, FastWalkWithConfig
├── copy.go           # Copy
├── parallel_copy.go  # ParallelCopy
├── tools.go          # Size, Count, Exists, Equal
└── *_test.go         # Tests
```

After Phase 3:
```
fstools/
├── entry.go          # Entry type
├── stream.go         # EntryStream, transformers
├── predicate.go      # Predicate, combinators
├── find.go           # Find builder
├── walk.go           # Walk functions (existing)
├── fastwalk.go       # FastWalk (existing)
├── copy.go           # Copy (existing)
├── parallel_copy.go  # ParallelCopy (existing)
├── atomic.go         # AtomicWrite, transactions
├── diff.go           # Diff operations
├── sync.go           # Sync operations
├── tools.go          # Utilities (existing)
├── progress.go       # Progress reporting
├── docs/             # Documentation
│   ├── DEVELOPMENT_PLAN.md
│   ├── PHASE1_IMPLEMENTATION.md
│   ├── PHASE2_IMPLEMENTATION.md
│   ├── PHASE3_IMPLEMENTATION.md
│   └── C4_INTEGRATION.md
└── *_test.go         # Tests
```

---

## Success Metrics

After completing Phases 1-3, fstools should:

1. **Pass all tests** with race detector enabled
2. **Handle large directories** (100k files) without OOM
3. **Cancel cleanly** when context is cancelled
4. **Report progress** for operations taking >1 second
5. **Match or beat** stdlib performance for equivalent operations
6. **Work with any Filer** including memfs, osfs, s3fs, etc.
7. **Have clear documentation** with examples

---

## Related Documents

- [PHASE1_IMPLEMENTATION.md](./PHASE1_IMPLEMENTATION.md) - Foundation implementation details
- [PHASE2_IMPLEMENTATION.md](./PHASE2_IMPLEMENTATION.md) - Atomic operations details
- [PHASE3_IMPLEMENTATION.md](./PHASE3_IMPLEMENTATION.md) - Sync & Diff details
- [C4_INTEGRATION.md](./C4_INTEGRATION.md) - Content-addressable integration
- [c4 PROPOSAL-absfs-integration.md](/Users/joshua/ws/active/c4/PROPOSAL-absfs-integration.md) - c4 ecosystem proposal
