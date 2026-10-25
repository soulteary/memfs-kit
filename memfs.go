// Copyright (c) 2021 The memfs Authors. All rights reserved.
// Copyright 2026 soulteary and the memfs-kit contributors.
// SPDX-License-Identifier: BSD-3-Clause AND Apache-2.0
// Derived from psanford/memfs and soulteary/memfs; modified for memfs-kit.
// Original portions retain the BSD license in LICENSES/memfs-BSD-3-Clause.txt.
// Modifications are licensed under Apache-2.0; see LICENSE.

package memfs

import (
	"bytes"
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
)

// FS is a writable in-memory filesystem that implements fs.FS, fs.StatFS,
// fs.ReadDirFS, and fs.SubFS.
// Use New to construct an FS; its zero value is not ready for use.
//
// Filesystem operations are safe for concurrent use, including operations on
// views returned by Sub. Open returns a snapshot of a file's contents and
// metadata, or a directory's sorted entries. Later writes do not change an
// already-open handle. Permission bits are metadata, not access controls.
type FS struct {
	tree     *tree
	root     *node
	openHook openHook
	prefix   string
}

type tree struct {
	mu sync.RWMutex
}

// All access to nodes is protected by the shared tree lock. Published file
// nodes are immutable: WriteFile replaces them rather than editing their data.
type node struct {
	info     fileInfo
	data     []byte
	children map[string]*node
}

var (
	_ fs.FS        = (*FS)(nil)
	_ fs.StatFS    = (*FS)(nil)
	_ fs.ReadDirFS = (*FS)(nil)
	_ fs.SubFS     = (*FS)(nil)
)

// New creates an empty filesystem. Its root directory has mode 0755.
// Options are applied in order; nil options are ignored.
func New(opts ...Option) *FS {
	var options fsOption
	for _, opt := range opts {
		if opt != nil {
			opt.setOption(&options)
		}
	}
	return &FS{
		tree: &tree{},
		root: &node{
			info:     fileInfo{name: ".", mode: fs.ModeDir | 0o755, modTime: time.Now()},
			children: make(map[string]*node),
		},
		openHook: options.openHook,
	}
}

// MkdirAll creates a directory and any missing parents with the given
// permission bits. Existing directory permissions are unchanged. No umask or
// permission checks are applied. Names must satisfy fs.ValidPath.
func (rootFS *FS) MkdirAll(name string, perm fs.FileMode) error {
	if !fs.ValidPath(name) {
		return pathError("mkdir", name, fs.ErrInvalid)
	}
	if name == "." {
		return nil
	}
	rootFS.tree.mu.Lock()
	defer rootFS.tree.mu.Unlock()

	current := rootFS.root
	for part := range strings.SplitSeq(name, "/") {
		child, exists := current.children[part]
		if exists {
			if child.children == nil {
				return pathError("mkdir", name, fs.ErrInvalid)
			}
		} else {
			now := time.Now()
			child = &node{
				info: fileInfo{
					name: part, mode: fs.ModeDir | permissionBits(perm), modTime: now,
				},
				children: make(map[string]*node),
			}
			current.children[part] = child
			current.info.modTime = now
		}
		current = child
	}
	return nil
}

// WriteFile creates or replaces a file, copying data before returning. Its
// parent directory must already exist. Replacing a file preserves its mode
// and updates its modification time. Open handles retain their old snapshot.
// Permission bits are recorded without applying a umask or checking access.
func (rootFS *FS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	if !fs.ValidPath(name) {
		return pathError("write", name, fs.ErrInvalid)
	}
	if name == "." {
		return pathError("write", name, fs.ErrExist)
	}
	rootFS.tree.mu.Lock()
	defer rootFS.tree.mu.Unlock()

	parent, err := rootFS.lookup(path.Dir(name))
	if err != nil {
		return pathError("write", name, err)
	}
	if parent.children == nil {
		return pathError("write", name, fs.ErrInvalid)
	}
	base := path.Base(name)
	existing, exists := parent.children[base]
	mode := permissionBits(perm)
	if exists {
		if existing.children != nil {
			return pathError("write", name, fs.ErrExist)
		}
		mode = existing.info.mode
	}
	now := time.Now()
	parent.children[base] = &node{
		info: fileInfo{name: base, size: int64(len(data)), mode: mode, modTime: now},
		data: bytes.Clone(data),
	}
	if !exists {
		parent.info.modTime = now
	}
	return nil
}

// Open opens a read-only snapshot of the named file or directory. Names are
// relative, slash-separated fs.ValidPath names; "." opens this view's root.
// Regular file handles also implement io.Seeker and io.ReaderAt.
func (rootFS *FS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, pathError("open", name, fs.ErrInvalid)
	}
	rootFS.tree.mu.RLock()
	entry, err := rootFS.lookup(name)
	if err == nil && entry.children != nil {
		info := entry.info
		if name == "." {
			info.name = "."
		}
		entries := make([]fs.DirEntry, 0, len(entry.children))
		for _, child := range entry.children {
			var childName, hookName string
			if rootFS.openHook != nil && !child.info.IsDir() {
				childName = path.Join(name, child.info.name)
				hookName = rootFS.hookPath(childName)
			}
			entries = append(entries, newDirEntry(
				childName, hookName, child.info, child.data, rootFS.openHook,
			))
		}
		rootFS.tree.mu.RUnlock()
		slices.SortFunc(entries, func(a, b fs.DirEntry) int {
			return strings.Compare(a.Name(), b.Name())
		})
		return &dirFile{path: name, info: info, entries: entries}, nil
	}
	var info fileInfo
	var data []byte
	if err == nil {
		info, data = entry.info, entry.data
	}
	rootFS.tree.mu.RUnlock()

	if err != nil {
		err = pathError("open", name, err)
		if rootFS.openHook == nil || !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	if rootFS.openHook != nil {
		info, data, err = applyOpenHook(rootFS.openHook, name, rootFS.hookPath(name), info, data, err, true)
		if err != nil {
			return nil, err
		}
	}
	f := &File{path: name, info: info}
	f.reader.Reset(data)
	return f, nil
}

