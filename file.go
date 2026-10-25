// Copyright (c) 2021 The memfs Authors. All rights reserved.
// Copyright 2026 soulteary and the memfs-kit contributors.
// SPDX-License-Identifier: BSD-3-Clause AND Apache-2.0
// Derived from psanford/memfs and soulteary/memfs; modified for memfs-kit.
// Original portions retain the BSD license in LICENSES/memfs-BSD-3-Clause.txt.
// Modifications are licensed under Apache-2.0; see LICENSE.

package memfs

import (
	"bytes"
	"io"
	"io/fs"
	"sync"
	"time"
)

// File is a read-only snapshot returned by Open for a regular file.
// Each handle has its own position; methods are safe for concurrent use.
// Obtain File values through Open rather than constructing them directly.
type File struct {
	mu     sync.Mutex
	path   string
	info   fileInfo
	reader *bytes.Reader
	closed bool
}

var (
	_ fs.File        = (*File)(nil)
	_ io.ReadSeeker  = (*File)(nil)
	_ io.ReaderAt    = (*File)(nil)
	_ fs.ReadDirFile = (*dirFile)(nil)
)

// Stat returns the metadata captured when the file was opened.
func (f *File) Stat() (fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, pathError("stat", f.path, fs.ErrClosed)
	}
	return f.info, nil
}

// Read reads from the handle's current position.
func (f *File) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, pathError("read", f.path, fs.ErrClosed)
	}
	return f.reader.Read(p)
}

// ReadAt reads at offset without changing the handle's current position.
func (f *File) ReadAt(p []byte, offset int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, pathError("readat", f.path, fs.ErrClosed)
	}
	return f.reader.ReadAt(p, offset)
}

// Seek changes the handle's current position using the io.Seek* constants.
// Seeking beyond the end is permitted; seeking before the beginning is not.
func (f *File) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, pathError("seek", f.path, fs.ErrClosed)
	}
	return f.reader.Seek(offset, whence)
}

// Close closes the handle. Further operations return fs.ErrClosed.
func (f *File) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return pathError("close", f.path, fs.ErrClosed)
	}
	f.closed = true
	return nil
}

type dirFile struct {
	mu      sync.Mutex
	path    string
	info    fileInfo
	entries []fs.DirEntry
	index   int
	closed  bool
}

func (d *dirFile) Stat() (fs.FileInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, pathError("stat", d.path, fs.ErrClosed)
	}
	return d.info, nil
}

func (d *dirFile) Read([]byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, pathError("read", d.path, fs.ErrClosed)
	}
	return 0, pathError("read", d.path, fs.ErrInvalid)
}

func (d *dirFile) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return pathError("close", d.path, fs.ErrClosed)
	}
	d.closed = true
	return nil
}

func (d *dirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, pathError("readdir", d.path, fs.ErrClosed)
	}
	remaining := len(d.entries) - d.index
	if n > 0 && remaining == 0 {
		return []fs.DirEntry{}, io.EOF
	}
	count := remaining
	if n > 0 && n < count {
		count = n
	}
	// Copy the slice so callers cannot alter entries on a later page. Even an
	// exhausted ReadDir(n <= 0) returns a non-nil, empty slice and no error.
	entries := make([]fs.DirEntry, count)
	copy(entries, d.entries[d.index:d.index+count])
	d.index += count
	return entries, nil
}

type fileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
}

func (i fileInfo) Name() string       { return i.name }
func (i fileInfo) Size() int64        { return i.size }
func (i fileInfo) Mode() fs.FileMode  { return i.mode }
func (i fileInfo) ModTime() time.Time { return i.modTime }
func (i fileInfo) IsDir() bool        { return i.mode.IsDir() }
func (i fileInfo) Sys() any           { return nil }

type dirEntry struct {
	info fileInfo
}

func (d dirEntry) Name() string               { return d.info.Name() }
func (d dirEntry) IsDir() bool                { return d.info.IsDir() }
func (d dirEntry) Type() fs.FileMode          { return d.info.Mode().Type() }
func (d dirEntry) Info() (fs.FileInfo, error) { return d.info, nil }
