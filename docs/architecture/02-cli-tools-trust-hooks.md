# Pigo 架构报告：CLI 前端、工具层、信任、Hooks

## 1. 整体分层

pigo 采用"前端 → ACP 通道 → AgentCore → 工具层"四层架构。`internal/cli` 包含多种前端形态（TUI、REPL、headless），但**全部统一走 ACP（Agent Control Protocol）通道**，ACP 作为进程内 server 暴露 `session/prompt`、`session/request_permission` 等 JSON-RPC 方法。`cmd/pigo` 根据 TTY 和 `--no-tui` 标志决定启动 TUI 还是 REPL，headless 由 `--print`/`--stream-json` 走独立 driver。

## 2. internal/cli（前端入口）

| 子包 | 职责 |
|---|---|
| `doc.go` / `host.go` | 定义 `Host` 接口（session 协作方、trust、hooks dispatcher 的只读视图）和 `Editor` 接口（单行输入契约），使 `/goal`、`/btw`、`/status`、REPL 子包不直接依赖聚合类型 |
| `run/` | 共享 run 组装层：`SetupEnv` 解析 provider、构建工具集、发现 skills/plugins、应用 `ToolPolicy`；`ResolveHookSet` 按 default<global<project<env 分层加载 hooks；`InstallDriverHooks`/`InstallSeams` 是六路 driver 的统一 hook 缝合点 |
| `tui/` | Bubble Tea v2 全屏 UI（alt-screen），通过 `acp_bridge.go` 调用 `client.Prompt` 并泵送 ACP 通知到 tea.Msg，实现 tool_card、transcript、status_bar |
| `repl/` | 行式 REPL，`Run()` 强制走 `RunACP()`：启动 in-process ACP server，创建/恢复 session，进入 read-prompt-stream 循环 |
| `headless/` | 非交互单 run driver：解析 `HeadlessMode`（text / stream-json），打开 session，组装 `AgentContext`，调用 `runtime.RunHeadless`，结束后持久化 |
| `acpcmd/` | ACP stdio 入口：外部工具通过 stdin/stdout JSON-RPC 连接，走 `server_stdio.go` |

**前端约定**：TUI 与 REPL 均走 ACP（见 `tui/acp_bridge.go` 和 `repl/interactive.go: Run -> RunACP`），headless 走传统 `runtime.RunHeadless` 但 hook 缝合同样经过 `InstallDriverHooks`。

## 3. internal/agenttool（工具实现与分发）

- **`registry.go`**：`ToolRegistry` 以 name→`AgentTool` 映射存储工具，注册时预编译 JSON Schema（santhosh-tekuri/jsonschema v6），参数校验失败返回 `FieldError` 列表（field-level 错误，不中断 run）。
- **`tool_executor.go`**：三阶段执行 `prepare → execute → finalize`。每阶段可选 hook：`PrepareArguments`（重写参数）→ `BeforeToolCall`（trust 门 + PreToolUse hook 在此链入）→ `AfterToolCall`（PostToolUse hook 在此链入）。所有失败（未知工具、校验失败、hook 拦截、panic、Go error）均编码为 `AgentToolResult{IsError:true}`，不抛出 Go error，确保模型始终收到 `ToolResultMessage`。事件通过 `EmitFunc` 发出 `ToolExecutionPendingEvent` → `ToolExecutionStartEvent` → `ToolExecutionUpdateEvent`（部分结果）→ `ToolExecutionEndEvent`。最大结果字节数 100KB（`toolResultMaxBytes`），单工具有各自更紧的内层 cap。
- **工具清单**：`bash_tool`（支持后台 + 控制）、`edit_tool`、`write_tool`、`read_tool`、`search_tool`（grep/find）、`websearch_tool`（多后端）、`webfetch_tool`、`goal_tool`、`todo_tool`、`memory_tool`、`batch_executor`（并发批处理）。

## 4. internal/trust（文件操作信任）

- **`manager.go`**：`Manager` 加载 `~/.pigo/trust.json`（或 `$PIGO_HOME/trust.json`），存储 `path -> *bool`（null= undecided）。`NearestTrustDecision` 沿 cwd 向上 WalkUp 找最近决定；`IsTrusted` 先查 session-level `map[string]bool`（"just once"），再查持久化数据。`SetSessionTrust` 仅进程内生效；`SetDecision` 原子写入（temp+rename，0o600）。
- **`interactive.go`**：首次启动 `EnsureTrustPrompt` 询问信任级别；`BeforeToolCall` 为 `bash/write/edit` 构建 permission hook（`SideEffectTools` 白名单）；`/trust on|off|once|status` 注册为 slash 命令。`ConfirmToolCall` 输出单行预览（最多 200 runes）。
- **边界**：主目录信任（`IsTrusted` WalkUp 语义）；附加目录（`ACPPermissionBroker` 按 session.cwd 单独查询）合并时各自独立，不互相继承。

