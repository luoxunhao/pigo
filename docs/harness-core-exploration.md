# pigo harness 核心执行层架构探索报告

> 探索对象：`E:\project\pigo`（Go 编写的 coding agent）
> 范围：`internal/harness/cli`、`internal/harness/core/{commands,session,run,tools}`、`internal/harness/utils/{trust,dream}` 以及与之耦合的 `internal/acp` 与 `internal/core/{agentcore,runtime,hooks}`。
> 结论先行：**前端与核心会话交互的唯一通道是 ACP（Agent Client Protocol）**。所有交互式前端（TUI、REPL）都通过进程内 ACP server（`acp.StartInProcessWithHooks`）驱动，而 `acpcmd` 则是同一套装配对外暴露为 stdio server；headless 则直接调用 `runtime.RunHeadless`（也复用同一套 run 装配与 hook seam）。核心执行层实际上由 `internal/acp`（协议/调度）＋`internal/harness/core/session/manager`（会话生命周期）＋`internal/harness/core/run`（运行装配）＋`internal/harness/core/tools`（工具执行）＋`internal/core/runtime`（agent loop）组成。

---

## 1. 分层总览

```
cmd/pigo
  └─ internal/harness/cli            # 前端层：命令解析、TUI/REPL/headless/acpcmd 装配
       ├─ tui/     RunACP()  -> acp.StartInProcessWithHooks()
       ├─ repl/    RunACP()  -> acp.StartInProcessWithHooks()
       ├─ headless/ Run()    -> runtime.RunHeadless() 直接驱动
       ├─ acpcmd/  Run()     -> acp.ServeStdioWithSessionContext()
       └─ host.go  Host / Editor 契约（/goal /btw /status /REPL 共享）
  └─ internal/acp                   # 协议层：Server(transport) + Dispatcher(方法分发) + SessionManager
       ├─ server.go / server_stdio.go / server_inprocess.go
       ├─ dispatch.go               # ACP 方法面：session/new、session/prompt、pigo/command 等
       ├─ command_host.go           # 把 commands.Command 的 Host 适配到 Dispatcher
       └─ events/events.go          # agentcore.AgentEvent -> ACP session/update payload
  └─ internal/harness/core
       ├─ session/                  # JSONL 会话文件格式 + Store（load/save/append/fork）
       ├─ session/manager           # AcpSession + SessionManager + SessionRunner 接口
       ├─ session/store             # 项目作用域 store（metadata + index + transcript）
       ├─ run/                      # run.Env/SetupEnv、RuntimeRunner、hook seam、toolpolicy
       ├─ commands/                 # 斜杠命令实现（ACP 版 Command + Host 契约）
       └─ tools/                    # ToolRegistry + 三阶段执行器 + bash/fs/web/state 工具
  └─ internal/core/runtime          # agent loop（RunHeadless/agentLoop/runLoop/SlashRegistry/Reminder）
  └─ internal/core/agentcore        # AgentTool/AgentEvent/MessageList 等核心类型
  └─ internal/core/hooks            # HookSet/Dispatcher/Runner（外部 hook 命令）
  └─ internal/harness/utils
       ├─ trust/                    # 目录信任决策持久化 + Broker（ACP 权限） + 交互确认
       └─ dream/                    # 记忆整合（BuildPlan/Runner/Consolidator/distill/scheduler）
```

---

## 2. 关键文件清单与职责

### 2.1 前端层 `internal/harness/cli`

| 文件 | 职责 |
|---|---|
| `cli/host.go` | `Host` 接口：/goal、/btw、/status、REPL 访问会话实时状态（Store/AgentCtx/Live/Registry/Slash/Trust/Goal/Telemetry…）的接缝；`Editor` 行输入契约；`ErrLineInterrupted`。 |
| `cli/persist.go` | `PersistTurn(out, h Host)`：把一轮产生的新消息用 `AppendBranch` 追加为会话树分支；处理自动 compact 导致的消息列表收缩（回退为线性 `Save`）。 |
| `cli/tui/run.go` | TUI 入口：`Run` 直接转发 `RunACP`（ACP 是唯一前端入口）。 |
| `cli/tui/acp_session.go` | TUI 的 `RunACP`：装配 `acp.RuntimeRunner`＋`acp.StartInProcessWithHooks`，用 `withACPSession` 绑定 Model：`startRunFn`→`startACPRun`、`interruptFn`→`client.Cancel`，并把内置斜杠命令 `ReplaceBuiltin` 成经 `client.Command` 路由到 pigo/command。 |
| `cli/tui/acp_bridge.go` | `startACPRun`：起 goroutine 调 `client.Prompt`，把 ACP 通知（`session/update`、`pigo/event`）经 `acpToTea`/`updateToTea` 映射成 TUI msg（`textDeltaMsg`/`toolStartMsg`/`toolEndMsg`/`telemetryMsg`）。 |
| `cli/tui/acp_permission.go` | `permissionRequestedMsg`/`respondPermission`：把 `session/request_permission` 请求转进 Bubble Tea 循环，y/a/n/r/esc 映射到 ACP outcome。 |
| `cli/repl/acp_repl.go` | 行式 REPL：`RunACP`（进程内 ACP server）＋`runACPInteractive` 读行循环；`runACPPrompt` 消费 `client.Notifications()` 打印流式文本；`SetPermissionHandler` 处理权限按键。 |
| `cli/headless/headless.go` | 无头驱动：`Run(ctx, RunParams, out, errOut)`，解析插件斜杠命令、`openHeadlessSession`、`run.NewConfig` 装配、`run.InstallDriverHooks` 统一挂 hook、`runtime.RunHeadless` 执行。 |
| `cli/headless/session.go` | `headlessSession`（store/header/curLeaf/persisted）、`openHeadlessSession`（resume 或新建，含 legacy 迁移）、`persist`（AppendBranch 尾部持久化）、`EnsureProjectSession`/`migrateLegacySession`/`AllSessionHeaders`/`PrintSessions`/`MostRecentSessionID`。 |
| `cli/acpcmd/acpcmd.go` | 独立 stdio ACP server：`Run(ctx, opts, stdin, stdout, stderr)`，`setupEnv`→`run.SetupEnv`，装配 `acp.RuntimeRunner`＋`sessionContextBuilder`＋`acp.ServeStdioWithSessionContext`。 |
| `cli/acpcmd/sessionctx.go` | `sessionContextBuilder` 实现 `acp.SessionContextFactory`：按 cwd 重建 system prompt/工具根/斜杠 registry（registry 按 `cwd+trust.Fingerprint` 缓存，信任变化即失效）。 |
| `cli/doc.go` | 说明 cli 子包布局与 Host 契约动机。 |
| `cli/liveconfig.go` | `LiveConfig`：运行时可变模型/thinking/上下文窗口等（`/model` 等命令改它）。 |

