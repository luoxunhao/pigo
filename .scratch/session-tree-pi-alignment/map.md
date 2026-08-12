# Wayfinder Map: pigo 会话树对齐 pi

> Local-markdown tracker。Effort：pigo 会话树迁移设计与实现。
> Status: implementation complete（2026-08-12）：SQLite canonical、v4 codec、ProjectLeaf、compaction 落盘、ACP/HTTP tree surface、子 agent SQLite 图、TUI/REPL 树交互与 SDK tree v1 已实现；遗留 TUI compaction queue 与 Zed 手动验收待补。

## Destination

已产出可交接的迁移设计并完成实施：pigo 的会话树从“持久化层树”对齐为 pi 的“运行时树模型”，以逐字复刻的 pi SQLite schema 作为 canonical 存储；v4 typed JSONL 只做导出/导入。运行时不再读取旧 JSONL 目录，旧会话由隔离脚本归档。

## Deliverables

- SPEC: [tasks/spec-session-tree-pi-alignment.md](../../tasks/spec-session-tree-pi-alignment.md)
- ADR: [0006-sqlite-canonical-session-storage.md](../../docs/adr/0006-sqlite-canonical-session-storage.md) / [0007-v4-jsonl-export-import-only.md](../../docs/adr/0007-v4-jsonl-export-import-only.md) / [0008-legacy-session-formats-deprecated.md](../../docs/adr/0008-legacy-session-formats-deprecated.md) / [0009-retained-tail-replaces-memory-checkpoint.md](../../docs/adr/0009-retained-tail-replaces-memory-checkpoint.md) / [0010-plugin-declared-subagent-sessions.md](../../docs/adr/0010-plugin-declared-subagent-sessions.md)
- Implementation issues: [issues/01-sqlite-schema-migrations-writer-lease.md](issues/01-sqlite-schema-migrations-writer-lease.md) ... [issues/16-map-tracker-closeout.md](issues/16-map-tracker-closeout.md)

## Notes

- Domain：pigo `internal/session`、`internal/sessionstore`、`internal/compaction`、`internal/runtime`、serve/ACP；参照 pi 的 `SessionManager`、compaction 与 `packages/session-backends/sqlite-node`。
- 每个 ticket 先查：本 map、grilling、domain-modeling、to-spec；参照代码位于 `E:\project\pi`（main @ 666d8972f）。
- 本地 tracker 惯例：`.scratch/<effort>/map.md` + `tickets/`；ticket 用 `Type:` / `Status:` / `Blocked by:` 标注。
- 已锁定 charting 决策见 Decisions so far；后续 ticket 只能细化，不能推翻。
- 07 子 agent 插件化方向（删除内置 task、插件声明式注册）已获用户确认（2026-08-12）。

## Decisions so far

