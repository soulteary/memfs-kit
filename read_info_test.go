// Copyright 2026 soulteary and the memfs-kit contributors.
// SPDX-License-Identifier: Apache-2.0

package memfs_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	memfs "github.com/soulteary/memfs-kit"
)

func TestReadCursorBoundaries(t *testing.T) {
	for _, content := range []string{"", "0123456789"} {
		t.Run(fmt.Sprintf("size=%d", len(content)), func(t *testing.T) {
			root := memfs.New()
			mustWrite(t, root, "file", content)
			file := mustOpen(t, root, "file")
			seeker, readerAt := file.(io.Seeker), file.(io.ReaderAt)
			want := bytes.NewReader([]byte(content))
			operations := []struct {
				kind   string
				size   int
				offset int64
				whence int
			}{
				{kind: "read", size: 0},
				{kind: "read", size: 1},
				{kind: "readat", size: 3, offset: 4},
				{kind: "seek", whence: io.SeekCurrent},
				{kind: "read", size: 3},
				{kind: "read", size: 0},
				{kind: "seek", offset: -2, whence: io.SeekEnd},
				{kind: "read", size: 5},
				{kind: "read", size: 0},
				{kind: "read", size: 1},
				{kind: "seek", offset: 3, whence: io.SeekEnd},
				{kind: "read", size: 0},
				{kind: "readat", size: 2, offset: -1},
				{kind: "readat", size: 0, offset: int64(len(content))},
				{kind: "readat", size: 0, offset: 0},
				{kind: "seek", offset: math.MaxInt64, whence: io.SeekStart},
				{kind: "read", size: 1},
				{kind: "seek", offset: 1, whence: io.SeekCurrent},
				{kind: "seek", offset: -math.MaxInt64, whence: io.SeekCurrent},
				{kind: "read", size: 1},
			}
			errorText := func(err error) string {
				if err == nil {
					return ""
				}
				return err.Error()
			}
			for i, op := range operations {
				if op.kind == "seek" {
					gotOffset, gotErr := seeker.Seek(op.offset, op.whence)
					wantOffset, wantErr := want.Seek(op.offset, op.whence)
					if gotOffset != wantOffset || errorText(gotErr) != errorText(wantErr) {
						t.Fatalf("operation %d Seek(%d,%d)=(%d,%v), want (%d,%v)", i, op.offset, op.whence, gotOffset, gotErr, wantOffset, wantErr)
					}
					continue
				}
				gotBuffer, wantBuffer := bytes.Repeat([]byte{0xcc}, op.size), bytes.Repeat([]byte{0xcc}, op.size)
				var gotN, wantN int
				var gotErr, wantErr error
				if op.kind == "read" {
					gotN, gotErr = file.Read(gotBuffer)
					wantN, wantErr = want.Read(wantBuffer)
				} else {
					gotN, gotErr = readerAt.ReadAt(gotBuffer, op.offset)
					wantN, wantErr = want.ReadAt(wantBuffer, op.offset)
				}
				if gotN != wantN || errorText(gotErr) != errorText(wantErr) || !bytes.Equal(gotBuffer, wantBuffer) {
					t.Fatalf("operation %d %s: got=(%d,%x,%v), want=(%d,%x,%v)", i, op.kind, gotN, gotBuffer, gotErr, wantN, wantBuffer, wantErr)
				}
				gotOffset, _ := seeker.Seek(0, io.SeekCurrent)
				wantOffset, _ := want.Seek(0, io.SeekCurrent)
				if gotOffset != wantOffset {
					t.Fatalf("operation %d moved cursor to %d, want %d", i, gotOffset, wantOffset)
				}
			}
		})
	}
}

