# pigo 二进制体积分析报告

**Project:** pigo (github.com/smallnest/pigo)
**构建目标:** `pigo.exe`（`go build -o pigo.exe ./cmd/pigo`）
**环境:** Windows / amd64，Go 1.27rc1，CGO_ENABLED=0
**日期:** 2026-08-07

---

## Executive Summary

默认 `go build` 产出的 `pigo.exe` 为 **42.5 MB**（42,457,600 字节 / 40.5 MiB）。体积不是单一原因，而是"调试信息 + 重型依赖"叠加的结果：

- **约 28%（~11.9 MB）是 DWARF 调试信息与符号表**——用 `-ldflags="-s -w"` 剥离后二进制立即降到 **30.6 MB**（29.2 MiB）。这是最大的单一可消除项。
- **约 70% 是依赖栈**——纯 Go SQLite（`modernc.org/sqlite`）、charmbracelet TUI 全家桶（bubbletea v2 / lipgloss v2 / glamour + chroma）、openai-go SDK 等本来就大。
- **嵌入资产仅 ~0.66 MB**（skills 621K + remotecontrol web 16K + pihost.mjs 24K），可忽略。

结论：开发构建体积正常，无需过度担心；发布构建用 `-s -w`（goreleaser release 构建即如此）可立省约 12 MB。

---

## 测量方法

- 直接用 `go build` 与 `go build -ldflags="-s -w"` 对比整体体积。
- 对主要依赖在仓库内建临时探针包（复用仓库 go.mod 的真实版本解析），用 `-ldflags="-s -w"` 单独构建测得净贡献，避免默认构建的调试信息干扰对比。
- 注意：各探针之间有共享的传递依赖，单个探针体积为**净贡献的近似**，相加会重复计入重叠部分，故下表是量级分解而非精确到字节的加法。

## 体积分解

| 贡献者 | 大小（剥离后探针/实测） | 说明 |
|---|---|---|
| **DWARF 调试信息 + 符号表** | ~11.9 MB | 默认 `go build` 不带 `-ldflags`；`-s -w` 剥离后 42.5 MB → 30.6 MB |
| **TUI / 渲染栈**（bubbletea v2 + lipgloss v2 + glamour + chroma + x/ansi） | ~7.9 MB | TUI/REPL 前端；chroma 语法高亮依赖源码即 ~9 MB |
| **modernc.org/sqlite** | ~6.0 MB | 纯 Go 移植版 SQLite（C 源码逐行转译，模块源码 149 MB），供 `internal/memory/store.go`（持久记忆）使用 |
| **openai-go SDK** | ~4.3 MB | 官方 OpenAI 客户端 |
| **其余**（html-to-markdown、jsonschema、yaml/toml、websocket、qrcode、goldmark、x/net、x/text + pigo 自身 ~20 个包） | ~10 MB（量级） | agentcore、双协议 provider、agenttool、runtime、dream、remote control 等 |
| **嵌入资产**（skills + remotecontrol/web + pihost.mjs） | ~0.66 MB | 可忽略 |

## 关键依赖溯源

| 依赖 | 源码体积 | 用途 | 二进制净贡献 |
|---|---|---|---|
| `modernc.org/sqlite` v1.55.0 | 149 MB | `internal/memory/store.go` 持久记忆 | ~6.0 MB |
| `modernc.org/libc` v1.74.1 | 91 MB | sqlite 的传递依赖 | 计入 sqlite |
| `golang.org/x/text` v0.40.0 | 30 MB | 国际化/文本处理 | 计入"其余" |
| `github.com/alecthomas/chroma/v2` | 9.0 MB | 代码高亮（glamour 栈） | 计入 UI 栈 |
| `golang.org/x/net` v0.57.0 | 9.0 MB | 网络 | 计入"其余" |

`modernc.org/sqlite` + `modernc.org/libc` 合计 240 MB 源码，是仓库里最大的第三方依赖，也是二进制中最大的单一代码来源（~6 MB 净贡献）。

## 缩小构建体积

```powershell
# 立省约 12 MB（发布构建标准做法，goreleaser 的 release 构建即如此）
go build -ldflags="-s -w" -o pigo.exe ./cmd/pigo

# 可再叠加 -trimpath 去掉构建路径信息
go build -ldflags="-s -w" -trimpath -o pigo.exe ./cmd/pigo
```

版本号 / commit / 构建时间由 goreleaser 通过 `-ldflags -X main.version=...` 注入（见 `cmd/pigo/main.go:43`、`.goreleaser.yaml`），本地开发构建显示 `pigo dev (commit none, built unknown)` 属正常。

## 备注

- 各依赖探针贡献存在重叠（共享传递依赖被重复计入），因此本报告为**量级分解**，精确到字节需要更细粒度的 linkmap 分析。
- 42.5 MB 对 Windows 桌面 CLI 属常见量级；体积优化的收益点集中在发布构建的 `-s -w`，而非替换依赖。
