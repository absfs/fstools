# C4 Integration Documentation

**Status**: Speculative / Future Phase
**Depends On**: Phase 1-3, c4 ecosystem development
**Related**: [c4 PROPOSAL-absfs-integration.md](/Users/joshua/ws/active/c4/PROPOSAL-absfs-integration.md)

---

## Overview

This document describes how fstools will integrate with the c4 ecosystem to provide content-addressable enhancements. This is Phase 4 work and depends on:

1. Completion of fstools Phases 1-3
2. Implementation of c4 ecosystem features (ManifestStore, c4ops, etc.)

**Note**: This is speculative documentation. Implementation details may change based on c4 ecosystem development.

---

## The Integration Model

fstools remains generic - it works with any absfs.Filer. Content-addressable capabilities are **explicitly provided** when available, not detected through interfaces.

```
┌─────────────────────────────────────────────────────────┐
│                    Application                           │
├─────────────────────────────────────────────────────────┤
│                                                          │
│   fstools (generic)           c4m (content-aware)       │
│   ├── Walk, Copy, Find        ├── ManifestStore         │
│   ├── Sync, Diff              ├── GenerateFromFiler     │
│   ├── EntryStream             ├── Diff (manifest)       │
│   └── Atomic                  └── ID mappings           │
│                                                          │
│              │                        │                  │
│              │ Filer interface        │ Optional         │
│              │                        │ enhancement      │
│              ▼                        ▼                  │
│   ┌────────────────────────────────────────────┐        │
│   │         absfs composition stack             │        │
│   │   lockfs(cachefs(encryptfs(c4fs)))         │        │
│   └────────────────────────────────────────────┘        │
│                                                          │
│   User retains references to layers for direct access   │
│                                                          │
└─────────────────────────────────────────────────────────┘
```

---

## Part 1: Enhanced Sync with Manifests

When manifest stores are available, Sync can skip expensive file comparisons.

### SyncOptions Extension

```go
// Extended SyncOptions with manifest support
type SyncOptions struct {
    // ... existing fields from Phase 3 ...

    // Optional: ManifestStore for source filesystem
    // If provided, uses manifest for comparison instead of stat/read
    SourceManifest c4m.ManifestStore

    // Optional: ManifestStore for destination filesystem
    DestManifest c4m.ManifestStore

    // UpdateManifests updates manifest stores after sync
    UpdateManifests bool
}
```

### Implementation Pattern

```go
func Sync(ctx context.Context, srcFS absfs.Filer, dstFS absfs.FileSystem, srcRoot, dstRoot string, opts *SyncOptions) (*SyncResult, error) {
    // If both manifests available, use manifest-based diff (fast)
    if opts.SourceManifest != nil && opts.DestManifest != nil {
        return syncWithManifests(ctx, srcFS, dstFS, srcRoot, dstRoot, opts)
    }

    // If only source manifest, use hybrid approach
    if opts.SourceManifest != nil {
        return syncWithSourceManifest(ctx, srcFS, dstFS, srcRoot, dstRoot, opts)
    }

    // Fall back to generic sync (Phase 3)
    return syncGeneric(ctx, srcFS, dstFS, srcRoot, dstRoot, opts)
}

func syncWithManifests(ctx context.Context, srcFS absfs.Filer, dstFS absfs.FileSystem, srcRoot, dstRoot string, opts *SyncOptions) (*SyncResult, error) {
    // 1. Snapshot both manifests (MVCC - no locks held during diff)
    srcSnap, _ := opts.SourceManifest.Snapshot()
    defer srcSnap.Release()

    dstSnap, _ := opts.DestManifest.Snapshot()
    defer dstSnap.Release()

    // 2. Diff manifests (fast - just comparing C4 IDs)
    diff := diffManifestSnapshots(srcSnap, dstSnap, srcRoot, dstRoot)

    // 3. Apply changes (same as Phase 3, but using C4 IDs)
    // ...

    // 4. Update destination manifest if requested
    if opts.UpdateManifests {
        for _, entry := range diff.Added {
            opts.DestManifest.Set(entry.Path, &c4m.Entry{
                ID: entry.SourceID,
                // ...
            })
        }
    }

    return result, nil
}
```

### Usage

```go
// Setup with manifests
srcManifest := c4m.NewManifestStore(c4kv.NewSQLite("/src/.c4/manifest.db"))
dstManifest := c4m.NewManifestStore(c4kv.NewSQLite("/dst/.c4/manifest.db"))

// Fast sync using manifests
result, err := Sync(ctx, srcFS, dstFS, "/src", "/dst", &SyncOptions{
    SourceManifest:  srcManifest,
    DestManifest:    dstManifest,
    UpdateManifests: true,
})

// Manifest comparison: O(n) string compares
// vs File comparison: O(n) stat calls + potentially O(bytes) reads
```

