# Follow-up correctness review

Reviewed on 2026-10-25 against the initial migration,
[`3a7e7bb`](https://github.com/soulteary/memfs-kit/commit/3a7e7bb160df58fc5f2358eda6f21385f0787f44).
This review found three additional defects. It also added coverage for two
concurrency contracts that were already implemented correctly.

## 1. Hook metadata did not describe the file being read

An existing file transformed by a deterministic open hook had two conflicting
metadata views: `Open`/`Stat` reported the transformed size and modification
time, while a directory entry's `Info` still reported the stored values.

This was reproduced without concurrent writes or changing hook state:

- `fstest.TestFS` rejected the disagreement between `DirEntry.Info` and file
  `Stat`.
- `archive/tar.Writer.AddFS` used the stored size for its header, then opened
  the transformed contents and failed with `archive/tar: write too long`.

The fix shares hook transformation and metadata calculation between `Open`
and regular-file directory entries. An entry captures its stored data and
metadata when the directory is opened. Its first `Info` call applies the hook
outside tree and handle locks and caches the metadata or error. Listing names
and calling `Name`, `Type`, or `IsDir` still do not execute hooks.

The tests in [`hook_metadata_test.go`](../hook_metadata_test.go) fail against
the initial migration and pass with this fix. They cover standard-library
consumers, snapshot isolation after overwrite, nested subtree paths, lazy and
concurrent metadata requests, hook errors, and callbacks that write to the FS.

The stored snapshot does not freeze external hook state. An archive operation
that separately obtains metadata and opens content needs a deterministic hook
or stable application state. A callback must not recursively request the same
entry's `Info` while that result is being initialized.

## 2. A pass-through hook changed error classification

Returning the supplied `origErr` was documented to preserve default behavior,
but `Open` wrapped that existing `*fs.PathError` a second time. The resulting
error repeated the open/path context, and `os.IsNotExist` returned false even
though it returned true without a hook. `errors.Is` continued to work, which
is why the first suite did not catch the compatibility regression.

The shared hook helper now returns the original error object unchanged when
the callback returns `origErr`. New callback errors still receive the current
open/path context; unrelated error chains are not flattened.

[`hook_errors_test.go`](../hook_errors_test.go) verifies error identity,
strings, old and new error classification helpers, subtree-relative error
paths, custom sentinels, and non-comparable custom error values.

## 3. Closed handles retained obsolete snapshots

`Close` set the closed flag but left the reader or directory entries attached
to the handle. An application retaining closed handles could therefore keep
obsolete file data alive after it had already been overwritten in the tree.

An independent heap experiment retained eight closed handles to overwritten
8 MiB files, then replaced the stored file with a small value. After garbage
collection, the initial migration retained approximately 64 MiB; after the
fix, the retained increment rounded to 0.0 MiB under the same experiment.

File close now clears its reader reference, and directory close clears its
entry list, under their existing locks. Operations after close keep the same
error behavior. Entries already returned to the caller remain valid: their
captured snapshots belong to those entries, not to the closed handle.

Exact heap sizes and GC timing are not CI assertions. Existing closed-handle
tests and the additional directory concurrency test check operational safety;
the independent allocation experiment verifies the lifetime correction.

## Additional concurrency coverage

[`concurrency_views_test.go`](../concurrency_views_test.go) adds tests for:

- simultaneous writes and reads through parent and subtree views of the same
  file, preserving complete snapshots and existing permissions;
- concurrent pagination and close on one directory handle, including continued
  use of entries that were returned before the handle closed.

Both tests passed with the race detector against the initial migration and
the updated implementation. They close coverage gaps; they are not evidence
of two additional implementation bugs.

## Validation

The complete suite passed with Go 1.27.3 under `go test -race -count=1`, with
97.4% statement coverage. Formatting, `go vet`, and module consistency checks
also passed. The existing cross-platform and fuzz CI remains in place.
