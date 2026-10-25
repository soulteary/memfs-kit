// Copyright 2026 soulteary.
// SPDX-License-Identifier: Apache-2.0

package memfs_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	memfs "github.com/soulteary/memfs-kit"
)

func mustWrite(t *testing.T, fsys *memfs.FS, name, content string) {
	t.Helper()
	if err := fsys.WriteFile(name, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func mustOpen(t *testing.T, fsys fs.FS, name string) fs.File {
	t.Helper()
	f, err := fsys.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestFilesystemConformance(t *testing.T) {
	fsys := memfs.New()
	for _, name := range []string{"empty", "nested/deep"} {
		if err := fsys.MkdirAll(name, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	names := []string{"z.txt", "a.txt", "middle.txt", "nested/b.txt", "nested/a.txt", "nested/deep/leaf.txt"}
	for _, name := range names {
		mustWrite(t, fsys, name, name)
	}
	if err := fstest.TestFS(fsys, names...); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".", "nested", "nested/deep", "empty"} {
		t.Run(dir, func(t *testing.T) {
			sub, err := fsys.Sub(dir)
			if err != nil {
				t.Fatal(err)
			}
			info, err := fs.Stat(sub, ".")
			if err != nil || info.Name() != "." || !info.IsDir() {
				t.Fatalf("subtree root: info=%v, err=%v", info, err)
			}
			var expected []string
			for _, name := range names {
				if dir == "." {
					expected = append(expected, name)
				} else if strings.HasPrefix(name, dir+"/") {
					expected = append(expected, strings.TrimPrefix(name, dir+"/"))
				}
			}
			if err := fstest.TestFS(sub, expected...); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDirectoryPaginationSnapshot(t *testing.T) {
	fsys := memfs.New()
	for _, name := range []string{"e", "c", "a", "d", "b"} {
		mustWrite(t, fsys, name, name)
	}
	dir := mustOpen(t, fsys, ".").(fs.ReadDirFile)
	// Opening a directory captures a stable listing, including entry metadata.
	mustWrite(t, fsys, "f", "created after open")
	mustWrite(t, fsys, "a", "rewritten after open")
	var got []string
	for attempt := 0; attempt < 5; attempt++ {
		entries, err := dir.ReadDir(2)
		if len(entries) > 2 {
			t.Fatalf("ReadDir(2) returned %d entries", len(entries))
		}
		for _, entry := range entries {
			got = append(got, entry.Name())
			if entry.Name() == "a" {
				info, infoErr := entry.Info()
				if infoErr != nil || info.Size() != 1 {
					t.Fatalf("directory snapshot changed: info=%v, err=%v", info, infoErr)
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"a", "b", "c", "d", "e"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paginated entries = %v, want %v", got, want)
	}
	if entries, err := dir.ReadDir(1); len(entries) != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("exhausted ReadDir(1) = %v, %v", entries, err)
	}
	if entries, err := dir.ReadDir(-1); len(entries) != 0 || err != nil {
		t.Fatalf("exhausted ReadDir(-1) = %v, %v", entries, err)
	}
	if err := dir.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := dir.ReadDir(1); !errors.Is(err, fs.ErrClosed) {
		t.Fatalf("ReadDir after Close = %v", err)
	}
	if _, err := dir.Stat(); !errors.Is(err, fs.ErrClosed) {
		t.Fatalf("directory Stat after Close = %v", err)
	}
	if _, err := dir.Read(make([]byte, 1)); !errors.Is(err, fs.ErrClosed) {
		t.Fatalf("directory Read after Close = %v", err)
	}
}

func TestMkdirAllConflictReleasesLocks(t *testing.T) {
	fsys := memfs.New()
	mustWrite(t, fsys, "file", "content")
	if err := fsys.MkdirAll("file/child", 0o755); err == nil {
		t.Fatal("MkdirAll accepted a regular file as a parent")
	}
	done := make(chan error, 1)
	go func() { done <- fsys.MkdirAll("after/child", 0o755) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("MkdirAll left a lock held after its error return")
	}
}

func TestWriteAndOpenIsolation(t *testing.T) {
	fsys := memfs.New()
	input := []byte("original")
	if err := fsys.WriteFile("file", input, 0o640); err != nil {
		t.Fatal(err)
	}
	input[0] = 'X'
	old := mustOpen(t, fsys, "file")
	if err := fsys.WriteFile("file", []byte("replacement"), 0o777); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(old)
	if err != nil || string(got) != "original" {
		t.Fatalf("opened file changed: %q, %v", got, err)
	}
	info, err := old.Stat()
	if err != nil || info.Size() != int64(len("original")) {
		t.Fatalf("Stat size changed after Read: info=%v, err=%v", info, err)
	}
	current, err := fs.Stat(fsys, "file")
	if err != nil || current.Mode().Perm() != 0o640 {
		t.Fatalf("overwrite changed existing permissions: info=%v, err=%v", current, err)
	}
	got, err = fs.ReadFile(fsys, "file")
	if err != nil || string(got) != "replacement" {
		t.Fatalf("new content = %q, %v", got, err)
	}
	got[0] = 'Y'
	again, err := fs.ReadFile(fsys, "file")
	if err != nil || string(again) != "replacement" {
		t.Fatalf("ReadFile leaked mutable storage: %q, %v", again, err)
	}
	if err := fsys.WriteFile(".", []byte("invalid"), 0o640); err == nil {
		t.Fatal("WriteFile replaced the root directory")
	}
	if err := fstest.TestFS(fsys, "file"); err != nil {
		t.Fatal(err)
	}
}

func TestSeekReadAtAndClosedFile(t *testing.T) {
	fsys := memfs.New()
	mustWrite(t, fsys, "file", "0123456789")
	f := mustOpen(t, fsys, "file")
	seeker, ok := f.(io.Seeker)
	if !ok {
		t.Fatal("file does not implement io.Seeker")
	}
	readerAt, ok := f.(io.ReaderAt)
	if !ok {
		t.Fatal("file does not implement io.ReaderAt")
	}
	if pos, err := seeker.Seek(-3, io.SeekEnd); pos != 7 || err != nil {
		t.Fatalf("SeekEnd(-3) = %d, %v", pos, err)
	}
	buf := make([]byte, 4)
	if n, err := readerAt.ReadAt(buf, 8); n != 2 || !errors.Is(err, io.EOF) || string(buf[:n]) != "89" {
		t.Fatalf("ReadAt near EOF = %d, %q, %v", n, buf[:n], err)
	}
	if pos, err := seeker.Seek(0, io.SeekCurrent); pos != 7 || err != nil {
		t.Fatalf("ReadAt moved cursor to %d: %v", pos, err)
	}
	if _, err := seeker.Seek(-1, io.SeekStart); err == nil {
		t.Fatal("negative seek succeeded")
	}
	if _, err := seeker.Seek(0, 99); err == nil {
		t.Fatal("invalid seek whence succeeded")
	}
	if pos, err := seeker.Seek(0, io.SeekCurrent); pos != 7 || err != nil {
		t.Fatalf("invalid Seek changed cursor to %d: %v", pos, err)
	}
	if pos, err := seeker.Seek(100, io.SeekStart); pos != 100 || err != nil {
		t.Fatalf("seek beyond EOF = %d, %v", pos, err)
	}
	if n, err := f.Read(buf); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("Read beyond EOF = %d, %v", n, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	checks := map[string]func() error{
		"Read":   func() error { _, err := f.Read(buf); return err },
		"ReadAt": func() error { _, err := readerAt.ReadAt(buf, 0); return err },
		"Seek":   func() error { _, err := seeker.Seek(0, io.SeekStart); return err },
		"Stat":   func() error { _, err := f.Stat(); return err },
		"Close":  f.Close,
	}
	for name, check := range checks {
		if err := check(); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("%s after Close = %v", name, err)
		}
	}
}

func TestHTTPRangeRequest(t *testing.T) {
	fsys := memfs.New()
	mustWrite(t, fsys, "file.txt", "0123456789")
	req := httptest.NewRequest(http.MethodGet, "/file.txt", nil)
	req.Header.Set("Range", "bytes=2-5")
	response := httptest.NewRecorder()
	http.FileServer(http.FS(fsys)).ServeHTTP(response, req)
	if response.Code != http.StatusPartialContent || response.Body.String() != "2345" {
		t.Fatalf("Range response = %d, %q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Range"); got != "bytes 2-5/10" {
		t.Fatalf("Content-Range = %q", got)
	}
}

func TestHTTPHookDoesNotReuseStoredModificationTime(t *testing.T) {
	var replacement string
	fsys := memfs.New(memfs.WithOpenHook(func(_ string, content []byte, err error) ([]byte, error) {
		if replacement != "" {
			return []byte(replacement), err
		}
		return content, err
	}))
	mustWrite(t, fsys, "file.txt", "stored content")
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := entries[0].Info()
	if err != nil || stored.ModTime().IsZero() {
		t.Fatalf("stored metadata = %v, %v", stored, err)
	}
	passthrough, err := fs.Stat(fsys, "file.txt")
	if err != nil || !passthrough.ModTime().Equal(stored.ModTime()) {
		t.Fatalf("passthrough hook changed modification time: info=%v, err=%v", passthrough, err)
	}

	handler := http.FileServer(http.FS(fsys))
	replacement = "first"
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/file.txt", nil))
	if first.Code != http.StatusOK || first.Body.String() != "first" {
		t.Fatalf("first dynamic response = %d, %q", first.Code, first.Body.String())
	}
	if got := first.Header().Get("Last-Modified"); got != "" {
		t.Fatalf("dynamic content advertised stale Last-Modified: %q", got)
	}

	replacement = "second"
	req := httptest.NewRequest(http.MethodGet, "/file.txt", nil)
	// A client may still hold a validator from before a hook was installed.
	// The stored file has not changed, but the bytes returned by the hook have.
	req.Header.Set("If-Modified-Since", stored.ModTime().UTC().Format(http.TimeFormat))
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)
	if second.Code != http.StatusOK || second.Body.String() != "second" {
		t.Fatalf("conditional dynamic response = %d, %q; want 200 with updated bytes", second.Code, second.Body.String())
	}
	if got := second.Header().Get("Last-Modified"); got != "" {
		t.Fatalf("conditional dynamic response advertised stale Last-Modified: %q", got)
	}
}

func TestSubtreeIsolationAndHookInheritance(t *testing.T) {
	var hookPaths []string
	fsys := memfs.New(memfs.WithOpenHook(func(name string, content []byte, err error) ([]byte, error) {
		hookPaths = append(hookPaths, name)
		return bytes.ToUpper(content), err
	}))
	if err := fsys.MkdirAll("inside/nested", 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, fsys, "outside", "secret")
	mustWrite(t, fsys, "inside/nested/file", "content")
	sub, err := fsys.Sub("inside")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Open("../outside"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("subtree traversal = %v", err)
	}
	if _, err := sub.Open("outside"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("subtree escaped its root: %v", err)
	}
	nested, err := fs.Sub(sub, "nested")
	if err != nil {
		t.Fatal(err)
	}
	got, err := fs.ReadFile(nested, "file")
	if err != nil || string(got) != "CONTENT" {
		t.Fatalf("subtree lost hook: content=%q, err=%v", got, err)
	}
	if want := []string{"inside/outside", "inside/nested/file"}; !reflect.DeepEqual(hookPaths, want) {
		t.Fatalf("subtree hook paths = %v, want %v", hookPaths, want)
	}
}

func TestOpenHookMissingFilesAndErrors(t *testing.T) {
	denied := errors.New("hook refused this request")
	var called []string
	fsys := memfs.New(memfs.WithOpenHook(func(name string, content []byte, origErr error) ([]byte, error) {
		called = append(called, name)
		if !errors.Is(origErr, fs.ErrNotExist) || content != nil {
			t.Errorf("missing file hook arguments: path=%q, content=%q, error=%v", name, content, origErr)
		}
		var pathErr *fs.PathError
		if !errors.As(origErr, &pathErr) {
			t.Errorf("hook original error is not a PathError: %v", origErr)
		}
		switch name {
		case "virtual":
			return []byte("generated"), nil
		case "empty":
			return nil, nil
		case "denied":
			return nil, denied
		default:
			return content, origErr
		}
	}))
	if err := fsys.MkdirAll("directory", 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, fsys, "file", "content")
	got, err := fs.ReadFile(fsys, "virtual")
	if err != nil || string(got) != "generated" {
		t.Fatalf("generated file = %q, %v", got, err)
	}
	got, err = fs.ReadFile(fsys, "empty")
	if err != nil || len(got) != 0 {
		t.Fatalf("generated empty file = %q, %v", got, err)
	}
	if _, err := fsys.Open("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("hook lost original missing-file error: %v", err)
	}
	if _, err := fsys.Open("denied"); !errors.Is(err, denied) {
		t.Fatalf("hook lost callback error: %v", err)
	}
	for _, name := range []string{"../invalid", "file/child"} {
		if _, err := fsys.Open(name); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Open(%q) = %v, want ErrInvalid", name, err)
		}
	}
	_ = mustOpen(t, fsys, "directory")
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if want := []string{"directory", "file"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("hook persisted virtual files: %v", names)
	}
	if want := []string{"virtual", "empty", "missing", "denied"}; !reflect.DeepEqual(called, want) {
		t.Fatalf("hook called for directory or invalid path: %v", called)
	}
}

func TestOpenHookContentIsolation(t *testing.T) {
	var calls int
	output := []byte("hook result")
	fsys := memfs.New(memfs.WithOpenHook(func(_ string, content []byte, err error) ([]byte, error) {
		calls++
		if calls == 1 {
			content[0] = 'X'
			return output, nil
		}
		return content, err
	}))
	mustWrite(t, fsys, "file", "original")
	f := mustOpen(t, fsys, "file")
	output[0] = 'Y'
	got, err := io.ReadAll(f)
	if err != nil || string(got) != "hook result" {
		t.Fatalf("hook result aliases caller storage: %q, %v", got, err)
	}
	got, err = fs.ReadFile(fsys, "file")
	if err != nil || string(got) != "original" {
		t.Fatalf("hook input mutation changed stored file: %q, %v", got, err)
	}
}

func TestOpenHookCanUseFilesystem(t *testing.T) {
	var fsys *memfs.FS
	fsys = memfs.New(memfs.WithOpenHook(func(_ string, content []byte, err error) ([]byte, error) {
		if mkdirErr := fsys.MkdirAll("created/by/hook", 0o755); mkdirErr != nil {
			return nil, mkdirErr
		}
		return content, err
	}))
	mustWrite(t, fsys, "file", "content")
	done := make(chan error, 1)
	go func() { _, err := fs.ReadFile(fsys, "file"); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Open invoked the hook while holding a filesystem lock")
	}
}

func TestConcurrentFilesystemOperations(t *testing.T) {
	fsys := memfs.New()
	if err := fsys.MkdirAll("data", 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, fsys, "data/shared", "worker:initial")
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < 64; iteration++ {
				content := []byte(fmt.Sprintf("worker:%d:%d", worker, iteration))
				if err := fsys.WriteFile("data/shared", content, 0o600); err != nil {
					t.Error(err)
					return
				}
				content[0] = 'X'
				if err := fsys.MkdirAll(fmt.Sprintf("data/worker-%d", worker), 0o755); err != nil {
					t.Error(err)
					return
				}
				f, err := fsys.Open("data/shared")
				if err != nil {
					t.Error(err)
					return
				}
				info, statErr := f.Stat()
				got, readErr := io.ReadAll(f)
				_ = f.Close()
				if statErr != nil || readErr != nil || !bytes.HasPrefix(got, []byte("worker:")) || info.Size() != int64(len(got)) {
					t.Errorf("inconsistent snapshot: info=%v, data=%q, stat=%v, read=%v", info, got, statErr, readErr)
					return
				}
				entries, err := fs.ReadDir(fsys, "data")
				if err != nil {
					t.Error(err)
					return
				}
				names := make([]string, len(entries))
				for i, entry := range entries {
					names[i] = entry.Name()
				}
				if !sort.StringsAreSorted(names) {
					t.Errorf("concurrent directory listing is unsorted: %v", names)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
}

func TestConcurrentFileHandleOperations(t *testing.T) {
	fsys := memfs.New()
	mustWrite(t, fsys, "file", "0123456789")
	f := mustOpen(t, fsys, "file")
	seeker := f.(io.Seeker)
	readerAt := f.(io.ReaderAt)
	var wg sync.WaitGroup
	var halfway sync.WaitGroup
	halfway.Add(4)
	start := make(chan struct{})
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			buf := make([]byte, 3)
			for iteration := 0; iteration < 100; iteration++ {
				if iteration == 50 {
					halfway.Done()
				}
				if _, err := seeker.Seek(0, io.SeekStart); err != nil && !errors.Is(err, fs.ErrClosed) {
					t.Error(err)
				}
				if _, err := f.Read(buf); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, fs.ErrClosed) {
					t.Error(err)
				}
				if n, err := readerAt.ReadAt(buf, 3); err == nil {
					if n != 3 || string(buf) != "345" {
						t.Errorf("ReadAt = %d, %q", n, buf)
					}
				} else if !errors.Is(err, fs.ErrClosed) {
					t.Error(err)
				}
				if _, err := f.Stat(); err != nil && !errors.Is(err, fs.ErrClosed) {
					t.Error(err)
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		halfway.Wait()
		_ = f.Close()
	}()
	close(start)
	wg.Wait()
}