---

## Part 2: Enhanced Diff with Manifests

```go
// DiffOptions extension
type DiffOptions struct {
    // ... existing fields ...

    // Optional: Use manifest for comparison
    SourceManifest c4m.ManifestStore
    DestManifest   c4m.ManifestStore
}

func Diff(ctx context.Context, srcFS, dstFS absfs.Filer, srcRoot, dstRoot string, opts *DiffOptions) (*DiffResult, error) {
    if opts.SourceManifest != nil && opts.DestManifest != nil {
        return diffWithManifests(ctx, opts)
    }
    return diffGeneric(ctx, srcFS, dstFS, srcRoot, dstRoot, opts)
}

func diffWithManifests(ctx context.Context, opts *DiffOptions) (*DiffResult, error) {
    result := &DiffResult{}

    srcSnap, _ := opts.SourceManifest.Snapshot()
    defer srcSnap.Release()

    dstSnap, _ := opts.DestManifest.Snapshot()
    defer dstSnap.Release()

    // Build destination map
    dstEntries := make(map[string]*c4m.Entry)
    dstSnap.Range("", func(path string, entry *c4m.Entry) bool {
        dstEntries[path] = entry
        return true
    })

    // Compare
    srcSnap.Range("", func(path string, srcEntry *c4m.Entry) bool {
        dstEntry, exists := dstEntries[path]

        if !exists {
            result.Added = append(result.Added, &DiffEntry{
                Type:     DiffAdded,
                Path:     path,
                SourceID: srcEntry.ID.String(),
            })
            return true
        }

        // Compare by C4 ID - O(1) string compare!
        if srcEntry.ID != dstEntry.ID {
            result.Modified = append(result.Modified, &DiffEntry{
                Type:     DiffModified,
                Path:     path,
                SourceID: srcEntry.ID.String(),
                DestID:   dstEntry.ID.String(),
            })
        } else {
            result.Unchanged++
        }

        delete(dstEntries, path)
        return true
    })

    // Remaining are removed
    for path, entry := range dstEntries {
        result.Removed = append(result.Removed, &DiffEntry{
            Type:   DiffRemoved,
            Path:   path,
            DestID: entry.ID.String(),
        })
    }

    return result, nil
}
```

---

## Part 3: Content Deduplication

When source and destination share a content store, sync can deduplicate:

```go
// DeduplicatingSyncOptions extends SyncOptions for content-addressed storage
type DeduplicatingSyncOptions struct {
    SyncOptions

    // Content stores (if same store, can deduplicate)
    SourceStore c4.Store
    DestStore   c4.Store
}

func SyncWithDedup(ctx context.Context, srcFS absfs.Filer, dstFS absfs.FileSystem, srcRoot, dstRoot string, opts *DeduplicatingSyncOptions) (*SyncResult, error) {
    // If both use same content store, we can skip copying
    if opts.SourceStore == opts.DestStore {
        return syncDeduplicated(ctx, srcFS, dstFS, srcRoot, dstRoot, opts)
    }

    // Check if destination store already has content
    // (common in distributed systems)
    // ...
}

func syncDeduplicated(ctx context.Context, srcFS absfs.Filer, dstFS absfs.FileSystem, srcRoot, dstRoot string, opts *DeduplicatingSyncOptions) (*SyncResult, error) {
    diff, _ := diffWithManifests(ctx, opts.SourceManifest, opts.DestManifest)

    for _, entry := range diff.Added {
        // Check if content exists in shared store
        if opts.DestStore.Has(entry.SourceID) {
            // Content already exists - just update manifest
            opts.DestManifest.Set(entry.Path, &c4m.Entry{ID: entry.SourceID})
            result.Deduplicated++
        } else {
            // Actually copy content
            // ...
        }
    }

    return result, nil
}
```

---

## Part 4: Offline / Airgapped Workflows

For systems that can't communicate directly, manifests enable offline sync:

### Export State

```go
// ExportManifest exports filesystem state for offline transfer
func ExportManifest(ctx context.Context, fs absfs.Filer, root string, w io.Writer) error {
    // Generate manifest
    gen := c4m.NewGenerator(nil, fs) // No store - streaming
    manifest := gen.GenerateFull(ctx, root)

    // Serialize to portable format
    return c4m.WriteTo(manifest, w)
}

// Usage:
// System A: Export state
f, _ := os.Create("state.c4m")
ExportManifest(ctx, srcFS, "/data", f)
// Transfer state.c4m to System B via sneakernet
```

### Import and Diff

