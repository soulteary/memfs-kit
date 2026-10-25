// Copyright 2026 soulteary.
// SPDX-License-Identifier: Apache-2.0

package memfs_test

import (
	"fmt"
	"io"
	"io/fs"
	"strings"
	"testing"

	memfs "github.com/soulteary/memfs-kit"
)

// Keep only metadata, counts, and already-closed handles after a benchmark.
var (
	benchmarkFile       fs.File
	benchmarkInfo       fs.FileInfo
	benchmarkEntryCount int
)

func benchmarkFileFS(b *testing.B, depth, size int, opts ...memfs.Option) (*memfs.FS, string) {
	b.Helper()
	fsys := memfs.New(opts...)
	dir := strings.Repeat("dir/", depth-1)
	if dir != "" {
		if err := fsys.MkdirAll(strings.TrimSuffix(dir, "/"), 0o755); err != nil {
			b.Fatal(err)
		}
	}
	name := dir + "file"
	if err := fsys.WriteFile(name, make([]byte, size), 0o644); err != nil {
		b.Fatal(err)
	}
	return fsys, name
}

func benchmarkDirectoryFS(b *testing.B, count int, opts ...memfs.Option) *memfs.FS {
	b.Helper()
	fsys := memfs.New(opts...)
	if err := fsys.MkdirAll("dir", 0o755); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < count; i++ {
		if err := fsys.WriteFile(fmt.Sprintf("dir/f%05d", i), []byte("value"), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	return fsys
}

func benchmarkOpenClose(b *testing.B, fsys fs.FS, name string) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	var file fs.File
	var err error
	for i := 0; i < b.N; i++ {
		file, err = fsys.Open(name)
		if err != nil {
			b.Fatal(err)
		}
		if err := file.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	benchmarkFile = file
}

func benchmarkFSStat(b *testing.B, fsys fs.FS, name string) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	var info fs.FileInfo
	var err error
	for i := 0; i < b.N; i++ {
		info, err = fs.Stat(fsys, name)
		if err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	benchmarkInfo = info
}

func BenchmarkOpenClose(b *testing.B) {
	for _, depth := range []int{1, 12} {
		b.Run(fmt.Sprintf("Depth%d", depth), func(b *testing.B) {
			fsys, name := benchmarkFileFS(b, depth, 4096)
			benchmarkOpenClose(b, fsys, name)
		})
	}
}

func BenchmarkFileStat(b *testing.B) {
	for _, depth := range []int{1, 12} {
		b.Run(fmt.Sprintf("Depth%d", depth), func(b *testing.B) {
			fsys, name := benchmarkFileFS(b, depth, 4096)
			file, err := fsys.Open(name)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() {
				if err := file.Close(); err != nil {
					b.Error(err)
				}
			})
			b.ReportAllocs()
			b.ResetTimer()
			var info fs.FileInfo
			for i := 0; i < b.N; i++ {
				info, err = file.Stat()
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			benchmarkInfo = info
		})
	}
}

func BenchmarkFSStat(b *testing.B) {
	for _, depth := range []int{1, 12} {
		b.Run(fmt.Sprintf("Depth%d", depth), func(b *testing.B) {
			fsys, name := benchmarkFileFS(b, depth, 4096)
			benchmarkFSStat(b, fsys, name)
		})
	}
}

func BenchmarkDirectory(b *testing.B) {
	for _, count := range []int{0, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("Entries%d", count), func(b *testing.B) {
			b.Run("Stat", func(b *testing.B) {
				fsys := benchmarkDirectoryFS(b, count)
				benchmarkFSStat(b, fsys, "dir")
			})
			b.Run("OpenClose", func(b *testing.B) {
				fsys := benchmarkDirectoryFS(b, count)
				benchmarkOpenClose(b, fsys, "dir")
			})
			b.Run("ReadDir", func(b *testing.B) {
				fsys := benchmarkDirectoryFS(b, count)
				b.ReportAllocs()
				b.ResetTimer()
				var entries []fs.DirEntry
				var err error
				for i := 0; i < b.N; i++ {
					entries, err = fs.ReadDir(fsys, "dir")
					if err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				if len(entries) != count {
					b.Fatalf("ReadDir returned %d entries, want %d", len(entries), count)
				}
				benchmarkEntryCount = len(entries)
			})
		})
	}
}

var benchmarkHookCases = []struct {
	name      string
	transform bool
}{
	{name: "Passthrough"},
	{name: "Transform", transform: true},
}

var benchmarkContentSizes = []struct {
	name string
	size int
}{
	{name: "4KiB", size: 4 << 10},
	{name: "1MiB", size: 1 << 20},
}

func benchmarkHookOption(transform bool) memfs.Option {
	return memfs.WithOpenHook(func(_ string, content []byte, err error) ([]byte, error) {
		if err != nil {
			return content, err
		}
		// The hook receives a private copy. Transform it without adding an
		// allocation of our own to the filesystem's copy costs.
		if transform && len(content) != 0 {
			content[0] ^= 1
		}
		return content, nil
	})
}

func BenchmarkHookOpenClose(b *testing.B) {
	for _, hook := range benchmarkHookCases {
		for _, content := range benchmarkContentSizes {
			b.Run(hook.name+"/"+content.name, func(b *testing.B) {
				fsys, name := benchmarkFileFS(b, 2, content.size, benchmarkHookOption(hook.transform))
				b.SetBytes(int64(content.size))
				benchmarkOpenClose(b, fsys, name)
			})
		}
	}
}

func BenchmarkHookReadDirFirstInfo(b *testing.B) {
	for _, hook := range benchmarkHookCases {
		for _, content := range benchmarkContentSizes {
			b.Run(hook.name+"/"+content.name, func(b *testing.B) {
				fsys, _ := benchmarkFileFS(b, 2, content.size, benchmarkHookOption(hook.transform))
				b.SetBytes(int64(content.size))
				b.ReportAllocs()
				b.ResetTimer()
				var info fs.FileInfo
				for i := 0; i < b.N; i++ {
					// A fresh listing gives each iteration an unresolved Info
					// callback rather than timing its cached result.
					entries, err := fs.ReadDir(fsys, "dir")
					if err != nil {
						b.Fatal(err)
					}
					if len(entries) != 1 {
						b.Fatalf("ReadDir returned %d entries, want 1", len(entries))
					}
					info, err = entries[0].Info()
					if err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				benchmarkInfo = info
			})
		}
	}
}

func BenchmarkWriteFileExistingDepth12(b *testing.B) {
	fsys, name := benchmarkFileFS(b, 12, 4096)
	data := make([]byte, 4096)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := fsys.WriteFile(name, data, 0o644); err != nil {
			b.Fatal(err)
		}
	}
}

var benchmarkResult int64

func BenchmarkSmallChunkRead(b *testing.B) {
	const size = 4 << 10
	for _, chunk := range []int{16, 64, 256, 4096} {
		b.Run(fmt.Sprintf("4KiB/Chunk%dB", chunk), func(b *testing.B) {
			fsys := memfs.New()
			data := make([]byte, size)
			for i := range data {
				data[i] = byte(i*31 + 7)
			}
			if err := fsys.WriteFile("file", data, 0o644); err != nil {
				b.Fatal(err)
			}
			buffer := make([]byte, chunk)
			var wantChecksum int64
			for offset := 0; offset < len(data); offset += chunk {
				end := min(offset+chunk, len(data))
				wantChecksum += int64(data[offset]) + int64(data[end-1])
			}
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			var total int
			var checksum int64
			for i := 0; i < b.N; i++ {
				file, err := fsys.Open("file")
				if err != nil {
					b.Fatal(err)
				}
				total, checksum = 0, 0
				for {
					n, err := file.Read(buffer)
					if n > 0 {
						total += n
						checksum += int64(buffer[0]) + int64(buffer[n-1])
					}
					if err != nil {
						if err != io.EOF {
							b.Fatal(err)
						}
						break
					}
				}
				if err := file.Close(); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if total != size || checksum != wantChecksum {
				b.Fatalf("read = %d bytes, checksum %d; want %d bytes, checksum %d", total, checksum, size, wantChecksum)
			}
			benchmarkResult = int64(total) + checksum
		})
	}
}

func benchmarkReadDirAllInfo(b *testing.B, fsys fs.FS, count int) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	var entries []fs.DirEntry
	var totalSize int64
	var err error
	for i := 0; i < b.N; i++ {
		entries, err = fs.ReadDir(fsys, "dir")
		if err != nil {
			b.Fatal(err)
		}
		totalSize = 0
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				b.Fatal(err)
			}
			totalSize += info.Size()
		}
	}
	b.StopTimer()
	if len(entries) != count || totalSize != int64(5*count) {
		b.Fatalf("listing metadata = %d entries, %d bytes; want %d entries, %d bytes", len(entries), totalSize, count, 5*count)
	}
	for i, entry := range entries {
		if want := fmt.Sprintf("f%05d", i); entry.Name() != want {
			b.Fatalf("entry %d = %q, want %q", i, entry.Name(), want)
		}
	}
	benchmarkResult = int64(len(entries)) + totalSize
}

