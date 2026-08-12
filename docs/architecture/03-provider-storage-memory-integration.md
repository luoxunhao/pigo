# Provider · SessionStore · Memory · Dream 架构报告

## 1. internal/provider — 模型 Provider 与流式协议适配

**职责**：将不同 LLM 供应商的 API 统一为一个 `Provider` 接口，屏蔽 wire 格式差异，向下对接 HTTP/SSE 传输层，向上为 agent loop 提供 `StreamFn` 合约。

**关键文件与类型**：
- `provider_interface.go`：定义 `Provider` 接口（`Name/Models/StreamCompletion`）、`Model`（provider 无关的模型元数据）、`CompletionRequest`、`StreamConfig`、`StreamFn` 合约。
- `protocol.go`：`NormalizeProtocol` 将 `openai/openai/chat/openai/resp_api/anthropic` 规范化为内部选择器；`ProtocolLabel` 用于启动 banner 展示。
- `registry.go`：`ProviderSpec` 表是单一事实源，记录每个内置供应商的 `EnvVars/DefaultBaseURL/Protocol/AuthScheme/ExtraHeaders`；支持 30+ 供应商（anthropic、openai、deepseek、nvidia、google、groq、cerebras、xai、openrouter、mistral、minimax/minimax-cn、moonshotai、qianfan、volcengine、dashscope、hunyuan、azure-openai、amazon-bedrock、google-vertex、cloudflare-* 等）。
- `transport.go`：`StreamRequest` 是共享传输层，实现 **双失败模型**（FR-13）：仅在"无法构建流"时返回 error；流内运行时错误通过 `StreamErrorEvent`（`stopReason=error/aborted`）承载。含连接重试（429/503/529）、双看门狗（idle + content stall）。
- `openai.go`、`anthropic.go`：`OpenAIDecoder` / `AnthropicDecoder` 分别是 OpenAI Chat Completions 与 Anthropic Messages 的 SSE 状态机解码器，支持 thinking/reasoning stream。
- `providers.go`：两种驱动形状覆盖所有供应商：`openAICompatDriver`（OpenRouter/Ollama/NVIDIA/Azure等）与 `anthropicCompatDriver`（Bedrock 等）；默认 base URL 与 auth header 按 spec 注入。
- `resolve.go`：`ResolveProvider` 按优先级解析：`--provider` > `--protocol` > 预设目录 > 模型名启发式（ollama/nvidia/claude-* 等）> 默认 OpenRouter；`ResolveNamedProvider` 按 spec.Protocol 路由到对应驱动。
- `auth.go`：`CredentialStore` 实现三层层级解析（OAuth TokenSource → CLI `--api-key` 覆盖 → 环境变量 → config 文件）；`envAPIKey` 按 `spec.EnvVars` 顺序查找；`TokenSource` 支持 OAuth 刷新。密钥只暴露 `HasCredential`/`apiKeyConfigured`，从不记录 secret。

**config.toml 模型配置读取**：`provider.ResolveProvider` 从 `config.toml`（经由 `internal/cli/config`）读取 `model`/`provider`/`protocol`/`base_url`，再结合环境变量与 flag 解析。

## 2. internal/sessionstore — 会话与转录持久化

**职责**：项目级会话持久化，替代 legacy flat JSONL store，支持分支树与跨项目列表。

**结构**：
- `Store`：目录级句柄，`$PIGO_HOME/projects/<workspace-slug>/sessions/`；`sessionsDir` 持有 metadata 与 transcript。
- `Metadata`（JSON，camelCase 兼容 ash）：`sessionId/sessionName/agentType/modelName/createdAt/lastActiveAt/turnCount/messageCount/toolCallCount/status/tags/parentSessionId/subagentType` 等。
- `IndexFile`：项目级 session 索引，按 `lastActiveAt` 排序；`List` 优先读索引，不一致则重建。
- 转录：委托 `session.Store`（JSONL），支持 `Append` / `AppendBranch`（带 `parentLeafID` 分支）/ `LoadEntries`。
- `ListAll(pigoHome)`：跨项目扫描所有 `.metadata.json`，按 `LastActiveAt` 降序，供 `/dream` 与 `--list-sessions` 使用。
- SchemaVersion=1；读取时未知更高版本为硬错误；写操作使用 atomic temp+rename。

