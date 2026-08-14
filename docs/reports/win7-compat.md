# pigo 在 Windows 7 下的兼容性分析

> 日期：2026-07-26
> 结论先行：**pigo 无法在 Windows 7 上运行，且不是个别功能不可用，而是任何模式（`acp` / `serve` / TUI / REPL / headless）在进程启动阶段就会致命退出。** 根因在 Go 工具链与运行时层，与 pigo 自身代码无关；TUI 依赖栈（bubbletea v2 系列）还有第二层硬性不兼容。

---

## 1. 硬性阻断：Go 1.21+ 运行时在 Win7 上无法启动

### 1.1 官方支持政策

- `go.mod` 声明 `go 1.27rc1`（本机工具链 go1.27rc2）。
- Go 官方自 **Go 1.21 起最低要求 Windows 10**（及 Windows Server 2016+）；**Go 1.20 是最后一个支持 Windows 7 / 8 / Server 2008 / Server 2012 的版本**（Go 1.20 发布说明中已提前预告）。
- 因此 pigo 当前声明的工具链版本在政策上就不支持 Win7。

### 1.2 运行时硬性检查（源码证据，go1.27rc2）

Go 运行时在 `osinit()` 里**无条件**执行 `loadOptionalSyscalls()`（`GOROOT/src/runtime/os_windows.go`）：

```go
func loadOptionalSyscalls() {
	bcryptPrimitives := windowsLoadSystemLib(bcryptprimitivesdll[:])
	if bcryptPrimitives == 0 {
		throw("bcryptprimitives.dll not found")
	}
	_ProcessPrng = windowsFindfunc(bcryptPrimitives, []byte("ProcessPrng\000"))
	...
}
```

- `bcryptprimitives.dll` 是 **Windows 10 引入的 DLL**，Win7/8/8.1 的 System32 中不存在。
- DLL 加载失败 → `throw("bcryptprimitives.dll not found")` → 进程在进入 `main` 之前就以 fatal error 退出。
- 即便 DLL 存在，其导出 `ProcessPrng` 也是 Win10 专属 API（运行时随机数来源）。

**实际表现**：在 Win7 上双击/命令行运行任何 pigo.exe，立即在 stderr 输出 `runtime: bcryptprimitives.dll not found` 并退出，与模式无关（`pigo acp`、`pigo serve`、TUI、REPL 全部一样）。

**附带影响**：Go 1.21+ 的工具链本身也不能在 Win7 上运行，因此在 Win7 上无法编译 pigo。pigo 是纯 Go（`CGO_ENABLED=0`，见 `.goreleaser.yaml`），只能从 Win10+ / Linux / macOS 交叉编译出 Win7 可加载的 PE 文件——但运行时这一关无论如何过不去。

## 2. 第二层硬性阻断：TUI 依赖栈要求 Windows 10 的 VT 模式

即使把工具链降到 Go 1.20（Win7 最后可运行版本），TUI 也起不来，因为整个 charmbracelet v2 栈在 Windows 上硬性依赖 VT（Virtual Terminal）模式，而 **VT 模式是 Windows 10 1511（TH2）才引入的**：

- **bubbletea v2.0.8 `tty_windows.go` `initInput()`**：对 stdin 调 `SetConsoleMode(mode|ENABLE_VIRTUAL_TERMINAL_INPUT)`，对 stdout 调 `SetConsoleMode(mode|ENABLE_VIRTUAL_TERMINAL_PROCESSING|DISABLE_NEWLINE_AUTO_RETURN)`，**任一失败直接返回 error**，`p.Run()` 报错退出。Win7 的 conhost 不认识这些标志位，`SetConsoleMode` 返回 `ERROR_INVALID_PARAMETER`。
- **ultraviolet（glamour v1.0.0 的渲染引擎）`terminal_windows.go` / `poll_windows.go`**：同样的 `ENABLE_VIRTUAL_TERMINAL_*` 硬性要求（TUI 里 markdown 渲染走 glamour）。
- **lipgloss v1.x `ansi_windows.go`**：源码注释自认 "this only works with Windows 10"。
- pigo TUI 本身重度依赖 VT：`tea.View{AltScreen: true, MouseMode: tea.MouseModeCellMotion}`（`internal/cli/tui/model.go`）、Kitty keyboard protocol（`internal/cli/tui/input.go`）、OSC52 剪贴板（`tea.SetClipboard`/`ReadClipboard`）、背景色查询（`tea.RequestBackgroundColor`）。Win7 conhost **完全不解析 ANSI/VT 序列**，即使能启动也会满屏转义码乱码。
- ConPTY（Windows 10 1809+）在 Win7 上不存在。

