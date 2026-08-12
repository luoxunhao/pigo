# 02 - TUI/REPL 树交互线框

> 来源：ticket 09 grilling，2026-08-12 确认。本文是 spec 素材，不写实现代码。

## 1. 范围

本线框覆盖：

- TUI 全屏树选择器（`/tree`）。
- label 编辑（TUI 内联 + REPL `/label`）。
- branch summary 三选一询问与生成中状态。
- compaction 状态显示、TUI 队列与 REPL 阻塞中止。
- REPL 文本 fallback 的等价交互。

后置项（另开 ticket）：

- 树搜索、过滤模式、折叠、复制、label 时间戳。
- TUI `/fork` 与 `/clone` 选择器。
- `/tree` 相关 ACP/HTTP 命令的 UI 编排。

## 2. 决策摘要

- 交付形态：`research/02-tree-interaction-wireframes.md` 线框文档；ticket 09 保留决议摘要。
- TUI 入口：仅 `/tree`，不做 double-Esc。
- TUI 形态：全屏模态选择器，输入框隐藏，Esc 返回主界面。
- 树默认视图：隐藏 label/custom/model_change/thinking_level_change/session_info 与纯工具 assistant；保留 user、assistant 文本、tool result、compaction、branch_summary。
- 分支排序：含当前 leaf 的分支优先，其余按时间顺序。
- 默认选中：当前 leaf；选中当前 leaf 是 no-op，关闭并提示 `Already at this point`。
- leaf 语义：选中 user/custom_message 时 leaf 指向其 parent，并回填消息文本；其他 entry 直接指向该 entry；根 user message 等效 reset leaf。
- label：任意 entry 可加；树行显示 `[label]` 前缀；空值删除。
- branch summary：每次非当前 leaf 导航都询问三选一；生成成功才提交导航；失败/中止不切换 leaf。
- 配置：`[tree] branch_summary_skip_prompt = false`；`skip_prompt=true` 只跳过询问，`--summary` 仍可覆盖。
- compaction：TUI 队列 + 自动提交；REPL 阻塞 + Ctrl+C 中止；队列失败/中止恢复回编辑器。

## 3. TUI 线框

### 3.1 `/tree` 模态

```text
┌ Session Tree ─────────────────────────────────────────────┐
│ ↑/↓ move · PgUp/PgDn page · Enter select · Esc cancel     │
│ Shift+L label · Alt+Up restore queue (compaction only)    │
├───────────────────────────────────────────────────────────┤
│ [fix] user: 修复 /tree 回填                     ← current │
│   └─ assistant: 已实现 leaf 切换                          │
│     └─ tool result: 3 files changed                       │
│   ├─ user: 检查 REPL fallback                             │
│   │   └─ assistant: 需要补充线框                          │
│   └─ branch summary: 树导航与回填已确认                    │
│     └─ compaction: 上下文已压缩                            │
├───────────────────────────────────────────────────────────┤
│ preview: branch summary 全文…                              │
└───────────────────────────────────────────────────────────┘
```

要点：

- 当前分支（含 `← current`）排在最前，默认选中当前 leaf。
- 当前 leaf 为 no-op；Enter 关闭并提示 `Already at this point`。
- 选中 user/custom_message：关闭模态，leaf 指向 parent；若主编辑器为空则回填消息文本。
- 选中其他 entry：关闭模态，leaf 指向该 entry，不回填编辑器。
- 选中 branch_summary 行时，底部 preview 区域显示完整总结。
- Esc 关闭模态并保留主编辑器原有草稿。

### 3.2 label 编辑

```text
Label (empty to remove):
[fix-rewind]
Enter save · Esc cancel
```

要点：

- `Shift+L` 打开 label 输入；Enter 保存，Esc 取消。
- 空值保存即删除 label。
- 保存后树行立即刷新为 `[label]` 前缀。

### 3.3 branch summary 询问

```text
Summarize branch?
  1. No summary
  2. Summarize
  3. Summarize with custom prompt
```

选择 `3` 后：

```text
Custom summarization instructions:
[focus on API changes only]
Enter submit · Esc back to choices
```

要点：

- 每次选中非当前 leaf 的节点后询问，除非 `branch_summary_skip_prompt=true`。
- 询问在模态内完成；主编辑器草稿不受影响。
- Esc 在 choices 中返回上一级：summary choices 中按 Esc 回到树选择器并保持同一选中项。

### 3.4 branch summary 生成中

```text
Summarizing branch… ▮
Esc cancel
```

要点：

- 关闭树模态，显示 spinner/状态行。
- Esc 中止总结，带同一选中项回到树选择器；不切换 leaf。
- 成功后才提交导航；失败回到树选择器并显示原因。

### 3.5 compaction 状态与队列