```go
// On System B:
remoteManifest, _ := c4m.ReadFrom(remoteFile)
localManifest, _ := c4m.GenerateFromFiler(ctx, localFS, "/data")

diff := c4m.Diff(remoteManifest, localManifest)

// Export list of needed content IDs
neededIDs := collectNeededIDs(diff)
writeIDList(neededIDs, "needed.txt")
// Transfer needed.txt back to System A
```

### Export Content

```go
// On System A: Export requested content
ids := readIDList("needed.txt")
f, _ := os.Create("content.tar")
c4.ExportContent(store, ids, f)
// Transfer content.tar to System B
```

### Import Content

```go
// On System B: Import content and update filesystem
c4.ImportContent(store, contentFile)

// Apply to filesystem
for _, entry := range diff.Added {
    content, _ := store.Get(entry.ID)
    // Write to filesystem
}
```

### fstools Helper Functions

```go
// PrepareOfflineSync generates manifest and exports needed data
func PrepareOfflineSync(ctx context.Context, fs absfs.Filer, root string, outputDir string) error {
    manifestPath := filepath.Join(outputDir, "manifest.c4m")
    // Generate and write manifest
    // ...
}

// ApplyOfflineSync applies transferred content to filesystem
func ApplyOfflineSync(ctx context.Context, fs absfs.FileSystem, root string, inputDir string) (*SyncResult, error) {
    // Read manifest and content
    // Apply to filesystem
    // ...
}
```

---

## Part 5: Eventually Consistent Filesystems

For distributed filesystems or replicated storage that doesn't provide strong consistency:

### Vector Clocks / Version Tracking

```go
// Extended Entry with version information
type VersionedEntry struct {
    *c4m.Entry
    Version     VectorClock
    LastUpdated time.Time
    UpdatedBy   string  // Node/client ID
}

// VectorClock for conflict detection
type VectorClock map[string]int64

func (v VectorClock) Increment(nodeID string) {
    v[nodeID]++
}

func (v VectorClock) Merge(other VectorClock) VectorClock {
    result := make(VectorClock)
    for k, val := range v {
        result[k] = val
    }
    for k, val := range other {
        if val > result[k] {
            result[k] = val
        }
    }
    return result
}

func (v VectorClock) Conflicts(other VectorClock) bool {
    vGreater := false
    otherGreater := false
    for k := range v {
        if v[k] > other[k] {
            vGreater = true
        } else if v[k] < other[k] {
            otherGreater = true
        }
    }
    for k := range other {
        if _, exists := v[k]; !exists {
            otherGreater = true
        }
    }
    return vGreater && otherGreater
}
```

### Conflict Resolution

```go
// ConflictStrategy determines how to handle conflicts
type ConflictStrategy int

const (
    ConflictLastWriteWins ConflictStrategy = iota
    ConflictFirstWriteWins
    ConflictKeepBoth
    ConflictManual
)

// MergeOptions for multi-source merging
type MergeOptions struct {
    Strategy     ConflictStrategy
    OnConflict   func(path string, versions []*VersionedEntry) (*VersionedEntry, error)
}

// MergeManifests merges multiple manifests with conflict detection
func MergeManifests(manifests map[string]*c4m.Manifest, opts *MergeOptions) (*c4m.Manifest, []Conflict, error) {
    merged := c4m.NewManifest()
    var conflicts []Conflict

    // Collect all paths
    allPaths := make(map[string][]*VersionedEntry)
    for source, manifest := range manifests {
        manifest.Range("", func(path string, entry *c4m.Entry) bool {
            allPaths[path] = append(allPaths[path], &VersionedEntry{
                Entry:   entry,
                Source:  source,
            })
            return true
        })
    }

    // Resolve each path
    for path, versions := range allPaths {
        if len(versions) == 1 {
            merged.Set(path, versions[0].Entry)
            continue
        }

        // Check for conflicts
        if hasConflict(versions) {
            resolved, err := resolveConflict(path, versions, opts)
            if err != nil {
                return nil, nil, err
            }
            if resolved != nil {
                merged.Set(path, resolved.Entry)
            }
            conflicts = append(conflicts, Conflict{
                Path:     path,
                Versions: versions,
                Resolved: resolved,
            })
        } else {
            // No conflict - take latest
            merged.Set(path, latestVersion(versions).Entry)
        }
    }

    return merged, conflicts, nil
}
```

### Multi-Site Sync

```go
// SyncMultiSite synchronizes multiple sites
func SyncMultiSite(ctx context.Context, sites map[string]*SiteConnection, opts *MultiSiteSyncOptions) error {
    // 1. Gather manifests from all sites
    manifests := make(map[string]*c4m.Manifest)
    for name, site := range sites {
        manifests[name], _ = site.GetManifest()
    }

    // 2. Merge with conflict resolution
    merged, conflicts, _ := MergeManifests(manifests, &MergeOptions{
        Strategy: opts.ConflictStrategy,
    })

    // 3. Handle unresolved conflicts
    for _, conflict := range conflicts {
        if conflict.Resolved == nil {
            resolved, _ := opts.OnConflict(conflict)
            merged.Set(conflict.Path, resolved)
        }
    }

    // 4. Push merged state to all sites
    for name, site := range sites {
        diff := c4m.Diff(merged, manifests[name])
        // Transfer missing content and update manifest
        site.ApplyDiff(diff, merged)
    }

    return nil
}
```