func BenchmarkReadDirInfo(b *testing.B) {
	for _, count := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("Entries%d", count), func(b *testing.B) {
			benchmarkReadDirAllInfo(b, benchmarkDirectoryFS(b, count), count)
		})
	}
}

func BenchmarkHookReadDirAllInfo(b *testing.B) {
	for _, hook := range benchmarkHookCases {
		for _, count := range []int{100, 1000} {
			b.Run(fmt.Sprintf("%s/Entries%d", hook.name, count), func(b *testing.B) {
				fsys := benchmarkDirectoryFS(b, count, benchmarkHookOption(hook.transform))
				// Each iteration obtains a fresh listing and resolves every entry.
				benchmarkReadDirAllInfo(b, fsys, count)
			})
		}
	}
}

func BenchmarkDirEntryInfoCached(b *testing.B) {
	cases := []struct {
		name string
		opts []memfs.Option
	}{
		{name: "NoHook"},
		{name: "Passthrough", opts: []memfs.Option{benchmarkHookOption(false)}},
		{name: "Transform", opts: []memfs.Option{benchmarkHookOption(true)}},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			fsys := benchmarkDirectoryFS(b, 1, c.opts...)
			entries, err := fs.ReadDir(fsys, "dir")
			if err != nil {
				b.Fatal(err)
			}
			if len(entries) != 1 {
				b.Fatalf("entry count = %d, want 1", len(entries))
			}
			entry := entries[0]
			info, err := entry.Info() // Resolve lazy hook metadata before timing.
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				info, err = entry.Info()
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if info.Size() != 5 {
				b.Fatalf("entry size = %d, want 5", info.Size())
			}
			benchmarkResult = info.Size()
		})
	}
}