## 3. REPL / 颜色输出（Go 1.20 假设下）

- REPL 行编辑器（`internal/cli/repl/line_editor.go` `ReadLine`）会 `exec.Command("stty", "-g")` 探测；Windows 无 `stty` → 探测失败 → **优雅回退**为普通 `ReadString`，不会崩，只是没有行内编辑/补全提示。
- `internal/cli/ui/color.go` 的 `Enabled()` 在 TTY 上输出 ANSI SGR 颜色；Win7 conhost 不解析 → 显示 `←[0m` 之类的原始转义码。可用 `NO_COLOR` 或安装 ansicon / 使用 ConEmu 缓解。

## 4. bash 工具在 Win7 上的降级

`internal/agenttool/bash_tool.go` 在 Windows 无 bash 时回退到 PowerShell（`powershell -Command`）：

- Win7 SP1 内置 **PowerShell 2.0**：没有 `ConvertFrom-Json` / `ConvertTo-Json`、`Invoke-RestMethod` 等（PS 3.0+ 才有），大量现代自动化脚本语法不可用；需另行安装 WMF 5.1。
- 装了 Git Bash 则走 bash 路径，无此问题。

## 5. 软性风险（即便排除上述硬阻断）

| 项目 | 影响 |
|---|---|
| 系统根证书库过期 | Win7 已于 2020-01 EOL，根证书更新停止；对使用较新 CA 的模型服务端点，Go 走系统根证书校验可能失败（纯 Go TLS 本身无问题）。 |
| 长路径不可用 | 运行时 `initLongPathSupport()` 仅对 build ≥ 10.0.15063 生效（`runtime/os_windows.go`），Win7 上项目路径超过 260 字符的文件操作可能失败。 |
| 发布产物无 32 位 | `.goreleaser.yaml` 仅构建 `windows/amd64` 与 `windows/arm64`，没有 `windows/386`；大量 Win7 是 32 位系统，连尝试的机会都没有。 |
| 无友好报错 | 目前 Win7 用户只会看到 `runtime: bcryptprimitives.dll not found` 这种底层 fatal，没有任何面向用户的提示（如"pigo 需要 Windows 10 及以上"）。 |

## 6. 不受影响的部分（问题不在 pigo 自身代码）

- **纯 Go 依赖**：`modernc.org/sqlite`（sessions.db，无 native DLL）、crypto/tls、HTTP/2、websocket 全部进程内实现，无 Win7 缺失的系统库依赖。
- **`internal/agenttool/process_tree_windows.go`**：Job Object（`KILL_ON_JOB_CLOSE`）、`PROCESS_QUERY_LIMITED_INFORMATION` 均为 Vista+ API，Win7 可用。
- **剪贴板**：`internal/clipboard/clipboard.go` 走 `clip.exe`，Win7 自带。
- 信号处理、`os.UserHomeDir`、自更新等均为跨平台实现，无 Win7 特有问题。

## 7. 结论与建议

1. **判定：Win7 完全不兼容。** 双层硬阻断（Go 1.21+ 运行时 + bubbletea v2 的 VT 要求），无配置或参数可绕过（`--no-tui` 也救不了第一层）。
2. **现实方案：明确最低系统要求为 Windows 10（1511+，建议 1809+）。** 这是 Go 官方与整个 charmbracecle v2 生态的既定方向，Win7 已 EOL 多年，为它维护 Go 1.20 + 去 TUI 的分支成本极高、收益趋近于零。
3. **可落地的小改进（可选）**：
   - 发布页/README 标注最低 Windows 版本；
   - 在 `cmd/pigo/main.go` 启动早期做 Windows 版本检测（`RtlGetVersion`），对 Win7 输出友好错误信息，替代现在运行时裸抛的 `bcryptprimitives.dll not found`（`golang.org/x/sys/windows` 已提供 `RtlGetVersion`）。