## 5. internal/session（会话模型）

- **`session.go`**：JSONL 追加日志，第一行 `SessionHeader`（schema v3：id/parentId 树形结构、Cwd、ContextFrom/Watermark 用于 compaction 继承）。v1/v2 文件加载时自动迁移（合成 id/parentId 链）。`Store` 提供 List/Load/Append/Persist。
- **`sessionstore/`**：SQLite canonical 存储（`$PIGO_HOME/sessions.db`），按 `sessions.cwd` 区分项目，支持 fork/clone 树导航。

## 6. internal/hooks（生命周期钩子）

- **`config.go`**：`HookConfig{Type:"command", Command, Timeout}`、`HookMatcherConfig{Matcher, Hooks}`、`HookSet map[string][]HookMatcherConfig`。
- **`matcher.go`**：空/* 匹配所有；`|`-分隔精确工具名列表；其余编译为 Go regexp。
- **`protocol.go`**：`HookInput`（仅可见字段，无密钥）/`HookOutput`（decision/reason/additionalContext/updatedInput）/`HookDecision`（合并后）。
- **`runner.go`**：通过 `sh -c` 执行命令，stdin 写单行 JSON，stdout/stderr 各上限 1MB；exit 0=allow，exit 2=block（Claude Code 语义），其他=执行失败；超时由 `context.WithTimeout` 控制，`WaitDelay=1s` 避免 grandchild 阻塞。
- **`dispatch.go`**：`Dispatcher.Dispatch` 按 eventType+toolName 匹配、顺序执行、合并；**fail-open**（hook 执行失败仅 warn，不阻断）；`PreToolUse` 首个 block 短路后续 hook（避免 block 后又被 rewrite）。
- **`notifier.go`**：`NewHookNotifier` 把 SessionEnd/PreCompact 等 `AgentEvent` 转发给 hook 的 Notification 事件。

**Hook 加载策略（FR-14）**：
- 全局 hooks：`$PIGO_HOME/config.json` 的 hooks 段，**始终加载**。
- 项目 hooks：`<cwd>/.pigo/config.json`，**仅在目录受信任时加载**（`Trusted(cwd)` 查 trust.json）。
- 未读/缺失 trust store 视为未信任（fail-closed），项目 hooks 静默跳过。
- 分层优先级：default < global < project < env。

**与 ACP permission 先后关系**：`InstallSeamsBefore`（ACP 路径）让 PreToolUse hook 在 ACP permission broker **之前**运行，这样 policy block 不会触发 client permission prompt；trust 门始终最先（chainBeforeToolCall 中 trust 在 hook 之前）。`InstallSeams`（headless/REPL 路径）则 hook 在 trust 之后（trust 先 run，hook 后 run）。

## 7. internal/builtinskills（内置技能）

- **`manifest.go`**：`Set{Name, Version, Root, Skills, FS}`，用 `//go:embed all:skills` 编译进二进制。当前只有 `goal-workflow` 集合（2026-07-24），含 prd/prd-to-spec/to-issues/review-it/ship-it 等 + architecture-diagram/weather 独立技能。
- **`bootstrap.go`**：`Bootstrap(pigoHome, skillsDir, logw)` 幂等安装：读 `builtin-skills.json` 状态，跳过已安装版本相同的集合，单技能失败不中断整体，半写树自动回滚。

## 8. internal/clipboard（剪贴板）

- **`clipboard.go`**：无 cgo、无第三方依赖，按平台探测 `pbcopy`（macOS）、`clip`（Windows）、`wl-copy`/`xclip`/`xsel`（Linux），首个可用即 pipe 文本到 stdin。`Available()` 预检；`Copy` 失败返回 `ErrUnavailable` 供调用方降级（打印而非复制）。

## 9. internal/remotecontrol（远程控制）

- **`server.go`**：内嵌 HTTP + WebSocket 服务器（`coder/websocket`），托管 SPA（`//go:embed web`）。核心常量：pairTTL=10min、maxFrame=64KB、outputFlush=16ms、outputQueue=256KB、replayRing=256KB。
- **`bridge.go`**：`Bridge` 将本地终端与远程浏览器合并：`OutputWriter` tee 输出、`RemoteInput` 通道注入远程 prompt、`Confirm` 阻塞等待远程/本地 decision（first-wins）。`Handler` 接口（OnInput/OnDecide）由 REPL bridge 实现。
- **`lanaddr.go`/`qr.go`/`token.go`**：LAN 地址发现、配对 QR 生成、一次性 token。

## 10. internal/selfupdate（自更新）

- **`update.go`**：`Run(ctx, current, out, errOut)` 调用 `LatestTag` 比较版本，`Updater.Apply` 下载 goreleaser 归档（tar.gz/zip）、校验 SHA256（来自 `checksums.txt`）、临时文件写入 + `os.Rename` 原子替换。Windows 用 zip，其余用 tar.gz，文件名模板与 `.goreleaser.yaml` 一致。
- **`version.go`/`cache.go`**：GitHub API 拉取 release tag，本地缓存避免每次都请求。

## 11. 关键类型/接口清单

| 类型/接口 | 包 | 职责 |
|---|---|---|
| `cli.Host` | `internal/cli` | session 协作方只读视图，/goal /btw /status 的抽象 seam |
| `cli.Editor` | `internal/cli` | 单行输入契约 |
| `agenttool.ToolRegistry` | `internal/agenttool` | name→AgentTool 注册表 + JSON Schema 校验 |
| `agenttool.ToolExecutorConfig` | `internal/agenttool` | 三阶段执行配置（Prepare/Before/After） |
| `trust.Manager` | `internal/trust` | 目录信任决策持久化与查询 |
| `trust.Decision` | `internal/trust` | Undecided/Trusted/Untrusted 三态 |
| `session.SessionHeader` | `internal/session` | 会话头（v3 树形） |
| `hooks.Dispatcher` | `internal/hooks` | 事件匹配 + 顺序执行 + 合并 |
| `hooks.HookSet` | `internal/hooks` | event→matchers 映射 |
| `hooks.HookInput/Output/Decision` | `internal/hooks` | 进程间协议 |
| `hooks.Runner` | `internal/hooks` | 单 hook 命令执行（超时/输出上限/退出码） |
| `acp.Client` | `internal/acp` | ACP 客户端（pump + permission handler） |
| `acp.Server` | `internal/acp` | ACP 服务器（dispatch loop） |
| `acp.AcpSession` | `internal/acp` | 单 ACP 会话（transcript/pending queue/cancel） |
| `acp.ACPPermissionBroker` | `internal/acp` | trust gate → ACP request_permission 桥接 |
| `remotecontrol.Server` | `internal/remotecontrol` | HTTP+WebSocket 配对服务器 |
| `remotecontrol.Bridge` | `internal/remotecontrol` | 本地终端↔远程浏览器合并 |
| `builtinskills.Set/Manifest` | `internal/builtinskills` | 内置技能集合声明 |
| `selfupdate.Updater` | `internal/selfupdate` | 自更新（下载+校验+原子替换） |

## 12. 模块间依赖关系

```
cmd/pigo
  └─ internal/cli
       ├─ run/          (SetupEnv, ResolveHookSet, InstallDriverHooks)
       │    ├─ agenttool   (ToolRegistry, ToolExecutor)
       │    ├─ hooks       (HookSet, Dispatcher)
       │    ├─ trust       (Manager)
       │    ├─ builtinskills (Manifest, Bootstrap)
       │    ├─ plugin      (Discover)
       │    └─ runtime     (RunConfig, HeadlessConfig)
       ├─ tui/           (acp_bridge → acp.Client)
       ├─ repl/          (RunACP → acp.StartInProcessWithHooks)
       └─ headless/      (runtime.RunHeadless)
            └─ acp/        (Server, Client, AcpSession, ACPPermissionBroker)
                 ├─ agentcore (AgentTool, AgentContext)
                 ├─ session   (Store)
                 ├─ trust     (Manager)
                 └─ runtime   (RunConfig)
internal/remotecontrol  (HTTP+WS server，不依赖 cli)
internal/clipboard      (纯工具，无依赖)
internal/selfupdate     (纯工具，无依赖)
```

**关键设计原则**：
1. `agentcore`/`runtime` 不依赖 cli/hooks/trust，保持核心干净。
2. hooks 包是 leaf（仅 stdlib），cli/run 层负责把 resolver 和 trust gate 喂进来。
3. ACP 是统一通道：TUI 和 REPL 都通过 `acp.Client` 与 in-process `acp.Server` 通信，共享同一套 session/permission/tool 协议。
4. trust 与 hooks 耦合：项目 hooks 加载受 trust 门控制（FR-14），未信任目录不加载项目级 `./.pigo/config.json`。
5. 工具执行三阶段 hook 链：trust `BeforeToolCall` → PreToolUse hook → 实际执行 → PostToolUse hook → `AfterToolCall` 覆写，每阶段失败都编码为 tool result。