- 目标模型（charting）：A+B。对齐 pi SessionManager JSONL 树语义，同时落地 pi SQLite backend 架构。
- 产出形态（charting）：只规划，产 spec/ADR/tickets；不在图内改代码。
- 兼容策略（charting）：语义对齐，pigo 自有 v4 JSONL；不追求与 pi 文件字节级互通。
- SQLite schema（charting）：逐字复刻 pi 的 `entries/lanes/records/facts/branch_tips/writer_leases` 等 schema。
- DB 拓扑（charting）：单库 `$PIGO_HOME/sessions.db`，`sessions.cwd` 区分项目。
- 语义边界（charting）：完整对齐 labels、branch_summary、model/thinking entry、compaction 行为、tree 命令语义。
- 旧会话（charting）：废弃 v1/v2/v3，不迁移、不向后兼容，一切从新开始。
- v4 JSONL（charting）：typed entry，与 SQLite entries 行一一对应。
- 子 agent（charting）：核心零内置子 agent，删除内置 `task`，插件声明式注册；独立子 session，`parent_session_id` + metadata 记录谱系；lanes 不冒充子 agent。
- ACP（charting）：`session/load` 消息带 `entryId/parentId`；`/tree` 返回结构化 JSON + 文本 fallback；不新增非标准 `session/*` 方法。
- UI（charting）：TUI/REPL 对齐 pi 树交互，REPL 保留文本 fallback。
- [09 TUI/REPL 树交互原型](tickets/09-tree-interaction-prototype.md)：线框交付到 research/02-tree-interaction-wireframes.md；TUI 全屏树模态 + label + branch summary + compaction queue；REPL 编号树 + `/label` + 阻塞中止；搜索/过滤/折叠/TUI fork-clone 后置。
- [01 pi 参照面锁定](tickets/01-pi-reference-surface.md)：事实清单已产出到 research/01-findings.md，pi entry/leaf/compaction/SQLite/live-session 参照面及与 pigo 的差异全部记录。
- [02 SQLite schema 与 writer lease 契约](tickets/02-sqlite-schema-contract.md)：per-operation writer lease（30s/10s + fence）、FTS 首版纳入、uuidv7 session id、完整 session_stats 列、migrations 表 + pi PRAGMA、name/label 走 facts。
- [03 v4 JSONL typed entry 契约](tickets/03-jsonl-v4-contract.md)：单行 pi 风格 header（version 4 + pigo 扩展字段）、9 种 pi typed entry、外层照 pi 内层保留 pigo agentcore JSON、按 seq 物理序往返、会话语义无损、label/session_info entry 表达 facts。
- [04 leaf 投影与 resume 语义](tickets/04-leaf-projection-and-resume.md)：main lane leaf 由 SQLite `lanes.leaf_id` 持久化；resume / `session/load` 默认取该 leaf；新增唯一 `ProjectLeaf` 投影入口，REPL/TUI/serve/ACP 共用，serve 不再按文件序读全部 entry。
- lanes 多游标语义（ticket 04）：同一会话可有多个命名 lane，每个 lane 持有独立 `leaf_id`；append 只推进对应 lane 的 leaf，共享同一棵 `entries` 树；`main` 为默认游标，side/remote 等用于侧线程与多客户端。
- [05 compaction 对齐设计](tickets/05-compaction-alignment.md)：`retainedTail` 自包含 checkpoint、split-turn 完整实现、retainedTail 虚拟边界迭代摘要、overflow 单次重试 + 提交前检查、全前端统一 append entry、TUI queue / REPL 阻塞中止、移除 memory/checkpoint.md。
- [06 ACP 树 surface 契约](tickets/06-acp-tree-surface.md)：树扩展只走 `_meta.pigo.sessionTree`；initialize 双向声明 v1、仅对声明客户端发送；`session/load` 回放带 messageId + entry/parent/entryType/seq/lane；`/tree` 结构化 JSON（nodes/currentLeafId/currentLane/labels/lanes）+ 文本 fallback；`session/status` 暴露 currentLeafId/currentLane/lanes；不新增 session/* 方法。
- [07 子 agent 会话图](tickets/07-subagent-session-graph.md)：删除内置 `task`，插件声明式注册子 agent 工具；确定性 `subagent-` id + eager 建行；`parent_session_id` 列为血缘权威；metadata 只写 `subagentType/plugin/parentToolCallId`；`session/list` 默认隐藏、显式 includeSubagents；`active/completed/archived` 状态；父删除不级联、running child 删除冲突；默认禁嵌套，goroutine 默认 + process 可选。
- [08 旧会话移除与入口清理](tickets/08-legacy-removal-and-entry-surface.md)：运行时完全不读 v1/v2/v3 与旧平铺目录；旧目录由仓库脚本隔离到 `$PIGO_HOME/legacy-sessions/`，pigo 不移动；`--resume/--continue/--list-sessions`/`/resume` 保留并只走 SQLite；`/export` v4 JSONL + HTML，`/import` 仅 v4 JSONL 且严格校验；`internal/session` 瘦身为 v4 编解码、`sessionstore` 重写为 SQLite canonical；新增 ADR + 文档全面改写。

## Not yet specified

- `/rewind` 与新树模型的关系。
- 多 serve 进程共享单库时的 open/close 生命周期（未开票）。
- 树搜索、过滤、折叠、复制、label 时间戳（ticket 09 后置）。
- TUI `/fork` 与 `/clone` 选择器（ticket 09 后置）。

## Out of scope

- 字节级兼容 pi JSONL。
- 旧 v1/v2/v3 会话迁移与向后兼容。
- 新增非标准 `session/*` ACP 方法。
- 整体移植 pi 的 extension 系统。
- 客户端（pi-web/ash-workbench）UI 改造。
- 本图内的代码实现。
- memory 能力与 checkpoint.md：本次重构移除；重构完成后另起 effort grill memory。
