# 07 - 子 agent 会话图

**Type:** grilling
**Status:** resolved
**Blocked by:** 01, 02

## Question

插件化的子 agent 如何创建 SQLite 子 session；`parent_session_id` + metadata 的字段契约；`session/list` 过滤规则；子会话生命周期与删除语义。（已确认：删除内置 `task`，核心零内置子 agent，插件声明式注册 subagent 工具。）

## Answer

已确认（2026-08-12，经 grilling 共识）。

### 方向

- 删除内置 `task` 工具，核心不再内置任何子 agent 工具；插件通过声明式 manifest（`subagents[]`）注册子 agent 工具（A1），核心保留 `SubAgentTool` / `ChildSession` / SQLite 原语作为执行承载。
- 主 agent 调用插件暴露的子 agent 工具（如 `worker`），不走 `tools/call` 往返；插件负责声明 agent 定义与编排，核心负责子会话创建、血缘、持久化与恢复。

### ID 与创建

- 子 session id 保留确定性派生：`subagent-<sha256(parentSessionId + "\x00" + toolCallId)[:16]>`，SQLite 中作为非 uuidv7 特例。
- 子 agent 调用开始时 eager 创建 SQLite 行，同一事务写 `sessions`、`session_sequences`、`session_stats`、`main lane` 并 claim writer lease；崩溃/取消后仍可重连，不延迟建行。

### 血缘与 metadata 契约

- `sessions.parent_session_id` 列是唯一血缘权威。
- metadata JSON 只写扩展字段：`sessionKind: "subagent"`、`subagentType`（工具名）、`plugin`（插件名）、`parentToolCallId`。
- 不重复写 `parentSessionId`，也不复制到 `customMetadata`；读模型从列重建父 id。
- 子会话默认名使用工具名，task description 可覆盖。
- 删除 `subagents.json`；`Registry` 只保留 live map，持久化关系从 SQLite 派生。

### session/list

- 默认隐藏子会话；`/resume` 继续过滤。
- HTTP `?includeSubagents=true`、ACP `_meta.pigo.sessionList` v1 时返回子会话。
- 子会话 summary 带 `parentSessionId`、`parentToolCallId`、`subagentType`、`plugin`。

### 生命周期与删除

- 状态：`active` / `completed`（可继续）/ `archived`（终态）；running 不落 metadata，由 `records` / `lanes.open_operation_id` 表达。
- 子会话完成后仍是标准会话：`session/prompt` 走 `ChildSession.Continue()`，`session/cancel` 中止当前回合。
- 父删除不级联，child 保留，`parent_session_id` 允许悬挂。
- running child 的 `session/delete` 返回冲突，必须先 cancel；child 可单独删除。

### 插件子 agent 声明

- `isolation` 默认 `goroutine`，`process` 显式声明；process 模式只用 builtin 工具。
- `tools` 按 builtins + 全部插件工具的合并注册表解析，未知名字 fail。
- 嵌套默认禁止；agent 声明 `nested` / `maxDepth` 才允许，core 用 context depth 守卫；child 工具集默认剔除 subagent 工具。
