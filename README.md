# memfs-kit

[![CI](https://github.com/soulteary/memfs-kit/actions/workflows/ci.yml/badge.svg)](https://github.com/soulteary/memfs-kit/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/soulteary/memfs-kit.svg)](https://pkg.go.dev/github.com/soulteary/memfs-kit)

[中文文档](README_CN.md)

A lightweight in-memory filesystem for Go, built around `io/fs`.

Create a file tree at runtime and pass it to consumers of `fs.FS`, including
templates and `http.FileServer(http.FS(root))`. The filesystem owns its data,
supports concurrent operations, and returns independent, seekable file
snapshots. There are no dependencies outside the Go standard library.

## Requirements and installation

Go **1.27.3 or newer**.

```sh
go get github.com/soulteary/memfs-kit
```

The module is `github.com/soulteary/memfs-kit`; the Go package name is `memfs`.

## Quick start

```go
package main

import (
    "fmt"
    "io/fs"
    "log"

    memfs "github.com/soulteary/memfs-kit"
)

func main() {
    root := memfs.New()
    if err := root.MkdirAll("assets", 0755); err != nil {
        log.Fatal(err)
    }
    if err := root.WriteFile("assets/hello.txt", []byte("hello"), 0644); err != nil {
        log.Fatal(err)
    }

    assets, err := fs.Sub(root, "assets")
    if err != nil {
        log.Fatal(err)
    }
    data, err := fs.ReadFile(assets, "hello.txt")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(string(data))
}
```

## API

| API | Purpose |
| --- | --- |
| `New(opts ...Option) *FS` | Create an empty filesystem; use this constructor instead of a zero-value `FS` |
| `(*FS).MkdirAll(path, perm)` | Create a directory and any missing parents |
| `(*FS).WriteFile(path, data, perm)` | Create or replace a file; its parent directory must already exist |
| `(*FS).Open(path)` | Open a read-only file or directory snapshot |
| `(*FS).Sub(dir)` | Return an `fs.FS` view of an existing subtree |
| `WithOpenHook(fn)` | Transform file content or provide a virtual file for an individual open |

`*FS` implements `fs.FS` and `fs.SubFS`. Use standard helpers such as
`fs.ReadFile`, `fs.ReadDir`, `fs.Stat`, `fs.WalkDir`, and `fs.Glob` with it.
Regular file handles implement `fs.File`, `io.Seeker`, and `io.ReaderAt`.
Directory handles implement `fs.ReadDirFile`.

## Behavior and boundaries

- **Paths:** use slash-separated, relative `io/fs` paths. `.` is the root;
  empty names, absolute paths, `..` components, and repeated slashes are
  rejected. Paths have the same syntax on Windows, macOS, and Linux.
- **Errors:** filesystem operations report `*fs.PathError` values. Use
  `errors.Is` to check errors such as `fs.ErrNotExist` and `fs.ErrInvalid`.
- **Data ownership:** `WriteFile` copies its input before publishing the
  file. Changes to the caller's byte slice after the call returns do not
  change stored data.
- **Open snapshots:** replacing a file does not change an already open
  handle's content or metadata. Each open has its own cursor; seeking past
  the end is allowed, and reading there returns EOF.
- **Directories:** an open directory contains a sorted snapshot of its
  entries. Repeated `ReadDir(n)` calls advance correctly, including EOF
  behavior for positive `n`. Changes made after the open appear on a new
  open, not in the existing snapshot.
- **Subtrees:** `Sub` shares the underlying tree. Later writes through the
  original filesystem are visible to later opens in the subtree. Its root
  is named `.` and it retains the original open hook.
- **Concurrency:** filesystem methods and individual handle methods are
  synchronized. Sharing a handle also shares its cursor; use separate opens
  when each goroutine needs independent sequential reading. Callers must
  synchronize any shared mutable state used by their own hooks.
- **Permissions:** mode bits are metadata, not access control. Replacing an
  existing file preserves its mode; the supplied mode applies to a new file.
  There is no operating-system umask or permission enforcement.
- **Scope:** writes use `WriteFile` and `MkdirAll`. There is no `OpenFile`,
  remove, rename, symlink, disk persistence, or operating-system mount API.

## Open hooks

`WithOpenHook` installs a callback with this signature:

```go
func(path string, existingContent []byte, origErr error) ([]byte, error)
```

The callback runs for regular files and missing paths. For a missing path,
`existingContent` is `nil` and `origErr` matches `fs.ErrNotExist`. Returning
bytes and a `nil` error supplies content for that open; returning
`existingContent, origErr` preserves normal behavior.

Directories, invalid paths, and paths that traverse through a regular file
do not invoke the callback. The callback runs outside internal filesystem
locks, so it may call filesystem methods. Both the input and returned bytes
are copied at the boundary; the callback cannot mutate the stored file by
changing its input slice.

Content returned by a hook is not written back to the tree. A synthesized
file uses mode `0444`, does not appear in directory listings, and cannot
create a virtual directory for `Sub`.

If a hook changes an existing file's content, the returned handle's `ModTime`
is zero (unknown). This prevents `http.FileServer` from using the stored
file's stale modification time to return an incorrect `304 Not Modified`.
Returning unchanged content preserves the original metadata. Synthesized
files also have an unknown modification time.

Hook paths are relative to the filesystem originally created by `New`.
For example, `fs.Sub(root, "assets")` followed by `Open("hello.txt")` passes
`assets/hello.txt` to the hook. Nested subtrees preserve the accumulated
prefix. A hook should use these full paths when matching its rules.

## Migration and upstream

Change the import path from `github.com/soulteary/memfs` to
`github.com/soulteary/memfs-kit`; the package name remains `memfs` and existing
`memfs.New()` calls still work.

`New` now accepts variadic options. Code assigning it to an exact
`func() *memfs.FS` variable must use a wrapper such as
`func() *memfs.FS { return memfs.New() }`.

This project starts from [soulteary/memfs](https://github.com/soulteary/memfs)
and incorporates reviewed ideas from
[psanford/memfs](https://github.com/psanford/memfs). The migration fixes locking,
directory enumeration, file ownership, and standard-library compatibility
issues instead of copying upstream unchanged. See the
[upstream review](docs/UPSTREAM_REVIEW.md) for the compared revisions and
per-commit decisions.

Compared with [vfs-kit](https://github.com/soulteary/vfs-kit), which already
provides a memory backend alongside disk, archive, and mount abstractions,
`memfs-kit` focuses on an in-memory implementation of standard `io/fs`
interfaces. Choose `vfs-kit` when you need its common read-write VFS interface
across multiple backends.

## Development

```sh
gofmt -w .
go vet ./...
go test ./...
go test -race -coverprofile=coverage.out ./...
go test -run='^$' -fuzz='^FuzzPathValidation$' -fuzztime=15s .
```

CI checks formatting, module consistency, and `go vet`; runs tests on Linux,
macOS, and Windows; and runs race detection, coverage reporting, and a short
fuzzing pass on Linux. GitHub Actions are pinned to verified commit SHAs and
updated by Dependabot.

## License

The project uses [Apache License 2.0](LICENSE). Code inherited from `memfs`
retains its [BSD-3-Clause license](LICENSES/memfs-BSD-3-Clause.txt), original
copyright notice, conditions, and disclaimer. See
[third-party source notices](THIRD_PARTY_NOTICES.md) for attribution and the
exact source revisions. Keep those notices with redistributions.
