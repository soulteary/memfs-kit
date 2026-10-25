# Performance

Version 1.1.0 introduced allocation and directory improvements, including the
standard `fs.StatFS` and `fs.ReadDirFS` optional interfaces. Version 1.1.1 further
reduces small-read and directory-entry metadata overhead. Both preserve the
filesystem's snapshot and hook behavior without adding dependencies or changing
existing method signatures. The results below distinguish the two releases and
their baselines.

## v1.1.0 optimized paths

- `Stat` captures metadata with one tree lookup. Directory metadata no longer
  builds or sorts its children. Regular and virtual files still execute the
  hook, including its metadata changes and errors, without constructing a file
  handle or copying content that will be discarded.
- Hooks still receive a private input copy. When their output equals stored
  content, an open handle reuses the original immutable snapshot. Different
  output is copied before becoming handle content. Metadata-only operations
  do not retain output, so they do not need an output copy.
- Path traversal uses `strings.SplitSeq` instead of allocating a `[]string`.
  Each file handle embeds its `bytes.Reader`; `Close` resets it to release its
  reference to the content snapshot.
- Directory opens construct full child paths only for regular files with a
  hook. `ReadDir` returns the fresh entry slice of a private directory handle,
  avoiding the fallback helper's slice copy and second sort. A handle's
  paginated `ReadDir` still copies returned slices to protect subsequent pages.

`Open` keeps a direct implementation to avoid extra calls and large return-value
copies on the ordinary file-opening path. `Stat` performs its own single lookup;
regression tests compare its behavior with `Open` followed by file `Stat`.

The global tree lock and `WriteFile`'s content copy remain unchanged. The
v1.1.1 section below describes reduced small-read and directory-metadata
overhead while retaining per-handle locking. `ReadAt` synchronization and directory-sort caching
are unchanged.

## Preserved behavior

Opened files and directory entries continue to describe the content and metadata
captured when their snapshots were created. Later writes cannot mutate them.
Hook input and returned bytes retained by application code cannot mutate stored
or opened content, including when an equal hook output permits snapshot reuse.

Directory listing does not execute hooks. Entries without a regular-file hook
already own their captured metadata; `Info` returns that immutable snapshot.
For a regular-file entry with a hook, its first `Info` resolves and caches the
metadata or error, including across concurrent calls. A hook panic is cached
and rethrown with the same value on later calls. Separate listings have separate
hook caches. Hooks execute outside tree and handle locks and may write to the filesystem. `FS.Stat` on regular or missing paths invokes the
hook on each call, like the previous `fs.Stat` fallback; it does not cache
external application state. `File.Stat` returns its already-captured metadata.

The new methods preserve the old helper errors, including the `open` operation
in lookup errors, original missing-path errors passed through a hook, and
a `readdir` path error with `not implemented` after successfully opening a regular
or virtual file. Subtree root names remain `.`, and hooks retain original-root-relative
paths. Empty directory results are non-nil, sorted result slices belong to their
caller, and entries remain usable after their private directory handle closes.

## Wrappers and optional interfaces

Standard helpers now dispatch to `Stat` and `ReadDir` directly. A wrapper that
anonymously embeds `*memfs.FS` also promotes these methods. If it overrides `Open`
to change behavior, it should explicitly override the optional methods too, or
hide them when requesting fallback dispatch:

```go
info, err := fs.Stat(struct{ fs.FS }{wrapped}, name)
entries, err := fs.ReadDir(struct{ fs.FS }{wrapped}, name)
```

The anonymous wrapper exposes only `Open`, so these helpers use the wrapper's
custom `Open` method. This is the standard optional-interface behavior of `io/fs`.

## Reproducing the v1.1.0 comparison

The baseline is commit `39284834af24fe80d4b607f2746813c8b7a41ea4`, compared with
v1.1.0. Use `benchmark_test.go` from the v1.1.0 tag for both revisions; copy only
that file into a clean baseline checkout. This reproduces the 27 historical
cases below rather than including later v1.1.1 benchmarks. Setup is outside
timed sections. Hook transformations mutate their private input in place, adding no allocation of their own. First-`Info`
benchmarks create a fresh directory listing on every iteration to avoid timing
the cached result.

