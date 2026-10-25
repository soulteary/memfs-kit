// Copyright 2026 soulteary and the memfs-kit contributors.
// SPDX-License-Identifier: Apache-2.0

package memfs_test

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	memfs "github.com/soulteary/memfs-kit"
)

func findHookTestEntry(t *testing.T, entries []fs.DirEntry, name string) fs.DirEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Name() == name {
			return entry
		}
	}
	t.Fatalf("directory has no entry %q", name)
	return nil
}

func TestHookMetadataStandardLibrary(t *testing.T) {
	root := memfs.New(memfs.WithOpenHook(func(_ string, content []byte, err error) ([]byte, error) {
		if err != nil {
			return content, err
		}
		return append([]byte("expanded:"), content...), nil
	}))
	mustWrite(t, root, "file.txt", "original")

	t.Run("fstest", func(t *testing.T) {
		if err := fstest.TestFS(root, "file.txt"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("tar", func(t *testing.T) {
		var archive bytes.Buffer
		writer := tar.NewWriter(&archive)
		if err := writer.AddFS(root); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		reader := tar.NewReader(&archive)
		header, err := reader.Next()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if want := "expanded:original"; header.Name != "file.txt" || header.Size != int64(len(want)) || string(content) != want {
			t.Fatalf("tar entry: name=%q size=%d content=%q", header.Name, header.Size, content)
		}
		if _, err := reader.Next(); err != io.EOF {
			t.Fatalf("tar end = %v, want EOF", err)
		}
	})
}

func TestHookMetadataDirectorySnapshot(t *testing.T) {
	for _, transform := range []bool{false, true} {
		name := "passthrough"
		if transform {
			name = "transformed"
		}
		t.Run(name, func(t *testing.T) {
			root := memfs.New(memfs.WithOpenHook(func(_ string, content []byte, err error) ([]byte, error) {
				if err != nil || !transform {
					return content, err
				}
				return append([]byte("expanded:"), content...), nil
			}))
			mustWrite(t, root, "file", "old")
			before, err := fs.Stat(root, "file")
			if err != nil {
				t.Fatal(err)
			}
			dir := mustOpen(t, root, ".").(fs.ReadDirFile)
			if err := root.WriteFile("file", []byte("new and longer content"), 0o777); err != nil {
				t.Fatal(err)
			}
			entries, err := dir.ReadDir(-1)
			if err != nil {
				t.Fatal(err)
			}
			entry := findHookTestEntry(t, entries, "file")
			info, err := entry.Info()
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() != before.Size() || info.Mode() != before.Mode() || !info.ModTime().Equal(before.ModTime()) {
				t.Fatalf("directory snapshot changed: got=%v, before=%v", info, before)
			}
			current, err := fs.Stat(root, "file")
			if err != nil || current.Size() == info.Size() {
				t.Fatalf("replacement did not remain separate: current=%v, snapshot=%v, err=%v", current, info, err)
			}
		})
	}
}

func TestHookMetadataSubtreePath(t *testing.T) {
	var paths []string
	root := memfs.New(memfs.WithOpenHook(func(name string, content []byte, err error) ([]byte, error) {
		paths = append(paths, name)
		return append(content, '!'), err
	}))
	if err := root.MkdirAll("assets/deep", 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, root, "assets/deep/file.txt", "x")
	assets, err := fs.Sub(root, "assets")
	if err != nil {
		t.Fatal(err)
	}
	deep, err := fs.Sub(assets, "deep")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(deep, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("directory listing invoked hook: %v", paths)
	}
	info, err := findHookTestEntry(t, entries, "file.txt").Info()
	if err != nil || info.Size() != 2 {
		t.Fatalf("subtree metadata = %v, %v", info, err)
	}
	if len(paths) != 1 || paths[0] != "assets/deep/file.txt" {
		t.Fatalf("subtree hook paths = %v", paths)
	}
}

func TestHookMetadataLazyConcurrentInfo(t *testing.T) {
	var calls atomic.Int32
	root := memfs.New(memfs.WithOpenHook(func(_ string, content []byte, err error) ([]byte, error) {
		calls.Add(1)
		return append(content, []byte("-expanded")...), err
	}))
	mustWrite(t, root, "file", "x")
	entries, err := fs.ReadDir(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	entry := findHookTestEntry(t, entries, "file")
	if entry.IsDir() || entry.Type() != 0 || entry.Name() != "file" {
		t.Fatalf("entry identity is incorrect: %v", entry)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("listing or identity methods called hook %d times", got)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			info, err := entry.Info()
			if err != nil {
				t.Error(err)
				return
			}
			if info.Size() != int64(len("x-expanded")) || !info.ModTime().IsZero() {
				t.Errorf("resolved info = %v", info)
			}
		})
	}
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent Info invoked hook %d times, want one", got)
	}
	if _, err := entry.Info(); err != nil || calls.Load() != 1 {
		t.Fatalf("cached Info = %v, calls=%d", err, calls.Load())
	}
}

func TestHookMetadataPreservesError(t *testing.T) {
	denied := errors.New("hook denied metadata")
	var calls atomic.Int32
	root := memfs.New(memfs.WithOpenHook(func(_ string, _ []byte, _ error) ([]byte, error) {
		calls.Add(1)
		return nil, denied
	}))
	mustWrite(t, root, "file", "x")
	entries, err := fs.ReadDir(root, ".")
	if err != nil {
		t.Fatalf("listing unexpectedly evaluated metadata: %v", err)
	}
	entry := findHookTestEntry(t, entries, "file")
	for range 2 {
		if _, err := entry.Info(); !errors.Is(err, denied) {
			t.Fatalf("Info error = %v, want hook error", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("error result not cached: %d calls", calls.Load())
	}
}

func TestHookMetadataCallbackRunsOutsideTreeLock(t *testing.T) {
	var root *memfs.FS
	root = memfs.New(memfs.WithOpenHook(func(_ string, content []byte, err error) ([]byte, error) {
		if err != nil {
			return content, err
		}
		if err := root.WriteFile("side-effect", []byte("created by hook"), 0o644); err != nil {
			return nil, err
		}
		return append(content, '!'), nil
	}))
	mustWrite(t, root, "file", "x")
	entries, err := fs.ReadDir(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	entry := findHookTestEntry(t, entries, "file")
	done := make(chan error, 1)
	go func() {
		info, err := entry.Info()
		if err == nil && info.Size() != 2 {
			err = errors.New("Info did not use hook output")
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("metadata hook blocked while writing to the filesystem")
	}
	entries, err = fs.ReadDir(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	findHookTestEntry(t, entries, "side-effect")
}
