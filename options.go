// Copyright (c) 2021 The memfs Authors. All rights reserved.
// Copyright 2026 soulteary and the memfs-kit contributors.
// SPDX-License-Identifier: BSD-3-Clause AND Apache-2.0
// Derived from psanford/memfs; modified for memfs-kit.
// Original portions retain the BSD license in LICENSES/memfs-BSD-3-Clause.txt.
// Modifications are licensed under Apache-2.0; see LICENSE.

package memfs

// Option configures a filesystem created with New.
type Option interface {
	setOption(*fsOption)
}

type openHook func(string, []byte, error) ([]byte, error)

type fsOption struct {
	openHook openHook
}

type openHookOption struct {
	hook openHook
}

func (o openHookOption) setOption(options *fsOption) {
	options.openHook = o.hook
}

// WithOpenHook sets a callback for Open and Stat on existing regular files or
// paths that return fs.ErrNotExist, and for resolving regular-file DirEntry.Info.
// Directories, invalid paths and paths through a regular file bypass the hook.
// A nil callback disables the hook.
//
// The path is relative to the original New root, even when using a Sub view.
// For an existing file, content is a private copy and origErr is nil. For a
// missing path, content is nil and errors.Is(origErr, fs.ErrNotExist) is true.
// Returning content, origErr preserves the default behavior. Returning data
// and a nil error supplies content for that operation without storing it in
// the filesystem. Synthesized files have mode 0444, a zero modification
// time, and do not appear in directory listings.
// Transformed existing files also have a zero modification time when their
// contents differ from the stored bytes, preventing stale HTTP cache validation.
//
// Directory enumeration and DirEntry.Name/Type/IsDir do not invoke the hook.
// The first DirEntry.Info call applies it to the directory's captured file
// snapshot and caches the metadata or error, including across concurrent calls.
// If the callback panics, later Info calls re-panic with the same value without
// invoking it again. Directory entries without a regular-file hook return their
// already-captured immutable metadata without executing a callback.
// Consumers that compare metadata with separately opened content, such as tar
// writers, require a deterministic hook or stable application state.
//
// The callback runs without tree or handle locks and may write to the FS.
// Content snapshots remain isolated from input or output bytes retained by the
// callback. Unchanged content reuses the immutable stored snapshot; different
// content is copied when needed by an open handle. Concurrent opens, Stat calls,
// or distinct entries may invoke it simultaneously; callers must synchronize
// their own hook state.
// A callback must not recursively Open or Stat the same path without a stopping rule.
// It must not call Info recursively on the same DirEntry, whose result is still
// being initialized.
func WithOpenHook(f func(string, []byte, error) ([]byte, error)) Option {
	return openHookOption{hook: f}
}