## 8. 方案评估：仅保留核心运行时（core → acp → webui）+ Go 版本降级

> 问题：如果去掉 TUI/REPL 等前端，只保留核心运行时（`acp` stdio server / `serve` HTTP），并允许把 Go 降级到支持 Win7 的版本，Win7 能跑吗？

### 8.1 架构层：砍掉 TUI 栈可以消除第二层阻断

charmbracelet 依赖（bubbletea / bubbles / lipgloss / glamour / ultraviolet / x/term / x/termios）**只被前端代码引用**：

- `internal/cli/tui/`：全屏 TUI（bubbletea v2 + bubbles + lipgloss v2）
- `internal/cli/ui/`：REPL 共享的 markdown 渲染（glamour + lipgloss v2）与颜色输出

核心包（`internal/runtime`、`internal/agentcore`、`internal/acp`、`internal/jsonrpc`、`internal/session`、`internal/sessionstore`、`internal/provider`、`internal/agenttool`、`internal/trust`、`internal/dream` 等）**零 charmbracelet 依赖**。ACP 是 stdio JSON-RPC、serve 是 HTTP，都不需要控制台 VT/TTY。

→ "core → acp → webui" 架构本身对 Win7 是友好的，**前提是构建时不含 `internal/cli/{tui,ui,repl}`**（或降级后重新实现无 glamour 的纯文本输出）。

### 8.2 但第一层阻断（Go 运行时）与架构无关，必须降 Go

`bcryptprimitives.dll` 硬加载发生在 `osinit()`，任何 Go 1.21+ 编译的二进制在 Win7 上都无法启动。**允许降 Go 版本是必要条件，且只能降到 Go 1.20.x**（Go 1.20 是最后官方支持 Win7 的版本线，最后补丁 1.20.14，2024-02 起 EOL）。

降级可行性评估（基于当前 go.mod 与 module cache 实测）：

| 层面 | 现状 | 降级成本 |
|---|---|---|
| go.mod 指令 | `go 1.27rc1` | 改 `go 1.20`，一行 |
| 核心依赖最低版本 | **全部要求 go 1.25.0**（x/sys v0.47、x/net v0.57、x/text v0.40、modernc sqlite v1.55、kin-openapi v0.142、oapi-codegen v2.8）；openai-go 要求 go 1.21；jsonschema/v6 要求 go 1.21 | 整体锁定到 2023 年初快照（x/sys ~v0.7、x/net ~v0.8、sqlite ~v1.21-1.23、kin-openapi ~v0.117…）；**openai-go 需替换为自写 OpenAI 兼容客户端**（pigo 本来就是 OpenAI-compatible 网关），jsonschema v6 → v5（API 变更）；可保留 pflag（go 1.12）、coder/websocket（go 1.19）等 |
| charmbracelet 栈 | glamour go 1.24、lipgloss v2 go 1.25、bubbletea v2 go 1.25 | 整个移除（见 8.1） |
| pigo 自身代码 | 使用了 `min`/`max` 内建（6 处）、`slices` 包（5 处）、`for range 100`（2 处，仅测试）——均为 Go 1.21+/1.22+ 特性；`errors.Join` 是 Go 1.20 自带，不用改 | 手写辅助函数替换，改动 < 20 处，工作量小 |

**结论：Go 1.20 降级在技术上是可行的**，主要成本在依赖锁定与 openai-go 替换，不在 pigo 自身代码。

### 8.3 Go 1.20 在 Win7 上的有利条件

