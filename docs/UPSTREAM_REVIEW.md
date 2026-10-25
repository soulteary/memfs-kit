# Upstream review and migration decisions

Reviewed on 2026-10-25. This document records why `memfs-kit` incorporates
selected upstream capabilities together with additional correctness fixes,
instead of copying the latest upstream implementation unchanged.

This is a historical migration review. Later implementation and performance
changes are documented in the [README](../README.md) and
[performance notes](PERFORMANCE.md).

## Sources reviewed

| Source | Reviewed revision |
| --- | --- |
| Existing fork, `soulteary/memfs` | [`b8d5ff9859d1c18004594f555725fb241563c1a0`](https://github.com/soulteary/memfs/commit/b8d5ff9859d1c18004594f555725fb241563c1a0) |
| Upstream, `psanford/memfs` | [`4ef911798f9bffd5efa9346abd6e81ff3896d114`](https://github.com/psanford/memfs/commit/4ef911798f9bffd5efa9346abd6e81ff3896d114) |
| Common ancestor | [`a001468d78ef7739d32665bb843adffbd9a6198a`](https://github.com/psanford/memfs/commit/a001468d78ef7739d32665bb843adffbd9a6198a) |

The [requested comparison](https://github.com/soulteary/memfs/compare/main...psanford:memfs:main)
has five upstream-only commits and two fork-only commits at these revisions.
Those five commits do **not** represent five independent missing features:
two reproduce fixes already included in the fork.

## Decisions for the five upstream commits

| Upstream commit | Change | Decision |
| --- | --- | --- |
| [`4dbf7e3`](https://github.com/psanford/memfs/commit/4dbf7e3e865eb177888cdcda9c163a5144a39e84) | Adds `FS.Sub`, implementing `fs.SubFS` | Adopt the capability with corrections: validate paths, handle `Sub(".")`, preserve the open hook, and share synchronization with the parent filesystem. |
| [`aee5b83`](https://github.com/psanford/memfs/commit/aee5b8307ee4984f15df329505336287990a2d39) | Adds `fstest.TestFS`, improves directory EOF/index handling, and masks `DirEntry.Type` to type bits | Preserve the intent and replace the insufficient directory implementation and fixture. The substantive patch is already present in the fork. |
| [`9788b7e`](https://github.com/psanford/memfs/commit/9788b7e45cd8128709e5b909383ec531b662090f) | Fixes the preceding commit's nonexistent `d.dirOffset` field to `d.idx` | No separate feature to import. The fork already uses the corrected field. |
| [`02fb08c`](https://github.com/psanford/memfs/commit/02fb08c0f8dbc858e131b6b257b51be2a5dcac9e) | Adds `New(opts ...Option)` and `WithOpenHook` | Adopt the API and fix missing-file invocation, content ownership, and sub-filesystem behavior. |
| [`4ef9117`](https://github.com/psanford/memfs/commit/4ef911798f9bffd5efa9346abd6e81ff3896d114) | Adds `io.Seeker` to opened files and separates the reader position from stored content | Adopt the capability and stable-size behavior; also synchronize handle operations and cover closed/error cases. |

The fork's [`8842b84`](https://github.com/soulteary/memfs/commit/8842b84a8b4ae243ee20e14394c2cfced34763e3)
already incorporated the `cugu` changes from
[`psanford/memfs#3`](https://github.com/psanford/memfs/pull/3), including both
the compatibility patch and its corrected index field. Applying the upstream
patches again would not fix the remaining pagination errors.

The existing `New()`, `MkdirAll`, `WriteFile`, and `Open` calls remain the basis
of the API. Ordinary `New()` calls remain valid after adding variadic options.
Code that assigns `New` to an exact `func() *FS` function type must use a small
wrapper because a variadic function has a different function type.

## Correctness findings

| Finding in the reviewed source | Impact | Migration treatment |
| --- | --- | --- |
| `MkdirAll` returns when a component is a regular file without unlocking the parent directory | Later operations on that directory can block indefinitely | Ensure all error paths release the tree lock. |
| Directory handles rebuild their entry list from a map on every call and use `i < n` instead of a page end based on the current offset | `ReadDir(1)` can return an empty page with no error before EOF; repeated calls can skip or repeat names | Snapshot and sort entries when opening a directory, then page through a stable list. |
| `WriteFile` publishes a new file in the directory map before initializing its content and permissions | Concurrent `Open` or directory reads can observe partially initialized state; the race detector reports conflicting accesses | Fully initialize an immutable file node and publish it under the shared tree lock. |
| Both versions retain the caller's input byte slice | Mutating the slice after `WriteFile` changes stored file contents unexpectedly | Copy input bytes when storing them. |
| Overwriting a file replaces its permissions despite the existing method comment promising otherwise | A content update can unintentionally change file mode metadata | Preserve the existing mode when overwriting a regular file. |
| The old fork uses `bytes.Buffer.Len()` for file size | `Stat().Size()` shrinks as the handle is read | Keep content size independent of the reader offset; the upstream seek change already moves in this direction. |
| The open hook is called only when the original open succeeds | The documented virtual-file use case never reaches the hook for a missing file | Invoke hooks for regular files and missing paths; provide the original error and allow a hook to return virtual content. |
| `Sub` accepts an empty path, fails for direct `Sub(".")`, and omits the parent hook | Direct method behavior is inconsistent and a subtree silently loses configured behavior | Validate `fs.ValidPath`, return the current filesystem for `.`, and preserve hook behavior with the original root-relative path. |
| Directory handles have no closed state, and handles have no synchronization | Directory operations continue after close; concurrent operations on one handle can race | Track closed state and serialize operations on each handle. |
| Missing-file tests use `err == nil && errors.Is(err, fs.ErrNotExist)` | The failure branch cannot be reached, so the assertion cannot catch regressions | Assert the intended sentinel directly and add `*fs.PathError` checks. |

These are correctness issues, not performance claims. In particular, the
presence of a directory mutex in the original implementation does not make
publication of file contents safe.

### Reproduction evidence

Standalone standard-library tests against the reviewed upstream revision,
using Go 1.27.3, reproduced the stuck directory lock, second-page failure,
input aliasing, overwritten mode, missing-file hook failure, and both direct
`Sub` path problems. A concurrent `WriteFile`/`Open` test under `go test -race`
reported races between `WriteFile`'s content/mode initialization and `open`'s
reads of those fields.

The upstream seek implementation passed the focused read/seek/stable-size
check. `WriteFile(".")` was also checked: it already rejects the operation,
so accepting writes to the root is **not** claimed as an upstream defect.

## Explicit behavior in `memfs-kit`

- Paths follow `io/fs` rules: slash-separated relative paths, `.` for the
  root, and no empty, absolute, `.`-component, or `..`-component paths.
- A filesystem and its sub-filesystems share their underlying tree and lock.
  Subtree writes are visible through subsequent opens in the parent.
- An open regular-file handle retains the content and metadata observed when
  it was opened. A later `WriteFile` publishes a new version for future opens.
- An open directory handle retains a sorted entry snapshot. Later writes do
  not reorder an in-progress listing.
- A positive-size `ReadDir` call may return its final nonempty partial page
  with a nil error. The next call returns an empty result and `io.EOF`.
  Nonpositive-size calls return the remaining entries with a nil error.
- A hook runs outside internal filesystem locks. It receives private input
  bytes, and its returned bytes are copied before becoming handle content.
  Applications must synchronize any shared mutable state in their hook.
- If a hook changes an existing file's content, that open view has an unknown
  (zero) modification time. A pass-through result retains the stored time.
  This avoids presenting the stored file's timestamp as a validator for
  dynamically generated content.
- A hook inherited by `Sub` receives a path relative to the original `New`
  root, including accumulated subtree prefixes. This preserves the meaning
  of an existing root-level hook.
- Hooks are not invoked for invalid paths, directories, or traversal through
  an existing regular file. Hook-created virtual files are read-only views
  with mode `0444`; opening one does not insert it into directory listings.

This remains an in-process memory filesystem. It does not claim to implement
OS permission enforcement, symlinks, persistence, mounted filesystems, or the
full read-write `os.File` API. Those would be separate design decisions.

### Additional migration review: HTTP conditional requests

The initial migration gave stored files meaningful write timestamps, while
the older code used zero timestamps. Reviewing this improvement together with
open hooks uncovered an interaction: if a hook changed file content while
retaining the stored timestamp, `http.FileServer` could answer a later request
with `304 Not Modified` even though the generated response body had changed.

This was reproduced with two different hook responses and an
`If-Modified-Since` request. Changed hook content now clears the open view's
modification time; unchanged hook content keeps the stored time. The regression
suite covers this conditional-request behavior in addition to byte ranges.

The inherited `bytes.Reader.Seek` implementation was also reviewed for very
large offsets. Its current position and end position are nonnegative, so
positive signed overflow produces a negative result that the standard
implementation rejects without changing the position. There is no need to
duplicate that implementation with a separate offset arithmetic layer.

## Validation scope

The migration's regression coverage should exercise the observable contracts:

- `fstest.TestFS` with several siblings, nested directories, and sub-filesystems;
- complete paginated listings, EOF rules, directory closure, and stable snapshots;
- write/read ownership boundaries, overwrite permissions, and failed mkdir recovery;
- valid and invalid subtree paths, parent/subtree visibility, and inherited hooks;
- hook overrides, virtual files, hook errors, and reentrant hook operations;
- seeking, stable file metadata, closed handles, and independent file cursors;
- concurrent reads/writes and shared-handle operations under the race detector;
- HTTP file serving and byte ranges, which depend on correct file sizes and seeking.

The regression suite and CI validate these contracts against the new
implementation rather than relying on the old single-file fixture alone.

## Licensing and attribution

Both reviewed source revisions use the same BSD-3-Clause license. The original
copyright and full license are retained in
[`LICENSES/memfs-BSD-3-Clause.txt`](../LICENSES/memfs-BSD-3-Clause.txt).
New work is distributed under the repository's Apache-2.0 terms, with the
inherited BSD obligations preserved. See [`LICENSE`](../LICENSE) and
[`THIRD_PARTY_NOTICES.md`](../THIRD_PARTY_NOTICES.md) for the applicable notices.