---

## Part 6: Integration Points Summary

### fstools Functions Enhanced by c4

| Function | Without c4 | With c4 |
|----------|-----------|---------|
| **Diff** | size+mtime or byte compare | C4 ID compare (O(1) per file) |
| **Sync** | Full file copy | Skip if content exists |
| **Equal** | Byte compare | ID compare |
| **Find** | Walk + filter | Manifest query |
| **Dedup** | Hash all files | Use existing IDs |

### c4m Functions that Consume absfs

| Function | Purpose |
|----------|---------|
| **GenerateFromFiler** | Create manifest from any filesystem |
| **DiffManifests** | Compare two manifests |
| **MergeManifests** | Combine with conflict resolution |
| **ExportManifest** | Portable manifest format |

### Data Flow

```
absfs.Filer ─────┐
                 │
                 ├──► c4m.GenerateFromFiler ──► ManifestStore
                 │
                 ├──► fstools.Walk ──► EntryStream
                 │
                 └──► fstools.Copy ──► File operations

ManifestStore ───┐
                 │
                 ├──► c4m.Diff ──► DiffResult
                 │
                 ├──► fstools.Sync (enhanced) ──► SyncResult
                 │
                 └──► c4m.Export ──► Portable format
```

---

## Part 7: Testing Strategy

### Unit Tests
- Sync with mock ManifestStore
- Diff with manifests vs diff with files
- Conflict resolution strategies
- Version clock operations

### Integration Tests
- Generate manifest from memfs
- Sync between filesystems using manifests
- Round-trip export/import
- Multi-site merge scenarios

### Performance Tests
- Manifest diff vs file diff (speedup)
- Deduplication effectiveness
- Large manifest handling

---

## Part 8: Migration Path

### For Existing fstools Users

No changes required. All Phase 1-3 functionality works without c4.

### Adding c4 Support

```go
// Before: Generic sync
result, _ := fstools.Sync(ctx, srcFS, dstFS, "/src", "/dst", nil)

// After: Enhanced with manifests
srcManifest := c4m.NewManifestStore(...)
dstManifest := c4m.NewManifestStore(...)

result, _ := fstools.Sync(ctx, srcFS, dstFS, "/src", "/dst", &fstools.SyncOptions{
    SourceManifest: srcManifest,
    DestManifest:   dstManifest,
})
// Same API, faster execution
```

### Gradual Adoption

1. Start using fstools Phase 1-3 features
2. Add c4 manifests to important directories
3. Use manifest-enhanced Sync for those directories
4. Expand manifest coverage as beneficial

---

## Future Considerations

### Alternative Content ID Systems

While this documentation focuses on c4, the patterns could support other content identification:

```go
// Generic content identifier interface
type ContentIDer interface {
    ContentID(path string) (id string, scheme string, err error)
}

// c4fs implements ContentIDer
// ipfs-fuse could implement ContentIDer
// git-annex could implement ContentIDer
```

For now, we're only implementing c4 integration to keep the API simple.

### Bloom Filter Optimization

For large distributed systems, bloom filters can pre-filter sync:

```go
// Check what content remote likely has before requesting
bloom := remote.GetBloomFilter()
for _, id := range neededIDs {
    if bloom.MayContain(id) {
        // Check remote (might have it)
    } else {
        // Definitely doesn't have it - skip check
    }
}
```

### Streaming Manifest Updates

For continuous sync scenarios:

```go
// Watch for manifest changes
changes := srcManifest.Watch("/")
for change := range changes {
    // Apply change to destination
    applyChange(dstFS, dstManifest, change)
}
```

---

## Related Documents

- [DEVELOPMENT_PLAN.md](./DEVELOPMENT_PLAN.md) - Overall fstools development plan
- [PHASE1_IMPLEMENTATION.md](./PHASE1_IMPLEMENTATION.md) - Foundation
- [PHASE2_IMPLEMENTATION.md](./PHASE2_IMPLEMENTATION.md) - Atomic operations
- [PHASE3_IMPLEMENTATION.md](./PHASE3_IMPLEMENTATION.md) - Sync & Diff
- [c4 PROPOSAL-absfs-integration.md](/Users/joshua/ws/active/c4/PROPOSAL-absfs-integration.md) - c4 ecosystem proposal
