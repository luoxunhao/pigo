# 05 - Compaction retainedTail + split-turn + 迭代 + overflow

**What to build:** 把 compaction 对齐 pi harness：`CompactionEntry` 使用 `retainedTail` 自包含 checkpoint，删除 `firstKeptEntryId` 依赖；实现 split-turn（history + turn prefix summary 合并）；迭代 summary（上一次 retainedTail/details/summary 复用）；overflow 单次自动重试与 preflight aborted 检查。

**Blocked by:** 03 - v4 typed JSONL codec, 04 - ProjectLeaf 统一投影

**Status:** resolved

## Acceptance Criteria

- [x] compaction entry 含 `summary/retainedTail/tokensBefore/details?/usage?/fromHook?`
- [x] 上下文投影只读 compaction entry + retainedTail + 其后 entries
- [x] split turn 生成 turn prefix summary 并合并
- [x] 迭代 summary 从上次 retainedTail 末尾之后开始，previousSummary 走 update prompt
- [x] overflow/truncated length 同模型时移除失败 assistant 并自动重试一次
- [x] 每次 prompt 提交前检查 aborted；跳过模型不匹配与 compaction 边界前旧 usage
- [x] 压缩作为新 compaction entry 追加到当前 lane 并推进 leaf
- [x] 不再写 `firstKeptEntryId`；相关测试通过

**Type:** backend

**Priority:** high

**Spec Reference:** `tasks/spec-session-tree-pi-alignment.md` §8.1-8.5