```text
compaction (threshold)… ▮

queued:
  > 继续修 /tree 回填
  > 顺便看 REPL /label
Alt+Up restore all to editor
```

要点：

- 状态行显示触发原因：`manual` / `threshold` / `overflow`。
- compaction 期间编辑器保持可用；Enter 提交的内容进入 pending 队列，逐行显示。
- `Alt+Up` 把所有队列消息合并回编辑器并清空队列；compaction 继续执行。
- compaction 正常结束后，队列整批作为 steering 注入下一轮。
- 失败或中止时，队列恢复回编辑器，不自动发送。
- 队列中的斜杠命令分类处理：查询命令立即执行；会改会话/树的命令拒绝并提示。

## 4. REPL 文本 fallback

### 4.1 `/tree`

```text
session tree (run /tree <n> to switch):
 1. [fix] user: 修复 /tree 回填
 2.   └─ assistant: 已实现 leaf 切换
 3.     └─ tool result: 3 files changed
 4.   ├─ user: 检查 REPL fallback
 5.   │   └─ assistant: 需要补充线框
 6.   └─ branch summary: 树导航与回填已确认
 7.     └─ compaction: 上下文已压缩          ← current
```

选中后：

- user/custom_message：leaf 指向 parent，下一条输入预填消息文本，可编辑后回车。
- 其他 entry：leaf 指向该 entry，不回填。
- branch_summary：选中后打印完整总结。
- 当前 leaf：打印 `Already at this point`，不询问 summary。

### 4.2 branch summary 询问

```text
Summarize branch?
  1. No summary
  2. Summarize
  3. Summarize with custom prompt
> 2
Summarizing branch… (Ctrl+C cancels)
branch summary: 树导航与回填已确认
switched to branch at node 6 (18 messages)
```

选择 `3`：

```text
Custom summarization instructions:
> focus on API changes only
```

`--summary` 覆盖：

```text
/tree 6 --summary
/tree 6 --summary focus on API changes only
```

### 4.3 `/label`

```text
/label 1 修复回填
label set on node 1

/label 1
label cleared on node 1
```

行号与 `/tree` 输出共用；不带文本即删除。

### 4.4 compaction

```text
compacting (threshold)… (Ctrl+C cancels)
compacted: 12345 → 2345 tokens, summarized 40 messages, kept 12
```

中止：

```text
compacting (threshold)… (Ctrl+C cancels)
compaction cancelled
```

要点：

- compaction 期间不读新输入；提前敲入终端的下一句仍由下次 ReadLine 自然读到。
- Ctrl+C 中止 compaction 并回到提示符，不主动清空 typed-ahead。

## 5. 键位表

| 场景 | 键位 | 行为 |
|---|---|---|
| TUI 树模态 | `↑` / `↓` | 移动选中行 |
| TUI 树模态 | `PgUp` / `PgDn` | 翻页 |
| TUI 树模态 | `Enter` | 确认选中 |
| TUI 树模态 | `Esc` | 取消，返回主界面 |
| TUI 树模态 | `Shift+L` | 编辑当前行 label |
| TUI label 输入 | `Enter` / `Esc` | 保存 / 取消 |
| TUI summary choices | `1` / `2` / `3` | 选择 No summary / Summarize / Custom |
| TUI custom 输入 | `Enter` / `Esc` | 提交 / 返回 choices |
| TUI 总结中 | `Esc` | 中止总结，回树选择器 |
| TUI compaction | `Enter` | 提交文本进队列 |
| TUI compaction | `Alt+Up` | 取回全部队列到编辑器 |
| TUI compaction | `Esc` | 中止 compaction |
| REPL 总结/压缩中 | `Ctrl+C` | 中止并回到提示符 |

## 6. 状态流

```text
idle
  ├─ /tree → tree modal → select
  │           ├─ current leaf → close, "Already at this point"
  │           ├─ user/custom → summary prompt → commit leaf=parent + prefill
  │           ├─ other entry → summary prompt → commit leaf=target
  │           └─ Esc → close, keep draft
  │
  └─ run/compaction
              ├─ compaction_start → status(reason) + queue active
              ├─ Alt+Up → restore queue to editor
              ├─ Esc → abort compaction
              ├─ success → flush queue as steering batch
              └─ failure/abort → restore queue, show reason
```

## 7. 配置

```toml
[tree]
branch_summary_skip_prompt = false
```

默认 `false`：每次导航询问三选一。

设为 `true`：跳过询问，直接 No summary；`/tree N --summary` 仍可强制总结。

## 8. 交接边界

- 本文作为 tree 交互 spec 的线框素材。
- 后续 spec 需要覆盖：树数据来源（ticket 04/06）、compaction 事件（ticket 05）、label/facts 写入（ticket 02/03）。
- 后置能力不写进本线框的验收范围。
