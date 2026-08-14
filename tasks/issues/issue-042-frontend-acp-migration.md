# 前端与 ACP 迁移策略

> 状态：**superseded**——由 `tasks/spec/spec-018-contextbuild-registry-dsh-alignment.md` 取代（对齐目标改为 deepseek-harness），归档保留。

Type: grilling
Status: resolved
Blocked by: issue-038, issue-039, issue-040

## Question

REPL / TUI / headless / serve / ACP 如何统一切换到 `contextbuild`：resume / session/load 是否仍直接调 `Projection`；header `SystemPrompt` 缓存是否移除；中间兼容 shim 保留多久；serve 路径 compaction 后的 `Save` flatten 风险是否一并处理；ACP replay 需要新增哪些字段。

## Comments

- 2026-08-13 Codex: claimed via wayfinder workflow; grilling started.
- Q1: A - 统一切换：REPL/TUI/headless/serve/ACP 同步切到 contextbuild，删除旧 AgentContext 手工组装路径，不设运行时兼容 shim。
- Q2: A - store.Projection 保留为运行时上下文构建唯一存储入口，前端统一 Projection -> BuildSessionContext；HistoryWindow/store.Load 仅供 replay/UI 展示。
- Q3: A - 移除 header.SystemPrompt 输入语义，仅保留兼容写回且写回实际发送 prompt。
- Q4: A - serve 路径一并纳入：用 compacted 标记替代 len(msgs)<len(history) 启发式，统一 REPL/TUI/headless/serve 持久化语义。
- Q5: A - ACP 状态扩展走 _meta.pigo：session/load 与 session_info_update 增补 laneConfig（model/thinking/activeTools）与 systemPrompt；replay 不新增标准字段。

## Answer

2026-08-13 确认（wayfinder grilling）：

- 统一切换：REPL / TUI / headless / serve / ACP 同步切到 `contextbuild.BuildSessionContext + BuildProviderContext`；删除旧 `AgentContext` 手工组装路径，不设运行时兼容 shim。
- 投影入口：`store.Projection` 保留为运行时上下文构建的唯一存储入口，前端统一 `Projection -> BuildSessionContext`；`HistoryWindow` / `store.Load` 仅用于 ACP replay / UI 展示，不作为 LLM 上下文输入。
- header `SystemPrompt`：移除输入语义；仅保留兼容写回，且写回每轮实际发送的 prompt（按 issue-040）。
- serve compaction 持久化：一并纳入迁移；用 `compacted` 标记替代 `len(msgs) < len(history)` 启发式，统一 REPL/TUI/headless/serve 语义。
- ACP replay / 状态：不新增标准 ACP 字段；`session/load` 与 `session_info_update` 在 `_meta.pigo` 增补 `laneConfig`（model / thinking / activeTools）与 `systemPrompt`；replay 维持 compaction/branch_summary 跳过，custom 按 user 回放。
