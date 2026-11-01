// Copyright 2026 soulteary and the memfs-kit contributors.
// SPDX-License-Identifier: Apache-2.0

package memfs_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	memfs "github.com/soulteary/memfs-kit"
)

var (
	_ fs.StatFS    = (*memfs.FS)(nil)
	_ fs.ReadDirFS = (*memfs.FS)(nil)
)

// An explicit Open method hides optional interfaces and exercises the standard
// library's file-handle fallback against the same filesystem and timestamps.
type openOnlyFS struct{ fsys fs.FS }

func (f openOnlyFS) Open(name string) (fs.File, error) { return f.fsys.Open(name) }

func compareFastPathInfo(t *testing.T, got, want fs.FileInfo) {
	t.Helper()
	if got.Name() != want.Name() || got.Size() != want.Size() || got.Mode() != want.Mode() ||
		got.IsDir() != want.IsDir() || !got.ModTime().Equal(want.ModTime()) ||
		!reflect.DeepEqual(got.Sys(), want.Sys()) {
		t.Fatalf("metadata differs from Open fallback: got=%+v, want=%+v", got, want)
	}
}

func compareFastPathError(t *testing.T, got, want error, hookErr error) {
	t.Helper()
	if got == nil || want == nil {
		if (got == nil) != (want == nil) {
			t.Fatalf("error differs from Open fallback: got=%v, want=%v", got, want)
		}
		return
	}
	if got.Error() != want.Error() {
		t.Fatalf("error differs from Open fallback: got=%v, want=%v", got, want)
	}
	var gotPath, wantPath *fs.PathError
	if !errors.As(got, &gotPath) || !errors.As(want, &wantPath) ||
		gotPath.Op != wantPath.Op || gotPath.Path != wantPath.Path ||
		gotPath.Err.Error() != wantPath.Err.Error() {
		t.Fatalf("PathError differs from Open fallback: got=%#v, want=%#v", got, want)
	}
	for _, target := range []error{fs.ErrInvalid, fs.ErrNotExist, fs.ErrExist, hookErr} {
		if errors.Is(got, target) != errors.Is(want, target) {
			t.Fatalf("error classification for %v differs: got=%v, want=%v", target, got, want)
		}
		if wantPath.Err == target && gotPath.Err != target {
			t.Fatalf("PathError lost original cause %v: %#v", target, gotPath)
		}
	}
	if os.IsNotExist(got) != os.IsNotExist(want) {
		t.Fatalf("os.IsNotExist differs from Open fallback: got=%v, want=%v", got, want)
	}
}

func TestFastPathsMatchOpenFallback(t *testing.T) {
	for _, hooked := range []bool{false, true} {
		t.Run(fmt.Sprintf("hook=%t", hooked), func(t *testing.T) {
			denied := errors.New("hook refused file")
			var calls []hookCall
			var original error
			var opts []memfs.Option
			if hooked {
				opts = append(opts, memfs.WithOpenHook(func(name string, content []byte, err error) ([]byte, error) {
					calls = append(calls, hookCall{name, string(content), errors.Is(err, fs.ErrNotExist)})
					original = err
					switch path.Base(name) {
					case "denied":
						return nil, denied
					case "virtual":
						if err != nil {
							return []byte("virtual data"), nil
						}
					case "transformed":
						if err == nil {
							return append(content, '!'), nil
						}
					}
					return content, err
				}))
			}
			root := memfs.New(opts...)
			for _, prefix := range []string{".", "assets/deep"} {
				for _, dir := range []string{"dir", "empty"} {
					mustNoError(t, root.MkdirAll(path.Join(prefix, dir), 0o750))
				}
				for _, name := range []string{"stored", "passthrough", "transformed", "denied", "dir/z", "dir/a"} {
					mustWrite(t, root, path.Join(prefix, name), "stored bytes")
				}
			}
			assets, err := root.Sub("assets")
			mustNoError(t, err)
			nested, err := fs.Sub(assets, "deep")
			mustNoError(t, err)
			for _, view := range []struct {
				name, prefix string
				fsys         fs.FS
			}{{"root", "", root}, {"nested-sub", "assets/deep", nested}} {
				t.Run(view.name, func(t *testing.T) {
					for _, operation := range []string{"Stat", "ReadDir"} {
						for _, name := range []string{".", "dir", "empty", "stored", "passthrough", "transformed", "virtual", "denied", "missing", "../escape", "stored/child"} {
							t.Run(operation+"/"+name, func(t *testing.T) {
								checkFastPathOperation(t, hooked, name, operation, view.prefix, view.fsys, denied, &calls, &original)
							})
						}
					}
				})
			}
		})
	}
}

