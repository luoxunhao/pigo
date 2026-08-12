# 12 - REPL 树 fallback + label + compaction 中止

**What to build:** REPL 文本等价交互：`/tree` 编号树、`/tree N` 切换、`/label <n> [text]`、branch summary 编号询问与 `--summary` 覆盖、compaction 阻塞与 Ctrl+C 中止。

**Blocked by:** 04 - ProjectLeaf 统一投影, 05 - Compaction retainedTail + split-turn + 迭代 + overflow, 06 - Compaction 落盘统一

**Status:** partial

## Acceptance Criteria

- [ ] `/tree` 输出编号树，`/tree N` 与 TUI 同 leaf 语义
- [ ] 选中 user/custom_message 预填输入，可编辑后回车；当前 leaf 打印 `Already at this point`
- [ ] `/label` 与 `/tree` 共用编号，空文本删除 label
- [ ] branch summary 三选一与 custom prompt 输入；`--summary` 可覆盖配置
- [ ] 压缩期间阻塞读输入，Ctrl+C 中止并回到提示符
- [ ] typed-ahead 保留，中止后不主动清空
- [ ] 文本 fallback 与 TUI 行为一致

**Type:** frontend

**Priority:** medium

**Spec Reference:** `tasks/spec-session-tree-pi-alignment.md` §12