## 3. internal/dream — 记忆整合 / Distill

**职责**：将历史会话转录蒸馏为持久记忆条目，并合并/去重/清理记忆库。

**核心组件**：
- `config.go`：`Config{Enabled/IntervalDays/RecentSessions}`，默认 7 天间隔、20 个最近会话。
- `state.go`：`State{LastRunAt/LastStatus/LastReport}` 持久化到 `<memoryRoot>/global/dream/state.json`；`Due` 判断自动触发（首次运行不自动触发）。
- `lock.go`：`AcquireLock` 基于 `<memoryRoot>/global/dream/dream.lock` 的单实例锁；30 分钟 stale 可抢占。
- `plan.go`：`BuildPlan` 枚举 global + 当前项目 scope 下的 `.md`（排除 checkpoint/MEMORY.md），产出 `DedupeGroups/InvalidPathRefs/NearDupPairs`（Jaccard≥0.7）；`projectID` 用 sha256 前 12 位。
- `runner.go`：`Runner.Run` 流程：acquire lock → BuildPlan → Consolidate → applyDedupe → applyPathClean → applyConsolidation → updateScopeIndexes → Reconcile → SaveState。支持 `--dream --dry-run`。
- `consolidator.go`：`llmConsolidator` 驱动主会话模型合并/修剪（`dreamSystemPrompt`），`distill` 子步骤调用独立的 JSON 蒸馏模型。
- `distill.go`：`collectTranscripts` 按 `defaultTranscriptBudget=24000` 字节截断转录；`parseDistillResponse` 保守去重（`distillNewEntryTypes={user,feedback,project,reference}`）；`withinScope` 守卫路径不越界。
- `scheduler.go`：`Scheduler.Due` 轻量检查；`MaybeRunBackground` 在后台 goroutine 启动子进程（CLI `pigo --dream`），报告仅在有变更时提示。

**关系**：dream 消费 `sessionstore.ListAll`（distill 输入），写入 `internal/memory`（SQLite+FTS），读取 `memory.Store.Reconcile` 后更新 FTS。

## 4. internal/compaction — 上下文压缩

**职责**：当上下文接近窗口上限时，将历史摘要化并保留最近 N token。

**关键流程**（`compact.go`）：
- `FindCutPoint`（`cutpoint.go`）：从新到旧累加 `EstimateTokens`，到达 `KeepRecentTokens` 后 snap 到最近的 valid cut point（user/assistant，不切 toolResult）。
- `Compact`：切点之前 `[prevCompactionIndex+1, firstKeptIndex)` 的消息送入 `GenerateSummary`；累积 `FileOperations`（read/edited/written）附到 summary。
- `GenerateSummary`（`summary.go`）：使用结构化 prompt（首次/增量两模板），输出 `## Goal/## Constraints/## Progress/## Key Decisions/## Next Steps/## Critical Context`；`maxTokens=min(0.8*reserveTokens, model.MaxOutputTokens)`。
- `tokens.go`：`EstimateTokens` 用 `chars/4` 启发式（图片固定 4800 chars）；`EstimateContextTokens` 优先用最近 assistant `Usage` 块，余下估算；`ShouldCompact` 阈值：`tokens > window - reserveTokens`。
- `CompactionResult` 生成 `CompactionMessage`（`role=compaction`）嵌入会话；`RebuildContext` 返回 `summary + retained tail`。

## 5. internal/memory — 记忆库

**职责**：持久化 Markdown 记忆文件，提供 FTS 全文检索与路径解析。

