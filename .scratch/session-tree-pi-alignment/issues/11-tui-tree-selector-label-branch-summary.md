# 11 - TUI 树选择器 + label + branch summary

**What to build:** 按 `research/02-tree-interaction-wireframes.md` 实现 TUI `/tree` 全屏选择器：当前分支优先、默认选中当前 leaf、no-op、label 编辑、branch summary 三选一与生成中状态、compaction 状态/queue。

**Blocked by:** 04 - ProjectLeaf 统一投影, 05 - Compaction retainedTail + split-turn + 迭代 + overflow, 06 - Compaction 落盘统一

**Status:** partial

## Acceptance Criteria

- [ ] `/tree` 进入全屏模态；`↑/↓`、`PgUp/PgDn`、`Enter`、`Esc`、`Shift+L` 生效
- [ ] 默认选中当前 leaf，当前 leaf 是 no-op 并提示 `Already at this point`
- [ ] 默认视图隐藏 label/custom/model_change/thinking_level_change/session_info 与纯工具 assistant
- [ ] 选中 user/custom_message 时 leaf 指向 parent，空编辑器回填文本；根 user reset
- [ ] `Shift+L` 编辑 label，空值删除，树行立即刷新
- [ ] 非当前 leaf 导航询问 No summary / Summarize / Custom prompt
- [ ] branch summary 生成可取消，成功才提交导航；失败回树选择器显示原因
- [ ] compaction 状态显示原因，pending queue 可 `Alt+Up` 取回、Esc 中止、失败恢复
- [ ] 主编辑器草稿在 Esc 取消后不丢失

**Type:** frontend

**Priority:** medium

**Spec Reference:** `tasks/spec-session-tree-pi-alignment.md` §12