func TestConcurrentSmallReadsConsumeEachByteOnce(t *testing.T) {
	root := memfs.New()
	data := make([]byte, 256)
	for i := range data {
		data[i] = byte(i)
	}
	if err := root.WriteFile("file", data, 0o640); err != nil {
		t.Fatal(err)
	}
	file := mustOpen(t, root, "file")
	seeker, readerAt := file.(io.Seeker), file.(io.ReaderAt)
	closed := make(chan struct{})
	var observers, ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		observers.Go(func() {
			first := true
			for {
				pos, seekErr := seeker.Seek(0, io.SeekCurrent)
				if seekErr == nil && (pos < 0 || pos > int64(len(data))) || seekErr != nil && !errors.Is(seekErr, fs.ErrClosed) {
					t.Errorf("concurrent cursor query=(%d,%v)", pos, seekErr)
				}
				var buffer [1]byte
				n, readErr := readerAt.ReadAt(buffer[:], 77)
				if readErr == nil && (n != 1 || buffer[0] != 77) || readErr != nil && !errors.Is(readErr, fs.ErrClosed) {
					t.Errorf("concurrent ReadAt=(%d,%v,%v)", n, buffer, readErr)
				}
				info, statErr := file.Stat()
				if statErr == nil && info.Size() != int64(len(data)) || statErr != nil && !errors.Is(statErr, fs.ErrClosed) {
					t.Errorf("concurrent Stat=(%v,%v)", info, statErr)
				}
				if first {
					ready.Done()
					first = false
				}
				select {
				case <-closed:
					if _, err := file.Read(nil); !errors.Is(err, fs.ErrClosed) {
						t.Errorf("zero-length Read after Close=%v", err)
					}
					return
				default:
				}
			}
		})
	}
	ready.Wait()
	readBytes := make(chan byte, 8*(len(data)+1))
	var readers sync.WaitGroup
	for range 8 {
		readers.Go(func() {
			var buffer [1]byte
			for attempt := 0; attempt <= len(data); attempt++ {
				n, err := file.Read(buffer[:])
				if n == 1 {
					readBytes <- buffer[0]
				}
				if err != nil {
					if !errors.Is(err, io.EOF) {
						t.Errorf("small Read=%v", err)
					}
					return
				}
				if n != 1 {
					t.Errorf("small Read made no progress: %d", n)
					return
				}
			}
			t.Error("small reads did not reach EOF after more reads than file bytes")
		})
	}
	readers.Wait()
	if err := file.Close(); err != nil {
		t.Error(err)
	}
	close(closed)
	observers.Wait()
	close(readBytes)
	var counts [256]int
	for value := range readBytes {
		counts[value]++
	}
	for value, count := range counts {
		if count != 1 {
			t.Errorf("byte %d consumed %d times, want once", value, count)
		}
	}
}