**结构**：
- `store.go`：`Store` 封装 `*sql.DB`（pure-Go `modernc.org/sqlite`，单连接），`Open(dbPath, root, ccBase)` 建库并运行 `migrate`。
- `schema.go`：`memory_index` 表（path/scope/scope_id/type/body/fingerprint）+ `memory_fts` FTS5 虚拟表 + `ai/ad/au` 三个触发器保持 FTS 一致。
- `reconcile.go`：`Reconcile` 双根扫描（mimo root + ccBase），比较 size-mtime fingerprint，prune 缺失文件、index 新/改文件；`parseCcPath` 支持 Claude Code layout。
- `search.go`：`Search` 带 BM25 排序、relative score floor（默认 0.15）、optional scope/scope_id/type 过滤、`ReconcileFirst` 懒索引。
- `paths.go`：`Locator{Scope/ScopeID/Type/Path}` 解析 `<root>/<global|projects|sessions>/<id>/<type>/*.md`；`resolveProjectId` 用 sha256 前 12 位；`assertSafeComponent` 防 `..` 逃逸。

**Agent 使用**：通过 `MemoryTools` 注册为 agent tool；agent 通过 `search`/`read`/`write` 工具交互；`dream` 写入时由 `Reconcile` 重建 FTS。

## 6. internal/plugin — 插件系统

**职责**：启动外部可执行文件，通过 JSON-RPC 握手获取 manifest，暴露为 `AgentTool`/`Command`，并派发生命周期事件。

- `manager.go`：`Manager.Discover(dir)` 按字母序加载 dir 内可执行文件；`isExecutable` 在 Unix 检查 mode bit、Windows 检查 `.exe/.bat/.cmd`；失败插件警告日志跳过。
- `plugin.go`：`Load(path,args,stderr)` 启动子进程，10s 握手 `initialize` 取 `Manifest`；`Tools()` 每个 tool 适配 `pluginTool`（`ExecutionMode=Sequential`，死进程返回 error 不 panic）；`CallCommand` 路由到 `commands/call`；`SendEvent` 2s 超时 fire-and-forget。
- `events.go`：`EventParams{Type/Payload}`，`Manager.Subscribers` 检测订阅；`DispatchEvent` 按订阅隔离派发。

## 7. internal/pkgmgr — 包管理

**职责**：管理从 npm 安装的 pi 包（extension/skill/prompt/theme），维护 lockfile 与目录布局。

- `lockfile.go`：`Lockfile{Version/Packages}` 在 `$PIGO_HOME/packages.json`；`InstalledPackage{Name/Source/Version/Types/Files}`；`Get/Set/Remove/List/Save`；缺失文件 = 空 lockfile，损坏文件 = 硬错误。
- `layout.go`：按 `PackageType` 落到 `$PIGO_HOME/plugins`（extension）、skills 目录、`$PIGO_HOME/commands`（prompt）、`$PIGO_HOME/themes`（theme）。
- `install.go/fetch.go/classify.go/distribute.go`：npm 包获取、分类、分发到对应目录；`uninstall.go/update.go` 基于 lockfile `Files` 精确清理。

## 8. sdk/node/pigo-acp — Node ACP 客户端

**职责**：为 pi-web 提供 TypeScript ACP 协议客户端，通过 stdio JSON-RPC 与 pigo 进程通信。

- `package.json`：`name=pigo-acp@0.1.0`，ESM，Node ≥22.19，依赖 `@types/node` + `typescript`。
- `client.ts`：`PigoAcpClient` 封装 `spawn(pigo --acp)` stdio 管道；`request`/`notify`/`handleLine` 实现 JSON-RPC 2.0；`respondPermission` 处理工具权限请求；事件回调 `onUpdate/onEvent/onPermission/onStderr/onExit`。
- `types.ts`：`AcpMessage/AcpContentBlock/AcpPermissionRequest/InitializeResult/ListSessionsResult/PigoConfigResult/PigoConfigUpdate` 等；`PigoConfigResult.apiKeyConfigured` 安全暴露密钥状态。
- ACP 方法：`initialize/session/new·load·list·close·delete·prompt/cancel/model/set/pigo/models·config·messages/command`。

## 9. scripts — pi-web 启动/停止

`scripts/start-pigo-web.ps1`：PowerShell 脚本启动三进程（sessiond:8599 / web:8504 / vite:8505），写 PID 到 `$TEMP/pi-web-pigo/pids.txt`；`--Stop` 参数 kill 所有子进程；生成 `pi-web-config.json` 指向 `pigo --acp`。

