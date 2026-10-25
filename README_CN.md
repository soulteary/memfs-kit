# memfs-kit

[![CI](https://github.com/soulteary/memfs-kit/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/soulteary/memfs-kit/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/soulteary/memfs-kit.svg)](https://pkg.go.dev/github.com/soulteary/memfs-kit)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go Test Coverage](./.github/coverage.svg)](./.github/go-test-report.md)
[![Go Report Card](./.github/goreportcard.svg)](./.github/goreportcard-report.md)

[English](README.md)

围绕 Go 标准库 `io/fs` 构建的轻量内存文件系统。

在运行时创建文件树，再交给接受 `fs.FS` 的代码使用，例如模板引擎或
`http.FileServer(http.FS(root))`。文件系统拥有独立的数据副本，支持并发操作，
每次打开文件都会获得独立、支持定位读取的快照。运行和测试均不依赖第三方 Go 库。

## 环境与安装

需要 **Go 1.27.3 或更新版本**。

```sh
go get github.com/soulteary/memfs-kit
```

模块路径是 `github.com/soulteary/memfs-kit`，Go 包名保留为 `memfs`。

## 快速开始

```go
package main

import (
    "fmt"
    "io/fs"
    "log"

    memfs "github.com/soulteary/memfs-kit"
)

func main() {
    root := memfs.New()
    if err := root.MkdirAll("assets", 0755); err != nil {
        log.Fatal(err)
    }
    if err := root.WriteFile("assets/hello.txt", []byte("hello"), 0644); err != nil {
        log.Fatal(err)
    }

    assets, err := fs.Sub(root, "assets")
    if err != nil {
        log.Fatal(err)
    }
    data, err := fs.ReadFile(assets, "hello.txt")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(string(data))
}
```

## API

| API | 用途 |
| --- | --- |
| `New(opts ...Option) *FS` | 创建空文件系统；应使用构造函数，不支持直接使用 `FS` 的零值 |
| `(*FS).MkdirAll(path, perm)` | 创建目录及缺失的父目录 |
| `(*FS).WriteFile(path, data, perm)` | 新建或替换文件，父目录必须已经存在 |
| `(*FS).Open(path)` | 打开只读文件或目录快照 |
| `(*FS).Sub(dir)` | 返回现有子目录的 `fs.FS` 视图 |
| `WithOpenHook(fn)` | 为单次打开转换内容，或提供虚拟文件内容 |

`*FS` 实现 `fs.FS` 和 `fs.SubFS`，可以直接配合 `fs.ReadFile`、`fs.ReadDir`、
`fs.Stat`、`fs.WalkDir`、`fs.Glob` 等标准库函数使用。普通文件句柄实现
`fs.File`、`io.Seeker` 和 `io.ReaderAt`，目录句柄实现 `fs.ReadDirFile`。

## 行为与边界

- **路径**：使用 `/` 分隔的相对 `io/fs` 路径，`.` 表示根目录。空路径、绝对路径、
  `..` 路径段和重复的 `/` 会被拒绝；Windows、macOS 和 Linux 使用相同语法。
- **错误**：文件系统操作返回 `*fs.PathError`。使用 `errors.Is` 判断
  `fs.ErrNotExist`、`fs.ErrInvalid` 等错误。
- **数据所有权**：`WriteFile` 在发布文件前复制输入数据，调用返回后修改原始字节切片，
  不会改变已保存的文件。
- **文件快照**：替换文件不会改变已打开句柄中的内容和元数据。不同的打开操作拥有独立
  读指针；允许 `Seek` 到文件末尾之外，在那里读取返回 EOF。
- **关闭句柄**：`Close` 释放该句柄对文件内容或目录条目快照的引用；文件系统和其他
  已打开句柄仍保留各自的引用。关闭后的句柄操作返回 `fs.ErrClosed`。
- **目录快照**：打开目录时固定其条目并按名称排序；连续调用 `ReadDir(n)` 可以正确分页，
  正数 `n` 的 EOF 语义遵循标准库。目录打开后发生的变化需要重新打开才能看到。
- **子目录**：`Sub` 共享原文件树，父文件系统的后续写入会被子目录的后续打开操作看到。
  子目录视图的根名为 `.`，并继承原来的打开 hook。
- **并发**：文件系统方法和单个句柄的方法均有同步保护。共享句柄也会共享读指针，
  需要独立顺序读取的 goroutine 应分别打开文件。hook 自己使用的共享可变状态由调用方同步。
- **权限**：mode 仅作为元数据保存，不执行访问控制。覆盖已有文件保留原权限，新建文件才使用
  传入的权限；不应用操作系统的 umask。
- **范围**：通过 `WriteFile` 和 `MkdirAll` 写入，不提供 `OpenFile`、删除、重命名、符号链接、
  磁盘持久化或操作系统挂载 API。

## 打开 hook

`WithOpenHook` 接受以下回调：

```go
func(path string, existingContent []byte, origErr error) ([]byte, error)
```

打开普通文件或不存在的路径时会执行回调。路径不存在时，`existingContent` 为 `nil`，
`origErr` 可以通过 `errors.Is(origErr, fs.ErrNotExist)` 判断。返回字节内容和 `nil`
错误，即为本次打开提供内容；返回 `existingContent, origErr` 则保留默认行为。

目录列表中普通文件的 `DirEntry.Info()` 也会在首次调用时执行 hook。它以打开目录时
捕获的文件快照为输入，并缓存该条目的元数据结果或错误，使大小和修改时间反映转换后的
内容，支持归档写入等需要先取元数据再读文件的场景。打开目录、读取目录条目，以及调用
`Name`、`Type`、`IsDir` 都不会执行 hook。

目录、非法路径，以及穿过普通文件继续向下访问的路径不会触发 hook。回调在文件树锁和句柄锁
之外执行，可以调用文件系统方法。输入数据会复制，作为文件内容使用的输出数据也会复制，
修改输入切片不会改变底层文件内容。不同条目和打开操作可能并发调用 hook；不要在某个
条目的 hook 中递归调用该条目自己的 `Info()`，否则会等待自身尚未完成的首次计算。
递归打开同一路径也需要明确的终止条件。

hook 返回的内容不会写回文件树。临时合成的文件使用 `0444` 权限元数据，不出现在目录列表里，
也不能为 `Sub` 创建虚拟目录。

如果 hook 改变了已有文件的内容，返回句柄的 `ModTime` 会清零，表示修改时间未知，
避免 `http.FileServer` 依据底层文件已经失效的修改时间，错误返回 `304 Not Modified`。
原样返回内容时保留原来的元数据；临时合成文件的修改时间也为零。

条目首次调用 `Info()` 时读取当时的 hook 状态，后续调用复用缓存结果；之后的 `Open`
会再次执行 hook。归档写入等需要比较元数据与另一次打开结果的消费者，要求 hook 在这些
操作之间提供稳定的输出。

带有 hook 且尚未计算 `Info()` 的普通文件条目，会保留捕获的内容，直到首次计算完成或
该条目被丢弃。关闭目录会释放句柄自身的引用，但已经交给调用方的条目仍然有效，包括其
尚未执行的 `Info()`。目录打开时固定的是存储中的数据和元数据，不会冻结 hook 的外部状态。

回调的路径始终相对于最初 `New` 创建的根目录。例如对 `fs.Sub(root, "assets")` 调用
`Open("hello.txt")`，hook 收到的是 `assets/hello.txt`。嵌套子目录继续累计前缀，
因此 hook 应使用完整的原根相对路径匹配规则。

## 迁移与上游

将 import 路径从 `github.com/soulteary/memfs` 改为
`github.com/soulteary/memfs-kit` 即可使用新模块。包名仍为 `memfs`，原有
`memfs.New()` 调用继续有效。

`New` 现在接受可变数量的选项；仅当代码将它赋给精确的 `func() *memfs.FS` 类型变量时，
需要改用 `func() *memfs.FS { return memfs.New() }` 这样的包装函数。

本项目从 [soulteary/memfs](https://github.com/soulteary/memfs) 迁移，并参考了
[psanford/memfs](https://github.com/psanford/memfs) 的后续更新。迁移同时修复锁处理、
目录遍历、文件数据所有权与标准库兼容性问题，没有直接照搬上游实现。具体版本与逐提交取舍
见[上游审查记录](docs/UPSTREAM_REVIEW.md)。

[vfs-kit](https://github.com/soulteary/vfs-kit) 已经提供内存后端，以及磁盘、归档、挂载等
统一抽象。`memfs-kit` 专注标准 `io/fs` 接口下的内存文件系统；需要跨多种后端的统一读写
VFS 接口时，使用 `vfs-kit` 更合适。

## 开发验证

```sh
gofmt -w .
go vet ./...
go test ./...
go test -race -coverprofile=coverage.out ./...
go test -run='^$' -fuzz='^FuzzPathValidation$' -fuzztime=15s .
```

CI 检查格式、模块一致性和 `go vet`，在 Linux、macOS、Windows 上执行测试，并在 Linux
上运行竞态检测、覆盖率报告与短时 fuzz。
[Go Test Report](https://github.com/soulteary/go-test-report-action) 生成测试与覆盖率报告，
[Go Report Card](https://github.com/soulteary/goreportcard-action) 生成代码质量报告。
GitHub Actions 固定到经过核实的提交 SHA，由 Dependabot 跟踪更新。

推送到 `main`，或选择 `main` 手动运行 CI，全部必要检查通过后会刷新报告和徽章。
独立的发布 job 只回写五个报告文件，而且被测源码必须仍然对应默认分支 HEAD；
PR 只生成报告 artifact，不向仓库写入文件。触发条件、artifact 和发布规则见
[Go 报告策略](.github/GO-REPORTS.md)。

## 许可证

项目采用 [Apache License 2.0](LICENSE)。继承自 `memfs` 的代码保留原
[BSD-3-Clause 许可证](LICENSES/memfs-BSD-3-Clause.txt)、版权声明、条件和免责声明。
具体来源版本及署名见[第三方来源说明](THIRD_PARTY_NOTICES.md)，再分发时应一并保留。
