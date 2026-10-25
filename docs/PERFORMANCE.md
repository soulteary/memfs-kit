# Performance

Version 1.1.0 reduces allocation and directory work while preserving the
filesystem's existing snapshot and hook behavior. There are no new dependencies
or changes to existing method signatures. `Stat` and `ReadDir` add the standard
`fs.StatFS` and `fs.ReadDirFS` optional interfaces.

## Optimized paths

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

The global tree lock and `WriteFile`'s content copy remain unchanged. Per-handle
locking, shared `ReadAt` behavior, and directory-sort caching are outside this
optimization scope.

## Preserved behavior

Opened files and directory entries continue to describe the content and metadata
captured when their snapshots were created. Later writes cannot mutate them.
Hook input and returned bytes retained by application code cannot mutate stored
or opened content, including when an equal hook output permits snapshot reuse.

Directory listing does not execute hooks. Each entry's first `Info` call resolves
and caches its own metadata or error, including across concurrent calls. Separate
listings have separate caches. Hooks execute outside tree and handle locks and
may write to the filesystem. `FS.Stat` on regular or missing paths invokes the
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

## Reproducing the comparison

The baseline is commit `39284834af24fe80d4b607f2746813c8b7a41ea4`. Run the same
`benchmark_test.go` against both revisions; copy only that file into a clean
baseline checkout. Setup is outside timed sections. Hook transformations mutate
their private input in place, adding no allocation of their own. First-`Info`
benchmarks create a fresh directory listing on every iteration to avoid timing
the cached result.

```sh
go test -run='^$' -bench=. -benchmem -benchtime=200ms -count=5 -cpu=1 .
```

Compare `B/op` and `allocs/op` as well as time. Timings depend on CPU scheduling,
frequency, garbage collection, and the workload. A lower allocation count is not
an application-throughput guarantee. Benchmark results are observations, not CI
performance thresholds.

## Local allocation results

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

## Validation

The complete suite passed with the race detector and 97.9% statement coverage.
Formatting, go vet, and go mod tidy -diff passed. Path validation, including the
new methods, passed a 15-second two-worker fuzz run. Regression coverage compares
optional methods against standard-library Open-only fallbacks and exercises
subtree paths, errors, retained hook slices, independent lazy metadata caches,
concurrent Info calls, reentrant hooks, and snapshot ownership.