## 10. go.mod — 依赖清单

- `go 1.27rc1`，module `github.com/smallnest/pigo`
- **核心**：`charm.land/bubbles/v2 bubbletea/v2 lipgloss/v2`（TUI）、`github.com/BurntSushi/toml`（配置解析）、`github.com/openai/openai-go`（OpenAI SDK，responses 协议使用）、`modernc.org/sqlite`（纯 Go SQLite，memory store）、`github.com/coder/websocket`（WebSocket 支持）、`github.com/santhosh-tekuri/jsonschema/v6`（JSON Schema 验证）、`gopkg.in/yaml.v3`（YAML frontmatter 解析）、`github.com/kballard/go-shellquote`（shell 参数化）。
- **间接**：glamour（渲染）、ansi/termenv（终端）、chroma（代码高亮）、go-qrcode（QR 码）、pflag（CLI flag）、humanize、uuid、tidwall/gjson/sjson。

## 关键接口/类型一览

| 类型/接口 | 位置 | 职责 |
|---|---|---|
| `Provider` | `internal/provider/provider_interface.go` | 统一模型接口，`StreamCompletion` 返回事件流 |
| `Model` | 同上 | 模型元数据（contextWindow/maxOutputTokens/supportsThinking 等） |
| `StreamFn` | `internal/provider/protocol.go` | 流式完成函数合约（双失败模型） |
| `AssistantMessageEvent` | 同上 | 事件 sealed interface（start/text/thinking/toolcall/done/error） |
| `CredentialStore` | `internal/provider/auth.go` | 三层 API key 解析（OAuth/override/env/config） |
| `ProviderSpec` | `internal/provider/registry.go` | 供应商元数据注册表 |
| `Store` | `internal/sessionstore/store.go` | 项目级会话持久化 |
| `Metadata` | 同上 | 会话 JSON 元数据 |
| `Runner` | `internal/dream/runner.go` | 记忆整合运行器 |
| `Consolidator` | `internal/dream/consolidator.go` | LLM 驱动合并/修剪接口 |
| `Plan` | `internal/dream/plan.go` | 确定性整合计划（dedupe/near-dup/dead-refs） |
| `Store` | `internal/memory/store.go` | SQLite+FTS5 记忆库 |
| `Manager` | `internal/plugin/manager.go` | 插件发现与聚合 |
| `Plugin` | `internal/plugin/plugin.go` | 单插件 JSON-RPC 客户端 |
| `Lockfile` | `internal/pkgmgr/lockfile.go` | 包安装状态锁文件 |
| `PigoAcpClient` | `sdk/node/pigo-acp/src/client.ts` | Node ACP stdio 客户端 |

## 模块间依赖关系

```
cmd/pigo
  ├── agent/ (agent loop)
  │     ├── internal/provider.Provider  ← StreamFn
  │     ├── internal/sessionstore.Store  ← 持久化
  │     ├── internal/compaction.Compact  ← 上下文管理
  │     └── internal/plugin.Manager      ← 工具注入
  │
  ├── internal/dream.Runner
  │     ├── internal/memory.Store.Reconcile
  │     ├── internal/sessionstore.ListAll  ← distill 输入
  │     └── internal/provider.StreamFn    ← consolidate/distill 调用
  │
  ├── internal/memory.Store
  │     └── modernc.org/sqlite
  │
  └── sdk/node/pigo-acp (独立 npm 包)
        └── pigo.exe --acp (stdio JSON-RPC)
```

**数据流**：`agent loop` → `provider.StreamFn` → `transport.StreamRequest` → 各 `Decoder`；`compaction` 在 `ShouldCompact` 触发时调用 `provider` 生成 summary，写入 `sessionstore` 为 `CompactionMessage`；`dream` 在 `Scheduler.Due` 判定后异步子进程运行，从 `sessionstore` 读取转录，经 `consolidator` LLM 决策后写入 `memory`；`plugin` 发现后可执行文件，通过 `jsonrpc.Client` 与子进程双向通信，暴露 `AgentTool`。
