# 会话投影语义对齐

> 状态：**superseded**——由 `tasks/spec/spec-018-contextbuild-registry-dsh-alignment.md` 取代（对齐目标改为 deepseek-harness），归档保留。

Type: grilling
Status: resolved
Blocked by: issue-037

## Question

v4/SQLite entries -> `ProjectLeaf` -> `BuildSessionContext` 的投影规则要精确到什么程度？包括：哪些 entry 生成消息；custom entry 默认 projector 与未注册 projector 的行为；`active_tools_change` 的持久化格式与投影推导；model / thinking 状态的最新值规则；compaction `retainedTail` 与后续 entry 的顺序。

## Comments

- 2026-08-13 Codex: claimed via wayfinder workflow; grilling started.
- Q1: A - 消息投影边界：message / compaction / branch_summary 直接生成；custom 仅经 entryProjector；移除 custom_message 独立类型。
- Q2: A - custom 投影无默认 projector；未注册 customType 完全跳过。
- Q3: A - BuildSessionContext 丢弃 stopReason=error/aborted 的 assistant 消息，保留 length；未来 deferred 同规则；历史/ACP 回放走 raw path。
- Q4: A（被 Q5=B 取代）- activeToolNames 不再作为 entry 持久化，改由配置寄存器承载。
- Q5: B - model/thinking/activeTools 以配置寄存器为权威，不投影自 entry；entry 只作历史；Q4 相应回滚。
- Q6: B - 配置寄存器按 lane 级实现，对齐 pi lane.config；新建 lane 复制 seed；需要 lane_config 表与 v4 导出格式。
- Q7: A - 只保留最新 compaction；顺序为 summary -> retainedTail -> 后续 entry；不去重。
- Q8: A - ProjectLeaf 增加 Config *LaneConfig；Store.Projection 加载 lane.config；BuildSessionContext 仍只接收 proj。
- Q9: A - 新增 agentcore.CustomMessage（role=custom）；projector 可返回；ConvertToLlm 转 user；TUI/ACP 可区分。
- Q10: B - 不兼容旧会话；lane.config 缺失视为 error；新建会话创建时即初始化 lane.config。

## Answer

投影契约（2026-08-13 确认）：

- `Messages`：`message / compaction / branch_summary` 直接生成；`custom` 仅经 `EntryProjector` 按 `customType` 注册生成，未注册跳过；移除 `custom_message` entry 类型。
- assistant `stopReason=error/aborted`（未来 `deferred` 同规则）不进 `Messages`，`length` 保留；raw history / ACP replay 不受影响。
- compaction：只保留最新 compaction，顺序为 summary -> retainedTail -> 后续 entry，不去重。
- model / thinking / activeTools 以 lane.config 为权威，不从 entry 推导；lane.config 按 lane 级实现，新建 lane 复制 seed，新建会话创建时初始化；缺失配置视为 error，不兼容旧会话。
- `ProjectLeaf` 增加 `Config *LaneConfig`；`Store.Projection` 加载 lane.config；`BuildSessionContext` 仍只接收 `proj`。
- 新增 `agentcore.CustomMessage`（`role="custom"`）；projector 可返回，`ConvertToLlm` 转成 user；TUI/ACP 可区分渲染。
