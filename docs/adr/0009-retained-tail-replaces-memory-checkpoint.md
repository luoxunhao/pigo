# Compaction checkpoint: retainedTail self-contained entry replaces memory/checkpoint.md

pigo 的 compaction 采用 harness 风格 `retainedTail` 自包含 checkpoint：`compaction` entry 内嵌 `summary + retainedTail + tokensBefore`，上下文投影只读该 entry 与它之后的 entry，不再依赖 `firstKeptEntryId` 重放被压缩 entry。上一次 compaction 的 `retainedTail` 会虚拟成消息纳入下一次可压缩范围，并配合迭代 summary。

同时完整实现 split-turn（history summary + turn prefix summary 合并）、overflow 单次自动重试、每次 prompt 前 aborted 检查。压缩永远作为新 `compaction` entry 追加到当前 lane 并推进 leaf，REPL/TUI/serve/headless 共用 `AppendCompaction`。

本重构移除 `<memoryRoot>/sessions/<id>/checkpoint.md` 的会话集成与 `/rebuild` 的 checkpoint 优先路径；memory 能力本身不在此范围，另起 effort 设计。完整设计见 `tasks/spec-session-tree-pi-alignment.md` §8。