### 2.2 协议层 `internal/acp`

| 文件 | 职责 |
|---|---|
| `acp/server.go` | `Server`（transport+handler），`Serve` 分发循环；`Handler`/`DeferredHandler` 接口；initialize 能力声明。 |
| `acp/server_stdio.go` | `ServeStdio`/`ServeStdioWithRegistry`/`ServeStdioWithSessionContext`：newline-delimited JSON-RPC over stdio。 |
| `acp/server_inprocess.go` | `StartInProcess(WithHooks)`：`NewChannelPair` 通道传输＋`newDispatcherWithHooks`，返回 `*Client`＋stop 函数。 |
| `acp/dispatch.go` | `Dispatcher`：ACP 方法面实现（session/new、session/load、session/prompt、session/cancel、session/delete、model/set、pigo/* 扩展方法），`runPrompt`、`resolveSlash`、`eventSink`（把 agentcore 事件映射为 session/update 通知）、`turnHooks`（绑定 hookSeam）、`beforeToolCall`（ACP 权限 broker）。 |
| `acp/command_host.go` | `commandHost`：把 `commands.Host` 契约实现到 Dispatcher 上（RunSideQuestion/CompactSession/RebuildSession/ConsolidateDream/MemoryStatus/RemoteControl…）。 |
| `acp/events/events.go` | `Mapper`：`agentcore.AgentEvent → []session/update payload`；文本增量 delta 计算（`deltaTracker`）、bash 终端卡片、文件工具 diff 卡片、usage_update。 |
| `acp/runner_reexport.go` / `permission_reexport.go` | `RuntimeRunner = run.RuntimeRunner`、`ACPPermissionBroker = trust.Broker` 别名导出。 |
| `acp/payload/messages.go` 等 | ACP 消息/Entry 互转（`EntryToACPMessage` 等）。 |

### 2.3 会话核心 `internal/harness/core/session` 与 `session/manager`、`session/store`

| 文件 | 职责 |
|---|---|
| `session/session.go` | JSONL 会话格式：`SessionHeader`（schema v3，id/parentId 树）、`Entry`、`Store`（Save/SaveEntries/Append/AppendBranch/Fork/Load/LoadEntries/List/Export 相关）、`PathToLeaf`/`RenderTreeLines`（树视图）。 |
| `session/export.go` | `WriteJSONL`/`ReadJSONL`/`Store.Export`/`Store.Import`/`WriteHTML`（自包含 HTML 转录）。 |
| `session/inherit.go` | 上下文继承（`ContextFrom` checkpoint 恢复）。 |
| `session/sessioncontext.go` | `SessionContext`（SysPrompt/Tools/Registry/AdditionalDirectories）、`SessionContextFactory`、`EventMapper`、`CloneToolsForSession`（按 cwd 克隆工具根）。 |
| `session/manager/session.go` | `AcpSession`（单会话状态＋pending 队列）、`SessionManager`（会话注册表＋项目 store 缓存）、`SessionRunner`/`TooledRunner` 接口、`TurnHooks`。 |
| `session/store/store.go` | 项目作用域 store：`$PIGO_HOME/projects/<slug>/sessions/`，`Metadata`＋`IndexFile`＋JSONL transcript；`Create`/`Load`/`Append`/`AppendBranch`/`List`/`ListAll`/`Delete`/`WorkspaceSlug`。 |

### 2.4 运行装配 `internal/harness/core/run`

| 文件 | 职责 |
|---|---|
| `run/run.go` | `Env`/`SetupEnv`（provider 解析、工具构建、policy 过滤、技能加载、插件发现、system prompt）、`BuiltinTools`、`BuiltinToolsExcept`、`SessionTaskTool`、`NewConfig`、`ResolveThinkingLevel`/`ResolveHookSet`/`Trusted`、目录常量。 |
| `run/runner.go` | `RuntimeRunner`（`Run`/`RunWithTools`/`ResolveForModel`）：生产级 `SessionRunner`，经 `runtime.RunHeadless` 驱动真实 loop，`ConfiguredModelStore` 解析自定义模型。 |
| `run/hooks_seam.go` | `SessionHookSeam()`：返回 per-session hook 安装器（resolve→BuildDispatcher→InstallSeamsBefore）。 |
| `run/hooks_seam_func.go` | `HookSeamFunc` 类型：`func(cfg *runtime.RunConfig, sessionID, projectDir string) error`。 |
| `run/hooks_install.go` | `HookDeps`、`InstallHooks`、`BuildDispatcher`、`InstallSeams`/`InstallSeamsBefore`、`preToolCallHook`/`postToolCallHook`（hook 事件 → Before/AfterToolCall seam）、`chainBeforeToolCall`/`chainAfterToolCall`/`chainShouldStop`、`installSubagentConfigHooks`（子代理不能绕过 PreToolUse）。 |
| `run/hooks_driver.go` | `InstallDriverHooks`：所有 driver 统一的 hook 装配收敛点（SessionStart 内联＋SessionEnd/PreCompact 观察者链）。 |
| `run/toolpolicy.go` | `ToolPolicy`：`--allowed-tools/--disallowed-tools` 边界（注册层过滤，结构性生效）。 |
| `run/reminders.go` | `TodoReminders(tools)`：把 todo 状态包装成 per-turn system-reminder。 |

### 2.5 斜杠命令 `internal/harness/core/commands`

| 文件 | 职责 |
|---|---|
| `commands/commands.go` | `Command` 类型、`Host` 契约、`Build()` 返回 21 个命令的 map（model/think/steering/follow-up/trust/status/compact/session/help/name/changelog/copy/export/import/rebuild/memory/goal/btw/rewind/fork/tree/dream/remote-control）、`SessionStatusText`、`truncateTurns` 等。 |

斜杠 registry 本体在 `internal/core/runtime/slashcommand.go`（`SlashRegistry`/`SlashCommand`/`ResolveOutcome`，内置＞项目＞全局＞包＞设置＞CLI 的 Tier 冲突规则）。

### 2.6 工具 `internal/harness/core/tools`

| 文件 | 职责 |
|---|---|
| `tools/registry.go` | `ToolRegistry`：按名注册、JSON Schema 编译校验、`ValidationErrorResult`（字段级错误工具结果）。 |
| `tools/tool_executor.go` | 三阶段执行：`executeToolCall`（emit Pending→prepare→emit Start→runTool→finalize→emit End）、`prepareToolCall`（lookup→prepareArguments→Validate→BeforeToolCall）、`runToolWithRetry`、`finalizeToolCall`（AfterToolCall 覆写＋结果字节预算裁剪）。 |
| `tools/batch_executor.go` | `BatchConfig`/`ExecuteToolCalls`：顺序/并行（`batchRequiresSequential`），index-backfill 保序。 |
| `tools/tool_retry.go` | `isRetryableToolError`（超时/网络瞬时错误重试，取消/panic 终结）、`maxToolRetries=2`。 |
| `tools/reexport.go` | 统一导出 fs/bash/web/state 类型别名。 |
| `tools/fs/*` | `ReadTool`（行限 2000）、`WriteTool`/`EditTool`（`FileSnapshotRecorder` 快照、root 边界 `resolveWithin`/`resolveWithinAny`）、`GrepTool`/`FindTool`/`LsTool`（.gitignore 感知）、`file_snapshot.go`（/rewind 恢复点）。 |
| `tools/bash/*` | `BashTool`（顺序执行、流式 partial、超时/取消、`run_in_background`→`BashJobStore`）、`bash_output`/`kill_bash`、Windows shell 探测（Git Bash/WSL/PowerShell/cmd）。 |
| `tools/web/*` | `WebFetchTool`、`WebSearchTool`（Tavily/Brave/DDG 后端自选）、`htmlmarkdown`。 |
| `tools/state/*` | `TodoTool`（TodoStore）、`GoalTool`（GoalState）、`MemorySearchTool`（BM25 memory store）。 |
| `tools/internal/toolutil/toolutil.go` | `DecodeArgs`/`ErrorResult`/`TruncateToBudget` 共享工具。 |

### 2.7 信任与记忆

| 文件 | 职责 |
|---|---|
| `utils/trust/manager.go` | `Manager`：`trust.json`（path→*bool）持久化、`IsTrusted`（session 优先于持久化）、`SetDecision`/`SetSessionTrust`/`ClearSessionTrust`/`NearestTrustDecision`/`Fingerprint`。 |
| `utils/trust/permission_broker.go` | `Broker`：ACP `session/request_permission` 通道，把 allow_once/allow_always/reject_once/reject_always 映射回 trust.Manager。 |
| `utils/trust/interactive.go` | REPL 侧：`EnsureTrustPrompt`/`ConfirmToolCall`/`BeforeToolCall`（终端确认）＋`SideEffectTools`（bash/write/edit）＋`RegisterCommand`（/trust）。 |
| `utils/dream/*` | 记忆整合：`plan.go`（BuildPlan）、`runner.go`（Runner：锁→BuildPlan→Consolidate→apply→Reconcile→SaveState）、`consolidator.go`（llmConsolidator）、`distill.go`（parseDistillResponse）、`scheduler.go`（后台调度）、`apply.go`（model completer）、`state.go`/`lock.go`/`report.go`/`config.go`。 |

### 2.8 底层 `internal/core`

| 文件 | 职责 |
|---|---|
| `core/agentcore/tool.go` | `AgentTool` 接口、`AgentToolCall`/`AgentToolResult`、`ToolExecutionMode`、`InferToolKind`。 |
| `core/agentcore/helpers.go` | `BeforeToolCallFunc`/`AfterToolCallFunc`/`PrepareArgumentsFunc`/`EmitFunc`/`BeforeToolCallDecision`。 |
| `core/agentcore/event.go` | `AgentEvent` 密封接口与全部事件类型（agent_start…tool_execution_pending/start/update/end…）。 |
| `core/runtime/loop.go` | `RunConfig`/`agentLoop`/`runLoop`：双层循环（内层 turn 到无工具，外层 steering/follow-up），`cfg.Batch.ExecuteToolCalls` 是工具执行入口。 |
| `core/runtime/headless.go` | `HeadlessConfig`/`RunHeadless`：PrintMode/StreamJSONMode，`DrainStream`。 |
| `core/runtime/slashcommand.go` | `SlashRegistry`/`SlashCommand`/`ResolveOutcome`（见 2.5）。 |
| `core/hooks/dispatch.go` + `protocol.go` + `runner` | `HookSet`（`map[string][]HookMatcherConfig`）、`Dispatcher.Dispatch(ctx,eventType,toolName,input)`、`HookInput`/`HookOutput`/`HookDecision`（exit 0=allow，2=block，stdout JSON 可带 updatedInput/additionalContext）。 |

---

## 3. 前端与核心会话如何交互：统一通道是 ACP

1. **TUI**（`cli/tui/acp_session.go` `RunACP`）：
   - 构建 `acp.RuntimeRunner`（内含 `run.Env` 解析出的 provider/tools）→ `acp.StartInProcessWithHooks(runner, home, model, sysPrompt, cwd, mgr, dreamCfg, run.SessionHookSeam())`。
   - 该函数 `NewChannelPair()` 建内存通道，`newDispatcherWithHooks` 装配 `Dispatcher`（内含 `NewSessionManager(runner)` 与 `ACPPermissionBroker`），`NewServer(serverT, disp).Serve` 在 goroutine 跑分发循环，返回 `*acp.Client`。
   - Bubble Tea `Model.startRunFn` 调 `startACPRun(client, sessionID, prompt, ch)`：goroutine 里 `client.Prompt`，同时把 `client.Notifications()` 经 `acpToTea` 转成 tea.Msg；`interruptFn` 调 `client.Cancel`。
   - 斜杠命令：`installACPSlashCommands` 用 `ReplaceBuiltin` 把 model/think/trust/status/rewind/fork/tree/compact/session/help/copy/export/import/rebuild/memory/goal/btw/dream/remote-control 全部改为 `client.Command(ctx, sessionID, "/name args")`。
2. **REPL**（`cli/repl/acp_repl.go`）：同样的 `acp.StartInProcessWithHooks`，`runACPInteractive` 读行循环：`/` 开头走 `client.Command`，否则 `runACPPrompt`（`client.Prompt`＋`Notifications()` 流式打印）；`SetPermissionHandler` 把 y/a/n/r 映射成 allow_once/allow_always/reject_once/reject_always。
3. **acpcmd**（`cli/acpcmd/acpcmd.go`）：`acp.ServeStdioWithSessionContext`，用 `sessionContextBuilder`（`SessionContextFactory`）按每个会话 cwd 重建 prompt/工具/registry —— 这是共享多工作区进程模式。
4. **headless**（`cli/headless/headless.go`）：不走 ACP，直接 `run.NewConfig(...)` + `run.InstallDriverHooks(...)` + `runtime.RunHeadless`；但复用同一套 `run.Env`/hook seam/trust/持久化，且同样落在项目作用域 store 上。

> 统一通道的结论：**ACP 是交互式前端与核心会话的唯一统一通道**；`Dispatcher` 是协议方法分发中枢，`SessionManager` 是其背后的会话所有者；headless 是绕过 ACP 但复用运行装配的例外。

---

## 4. SessionManager / AcpSession：职责与生命周期

### AcpSession（`session/manager/session.go`）
字段：`ID`、`Cwd`、`Store *sstore.Store`（项目 store）、`Header session.SessionHeader`、`Mapper session.EventMapper`、`Tools []agentcore.AgentTool`、`Registry *runtime.SlashRegistry`、`AdditionalDirectories`、`Messages agentcore.MessageList`、`Persisted int`（已在磁盘的消息数）、`CurLeaf string`（磁盘树当前叶子 id）、`Model`/`Thinking`/`Goal`、`SteeringMode`/`FollowUpMode`（one-at-a-time/all）。

运行槽与 pending 队列：
- `SetCancel(cancel)`/`Cancel()`：单会话取消句柄。
- `TryRun(p *QueuedPrompt) bool`：抢占唯一 turn 槽，失败则入队。
- `WaitForTurn(p)`：等待被 steering/follow-up 派发或成为队首。
- `PopSteering(all)`/`PopFollowUp(all)`：从队列弹出待派发 prompt（`popQueue`）。
- `FinishTurn(stopReason, runErr)`：释放槽位，关闭所有已派发 prompt 的 `Done`，唤醒下一个 runner。
- `PersistRewind()`：rewind/compact 后重写转录与 metadata。

### SessionManager
- 持有 `sessions map[string]*AcpSession`、`stores map[string]*sstore.Store`（按 workspace slug 缓存）、`runner SessionRunner`、`MapperFactory func(cwd) session.EventMapper`。
- `StoreForWorkspace(pigoHome, workspacePath)`：一次进程内打开一个项目 store。
- `New(cwd, model, ctx, st)`：新建持久化会话（`session.NewID` 时间序 id，`st.Create(meta, header, nil)`）。
- `Load(cwd, sessionID, model, ctx, st)`：从磁盘恢复（重建 system prompt、`CurLeaf=最后 entry id`、`Persisted=len(msgs)`），并取消旧实例。
- `Run(ctx, sess, prompt, images, beforeToolCall, onEvent, hooks)`：核心 turn 执行——
  1. defer 计算 stopReason（end_turn/cancelled/max_tokens）并 `sess.FinishTurn`；
  2. `context.WithCancel` + `sess.SetCancel`，起 `watchHangDump`（PIGO_HOME/turn-hang-<session>.dump）；
  3. 默认 steering/follow-up hooks 接 `PopSteering/PopFollowUp`；
  4. 若 runner 实现 `TooledRunner` 则 `RunWithTools`（带会话级工具），否则 `Run`；
  5. `runCtx.Err()` 非空 → `ErrTurnCancelled`；
  6. 持久化尾部：`messages[sess.Persisted:]` → `sess.Store.AppendBranch(sess.ID, sess.Header, sess.CurLeaf, tail)`，推进 `CurLeaf`/`Persisted`。

### SessionRunner 接口（runner 契约）
```go
type SessionRunner interface {
    Run(ctx context.Context, prompt string, images []agentcore.Content,
        history agentcore.MessageList, sysPrompt, model, thinking string,
        beforeToolCall agentcore.BeforeToolCallFunc,
        onEvent func(agentcore.AgentEvent),
        hooks TurnHooks) (agentcore.MessageList, *agentcore.AssistantMessage, error)
}
type TooledRunner interface {
    RunWithTools(ctx context.Context, prompt string, images []agentcore.Content,
        history agentcore.MessageList, sysPrompt string, tools []agentcore.AgentTool,
        model, thinking string, beforeToolCall agentcore.BeforeToolCallFunc,
        onEvent func(agentcore.AgentEvent), hooks TurnHooks) (agentcore.MessageList, *agentcore.AssistantMessage, error)
}
type TurnHooks struct {
    Steering     func(ctx context.Context) []agentcore.AgentMessage
    FollowUp     func(ctx context.Context, agentCtx *agentcore.AgentContext) []agentcore.AgentMessage
    InstallSeams func(cfg *runtime.RunConfig) error
}
```
生产实现是 `run.RuntimeRunner`（`acp.RuntimeRunner` 别名）。

---

## 5. 斜杠命令 registry：注册与分发

### 注册（三类来源）
- **编译期内置**：`runtime.RegisterBuiltin(SlashCommand)`（init() 全局 `builtinCommands`）。
- **实例内置于运行期**：`SlashRegistry.AddBuiltin/ReplaceBuiltin`（闭包捕获运行状态，如 trust、model 控制器）。ACP 迁移用 `ReplaceBuiltin` 覆盖内置命令指向 pigo/command。
- **声明式模板**：`AddUser/AddSkill/AddPlugin/AddProject/AddPackage/AddSettings/AddCLI`（markdown 模板，`ParseUserCommand` 解析 YAML frontmatter，`ExpandTemplate` 做 `$ARGUMENTS`/`$1` 展开）。

### 冲突规则
`SlashRegistry.add`：Tier（CLI < Settings < Package < Global < Project < Builtin）更高者胜，输者进 `shadowed`；同 tier 后写覆盖。

### 分发
- 会话内：`ResolveOutcome(input) (SlashOutcome, error)`：非 `/` → 原文返回；`Action` → 执行副作用返回 `SlashAction`；`Run` → 混合（message+prompt）；`Expand` → 纯 prompt 展开。
- ACP 路径：`Dispatcher.runPrompt` 先 `resolveSlash(sess, text)`（内部用 `sess.Registry.ResolveOutcome`），处理完直接回 `stopReason=end_turn`；未命中则把展开的 prompt 当普通 turn 执行。
- 独立 ACP 命令面：`Dispatcher.pigoCommand` → `commands.Build()` 的 `map[string]Command`，经 `commandHost`（`SendSessionUpdate`/`RunSideQuestion`/`CompactSession`/`ConsolidateDream`…）执行；`AvailableCommands` 合并 registry 与 commands map 供 `/help`。

---

## 6. RuntimeRunner 与 hook seam / 信任决策流转

### RuntimeRunner.RunWithTools（`run/runner.go`）
1. `ResolveForModel(model)`：自定义 `provider/model_id` 从 `ConfiguredModelStore` 解析，否则用启动 provider。
2. 把 user prompt 追加到 history 副本，构造 `AgentContext{SystemPrompt, Messages, Tools}`。
3. 建 `agenttools.ToolRegistry`，构造 `runtime.RunConfig{ LoopConfig{Model,Provider,APIKey,ThinkingLevel,Stream,ContextWindow,Compaction}, Batch: BatchConfig{ToolExecutorConfig{Registry, BeforeToolCall}} }`。
4. `hooks.InstallSeams(&cfg)`（若 TurnHooks.InstallSeams 非 nil）；`cfg.GetSteeringMessages/GetFollowUpMessages` 接 TurnHooks。
5. `runtime.RunHeadless(ctx, agentCtx, HeadlessConfig{Mode:PrintMode, Out:io.Discard, Run:cfg, OnEvent, Progress:io.Discard})`。
6. 成功且 `Snap != nil` → `snap.Commit("", "acp turn")`；返回 `agentCtx.Messages` 与 `LastAssistantOf`。

### Hook seam 装配（`run/hooks_install.go`）
- `HookDeps{SessionID, ProjectDir, WarnLog}` 注入每个 `HookInput`。
- `InstallSeams`/`InstallSeamsBefore`：把 `preToolCallHook`/`postToolCallHook` 链入 `BatchConfig.BeforeToolCall/AfterToolCall`，并 `installStopHook`（Stop seam）与 `installSubagentConfigHooks`（子代理工具继承 hook）。
- `preToolCallHook`：`d.Dispatch(ctx, hooks.EventPreToolUse, call.Name, HookInput{...})` → `HookDecision{Block, Reason, UpdatedInput}` → `BeforeToolCallDecision`。
- 顺序语义：**ACP 用 InstallSeamsBefore（用户 hook 在权限 broker 之前，policy 拦截不再弹窗）**；本地 REPL/TUI 用 InstallSeams（trust 先于 hook，trust 拦截短路）。`chainBeforeToolCall`：prev 先跑、block 短路。
- `SessionHookSeam()`（`run/hooks_seam.go`）：`ResolveHookSet(projectDir, Trusted(projectDir))`（不信任则不加载项目层 hook，fail-closed）→ `BuildDispatcher` → `InstallSeamsBefore`。

### 信任/权限决策流转（allow_always/allow_once/reject_always）
1. 每个 turn 由 `Dispatcher.beforeToolCall(sess)` 返回 `d.broker.BeforeToolCall(sess.ID, sess.Cwd)`（`trust.Broker`）。
2. `Broker.BeforeToolCall`：非 `SideEffectTools`（bash/write/edit）直接放行；`trust.IsTrusted(cwd)` 放行；否则先试 `remoteConfirm`（远程浏览器），再 `request(ctx, sessionID, call)` 发 `session/request_permission`（options：allow_once/allow_always/reject_once/reject_always）。
3. 决策映射：
   - `allow_once` → 放行（不持久化）；
   - `allow_always` → `trust.SetDecision(cwd, Trusted)` 持久化 + `trustChanged`（失效会话 registry）+ 放行；
   - `reject_always` → `SetDecision(cwd, Untrusted)` + 返回 `blockDecision("...untrusted")`；
   - reject_once/失败 → 仅本次 block。
4. 本地 REPL 侧 `trust.BeforeToolCall`：`ConfirmToolCall`（y/n/a），always→`SetSessionTrust`（仅本次进程），no→block。
5. block 决策最终在 `tools.prepareToolCall` 中变成 `AgentToolResult{IsError:true, Content:"tool ... blocked"}`，作为 `ToolResultMessage` 回喂模型 —— 模型会读到被拒原因。

---

## 7. 工具集、各自职责、工具事件

### 内置工具（`run.BuiltinTools(cwd, disabled)`）
`read`、`write`、`edit`、`grep`、`find`、`ls`（注：ls 在 reexport 中可见，BuiltinTools 注释未列但 fs 有）、`bash`、`bash_output`、`bash_kill`、`todo`、`webfetch`、`websearch`；`SetupEnv` 追加 `memory_search`、`task`（子代理）与插件工具。`ChildToolSet` 用 `BuiltinToolsExcept(cwd, policy, "task")` 做嵌套守卫（子代理不能再生子代理）。

- `read`：行数/行长裁剪（2000 行/2000 字节），root 边界 `resolveWithin`，`ExecutionMode=parallel`。
- `write`/`edit`：`Root+ExtraRoots`（skills 目录），`Snap.Record` 快照（/rewind），`ExecutionMode=sequential`。
- `grep`/`find`/`ls`：正则/glob/目录，`.gitignore` 感知，结果上限 1000。
- `bash`：`command`/`timeout_ms`/`run_in_background`，默认超时 2 分钟、上限 10 分钟、输出 30KB 裁剪，stdout/stderr 合并流式 `onUpdate`，非零退出码→error；`bash_output`/`bash_kill` 操作 `BashJobStore`。
- `todo`：整单替换式任务清单（TodoStore，per-session 共享）。
- `goal`：GoalState 状态机（idle/active/paused/blocked/complete/budget_limited）。
- `memory_search`：BM25 全文检索持久记忆（`ReconcileFirst=true` 惰性重建索引）。
- `task`：`subagent.SubAgentTool`，通过 `SessionTaskTool` 注入子 RunConfig（复用 provider、semaphore 并发上限）。
- `webfetch`/`websearch`：抓取/搜索（Tavily/Brave/DDG 自选）。

### 工具事件（pending → in_progress → completed/failed）
`tools/tool_executor.go` 的 `executeToolCall` 用 `emit agentcore.EmitFunc` 依次发出：
1. `ToolExecutionPendingEvent{ToolCallID, ToolName, Args}` —— 工具调用首次被观察到（hooks/权限/执行之前）。
2. `prepareToolCall`：registry 查找 → `PrepareArguments` → JSON Schema `Validate` → `BeforeToolCall`（trust/hook/权限，可 block 或重写 args）。
3. `ToolExecutionStartEvent`（`status=in_progress`）→ `runToolWithRetry` → `runTool`（`tool.Execute(ctx, id, args, onUpdate)`；onUpdate 发出 `ToolExecutionUpdateEvent` 流式 partial）。
4. `finalizeToolCall`：`AfterToolCall`（PostToolUse 追加 feedback）→ 结果字节预算裁剪（`clipToolResultContent`，默认 100KB）→ 发出 `ToolExecutionEndEvent{ToolCallID, ToolName, Result, IsError}`（isError→failed，否则 completed）→ 返回 `ToolResultMessage{..., Terminate}`。

批量：`ExecuteToolCalls` 顺序/并行（`batchRequiresSequential`），终止语义“所有结果 Terminate=true 才终止”。

ACP 侧映射（`events/events.go`）：Pending→`tool_call status=pending`；Start→`tool_call status=in_progress`（bash 卡片带 `terminal_info`，文件工具带 diff 快照）；Update→`tool_call_update in_progress`；End→`tool_call_update completed/failed`（bash 带 `terminal_output`/`terminal_exit`，文件变更发 diff 卡片）。`deltaTracker` 把 provider 的累计 partial 转成增量文本。

---

## 8. 会话与转录持久化

### 两层 store
1. `session.Store`（`session/session.go`）——**JSONL 转录**：首行 `SessionHeader`（Version=3、ID、CreatedAt/UpdatedAt、Model/Provider/SystemPrompt、ParentSession、Cwd、ContextFrom、ContextWatermark），之后每行一个 `Entry{ID, ParentID, Timestamp, Message}`（v1/v2 裸消息行读取时自动迁移成树）。方法：
   - `Save(header, msgs)` / `SaveEntries(header, entries)`（`atomicWrite`：tmp+rename）。
   - `Append(id, updatedAt, msgs)`（load-modify-save 线性追加，会压平树）。
   - `AppendBranch(header, parentLeafID, msgs)`（树感知：新链挂在 parentLeafID 下，返回新叶子 id）——REPL/TUI/manager/headless 统一用它。
   - `Fork(sourceID, leafID, now)`（/fork、/clone 底层：复制 root→leaf 路径到新会话，记录 ParentSession）。
   - `Load`/`LoadEntries`/`List`/`loadHeader`。
   - `Export`/`Import`/`WriteJSONL`/`ReadJSONL`/`WriteHTML`（`session/export.go`）。
2. `store.Store`（`session/store/store.go`）——**项目作用域 store**：`$PIGO_HOME/projects/<workspace-slug>/sessions/`，每会话一个 `*.metadata.json`＋转录 `.jsonl`＋项目级 `index.json`。`Metadata`（sessionId/sessionName/modelName/createdAt/lastActiveAt/turnCount/messageCount/toolCallCount/status/workspacePath…）与 `IndexFile` 采用 camelCase（对齐 ash，未来桌面客户端可直读）。方法：`Create`/`Load`/`Append`/`AppendBranch`/`SaveMetadata`/`UpdateHeader`/`List`/`ListAll`/`Delete`/`ImportEntries`/`WorkspaceSlug`（`ListAll` 是全项目列表，供 --list-sessions 与 /dream 蒸馏）。

### 写入路径（增量、分支、游标）
- `AcpSession.Persisted`＝已在磁盘的消息数，`CurLeaf`＝磁盘树当前叶子。每轮 `SessionManager.Run` 只把 `messages[Persisted:]` 经 `AppendBranch` 挂到 `CurLeaf` 下，避免重写整个文件、保留分支树。
- 自动 compaction 把消息列表缩到 `Persisted` 之下时：manager/headless 钳制游标，`cli.PersistTurn` 回退为线性 `Store.Save` 并重置叶子（`SetCurLeaf("")`→最后 entry id）。
- headless 路径同样：`headlessSession.persist` 用 `AppendBranch`。

### 信任持久化
`$PIGO_HOME/trust.json`：`{path: bool|null}`，`Manager.saveLocked` 用 `os.CreateTemp`+`Rename` 原子写（mode 0600）；session grant（`SetSessionTrust`）只存进程内。

### 记忆整合（dream）
`utils/dream/runner.go` `Runner.Run`：`AcquireLock`（被锁→跳过）→`BuildPlan`（dedupe 组/无效路径引用/近重复对）→`Consolidate`（`llmConsolidator`：merge/prune 决策 + `distill` 从最近会话 JSONL 提取新记忆）→（非 dry-run）`applyDedupe`/`applyPathClean`/`applyConsolidation`（全部经 `withinScope` 路径守卫）→`updateScopeIndexes`→`memory.Store.Reconcile`→`SaveState`。`/dream` 命令由 `commands.cmdDream`→`commandHost.ConsolidateDream`→`dream.Runner` 触发；`scheduler.go` 提供后台自动调度。

---

## 9. 一次完整会话运行主流程（请求 → 工具调用 → 响应）

以 ACP（TUI/REPL/acpcmd 同构）为例：

1. **启动装配**：`run.SetupEnv` 解析 provider/APIKey/模型，`BuiltinTools`＋memory/task/插件工具，`ApplyToolPolicy` 过滤，`BuildSystemPrompt`；`cli` 层把 `Env` 装入 `acp.RuntimeRunner`，`acp.StartInProcessWithHooks`（或 `ServeStdioWithSessionContext`）装配 `Dispatcher`＋`SessionManager`＋`ACPPermissionBroker`＋`SessionHookSeam`。
2. **会话建立**：client `Initialize` → `NewSession`/`LoadSession` → `Dispatcher.sessionNew/sessionLoad`：`sessionCtx(cwd, additionalDirs)` 生成 `SessionContext{SysPrompt, Tools(CloneToolsForSession), Registry}`，`manager.StoreForWorkspace` 打开项目 store，`manager.New/Load` 创建/恢复 `AcpSession`。
3. **prompt 进入**：client `Prompt(ctx, sessionID, text)` → `Dispatcher.HandleDeferredRequest`（session/prompt 异步）→ `runPrompt`：`parsePromptParams`（text/image/resource）→ 若 `/` 开头 `resolveSlash`（registry 展开或 commands map 执行）；`applyGoal` 前置 goal → `QueuedPrompt{Text, Images, Done}` → `sess.TryRun`（排队则 `WaitForTurn`）。
4. **turn 执行**：`manager.Run`：`TooledRunner.RunWithTools` → `RuntimeRunner`：构造 `AgentContext`，`runtime.RunHeadless` → `agentLoop` → `runLoop`：
   - 内层循环 `streamAssistantResponse`（`LoopConfig.Stream` 流式，`message_start/update/end` 事件，`TelemetryEvent` 累计）；
   - assistant 带工具调用 → `cfg.Batch.ExecuteToolCalls`（`BatchConfig.ExecuteToolCalls`）：
     - `emit(ToolExecutionPendingEvent)` → `executeToolCall` → `prepareToolCall`（registry→schema 校验→`BeforeToolCall`：`trust.Broker`/本地 trust/`PreToolUse` hook 链，可能发 `session/request_permission`）→ `emit(ToolExecutionStartEvent)` → `tool.Execute`（bash 流式 partial→`ToolExecutionUpdateEvent`）→ `finalizeToolCall`（`PostToolUse` hook、字节裁剪、`emit(ToolExecutionEndEvent)`）→ `ToolResultMessage` 追加进 `agentCtx.Messages`；
   - 无工具调用 → `TurnEndEvent` → `afterTurn`（steering→`GetSteeringMessages`/`ShouldStopAfterTurn`/`PrepareNextTurn`）→ 外层循环 `GetFollowUpMessages` → `AgentEndEvent`。
5. **事件回流**：`Dispatcher.eventSink` 把每个事件经 `Mapper.Map` 转成 `session/update` 通知（文本增量/thought/tool_call 卡片/usage_update）发给 client，TUI 经 `acpToTea` 渲染，REPL 打印，并同时发 `pigo/event`（compaction/subagent_progress）。
6. **持久化**：`manager.Run` 成功后 `sess.Store.AppendBranch` 追加尾部（转录+metadata 计数），`snap.Commit` 提交 rewind 恢复点；`runPrompt` 后台 `generateTitle`。
7. **收尾**：`manager.Run` defer 计算 stopReason（end_turn/cancelled/max_tokens）→ `sess.FinishTurn`（释放槽、唤醒队内下一个）→ `Dispatcher` 回 ACP 响应 `{stopReason}`；TUI/REPL 收到 `runEndMsg`/done 结束本轮。

---

## 10. 关键接口签名汇总

```go
// --- agentcore ---
type AgentTool interface {
    Name() string
    Description() string
    Schema() json.RawMessage
    ExecutionMode() ToolExecutionMode          // parallel | sequential
    Execute(ctx context.Context, id string, args json.RawMessage, onUpdate ToolUpdateFunc) (AgentToolResult, error)
}
type BeforeToolCallFunc func(ctx context.Context, call AgentToolCall) *BeforeToolCallDecision
type AfterToolCallFunc  func(ctx context.Context, call AgentToolCall, result AgentToolResult, isError bool) *AfterToolCallResult
type EmitFunc           func(ctx context.Context, ev AgentEvent) error
type BeforeToolCallDecision struct { Block bool; Content *ContentList; Details *any; UpdatedInput json.RawMessage }

// --- runtime ---
type RunConfig struct {
    LoopConfig
    Batch tool.BatchExecutor
    GetFollowUpMessages func(ctx, *AgentContext) []AgentMessage
    GetSteeringMessages func(ctx) []AgentMessage
    PrepareNextTurn     func(ctx, *AgentContext) *TurnUpdate
    ShouldStopAfterTurn func(ctx, *AgentContext) bool
    OnStop              func(ctx, *AgentContext) *StopDecision
    Reminders *ReminderRegistry
    EventBuffer int
    SessionID  string
    MemoryRoot string
}
func RunHeadless(ctx context.Context, agentCtx *agentcore.AgentContext, cfg HeadlessConfig) error

// --- session/manager ---
type SessionRunner interface { Run(...) (MessageList, *AssistantMessage, error) }   // 见 §4
type TooledRunner  interface { RunWithTools(...) (MessageList, *AssistantMessage, error) }
type SessionManager struct { ... }  // New/Load/Get/All/Close/DeleteEverywhere/StoreForWorkspace/Run
func (m *SessionManager) Run(ctx, sess *AcpSession, prompt string, images []agentcore.Content,
    beforeToolCall agentcore.BeforeToolCallFunc, onEvent func(agentcore.AgentEvent), hooks TurnHooks) (*agentcore.AssistantMessage, error)

// --- run ---
func SetupEnv(model, baseURL, protocol, providerName, apiKey string, noTools, noSkills bool,
    systemPrompt string, appendSystemPrompt []string, memEnabled bool, policy ToolPolicy) (Env, error)
func NewConfig(model, providerName string, thinking agentcore.ThinkingLevel, prov provider.Provider,
    creds *provider.CredentialStore, reg *agenttools.ToolRegistry, reminders *runtime.ReminderRegistry) runtime.RunConfig
type RuntimeRunner struct { Provider; ProviderName; Model; APIKey; ThinkingLevel; Tools; Compaction; ContextWindow; Snap; ConfiguredModels }
func (r *RuntimeRunner) RunWithTools(ctx, prompt, images, history, sysPrompt string, tools []agentcore.AgentTool,
    model, thinking string, beforeToolCall, onEvent, hooks manager.TurnHooks) (agentcore.MessageList, *agentcore.AssistantMessage, error)
type HookSeamFunc func(cfg *runtime.RunConfig, sessionID, projectDir string) error
func SessionHookSeam() func(cfg *runtime.RunConfig, sessionID, projectDir string) error

// --- hooks ---
type HookSet map[string][]HookMatcherConfig
func (d *Dispatcher) Dispatch(ctx context.Context, eventType, toolName string, input HookInput) HookDecision
type HookInput struct { EventType, SessionID, ProjectDir, ToolName string; ToolInput, ToolResponse json.RawMessage; Prompt, StopReason, Source, Trigger, Message string }
type HookDecision struct { Block bool; Reason, AdditionalContext string; UpdatedInput json.RawMessage }

// --- tools ---
type ToolRegistry struct { ... }  // Register/Get/List/Validate
type ToolExecutorConfig struct { Registry *ToolRegistry; PrepareArguments; BeforeToolCall; AfterToolCall; MaxResultBytes, MaxToolRetries int }
type BatchConfig struct { ToolExecutorConfig; ForceSequential bool }
func (cfg BatchConfig) ExecuteToolCalls(ctx, calls []agentcore.AgentToolCall, emit agentcore.EmitFunc) ([]agentcore.ToolResultMessage, bool)

// --- commands ---
type Command func(ctx context.Context, host Host, sess *manager.AcpSession, args string) (string, *Error)
type Host interface { SendSessionUpdate; SendTextChunk; SnapshotRecorder; TrustManager; LoadSession;
    RunSideQuestion; CompactSession; RebuildSession; ConsolidateDream; MemoryStatus;
    RemoteControlStart; RemoteControlStop; AvailableCommands }

// --- cli ---
type Host interface {  // 见 §2.1，replDeps aggregate 的实现
    Store() *session.Store; Header() session.SessionHeader; AgentCtx() *agentcore.AgentContext;
    Live() *LiveConfig; Registry() *agenttools.ToolRegistry; Reminders() *runtime.ReminderRegistry;
    Slash() *runtime.SlashRegistry; Creds() *provider.CredentialStore; Notifier() *plugin.EventNotifier;
    NotifierHandle() func(agentcore.AgentEvent); Trust() *trust.Manager; Goal() *agenttools.GoalState;
    Telemetry() *TelemetryHolder; Dispatcher() *hooks.Dispatcher; HookDeps() run.HookDeps;
    Cwd() string; Input() *bufio.Reader; ConfirmMu() *sync.Mutex;
    CurLeaf() string; SetCurLeaf(string); Persisted() int; SetPersisted(int);
    LastBtw() *agentcore.AgentContext; SetLastBtw(...); LastBtwBase() int; SetLastBtwBase(int) }

// --- trust ---
type Decision int  // Undecided/Trusted/Untrusted
func (m *Manager) IsTrusted(cwd string) bool
type Broker struct { ... }
func (b *Broker) BeforeToolCall(sessionID, cwd string) agentcore.BeforeToolCallFunc  // allow_once/allow_always/reject_always

// --- session store ---
func (s *Store) AppendBranch(sessionID string, header session.SessionHeader, parentLeafID string, messages agentcore.MessageList) (string, error)
func (s *Store) Fork(sourceID, leafID string, now time.Time) (SessionHeader, []Entry, error)
func ListAll(pigoHome string) ([]Metadata, error)
```

---

## 11. 架构要点与值得注意的设计

1. **ACP 全包围**：交互式前端（TUI/REPL）不再直接调 agent core，全部经进程内 ACP server（`StartInProcessWithHooks`）；acpcmd 是同一装配对外暴露 stdio。好处：前端与核心解耦、一个协议同时服务本地与外部客户端（Zed）、权限/hook/事件回放都走同一通道。headless 是唯一绕过 ACP 但复用 `run.Env`/`InstallDriverHooks` 的路径。
2. **session 上下文按工作区隔离**：`SessionContextFactory` + `CloneToolsForSession` 让一个共享进程可以服务多个 cwd，每个会话有独立工具根、slash registry（按 `cwd+trust.Fingerprint` 缓存失效）和 system prompt，绝不串 cwd 状态；`task` 工具通过 `SessionTaskTool` 保证子代理也不继承启动目录。
3. **会话树（schema v3）**：`Entry{ID, ParentID}` 让会话可 fork/clone/树导航；`AppendBranch` 增量写尾部、`Fork` 复制 root→leaf 路径、`PathToLeaf`/`RenderTreeLines` 支撑 `/tree`；v1/v2 旧文件读取时透明迁移。
4. **单一 hook 收敛点**：`run.InstallDriverHooks` 把所有 driver 的 hook 装配收敛成一处（原来是六个 RunConfig 装配点各自为政）；`InstallSeams`/`InstallSeamsBefore` 区分 ACP 与本地的前后顺序（权限 broker 与用户 hook 的先后不同）。空 HookSet → `NewDispatcher` 返回 nil → 热路径零开销（FR-18）。
5. **信任决策闭环**：trust 是“门”，hook 是“策略”，两者都在 `BeforeToolCall` seam 上；ACP 侧用 `Broker` 发 `session/request_permission`（allow_once/allow_always/reject_always），本地侧用 `ConfirmToolCall`（y/n/a）；`reject_always` 与 `allow_always` 都持久化到 `trust.json`，`Fingerprint` 驱动 registry 缓存失效。`SideEffectTools` 只含 bash/write/edit，读工具不过门。
6. **工具执行的失败皆结果化**：unknown tool/schema 校验失败/block/panic/上下文取消全部编码成 `AgentToolResult{IsError:true}` 回喂模型（绝不抛 Go error），模型总能读到可行动反馈；重试策略统一在单点 `runToolWithRetry`，只重试瞬时网络/IO 错误。
7. **双层循环 + 队列**：`runLoop` 内层跑 turn（有工具就继续），外层接 steering/follow-up 消息；`AcpSession` 的 pending 队列＋`TryRun/WaitForTurn/FinishTurn` 让并发 prompt 要么插队到 turn 中（steering/follow-up 模式 one-at-a-time/all），要么排队到下一轮；`session/cancel` 通过 `Cancel()` 广播取消句柄。
8. **字节预算多层防护**：每个工具有自己的内层上限（read 2000 行、bash 30KB、search 1000 条、webfetch 上限），executor 层再统一 `toolResultMaxBytes=100KB` 兜底，防止任何工具撑爆上下文。
9. **`/rewind` 与快照**：`FileSnapshotRecorder` 由 write/edit 共享（一个 run 一个 recorder），`RuntimeRunner.SnapshotRecorder()` 从工具集发现它，每轮 turn 后 `Commit("", "acp turn")`；`/rewind` 先恢复文件快照再截断会话历史。
10. **记忆整合（dream）是确定性与语义的分离**：`Runner` 做确定性的 dedupe/path-clean/Reconcile（全部 `withinScope` 路径守卫，防 LLM 逃逸 memory root），`Consolidator` 只做 LLM 语义决策（merge/prune/distill），解析失败默认 KEEP（保守）。
11. **诊断内建**：turn 挂起 3 分钟自动写 goroutine dump（`watchHangDump`，`PIGO_HANG_DUMP` 可调），`turn-debug.log` 记录异常 turn，`PIGO_DEBUG_TURN` 起 127.0.0.1:6060 debug 端点。
12. **协议层面注意点**：`Dispatcher` 的 outbound 用单 goroutine 写通道（`startOutboundWriter`），响应优先于通知，通知拥塞时丢弃、响应拥塞时转独立 goroutine，避免 stdout 写阻塞卡死 agent loop/子代理。
