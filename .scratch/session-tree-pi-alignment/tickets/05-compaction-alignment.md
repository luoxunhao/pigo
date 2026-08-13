# 05 - compaction 对齐设计

**Type:** grilling
**Status:** resolved
**Blocked by:** 01, 03, 04

## Question

pigo 的 compaction 如何按 pi 语义改造：split-turn 实现、迭代 previousSummary、overflow 自动重试、retainedTail checkpoint、headless 落盘、UI queue/abort，以及压缩后不再压平 sibling 分支。

## Answer

- compaction entry：采用未来 backend 的 `retainedTail` 自包含 checkpoint 模型，不保留 `firstKeptEntryId`；上下文投影只读该 entry 和它之后的 entry。
- split-turn：完整实现 pi 语义。切点落在单轮中间时，`turnPrefixMessages` 单独生成摘要，再与历史摘要合并。
- 迭代摘要：把上一次 compaction 的 `retainedTail` 虚拟成 entry 后继续切点，并用上一次 summary 走 update prompt；`fileOps` 从上次 details 累积。
- overflow 恢复：同模型 overflow / truncated length 时，先从 agent state 移除失败 assistant 消息，压缩后自动重试一次（只试一次）；每次 prompt 提交前也检查上一次 aborted 回复，跳过跨模型与 compaction 边界前的旧 usage。
- 持久化一致性：压缩永远作为新 `compaction` entry 追加到当前 lane 并推进 leaf；REPL/TUI/serve/headless 全部走同一 `AppendCompaction`，不再有压缩后 `Save()` 线性重写压平树的路径，headless 也必须落盘。
- UI：TUI 压缩期间输入进 compaction queue、`compaction_end` 后 flush、Esc/Ctrl+C 中止；REPL 阻塞读取 + Ctrl+C 中止，不引入队列。
- memory 能力：本次重构移除 pigo 的 memory/checkpoint.md 集成，严格对齐 pi；记忆系统设计在本次重构完成后另起 effort grill。