func TestFastReadDirIndependentLazySnapshots(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("fallback=%t/error=%t", fallback, fail), func(t *testing.T) {
				denied := errors.New("metadata denied")
				var calls atomic.Int32
				root := memfs.New(memfs.WithOpenHook(func(_ string, content []byte, err error) ([]byte, error) {
					calls.Add(1)
					if fail {
						return nil, denied
					}
					return append(content, '!'), err
				}))
				for _, name := range []string{"z", "stored", "a"} {
					mustWrite(t, root, name, "old")
				}
				var fsys fs.FS = root
				if fallback {
					fsys = openOnlyFS{root}
				}
				list := func() []fs.DirEntry {
					entries, err := fs.ReadDir(fsys, ".")
					mustNoError(t, err)
					var names []string
					for _, entry := range entries {
						names = append(names, entry.Name())
					}
					if !reflect.DeepEqual(names, []string{"a", "stored", "z"}) {
						t.Fatalf("directory names=%v", names)
					}
					return entries
				}
				first, second := list(), list()
				oldEntry := findHookTestEntry(t, first, "stored")
				// Callers own their returned slice; changing it cannot alter a later list.
				first[0] = first[2]
				list()
				if calls.Load() != 0 {
					t.Fatal("directory listing invoked the hook")
				}
				mustNoError(t, root.WriteFile("stored", []byte("replacement"), 0o777))

				var wg sync.WaitGroup
				for range 16 {
					wg.Go(func() { checkLazySnapshotInfo(t, fail, denied, oldEntry, int64(len("old!"))) })
				}
				wg.Wait()
				if got := calls.Load(); got != 1 {
					t.Fatalf("concurrent Info invoked hook %d times, want one", got)
				}
				secondEntry := findHookTestEntry(t, second, "stored")
				checkLazySnapshotInfo(t, fail, denied, secondEntry, int64(len("old!")))
				checkLazySnapshotInfo(t, fail, denied, secondEntry, int64(len("old!")))
				if got := calls.Load(); got != 2 {
					t.Fatalf("two independent lists invoked hook %d times, want two", got)
				}
				freshEntry := findHookTestEntry(t, list(), "stored")
				if calls.Load() != 2 {
					t.Fatal("fresh directory listing eagerly resolved metadata")
				}
				checkLazySnapshotInfo(t, fail, denied, freshEntry, int64(len("replacement!")))
				if got := calls.Load(); got != 3 {
					t.Fatalf("fresh entry reused another list's metadata cache: %d calls", got)
				}
			})
		}
	}
}

func TestFastPathHooksCanWriteFilesystem(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		for _, operation := range []string{"Stat", "ReadDir.Info"} {
			t.Run(fmt.Sprintf("fallback=%t/%s", fallback, operation), func(t *testing.T) {
				var root *memfs.FS
				var hookPath string
				var calls atomic.Int32
				root = memfs.New(memfs.WithOpenHook(func(name string, content []byte, err error) ([]byte, error) {
					hookPath = name
					calls.Add(1)
					if writeErr := root.WriteFile("side-effect", []byte("created"), 0o600); writeErr != nil {
						return nil, writeErr
					}
					return append(content, '!'), err
				}))
				mustNoError(t, root.MkdirAll("assets/deep", 0o750))
				mustWrite(t, root, "assets/deep/file", "old")
				assets, err := root.Sub("assets")
				mustNoError(t, err)
				view, err := fs.Sub(assets, "deep")
				mustNoError(t, err)
				if fallback {
					view = openOnlyFS{view}
				}
				done := make(chan error, 1)
				go func() {
					var info fs.FileInfo
					var err error
					if operation == "Stat" {
						info, err = fs.Stat(view, "file")
					} else {
						var entries []fs.DirEntry
						entries, err = fs.ReadDir(view, ".")
						if err == nil {
							info, err = entries[0].Info()
						}
					}
					if err == nil && (info.Size() != 4 || !info.ModTime().IsZero()) {
						err = fmt.Errorf("unexpected transformed metadata: %v", info)
					}
					done <- err
				}()
				select {
				case err := <-done:
					mustNoError(t, err)
				case <-time.After(3 * time.Second):
					t.Fatal("metadata hook blocked while writing to the filesystem")
				}
				if hookPath != "assets/deep/file" || calls.Load() != 1 {
					t.Fatalf("hook path=%q, calls=%d", hookPath, calls.Load())
				}
				entries, err := fs.ReadDir(root, ".")
				mustNoError(t, err)
				findHookTestEntry(t, entries, "side-effect")
			})
		}
	}
}

