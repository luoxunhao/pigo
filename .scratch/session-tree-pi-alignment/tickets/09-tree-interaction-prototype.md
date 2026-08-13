# 09 - TUI/REPL 树交互原型

**Type:** prototype
**Status:** resolved
**Blocked by:** 04, 05, 06

## Question

pi 风格 tree selector、labels 编辑、branch summary 询问、compaction 状态/queue 在 pigo TUI 与 REPL 中的行为形态；产出原型或线框作为 spec 素材，并明确 REPL 文本 fallback 的等价交互。

## Answer

已确认（2026-08-12，经 grilling 共识）。完整线框见 [research/02-tree-interaction-wireframes.md](../research/02-tree-interaction-wireframes.md)。

- **交付形态**：线框文档，不写实现代码；ticket 09 保留决议摘要并链接。
- **范围**：树导航、当前 leaf、label、branch summary、compaction 状态/queue 纳入；搜索、过滤、折叠、复制、label 时间戳、TUI `/fork` `/clone` 后置。
- **TUI**：仅 `/tree` 进入全屏模态；`↑/↓`、`PgUp/PgDn`、`Enter`、`Esc`、`Shift+L`；默认选中当前 leaf；当前分支优先；默认视图隐藏记账 entry 与纯工具 assistant。
- **leaf 语义**：user/custom_message 选 parent 并回填编辑器（仅空编辑器）；其他 entry 直接指向目标；根 user 等效 reset；当前 leaf 为 no-op。
- **label**：任意 entry 可加，树行显示 `[label]`；TUI 内联编辑、空值删除；REPL `/label <树行号> [文本]` 与 `/tree` 共用编号。
- **branch summary**：每次非当前 leaf 导航询问 No summary / Summarize / Summarize with custom prompt；TUI 模态小输入框，REPL 编号询问；配置 `[tree] branch_summary_skip_prompt = false`，`--summary` 可覆盖；生成中可中止，成功才提交导航。
- **compaction**：TUI 队列 + 整批 steering 自动提交，pending 逐行显示，`Alt+Up` 取回编辑，状态显示触发原因；失败/中止恢复队列；REPL 阻塞 + Ctrl+C 中止，typed-ahead 保留。
- **配置**：`[tree] branch_summary_skip_prompt = false`，默认询问；`skip_prompt=true` 只跳过询问，不禁止 `--summary`。