- 运行时无 `bcryptprimitives.dll` 依赖（1.20 时代随机数走 advapi32 的 RtlGenRandom，Win7 自带）。
- **TLS 根证书过期问题有解**：Go 1.20 引入 `crypto/x509.SetFallbackRoots()`（进程内嵌 Mozilla 根证书池），pigo 可在初始化时显式调用，绕开 Win7 系统根证书库过期（2020 年停止更新）。
- 长路径：Go 1.20 的 os 包已对超长路径自动加 `\\?\` 前缀，基本可用。
- 全部纯 Go（`CGO_ENABLED=0`），无 Win7 缺失的系统 DLL 依赖；`process_tree_windows.go` 的 Job Object 等 API 为 Vista+，Win7 可用。

### 8.4 剩余的三堵墙：webui 链路在 Win7 上仍然不行

即使 pigo 核心在 Win7 上跑起来，"core → acp → **webui**"的 webui 侧还有自己的兼容问题：

1. **Node 版本墙**：`sdk/node/pigo-acp/package.json` 声明 `engines: node >= 22.19.0`；Node 22 官方最低要求 Windows 10，**Win7 无法安装/运行**。Win7 上 Node 上限是 Node 18（最后官方支持 Win7 的主线，且已于 2025-04 EOL），且 Node 18 安装/运行还需 SHA-2 签名补丁（KB4474419 / KB4490628）。即便降 Node，pigo-acp 若用了 Node 22 专属 API（如原生 WebSocket/fetch）、pi-web 的 `tsx` + `vite` 工具链也要整体降级，链路风险高。
2. **浏览器墙**：Win7 上浏览器上限为 Chrome 109 / Edge 109 / Firefox 115 ESR（2024-09 后不再更新），现代前端产物未必兼容。
3. **ACP 客户端墙**：若客户端是 Zed / ash-workbench desktop，Zed Windows 版最低要求 Windows 10；客户端必须跑在 Win7 之外。

**Win7 上唯一现实的形态**：Win7 机器只跑 pigo 核心服务端（`pigo acp` 或 `pigo serve`），webui 与 ACP 客户端部署在局域网内另一台 Win10+ / Linux 机器上远程接入——这样 webui 的 Node 墙与浏览器墙都被绕开。

### 8.5 风险评估

- **三层 EOL**：Win7（2020-01）、Go 1.20（2024-02）、Node 18（2025-04）均已停止安全支持；依赖锁定在 2023 年初快照意味着失去后续所有安全/功能修复。
- 模型 API 协议演进（reasoning 字段、流式格式、新端点）需要 2023 年客户端自行适配，且随 provider 演进持续维护。
- 这是一个需要**长期维护的兼容分支**（如 `win7/go1.20` 分支），不是一次性改动。

### 8.6 建议

1. **默认路线（推荐）**：不做 Win7 支持，明确最低要求 Windows 10（1511+，建议 1809+）。
2. **有强部署需求（工控/老旧环境）时**：先做 spike 验证——Go 1.20 + 最小核心（acp + serve + sqlite + provider 自写客户端 + `SetFallbackRoots`），在 Win7 上确认模型调用、会话存储、工具执行全链路跑通，再决定是否正式维护分支。核心代码改写量小（<20 处），主要评估点在依赖锁定与 openai-go 替换。
3. webui 无论如何不要部署在 Win7 上（Node 22 墙不可逾越），Win7 只做 agent 服务端。

### 8.7 补充评估：webui 编译成嵌入式 dist 包（go:embed）可行吗？

**思路**：在开发机（Win10+/Linux）上用 Node 构建 webui 的静态产物，`go:embed` 进 pigo.exe，由 `pigo serve` 直接托管——Win7 上只剩浏览器访问 HTTP 端口，Node 运行时墙消失。**概念上成立，但 pi-web 当前形态不能直接这么做。**

#### pigo 侧：机制完全现成（工作很小）

- `internal/remotecontrol/server.go` 已有先例：`//go:embed web` + `http.FileServer` + 配对 token + cookie 鉴权 + WebSocket 帧协议（输出镜像 / 输入 / 工具确认），内嵌的 SPA（`internal/remotecontrol/web/`）已是成形的轻量实现（终端镜像风格，无框架依赖，Chrome 109 兼容）。
- `internal/httpapi` 已提供 webui 需要的全部后端 API（session 管理、prompt 同步/异步、SSE 事件流 `/api/v1/events`、commands、permission、config/providers、modes），并支持 `--cors` / `--password`（HTTP Basic）。
- 静态托管一个 dist 目录到 serve 路由，新增工作量约 1 个文件。

#### pi-web 侧：Next.js 16 server 应用，不能直接 embed（工程量大）

