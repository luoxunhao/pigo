# pigo 架构报告：入口 → ACP → Runtime

## 1. 总体分层

pigo 是一个 Go 编写的 coding agent，通过 **ACP（Agent Client Protocol）** 对外暴露 JSON-RPC 2.0 over stdio 服务，供 Zed 等 IDE 客户端使用。整体架构分为五层：

```
cmd/pigo (CLI 入口)
  └─ internal/cli/acpcmd (ACP 模式启动)
       └─ internal/acp (ACP 协议层：Dispatcher / Server / Transport / SessionManager)
            └─ internal/runtime (Agent Loop / Headless 驱动)
                 └─ internal/agentcore (消息/事件/工具核心类型)
                      └─ internal/provider (LLM 调用)
                           └─ internal/agenttool (工具实现)
```

## 2. cmd/pigo/main.go — CLI 入口

**职责**：解析 CLI 参数、叠加 config.toml 配置、分发到四种运行模式（TUI/REPL/headless/ACP）。

- 通过 `flag` 包解析选项，支持 `--acp`（ACP stdio server）、`--subagent-rpc`（子 agent JSON-RPC）、`--dream`（记忆整合）、`-p`（headless print）、无 prompt 时的 TUI/REPL 交互。
- `dispatch()` 是纯分发函数，根据选项分支；TUI/REPL 使用 Bubble Tea + 行式 REPL，headless 输出 text 或 stream-json。
- `applyFileConfig()` 实现优先级：CLI flag > config.toml > 默认值；`--cwd`（`-C`）最先执行 `os.Chdir`，保证所有 cwd 相关路径一致。

## 3. internal/acp — ACP 协议层

### 3.1 核心类型

- **`Dispatcher`**（`dispatch.go`）：ACP 请求分发的核心，持有 `SessionManager`、`Transport`、权限 Broker、信任管理器、斜杠命令注册表等。
- **`Server`**（`server.go`）：驱动 ACP 传输循环，接收消息后分发给 `Dispatcher`。
- **`Transport`**（`transport.go`）：抽象双向 JSON-RPC 传输，两种实现：
  - `channelTransport`：进程内通道对，供 TUI/REPL 内部使用。
  - `StdioTransport`：newline-delimited JSON-RPC 2.0，供外部 ACP 客户端（Zed）使用。
- **`AcpSession`**（`session.go`）：ACP 会话，包含 ID、cwd、工具集、注册表、消息历史、模型、思考级别、目标、转向/跟进模式等。

### 3.2 请求分发机制

`Dispatcher.HandleRequest()` 根据 method 路由：

