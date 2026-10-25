// Copyright 2026 soulteary and the memfs-kit contributors.
// SPDX-License-Identifier: Apache-2.0

// Package memfs provides an in-memory filesystem built around io/fs.
//
// Create a filesystem with New, populate it with FS.MkdirAll and FS.WriteFile,
// and consume it with standard helpers such as fs.ReadFile, fs.ReadDir,
// fs.WalkDir, fs.Sub, template.ParseFS and http.FS. Open returns read-only
// snapshots; regular files support seeking and positional reads.
//
// All names must satisfy fs.ValidPath. The root is ".", paths use forward
// slashes, and absolute paths, empty names and dot/dot-dot components are
// rejected. Backslashes are literal filename characters on every platform.
// There are no symlinks, OS mounts, persistence or permission enforcement.
//
// Filesystem operations and individual handle methods are safe for concurrent
// use. Hooks execute outside tree and handle locks and are responsible for
// the safety of their own shared state. Permission bits are metadata only.
package memfs
