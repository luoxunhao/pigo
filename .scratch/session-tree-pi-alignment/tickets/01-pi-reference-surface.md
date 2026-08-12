# 01 - pi 参照面锁定

**Type:** research
**Status:** resolved
**Blocked by:**

## Question

从 `E:\project\pi` 当前代码确认并记录可被后续 ticket 引用的事实清单：`SessionManager` 的 leaf 投影与 entry 类型、compaction 的 split-turn / retainedTail / 迭代摘要行为、SQLite backend 的 lanes/records/facts/branch cache/writer lease 语义与默认值，以及 pi 的 `/tree`、`/fork`、`/clone` 交互语义。

## Answer

事实清单已产出：research/01-findings.md。关键结论：

- pi `SessionManager` 有 9 种 entry 类型，leaf 驱动 append 与上下文投影；`buildContextEntries()` 只走 leaf 到 root 路径，compaction 在 coding-agent 用 `firstKeptEntryId`，harness 新格式用 `retainedTail` 自包含 checkpoint。
- pi SQLite backend 的表结构、lanes/records/facts/branch cache/branch_tips/writer_leases 语义已逐条记录；writer lease 默认 `ttlMs=30000`、`heartbeatIntervalMs=10000`，带 fence 的 takeover/renew/release。
- pi 的 `/tree` 选择语义、`/fork`/`/clone`、branch summary、compaction 触发与 split-turn 行为已记录。
- 与 pigo 当前实现的差异已逐节标注：pigo 无 entry type 联合、无 writer lease、无 branch cache、无 labels/branch summary、compaction 未实现 split-turn/迭代摘要、serve 路径不按 leaf 投影。