**标准 ACP 方法（session/* 与 model/*）**：
- `initialize`：返回协议版本、能力清单（loadSession、promptCapabilities、sessionCapabilities、pigo extensions 等）。
- `session/new`：创建新会话，需要绝对路径 `cwd`，构建 SessionContext，初始化 store。
- `session/load`：加载已有会话，支持子 agent 路由（`subagent-*` 前缀），回放历史消息。
- `session/list`、`session/delete`、`session/close`：会话管理。
- `session/prompt`：发送提示，异步执行（`HandleDeferredRequest`），支持斜杠命令优先。
- `session/cancel`：取消当前 turn 或队列。
- `session/set_mode`、`session/set_config_option`、`model/set`：模型/思考级别配置。

**pigo 扩展 RPC（pigo/*）**：
- `pigo/command`：执行斜杠命令（内置 + registry 中的用户命令）。
- `pigo/status`：返回会话状态文本。
- `pigo/models`：列出已配置模型。
- `pigo/models/discover`：通过 provider 的 /models 端点发现模型。
- `pigo/config`：读写模型配置（CRUD）。
- `pigo/config/test`：发送 "ping" 测试模型连通性。
- `pigo/messages`：分页获取会话历史。
- `pigo/trust/list`、`pigo/trust/set`：信任决策管理。
- 未实现：`pigo/rewind`、`pigo/fork`、`pigo/tree`、`pigo/goal`、`pigo/btw`、`pigo/dream`、`pigo/remotecontrol`。

### 3.3 会话生命周期

**session/new**：
1. 验证 `cwd` 为绝对路径。
2. 通过 `SessionContextFactory` 构建 `SessionContext`（含 sysPrompt、tools、registry）。
3. 获取或创建 `sessionstore.Store`（按 workspace slug 缓存）。
4. 调用 `SessionManager.New()` 创建会话，生成 ID，写入元数据。
5. 异步广播 `session/update`（startup info、available commands）。

**session/load**：
1. 区分子 agent 路由（`subagent-*` 前缀）与标准会话。
2. 从 store 加载 header、messages、metadata。
3. 重建 SessionContext（cwd 驱动的 sysPrompt/tools/registry）。
4. 恢复 thinking level 和 model。
5. 异步回放历史消息（`replaySession()`）并广播 announce。
6. 返回包含历史 messages 的 session payload。

**信任边界**：
- `ACPPermissionBroker` 在 `beforeToolCall` 钩子处拦截，基于 cwd 做信任决策。
- 信任变化时调用 `invalidateSessionRegistries()` 重建所有会话的注册表。

## 4. internal/runtime — 运行时核心

### 4.1 核心结构

- **`RunConfig`**（`loop.go`）：循环运行的完整配置，嵌入 `LoopConfig`（provider/stream/model/thinking）和 `Batch`（工具执行配置），以及四个 hook：
  - `GetFollowUpMessages`：outer loop 继续条件。
  - `GetSteeringMessages`：turn 间注入消息。
  - `PrepareNextTurn`：turn 间上下文/模型/思考级别替换。
  - `ShouldStopAfterTurn` / `OnStop`：停止条件与拦截。
- **`LoopEventStream`**：`EventStream[AgentEvent, []AgentMessage]` 的类型别名。

### 4.2 Agent 主循环（loop.go）

`runLoop()` 实现两层循环：
- **内层**：一次 turn = 流式获取 assistant 响应 → 批量执行工具调用 → 反馈结果，直到 assistant 无工具调用。
- **外层**：turn 结束后检查 `GetFollowUpMessages`，有则继续，否则结束。

关键控制流：
- `StopReasonLength`：响应被 token 截断时，失败所有工具调用让模型重发（最多 3 次连续）。
- `StopReasonError/Aborted`：立即结束 run。
- 自动 compaction：上下文接近限制时触发。
- 遥测：`TelemetryEvent` 在 run 结束时汇总 turn 数、工具耗时、compaction 次数、context 利用率。

### 4.3 Headless 驱动（headless.go）

`RunHeadless()` 是非交互式驱动入口：
- 支持 `PrintMode`（输出最终文本）和 `StreamJSONMode`（每事件一行 JSON）。
- `SubAgentProgressEvent` 写入 progress writer（stderr），不污染 stdout。
- 返回 `ErrRunFailed` 映射到进程退出码。

### 4.4 RuntimeRunner（acp/runner.go）

`RuntimeRunner` 是实现 `SessionRunner` 接口的生产级 runner：
- `Run()` / `RunWithTools()`：构建 `AgentContext` 和 `RunConfig`，调用 `runtime.RunHeadless()`。
- `ResolveForModel()`：根据 model ID 解析 provider（从 configured models 或启动 provider）。
- 共享 `FileSnapshotRecorder` 用于 rewind。

## 5. internal/agentcore — Agent 核心

### 5.1 核心类型

- **`AgentEvent`**（`event.go`）：sealed interface，14 种事件类型，包括 `agent_start`、`agent_end`、`turn_start/end`、`message_start/update/end`、`tool_execution_*`、`compaction_*`、`subagent_progress`、`telemetry`。
- **`Message`**（`message.go`）：sealed interface，4 种角色：`UserMessage`、`AssistantMessage`、`ToolResultMessage`、`CompactionMessage`。
- **`AgentContext`**（`tool.go`）：loop 输入状态（systemPrompt、messages、tools）。
- **`AgentTool`**（`tool.go`）：工具接口（Name/Description/Schema/ExecutionMode/Execute）。
- **`ThinkingLevel`**：6 级推理强度（off/minimal/low/medium/high/xhigh）+ max。
- **`EventStream`**：泛型事件流，支持异步 producer/consumer 模式。

### 5.2 消息流转

```
UserMessage → AssistantMessage (流式) → [ToolCallContent] → ToolResultMessage
                                              ↓
                                        下一轮循环
```

Compaction 在历史中插入 `CompactionMessage`，作为被摘要历史的占位符。

## 6. internal/jsonrpc — JSON-RPC 基础设施

**职责**：实现子进程 JSON-RPC 2.0 客户端，用于 MCP 插件、子 agent 等场景。

- **`Client`**（`transport.go`）：启动子进程，stdin 写请求，stdout 读响应。
- 通过 `pending` map（id → chan res）关联请求与响应，支持并发调用。
- `readLoop()` goroutine 持续读取 newline-delimited JSON，按 id 分发。
- `Close()` 优雅关闭 stdin，5 秒 grace 期后 kill 子进程。

## 7. internal/pihost — Pi 扩展宿主

**职责**：嵌入 Node.js 扩展宿主程序（`pihost.mjs`），通过 `go:embed` 打包。

- 加载 pi 扩展（`@earendil-works/pi-coding-agent`），暴露工具/命令给 pigo 插件系统。
- JSON-RPC 协议：`initialize` → Manifest，`tools/call` → 工具结果，`commands/call` → 命令结果。
- 故障隔离：SDK 加载失败在 `initialize` 前退出，插件被跳过。

## 8. 模块依赖关系

```
cmd/pigo
  ├─ internal/cli/* (repl, tui, headless, acpcmd, config, run, ui)
  │    └─ internal/runtime (SetupEnv, RunHeadless)
  │         └─ internal/agentcore (AgentContext, AgentTool, EventStream)
  │              └─ internal/provider (LLM streaming)
  │              └─ internal/agenttool (tools)
  │              └─ internal/compaction
  │              └─ internal/memory
  ├─ internal/acp
  │    ├─ internal/runtime (SessionRunner, RunHeadless)
  │    ├─ internal/agentcore
  │    ├─ internal/provider
  │    ├─ internal/sessionstore
  │    ├─ internal/trust
  │    └─ internal/plugin
  ├─ internal/jsonrpc (subprocess client)
  ├─ internal/pihost (go:embed pihost.mjs)
  └─ internal/dream (memory consolidation)
```

**关键接口**：
- `SessionRunner` / `TooledRunner`：ACP 与 runtime 的对接点。
- `Transport`：ACP 传输抽象，channel 和 stdio 实现。
- `AgentEvent` / `Message`：agentcore 的 sealed interfaces。
- `AgentTool`：工具执行接口。

## 9. 总结

pigo 采用清晰的**协议层（ACP）→ 运行时层（runtime）→ 核心层（agentcore）**三塔结构：
- **ACP 层**负责外部协议（JSON-RPC 2.0 over stdio），隔离客户端与内部实现。
- **Runtime 层**实现 agent 主循环，管理 turn、工具执行、compaction、遥测。
- **Agentcore 层**提供类型定义（事件、消息、工具），保持语言无关性。

设计亮点：
1. **Transport 抽象**：stdio 和 channel 共享同一 Dispatcher，TUI/REPL 也通过 ACP 通信。
2. **SessionContextFactory**：多 workspace 支持，每个会话独立 sysPrompt/tools/registry。
3. **EventStream 泛型**：替换 TypeScript generator，支持 back-pressure 和 result 提取。
4. **Hook 机制**：steering/follow-up/prepare/stop 钩子实现灵活的 turn 控制。
5. **Fault isolation**：插件/子 agent 故障不影响主进程，信任边界严格。