`E:\project\pi-web` 是 **Next.js 16 App Router + React 19 + Tailwind 4**，`next build` 产物是 `.next/`（Node server 产物），**不是静态文件集**，Go 无法托管。且运行时组件不止静态文件：

| pi-web 组件 | 性质 | 嵌入 dist 后的处置 |
|---|---|---|
| `app/api/*`（50+ 路由） | Node 运行时（Next.js API Routes） | 移除 |
| `proxy.ts`（middleware 安全层） | Node 运行时 | 移除（由 pigo 的 --password/--cors 替代） |
| `sessiond`（8599 端口） | Node 服务 | 移除 |
| `lib/pigo-client.ts` | **已完成**（PIGO_INTEGRATION.md：封装 pigo HTTP API，SSE 事件流） | 保留并改为浏览器直连 |
| `app/api/agent|sessions|models|…` | 纯 pigo HTTP API 代理 | 浏览器直连 pigo serve（CORS + Basic auth） |
| `app/api/files|git|auth|skills|plugins|worktrees|cwd` | pi-web 自有功能（文件浏览、git 面板、登录、插件等），非 pigo API | **要么搬进 pigo serve（新增 API，工程量大），要么砍掉** |

改造路径：`output: 'export'` 静态导出（或改 Vite 重建）→ 所有数据请求改为浏览器直连 pigo serve → 移除全部 Node 运行时组件。**这是 pi-web 项目的一等改造，不是配置开关**；且 8.4 的浏览器墙仍在：Win7 上限 Chrome 109，而 Tailwind 4 默认调色板用 oklch/color-mix（Chrome 111+），需构建时降级为 hex 色板，Next 16 的 target 也要降到 chrome109 实测。

#### 结论

- 若"webui"只需**远程控制/镜像 + 确认**：直接用 pigo 现有 `internal/remotecontrol` 的内嵌 SPA，成本趋近于零，无浏览器兼容风险。
- 若必须是 pi-web 全功能界面：嵌入 dist 需要 pi-web 静态化改造（PIGO_INTEGRATION.md 的 HTTP 化方向已完成一半），加上前端 target 降级到 Chrome 109，工程量不小；且**完整链路仍以 Go 1.20 降级分支为前提**（第一层阻断不变）。

## 附：验证方法

在 Win7 虚拟机（或真机）上直接运行 `pigo.exe --version` 即可复现：进程立即以 fatal 退出并输出 `runtime: bcryptprimitives.dll not found`，无需任何配置文件。若想验证第二层（TUI），需用 Go 1.20.x 交叉编译（`GOOS=windows GOARCH=amd64 CGO_ENABLED=0`），在 Win7 控制台运行 `pigo`（无参数），观察 `SetConsoleMode` 报错与转义码乱码。

## 证据索引

| 证据 | 位置 |
|---|---|
| go.mod 工具链声明 | `go.mod:3`（`go 1.27rc1`） |
| 运行时强制加载 bcryptprimitives.dll | `GOROOT/src/runtime/os_windows.go` `loadOptionalSyscalls()`（go1.27rc2 实测） |
| 官方 Windows 版本政策 | Go 1.20 发布说明（Go 1.21 起要求 Windows 10） |
| bubbletea v2 VT 硬性要求 | `github.com/charmbracelet/bubbletea/v2@v2.0.8/tty_windows.go` `initInput()` |
| ultraviolet VT 硬性要求 | `github.com/charmbracelet/ultraviolet@.../terminal_windows.go`、`poll_windows.go` |
| lipgloss 自述仅支持 Win10 | `github.com/charmbracelet/lipgloss@v1.1.1-.../ansi_windows.go` |
| TUI 使用 AltScreen/MouseMode | `internal/cli/tui/model.go`（`tea.View{AltScreen: true, ...}`） |
| REPL stty 回退 | `internal/cli/repl/line_editor.go:417` |
| bash → PowerShell 回退 | `internal/agenttool/bash_tool.go` `resolveShell` |
| Job Object 实现 | `internal/agenttool/process_tree_windows.go` |
| 发布平台矩阵 | `.goreleaser.yaml`（仅 windows/amd64、arm64，`CGO_ENABLED=0`） |