```sh
go test -run='^$' -bench=. -benchmem -benchtime=200ms -count=5 -cpu=1 .
```

Compare `B/op` and `allocs/op` as well as time. Timings depend on CPU scheduling,
frequency, garbage collection, and the workload. A lower allocation count is not
an application-throughput guarantee. Benchmark results are observations, not CI
performance thresholds.

## v1.1.0 allocation results

Measured on 2026-10-25 with Go 1.27.3, darwin/arm64, Apple M5, using the
command above. Both revisions used the same benchmark source. The 27 cases
each ran five samples; allocation counts and bytes were consistent across
samples. The changes were measured together, so these are combined results.

| Operation | Before B/op | After B/op | Before allocs/op | After allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Open/close, one-component path | 160 | 128 | 3 | 1 |
| Open/close, twelve-component path | 336 | 128 | 3 | 1 |
| File metadata through fs.Stat | 224 | 64 | 4 | 1 |
| Directory metadata, 1,000 entries | 96,592 | 64 | 2004 | 1 |
| Directory listing, 1,000 entries | 112,912 | 80,512 | 2004 | 1002 |
| Open/close, passthrough hook, 1 MiB | 2,097,328 | 1,048,704 | 5 | 2 |
| Open/close, transforming hook, 1 MiB | 2,097,328 | 2,097,280 | 5 | 3 |
| Listing + first Info, transforming hook, 1 MiB | 2,097,704 | 1,049,088 | 12 | 9 |

The unchanged two full-size copies in the transforming-hook Open case are
intentional: one protects stored input and the other isolates the returned
content. Metadata-only calls avoid the output copy for both hook cases.

Timing samples varied, including the unchanged file-handle Stat control.
To check the Open hot path, an
additional same-process comparison alternated baseline and optimized versions
for three pairs, reversing the middle pair. Median Open/close times decreased
from 60.30 ns to 47.31 ns for a one-component path and from 241.3 ns to 164.5 ns
for a twelve-component path. Transforming-hook Open for 1 MiB was approximately
unchanged (113.7 us versus 112.7 us), consistent with retaining both data copies.
These timing observations are local measurements, not throughput promises.

## v1.1.0 validation

The complete suite passed with the race detector and 97.9% statement coverage.
Formatting, go vet, and go mod tidy -diff passed. Path validation, including the
new methods, passed a 15-second two-worker fuzz run. Regression coverage compares
optional methods against standard-library Open-only fallbacks and exercises
subtree paths, errors, retained hook slices, independent lazy metadata caches,
concurrent Info calls, reentrant hooks, and snapshot ownership.

## v1.1.1: small reads and entry metadata

Version 1.1.1 improves two paths relative to v1.1.0:

- `File.Read` explicitly unlocks on its two return paths. This lets the
  compiler expand the mutex's normal unlock path without the deferred wrapper
  call on every small read. The same mutex still covers the closed check,
  content access and cursor update. `Read` itself is not inlined; `Seek`,
  `ReadAt` and `Close` retain their synchronization and behavior.
- Entries without a regular-file hook own a compact, immutable metadata
  snapshot. `Info` returns that existing snapshot instead of allocating another
  interface copy on every call. This applies to directories even when a hook
  is configured. Entries with hooks retain their separate, lazy `OnceValues`
  result, including cached errors and panic propagation.

Neither change adds an exported method or dependency. Plain entries and their
returned `FileInfo` retain no tree node or file data. Holding a returned
`FileInfo` keeps its metadata snapshot alive after the entry slice is discarded.
An unresolved regular-file entry with a hook still retains its captured content
until the first calculation completes or the entry is discarded. Listing names
still does not call hooks. Per-listing caches, reentrant callbacks, errors and
content ownership remain unchanged.

