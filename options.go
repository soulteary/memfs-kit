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

type fsOption struct {
	openHook func(string, []byte, error) ([]byte, error)
}

type openHookOption struct {
	hook func(string, []byte, error) ([]byte, error)
}

func (o openHookOption) setOption(options *fsOption) {
	options.openHook = o.hook
}

// WithOpenHook sets a callback for opening existing regular files or paths
// that return fs.ErrNotExist. Directories, invalid paths and paths through a
// regular file bypass the hook. A nil callback disables the hook.
//
// The path is relative to the original New root, even when using a Sub view.
// For an existing file, content is a private copy and origErr is nil. For a
// missing path, content is nil and errors.Is(origErr, fs.ErrNotExist) is true.
// Returning content, origErr preserves the default behavior. Returning data
// and a nil error supplies a read-only snapshot for this Open without storing
// it in the filesystem. Synthesized files have mode 0444, a zero modification
// time, and do not appear in directory listings.
// Transformed existing files also have a zero modification time when their
// contents differ from the stored bytes, preventing stale HTTP cache validation.
//
// The callback runs without filesystem locks and may write to the FS. Its
// returned bytes are copied before Open returns. Concurrent Open calls may
// invoke it simultaneously; callers must synchronize their own callback state.
// A callback must not recursively Open the same path without a stopping rule.
func WithOpenHook(f func(string, []byte, error) ([]byte, error)) Option {
	return openHookOption{hook: f}
}
