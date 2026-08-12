# 06 - Compaction 落盘统一 + UI queue/abort 事件

**What to build:** 统一 REPL/TUI/serve/headless 的 `AppendCompaction` 落盘路径；补齐 compaction 事件原因（manual/threshold/overflow）与 TUI queue、REPL 阻塞、abort 恢复；删除压缩后线性重写压平树的路径。

**Blocked by:** 05 - Compaction retainedTail + split-turn + 迭代 + overflow

**Status:** resolved

## Acceptance Criteria

- [x] 所有前端走同一 `AppendCompaction`，headless 也落盘
- [x] `compaction_start/end` 带原因，失败/取消有事件
- [x] TUI compaction 期间输入进 pending queue，结束后整批作为 steering 注入
- [x] TUI `Alt+Up` 取回全部队列；Esc 中止；失败/中止恢复队列与编辑器
- [x] REPL 压缩期间阻塞读，Ctrl+C 中止，typed-ahead 保留
- [x] 删除压缩后 `Save()` 线性重写压平树路径
- [x] queue 中斜杠命令分类：查询命令立即执行，改会话/树的命令拒绝并提示

**Type:** fullstack

**Priority:** high

**Spec Reference:** `tasks/spec-session-tree-pi-alignment.md` §8.5, §12