func TestEqualHookOutputRetainsSnapshotIsolation(t *testing.T) {
	for _, separateOutput := range []bool{false, true} {
		t.Run(fmt.Sprintf("separate-output=%t", separateOutput), func(t *testing.T) {
			var input, output []byte
			root := memfs.New(memfs.WithOpenHook(func(_ string, content []byte, err error) ([]byte, error) {
				input, output = content, content
				if separateOutput {
					output = bytes.Clone(content)
				}
				return output, err
			}))
			mustWrite(t, root, "file", "original")
			f := mustOpen(t, root, "file")
			input[0], output[1] = 'X', 'Y'
			got, err := io.ReadAll(f)
			if err != nil || string(got) != "original" {
				t.Fatalf("equal hook bytes escaped into open snapshot: data=%q, error=%v", got, err)
			}
			got, err = fs.ReadFile(root, "file")
			if err != nil || string(got) != "original" {
				t.Fatalf("retained hook bytes changed stored content: data=%q, error=%v", got, err)
			}
		})
	}
}
func checkFastPathOperation(t *testing.T, hooked bool, name, operation, prefix string, view fs.FS, denied error, calls *[]hookCall, original *error) {
	t.Helper()
	wantCalls := 0
	if hooked && fs.ValidPath(name) && name != "." && name != "dir" && name != "empty" && name != "stored/child" {
		wantCalls = 1
	}
	gotInfo, gotEntries, gotErr, gotCalls := runFastPathOperation(t, view, hooked, name, operation, prefix, wantCalls, calls, original)
	wantInfo, wantEntries, wantErr, wantCallsLog := runFastPathOperation(t, openOnlyFS{view}, hooked, name, operation, prefix, wantCalls, calls, original)
	compareFastPathError(t, gotErr, wantErr, denied)
	if !reflect.DeepEqual(gotCalls, wantCallsLog) {
		t.Fatalf("hook arguments differ: got=%v, want=%v", gotCalls, wantCallsLog)
	}
	if gotErr != nil {
		return
	}
	if operation == "Stat" {
		compareFastPathInfo(t, gotInfo, wantInfo)
		return
	}
	checkFastDirectoryEntries(t, gotEntries, wantEntries, denied, calls)

}

func checkFastDirectoryEntries(t *testing.T, gotEntries, wantEntries []fs.DirEntry, denied error, calls *[]hookCall) {
	t.Helper()
	if gotEntries == nil || wantEntries == nil || len(gotEntries) != len(wantEntries) {
		t.Fatalf("directory result differs: got=%v, want=%v", gotEntries, wantEntries)
	}
	for i, entry := range gotEntries {
		other := wantEntries[i]
		if entry.Name() != other.Name() || entry.IsDir() != other.IsDir() || entry.Type() != other.Type() {
			t.Fatalf("entry %d differs: got=%v, want=%v", i, entry, other)
		}
		if i > 0 && gotEntries[i-1].Name() >= entry.Name() {
			t.Fatalf("directory is not sorted: %v", gotEntries)
		}
		*calls = nil
		gotInfo, gotErr := entry.Info()
		gotCalls := append([]hookCall(nil), *calls...)
		*calls = nil
		wantInfo, wantErr := other.Info()
		compareFastPathError(t, gotErr, wantErr, denied)
		if !reflect.DeepEqual(gotCalls, *calls) {
			t.Fatalf("entry hook arguments differ: got=%v, want=%v", gotCalls, *calls)
		}
		if gotErr == nil {
			compareFastPathInfo(t, gotInfo, wantInfo)
		}
	}
}

func runFastPathOperation(t *testing.T, fsys fs.FS, hooked bool, name, operation, prefix string, wantCalls int, calls *[]hookCall, original *error) (fs.FileInfo, []fs.DirEntry, error, []hookCall) {
	t.Helper()

	*calls, *original = nil, nil
	var info fs.FileInfo
	var entries []fs.DirEntry
	var err error
	if operation == "Stat" {
		info, err = fs.Stat(fsys, name)
	} else {
		entries, err = fs.ReadDir(fsys, name)
	}
	if hooked && name == "missing" && err != *original {
		t.Fatalf("pass-through hook replaced original missing error: got=%v, original=%v", err, *original)
	}
	if len(*calls) != wantCalls {
		t.Fatalf("hook calls=%v, want %d", *calls, wantCalls)
	}
	if wantCalls != 0 && (*calls)[0].name != path.Join(prefix, name) {
		t.Fatalf("hook path=%q, want %q", (*calls)[0].name, path.Join(prefix, name))
	}
	return info, entries, err, append([]hookCall(nil), *calls...)

}

func checkLazySnapshotInfo(t *testing.T, fail bool, denied error, entry fs.DirEntry, size int64) {
	t.Helper()

	info, err := entry.Info()
	if fail {
		if !errors.Is(err, denied) {
			t.Errorf("metadata error=%v, want %v", err, denied)
		}
		return
	}
	if err != nil || info.Size() != size || info.Mode() != 0o640 || !info.ModTime().IsZero() {
		t.Errorf("snapshot metadata=%v, error=%v, want size=%d/mode=0640/zero timestamp", info, err, size)
	}

}

type hookCall struct {
	name, content string
	missing       bool
}