func TestDirectoryEntryMetadataSnapshots(t *testing.T) {
	for _, hooked := range []bool{false, true} {
		t.Run(fmt.Sprintf("hook=%t", hooked), func(t *testing.T) {
			var calls atomic.Int32
			var options []memfs.Option
			if hooked {
				options = append(options, memfs.WithOpenHook(func(_ string, content []byte, err error) ([]byte, error) {
					calls.Add(1)
					return content, err
				}))
			}
			root := memfs.New(options...)
			if err := root.MkdirAll("dir", 0o750); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, root, "file", "old")
			beforeFile, err := fs.Stat(root, "file")
			if err != nil {
				t.Fatal(err)
			}
			beforeDir, err := fs.Stat(root, "dir")
			if err != nil {
				t.Fatal(err)
			}
			entries, err := fs.ReadDir(root, ".")
			if err != nil {
				t.Fatal(err)
			}
			calls.Store(0)
			oldFile, oldDir := findHookTestEntry(t, entries, "file"), findHookTestEntry(t, entries, "dir")
			mustWrite(t, root, "file", "replacement is longer")
			mustWrite(t, root, "dir/new-child", "changes the directory timestamp")
			currentDir, err := fs.Stat(root, "dir")
			if err != nil {
				t.Fatal(err)
			}
			if currentDir.ModTime().Equal(beforeDir.ModTime()) {
				t.Fatal("creating a directory child did not change its modification time")
			}
			for range 8 {
				for _, pair := range []struct {
					entry fs.DirEntry
					want  fs.FileInfo
				}{{oldFile, beforeFile}, {oldDir, beforeDir}} {
					info, err := pair.entry.Info()
					if err != nil {
						t.Fatal(err)
					}
					compareFastPathInfo(t, info, pair.want)
				}
			}
			wantCalls := int32(0)
			if hooked {
				wantCalls = 1
			}
			if got := calls.Load(); got != wantCalls {
				t.Fatalf("repeated file/directory Info invoked hooks %d times, want %d", got, wantCalls)
			}
			fresh, err := fs.ReadDir(root, ".")
			if err != nil {
				t.Fatal(err)
			}
			freshDir, err := findHookTestEntry(t, fresh, "dir").Info()
			if err != nil {
				t.Fatal(err)
			}
			compareFastPathInfo(t, freshDir, currentDir)
			// Holding just FileInfo must keep its immutable metadata usable even
			// after entry slices are discarded and the current tree changes again.
			heldFile, err := oldFile.Info()
			if err != nil {
				t.Fatal(err)
			}
			heldDir, err := oldDir.Info()
			if err != nil {
				t.Fatal(err)
			}
			oldFile, oldDir, entries, fresh = nil, nil, nil, nil
			mustWrite(t, root, "file", "another replacement")
			mustWrite(t, root, "dir/another-child", "another directory timestamp change")
			runtime.GC()
			var readers sync.WaitGroup
			for range 8 {
				readers.Go(func() {
					for range 32 {
						for _, pair := range []struct {
							info, want fs.FileInfo
						}{{heldFile, beforeFile}, {heldDir, beforeDir}} {
							if pair.info.Name() != pair.want.Name() || pair.info.Size() != pair.want.Size() ||
								pair.info.Mode() != pair.want.Mode() || !pair.info.ModTime().Equal(pair.want.ModTime()) ||
								pair.info.IsDir() != pair.want.IsDir() || pair.info.Sys() != nil {
								t.Errorf("retained metadata changed: got=%v, want=%v", pair.info, pair.want)
								return
							}
						}
					}
				})
			}
			readers.Wait()
		})
	}
}

func TestHookInfoPanicIsCached(t *testing.T) {
	panicValue := &struct{ message string }{"hook panic"}
	var calls atomic.Int32
	root := memfs.New(memfs.WithOpenHook(func(_ string, _ []byte, _ error) ([]byte, error) {
		calls.Add(1)
		panic(panicValue)
	}))
	mustWrite(t, root, "file", "stored")
	list := func() fs.DirEntry {
		entries, err := fs.ReadDir(root, ".")
		if err != nil {
			t.Fatal(err)
		}
		return findHookTestEntry(t, entries, "file")
	}
	entry := list()
	if calls.Load() != 0 {
		t.Fatal("listing invoked the hook")
	}
	invoke := func(entry fs.DirEntry) (recovered any) {
		defer func() { recovered = recover() }()
		_, _ = entry.Info()
		return nil
	}
	start := make(chan struct{})
	panics := make(chan any, 16)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			<-start
			panics <- invoke(entry)
		})
	}
	close(start)
	wg.Wait()
	close(panics)
	for recovered := range panics {
		if recovered != panicValue {
			t.Errorf("Info recovered %v, want original panic value %v", recovered, panicValue)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent panicking Info invoked hook %d times, want once", got)
	}
	if recovered := invoke(entry); recovered != panicValue || calls.Load() != 1 {
		t.Fatalf("later Info did not replay the cached panic: recovered=%v, calls=%d", recovered, calls.Load())
	}
	// Another listing has its own lazy result, even after the first panics.
	if recovered := invoke(list()); recovered != panicValue || calls.Load() != 2 {
		t.Fatalf("another listing reused the first panic cache: recovered=%v, calls=%d", recovered, calls.Load())
	}
	mustWrite(t, root, "after-panic", "filesystem remains writable")
}
