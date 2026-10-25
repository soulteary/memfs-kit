// Copyright 2026 soulteary and the memfs-kit contributors.
// SPDX-License-Identifier: Apache-2.0

package memfs_test

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	memfs "github.com/soulteary/memfs-kit"
)

func TestPassthroughHookPreservesOriginalError(t *testing.T) {
	for _, subtree := range []bool{false, true} {
		name := "root"
		if subtree {
			name = "subtree"
		}
		t.Run(name, func(t *testing.T) {
			var original error
			var hookPath string
			root := memfs.New(memfs.WithOpenHook(func(name string, content []byte, err error) ([]byte, error) {
				hookPath, original = name, err
				return content, err
			}))
			var view fs.FS = root
			wantHookPath := "missing.txt"
			if subtree {
				if err := root.MkdirAll("assets", 0o755); err != nil {
					t.Fatal(err)
				}
				var err error
				view, err = root.Sub("assets")
				if err != nil {
					t.Fatal(err)
				}
				wantHookPath = "assets/missing.txt"
			}
			_, err := view.Open("missing.txt")
			if original == nil || err != original {
				t.Fatalf("pass-through hook replaced original error: got %v, original %v", err, original)
			}
			if hookPath != wantHookPath {
				t.Fatalf("hook path = %q, want %q", hookPath, wantHookPath)
			}
			if !errors.Is(err, fs.ErrNotExist) || !os.IsNotExist(err) {
				t.Fatalf("missing error lost classification: %v", err)
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) || pathErr.Op != "open" || pathErr.Path != "missing.txt" || pathErr.Err != fs.ErrNotExist {
				t.Fatalf("unexpected path error: %#v", err)
			}
			_, defaultErr := memfs.New().Open("missing.txt")
			if err.Error() != defaultErr.Error() {
				t.Fatalf("pass-through error = %q, default error = %q", err, defaultErr)
			}
		})
	}
}

func TestHookErrorContextThroughSubtreeAndEntryInfo(t *testing.T) {
	sentinel := errors.New("hook rejected content")
	var hookPath string
	var original error
	root := memfs.New(memfs.WithOpenHook(func(name string, _ []byte, err error) ([]byte, error) {
		hookPath, original = name, err
		return nil, sentinel
	}))
	if err := root.MkdirAll("assets", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("assets/file.txt", []byte("stored"), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := root.Sub("assets")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(view, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || hookPath != "" {
		t.Fatalf("directory listing should not invoke the file hook: entries=%v, hook path=%q", entries, hookPath)
	}
	operations := []struct {
		name string
		run  func() error
	}{
		{"Open", func() error { _, err := view.Open("file.txt"); return err }},
		{"DirEntry.Info", func() error { _, err := entries[0].Info(); return err }},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			hookPath, original = "", nil
			err := operation.run()
			if !errors.Is(err, sentinel) {
				t.Fatalf("hook error lost cause: %v", err)
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) || pathErr.Path != "file.txt" || pathErr.Err != sentinel {
				t.Fatalf("unexpected hook error context: %#v", err)
			}
			if original != nil || hookPath != "assets/file.txt" {
				t.Fatalf("existing-file hook: path=%q original=%v", hookPath, original)
			}
		})
	}
}

var errNonComparableHook = errors.New("non-comparable hook error")

// Error implementations may contain slices and cannot be compared for equality.
type nonComparableHookError struct {
	details []string
}

func (e nonComparableHookError) Error() string { return e.details[0] }
func (e nonComparableHookError) Is(target error) bool {
	return target == errNonComparableHook
}

func TestNonComparableHookErrorsDoNotPanic(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "missing"
		if existing {
			name = "existing"
		}
		t.Run(name, func(t *testing.T) {
			var original error
			root := memfs.New(memfs.WithOpenHook(func(_ string, _ []byte, err error) ([]byte, error) {
				original = err
				return nil, nonComparableHookError{details: []string{"custom error"}}
			}))
			if existing {
				if err := root.WriteFile("file.txt", []byte("stored"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, err := root.Open("file.txt")
			if !errors.Is(err, errNonComparableHook) {
				t.Fatalf("custom error lost classification: %v", err)
			}
			var custom nonComparableHookError
			if !errors.As(err, &custom) || len(custom.details) != 1 || custom.details[0] != "custom error" {
				t.Fatalf("custom error lost payload: %v", err)
			}
			if existing && original != nil {
				t.Fatalf("existing file got original error: %v", original)
			}
			if !existing && !errors.Is(original, fs.ErrNotExist) {
				t.Fatalf("missing file original error = %v", original)
			}
		})
	}
}