// Stat returns the metadata of a file or directory without opening a handle.
// Regular and virtual files retain Open's hook and error behavior. Directory
// metadata does not require constructing a directory-entry snapshot.
func (rootFS *FS) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, pathError("open", name, fs.ErrInvalid)
	}
	rootFS.tree.mu.RLock()
	entry, err := rootFS.lookup(name)
	var info fileInfo
	var data []byte
	var directory bool
	if err == nil {
		info, data = entry.info, entry.data
		directory = entry.children != nil
		if directory && name == "." {
			info.name = "."
		}
	}
	rootFS.tree.mu.RUnlock()

	if directory {
		return info, nil
	}
	// Match Open's errors and hook ordering without allocating a file handle
	// or looking up the path a second time.
	if err != nil {
		err = pathError("open", name, err)
		if rootFS.openHook == nil || !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	if rootFS.openHook != nil {
		info, _, err = applyOpenHook(rootFS.openHook, name, rootFS.hookPath(name), info, data, err, false)
		if err != nil {
			return nil, err
		}
	}
	return info, nil
}

// ReadDir returns a new, sorted snapshot of the directory's entries. As with
// Open, regular-file metadata is resolved lazily by each entry's Info method.
func (rootFS *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	f, err := rootFS.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dir, ok := f.(*dirFile)
	if !ok {
		// Match fs.ReadDir's fallback after a successful regular-file Open,
		// including hooks that supply virtual files or reject the open.
		return nil, pathError("readdir", name, errors.New("not implemented"))
	}
	// This handle is private: its entries have never been exposed by a
	// paginated read. Close only drops the handle's reference, so the caller
	// owns the returned slice and its lazy metadata after the handle closes.
	return dir.entries, nil
}

// applyOpenHook is shared by Open, Stat and lazy directory-entry metadata so all
// describe the same transformed file. Callers must not hold tree/handle locks.
// origErr is nil or the *fs.PathError constructed by Open for a missing path.
// snapshotContent is false when only the resulting metadata will be used.
func applyOpenHook(hook openHook, name, hookName string, info fileInfo, data []byte, origErr error, snapshotContent bool) (fileInfo, []byte, error) {
	content, err := hook(hookName, bytes.Clone(data), origErr)
	if err != nil {
		if origErr != nil && err == origErr {
			// A pass-through hook must preserve the original error, including
			// compatibility with os.IsNotExist's single-level unwrapping.
			return fileInfo{}, nil, origErr
		}
		return fileInfo{}, nil, pathError("open", name, err)
	}
	unchanged := origErr == nil && bytes.Equal(content, data)
	if origErr != nil {
		// A virtual file exists only for this operation and has no stored timestamp.
		info = fileInfo{name: path.Base(name), mode: 0o444}
	} else if !unchanged {
		// Stored timestamps cannot validate transformed HTTP content.
		info.modTime = time.Time{}
	}
	info.size = int64(len(content))
	if !snapshotContent {
		return info, nil, nil
	}
	if unchanged {
		// Published content is immutable. Reusing it also isolates the open
		// handle from hook input/output slices retained by the callback.
		return info, data, nil
	}
	return info, bytes.Clone(content), nil
}

// Sub returns a view of an existing directory. The view shares subsequent
// writes and the open hook with its parent. Hook paths remain relative to the
// original filesystem root, including through nested Sub calls.
func (rootFS *FS) Sub(dir string) (fs.FS, error) {
	if !fs.ValidPath(dir) {
		return nil, pathError("sub", dir, fs.ErrInvalid)
	}
	if dir == "." {
		return rootFS, nil
	}
	rootFS.tree.mu.RLock()
	defer rootFS.tree.mu.RUnlock()
	entry, err := rootFS.lookup(dir)
	if err != nil {
		return nil, pathError("sub", dir, err)
	}
	if entry.children == nil {
		return nil, pathError("sub", dir, fs.ErrInvalid)
	}
	return &FS{
		tree: rootFS.tree, root: entry, openHook: rootFS.openHook,
		prefix: rootFS.hookPath(dir),
	}, nil
}

// lookup requires the shared tree lock and an already validated path.
func (rootFS *FS) lookup(name string) (*node, error) {
	current := rootFS.root
	if name == "." {
		return current, nil
	}
	for part := range strings.SplitSeq(name, "/") {
		if current.children == nil {
			return nil, fs.ErrInvalid
		}
		child, exists := current.children[part]
		if !exists {
			return nil, fs.ErrNotExist
		}
		current = child
	}
	return current, nil
}

func (rootFS *FS) hookPath(name string) string {
	if rootFS.prefix == "" {
		return name
	}
	return path.Join(rootFS.prefix, name)
}

func permissionBits(mode fs.FileMode) fs.FileMode {
	return mode & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
}

func pathError(op, name string, err error) error {
	return &fs.PathError{Op: op, Path: name, Err: err}
}
