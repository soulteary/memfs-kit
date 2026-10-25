// Copyright 2026 soulteary.
// SPDX-License-Identifier: Apache-2.0

package memfs_test

import (
	"bytes"
	"errors"
	"io/fs"
	"path"
	"testing"

	memfs "github.com/soulteary/memfs-kit"
)

func FuzzPathValidation(f *testing.F) {
	for _, name := range []string{".", "", "file", "a/b", "/absolute", "../escape", "a/../b", "a//b", "a/./b", "a/", "a\\b", "a\x00b", "\xff", "目录/文件"} {
		f.Add(name, []byte("content"))
	}
	f.Fuzz(func(t *testing.T, name string, content []byte) {
		if len(name) > 4096 || len(content) > 64*1024 {
			t.Skip()
		}
		fsys := memfs.New()
		if !fs.ValidPath(name) {
			operations := []struct {
				name string
				run  func() error
			}{
				{"Open", func() error { _, err := fsys.Open(name); return err }},
				{"Stat", func() error { _, err := fsys.Stat(name); return err }},
				{"ReadDir", func() error { _, err := fsys.ReadDir(name); return err }},
				{"Sub", func() error { _, err := fsys.Sub(name); return err }},
				{"MkdirAll", func() error { return fsys.MkdirAll(name, 0o755) }},
				{"WriteFile", func() error { return fsys.WriteFile(name, content, 0o644) }},
			}
			for _, operation := range operations {
				err := operation.run()
				var pathErr *fs.PathError
				if !errors.Is(err, fs.ErrInvalid) || !errors.As(err, &pathErr) || pathErr.Path != name {
					t.Errorf("%s(%q) = %v, want PathError wrapping ErrInvalid", operation.name, name, err)
				}
			}
			return
		}
		if name == "." {
			if err := fsys.MkdirAll(name, 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := fsys.Sub(name); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := fsys.MkdirAll(path.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := fsys.WriteFile(name, content, 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := fs.ReadFile(fsys, name)
		if err != nil || !bytes.Equal(got, content) {
			t.Fatalf("round trip %q: got %q, want %q, err=%v", name, got, content, err)
		}
	})
}