The new `BenchmarkSmallChunkRead`, `BenchmarkReadDirInfo`,
`BenchmarkHookReadDirAllInfo` and `BenchmarkDirEntryInfoCached` cases are in
`benchmark_test.go`. For a before/after comparison, copy that benchmark file to
a clean v1.1.0 checkout as well as the v1.1.1 checkout. Use the same Go version
and run one benchmark process at a time:

```sh
go test -run='^$' \
  -bench='^Benchmark(SmallChunkRead|Directory|ReadDirInfo|HookReadDirAllInfo|DirEntryInfoCached)$' \
  -benchmem -benchtime=200ms -count=6 -cpu=1 .
```

Small-read operations open a fresh 4 KiB file, read it through EOF in the named
chunk size, and close it; they are not single-`Read` latency measurements.
Directory metadata operations create a fresh sorted listing each time, then
call every entry's `Info` and consume its size. Names-only controls obtain the
listing without calling `Info`. Hook cases use a five-byte stored value
per entry and transform the private input in place. Cached-`Info` cases resolve
the entry once before timing. Fixture creation and result validation are
outside timing.

### v1.1.1 measurements

Measured on 2026-10-25 with Go 1.27.3, darwin/arm64, Apple M5 and
`GOMAXPROCS=1`. A separate comparison harness imported the published v1.1.0
source (`d69c7500e0034579d72819a2a05cda1f97de960e`) and the v1.1.1 runtime
implementation under separate local module aliases in one binary. Each of 17 scenarios ran
six adjacent pairs, alternating AB/BA, with `-benchtime=200ms -count=1 -cpu=1`.
The harness's explicit trials require `-count=1` to preserve adjacency.

The two time columns below are separate sample medians. The last column is
computed from the median of the six paired before/after ratios, so it need not
equal the ratio of those two columns. All allocation measurements were stable
across the six samples.

| Operation | v1.1.0 median | v1.1.1 median | Paired time reduction |
| --- | ---: | ---: | ---: |
| Open/read 4 KiB/close, 16 B chunks | 1.433 us | 1.201 us | 16.9% |
| Open/read 4 KiB/close, 64 B chunks | 412.65 ns | 345.45 ns | 16.7% |
| Open/read 4 KiB/close, 256 B chunks | 184.05 ns | 153.95 ns | 16.6% |
| List names only, 1,000 entries | 141.17 us | 114.49 us | 18.4% |
| List + every Info, 100 entries | 10.61 us | 7.30 us | 32.4% |
| List + every Info, 1,000 entries | 156.32 us | 112.37 us | 26.9% |
| List + every Info, 10,000 entries | 2.141 ms | 1.559 ms | 27.1% |

Small reads remain 128 B/op and one allocation. Listing and reading all metadata
for 1,000 entries drops from 144,512 to 80,512 B/op (44.3% less) and from
2,002 to 1,002 allocations. Names-only listings retain their existing bytes and
allocation counts. Repeated no-hook `Info` calls drop from 64 B and one
allocation per call to zero. Fresh hook listings followed by every `Info` are
approximately unchanged in time (paired results from 0.3% slower to 3.4% faster)
and retain the same allocation counts; cached hook metadata remains allocation
free. The 4 KiB single-chunk read control had mixed pair timings, so no stable
timing improvement is claimed for it.

A further six-pair control used original `soulteary/memfs` v0.1.0
(`b8d5ff9859d1c18004594f555725fb241563c1a0`) with the same workloads. The
v1.1.1 1,000-entry listing plus every `Info` took about 18.9% less time than
the original and allocated 28.6% fewer bytes. The 64 B chunk read still took
about 48% more time than the original in the paired measurement. The original
does not synchronize a file handle's cursor or its close operation; this change
retains that synchronization. These controls use a static tree and independent
handles, where both implementations can be compared without concurrent writes
or shared-handle races.

The final code passed the full race-enabled suite with 97.9% statement coverage,
vet, formatting, module checks and a smoke run of every benchmark. Path
validation passed a 15-second, two-worker fuzz run. Added boundary, concurrent
cursor, retained metadata and panic-cache tests also passed against v1.1.0,
confirming that they check existing behavior. These local timings describe the
measured workloads and are not application-throughput guarantees.
