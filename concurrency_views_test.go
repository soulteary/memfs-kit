// Copyright 2026 soulteary.
// SPDX-License-Identifier: Apache-2.0

package memfs_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sync"
	"testing"

	memfs "github.com/soulteary/memfs-kit"
)

// This guards the shared-tree synchronization contract. It also passes before
// the hook metadata/lifetime fixes; it covers a gap in the original race suite.
func TestConcurrentParentAndSubtreeWrites(t *testing.T) {
	root := memfs.New()
	if err := root.MkdirAll("data", 0o750); err != nil {
		t.Fatal(err)
	}
	view, err := root.Sub("data")
	if err != nil {
		t.Fatal(err)
	}
	sub := view.(*memfs.FS)
	if err := sub.WriteFile("shared", bytes.Repeat([]byte("a"), 64), 0o640); err != nil {
		t.Fatal(err)
	}
	views := []struct {
		fsys *memfs.FS
		name string
	}{{root, "data/shared"}, {sub, "shared"}}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			writer, reader := views[worker%2], views[(worker+1)%2]
			for iteration := 0; iteration < 64; iteration++ {
				data := bytes.Repeat([]byte{byte('a' + worker)}, 64+iteration)
				if err := writer.fsys.WriteFile(writer.name, data, 0o777); err != nil {
					t.Error(err)
					return
				}
				// The FS must own the published data even across different views.
				data[0] = '!'
				f, err := reader.fsys.Open(reader.name)
				if err != nil {
					t.Error(err)
					return
				}
				info, statErr := f.Stat()
				got, readErr := io.ReadAll(f)
				_ = f.Close()
				if statErr != nil || readErr != nil {
					t.Errorf("read across views: stat=%v, read=%v", statErr, readErr)
					return
				}
				if len(got) < 64 || len(got) > 127 || got[0] < 'a' || got[0] > 'h' ||
					!bytes.Equal(got, bytes.Repeat(got[:1], len(got))) ||
					info.Size() != int64(len(got)) || info.Mode().Perm() != 0o640 {
					t.Errorf("inconsistent cross-view snapshot: data=%q, info=%v", got, info)
					return
				}
			}
		}(worker)
	}
	close(start)
	wg.Wait()

	if err := sub.WriteFile("shared", []byte("visible from parent"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := fs.ReadFile(root, "data/shared")
	if err != nil || string(got) != "visible from parent" {
		t.Fatalf("parent did not observe subtree write: data=%q, err=%v", got, err)
	}
}

// This guards concurrent use of one directory handle. The previous suite
// exercised this contract only for regular file handles.
func TestConcurrentDirectoryReadAndClose(t *testing.T) {
	root := memfs.New()
	for i := 0; i < 128; i++ {
		name := fmt.Sprintf("file-%03d", i)
		if err := root.WriteFile(name, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f, err := root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	dir := f.(fs.ReadDirFile)
	var wg sync.WaitGroup
	var firstReads sync.WaitGroup
	firstReads.Add(4)
	resume := make(chan struct{})
	seen := make(map[string]bool)
	var seenMu sync.Mutex
	checkEntries := func(entries []fs.DirEntry) {
		seenMu.Lock()
		defer seenMu.Unlock()
		for _, entry := range entries {
			if seen[entry.Name()] {
				t.Errorf("directory returned duplicate entry %q", entry.Name())
			}
			seen[entry.Name()] = true
			info, err := entry.Info()
			if err != nil || info.Size() != int64(len(entry.Name())) {
				t.Errorf("entry metadata changed while closing: info=%v, err=%v", info, err)
			}
		}
	}
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entries, err := dir.ReadDir(1)
			firstReads.Done()
			if err != nil || len(entries) != 1 {
				t.Errorf("initial directory read: entries=%v, err=%v", entries, err)
				return
			}
			<-resume
			// Already returned entries remain usable after the handle closes.
			checkEntries(entries)
			for iteration := 0; iteration < 64; iteration++ {
				entries, err := dir.ReadDir(2)
				checkEntries(entries)
				if errors.Is(err, fs.ErrClosed) || errors.Is(err, io.EOF) {
					return
				}
				if err != nil {
					t.Error(err)
					return
				}
				if _, err := dir.Stat(); err != nil && !errors.Is(err, fs.ErrClosed) {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		firstReads.Wait()
		close(resume)
		if err := dir.Close(); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	if _, err := dir.ReadDir(1); !errors.Is(err, fs.ErrClosed) {
		t.Fatalf("closed directory read = %v", err)
	}
}
