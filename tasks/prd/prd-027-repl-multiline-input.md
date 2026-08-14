# PRD: REPL 多行输入（Shift+Enter 与续行符）

## Introduction

pigo 的 REPL 目前只能单行输入：`replLineEditor.readLine`（`cmd/pigo/line_editor.go`）在 raw 模式下逐字节读取，遇到 `\r`/`\n` 立即提交，整个输入维护为一个 `input string`。用户想粘贴代码块、写多段说明或格式化的 prompt 时，无法在同一条消息里换行。

本功能为 REPL 增加多行输入能力：在支持的终端里用 **Shift+Enter** 换行、普通 **Enter** 提交；对无法区分 Shift+Enter 的终端（大多数终端默认如此），提供**行尾反斜杠 `\` 续行**作为通用兜底。多行缓冲区支持**完整跨行编辑**（方向键在已输入的多行间移动光标、任意行可回改）。整块多行文本作为一条消息发给 agent。

面向读者说明几个术语：raw 模式指终端逐字节交付按键、不做行缓冲；CSI-u / modifyOtherKeys 是终端上报组合键（如 Shift+Enter）的两种扩展协议；续行符指一行末尾的 `\`，表示"这一行没结束，下一行继续"。

## Goals

- 在 REPL 单条消息中输入多行文本，Enter 提交、Shift+Enter 换行。
- 对不支持 Shift+Enter 的终端，提供行尾 `\` 续行的通用兜底，能力不缺失。
- 多行缓冲区支持跨行光标移动与任意行编辑（不只是追加）。
- 保持现有单行体验不回退：历史浏览、斜杠命令/模型补全、Ctrl+C、Ctrl+D、管道/测试路径的行为不变。
- 多行输入作为一条消息提交并正确记入历史、可被历史浏览复原。

## User Stories

### US-001: 多行缓冲区与光标数据模型
**Description:** As a developer, I need the line editor to model input as multiple lines with a cursor position so multi-line editing and rendering can build on it.

**Acceptance Criteria:**
- [ ] `replLineEditor.readLine` 内部状态由单一 `input string` 改为多行表示（如 `lines []string` + 光标 `row, col`，col 以 rune 计）。
- [ ] 单行输入行为与改造前等价：输入可见字符追加到当前行、光标随之右移。
- [ ] 提交时把各行用 `\n` 连接为单个字符串返回，末尾无多余换行。
- [ ] 空输入提交返回空字符串（与现状一致）。
- [ ] `go build ./... && go vet ./...` 通过；`go test ./cmd/pigo/` 通过。

### US-002: 行尾反斜杠续行兜底
**Description:** As a user on any terminal, I want a trailing backslash to continue input on the next line so I can enter multi-line messages without relying on Shift+Enter.

**Acceptance Criteria:**
- [ ] 当当前行以单个未转义的 `\` 结尾时按 Enter：删除该 `\`，插入换行，光标落到新行行首，不提交。
- [ ] 转义的 `\\`（两个反斜杠）结尾时按 Enter：视为普通结尾，正常提交，提交文本保留一个字面 `\`。
- [ ] 行尾无 `\` 时按 Enter：提交整块（含之前用续行/换行产生的多行）。
- [ ] 提交文本中不包含用于续行的那个 `\` 字符。
- [ ] 新增单测覆盖：单行 `\` 续行、`\\` 不续行、多次续行累积成多行、续行后提交的最终字符串。

### US-003: Shift+Enter 换行（终端支持时）
**Description:** As a user on a capable terminal, I want Shift+Enter to insert a newline while Enter submits, matching common chat/editor conventions.

**Acceptance Criteria:**
- [ ] 进入 raw 模式时启用组合键上报（如发送 modifyOtherKeys/CSI-u 启用序列），退出时还原，还原逻辑与现有 stty 状态恢复一同执行。
- [ ] 收到 Shift+Enter 的上报序列（如 CSI-u 的 `\x1b[13;2u`）时：在光标处插入换行、不提交。
- [ ] 收到普通 Enter（`\r`/`\n`，或 CSI-u 下的无修饰 `\x1b[13u`）时：提交整块。
- [ ] 终端不上报该序列时，不产生错误输出，用户仍可用 US-002 的 `\` 续行（能力不缺失）。
- [ ] 未识别的转义序列被消费并忽略，不泄漏进提交文本（保持现有第 235 行 case 27 的行为）。
- [ ] 新增单测：喂入 Shift+Enter 序列产生换行、普通 Enter 提交（用可编程 `io.Reader` 驱动，无需真实终端）。

### US-004: 跨行光标移动
**Description:** As a user editing a multi-line buffer, I want arrow/Home/End keys to move the cursor across and within lines so I can edit any part before submitting.

**Acceptance Criteria:**
- [ ] ←/→ 在当前行内移动光标；→ 在行尾时跨到下一行行首，← 在行首时跨到上一行行尾（仅当存在相邻行）。
- [ ] ↑/↓ 在多行缓冲区（行数 > 1）内上下移动光标，目标列裁剪到目标行长度。
- [ ] 当缓冲区为单行且为空、或单行且光标在首行时，↑/↓ 仍触发既有历史浏览（保持现状不回退）。
- [ ] Home/End（或 Ctrl+A/Ctrl+E）移动到当前行首/行尾。
- [ ] 光标移动不修改缓冲内容。
- [ ] 新增单测覆盖跨行移动的边界（行首左移、行尾右移、列裁剪）。

### US-005: 多行渲染与光标重定位
**Description:** As a user, I want the terminal to correctly display all input lines and place the cursor where I'm editing so the multi-line buffer is usable.

**Acceptance Criteria:**
- [ ] 渲染时正确显示全部输入行；首行带 prompt，续行有一致的对齐/续行标识。
- [ ] 重绘后终端光标定位到逻辑光标 `(row, col)` 对应的屏幕位置。
- [ ] 编辑上面的行后重绘不残留旧字符（清除到行尾/清除多余行）。
- [ ] 补全建议（dim 提示）仅在单行、光标位于末尾时显示，避免与多行渲染冲突。
- [ ] 手动在真实终端验证：输入 3 行、上移改中间行、Enter 提交，显示与提交内容一致（终端为 raw-mode 交互，无法用浏览器验证，改为终端手测并在 PR 记录步骤）。

### US-006: 跨行编辑与退格
**Description:** As a user, I want backspace and character insertion to work correctly at any cursor position, including across line boundaries.

**Acceptance Criteria:**
- [ ] 在行中插入字符：插入到光标处，光标右移，后续字符右移。
- [ ] 行首退格：把当前行与上一行合并，光标落到合并点（仅当存在上一行）。
- [ ] 行中/行尾退格：删除光标左侧一个 rune（正确处理多字节 UTF-8）。
- [ ] 缓冲为空时退格无副作用。
- [ ] 新增单测覆盖：行内插入、行首退格合并行、多字节字符退格。

### US-007: 多行提交、历史记录与恢复
**Description:** As a user, I want a submitted multi-line message stored and recallable as a single history entry so I can reuse it.

**Acceptance Criteria:**
- [ ] 多行块提交后作为**一条**历史记录存入 `e.history`（保留内部换行）。
- [ ] 历史浏览（空行时 ↑）复原多行记录时，缓冲区恢复为对应多行、可继续编辑。
- [ ] `remember` 对多行内容的 trim 行为不破坏内部换行（仅去除整体首尾空白，或按需调整并说明）。
- [ ] 提交的多行字符串完整传递给 agent 一轮（`prompt := line` 路径不截断换行）。
- [ ] 新增单测：多行提交进入历史、历史复原为多行、提交字符串含 `\n`。

## Functional Requirements

- FR-1: 系统必须把 REPL 行编辑器的输入状态建模为可跨行的多行缓冲区并维护光标位置（row/col，col 以 rune 计）。
- FR-2: 当当前行以单个未转义 `\` 结尾时按 Enter，系统必须以换行续行而非提交。
- FR-3: 当行尾为 `\\` 或无 `\` 时按 Enter，系统必须提交整块，且提交文本不含续行用的 `\`。
- FR-4: 系统必须在进入 raw 模式时启用组合键上报，并在退出时还原。
- FR-5: 系统必须将 Shift+Enter 的上报序列解释为插入换行。
- FR-6: 系统必须将无修饰 Enter 解释为提交。
- FR-7: 系统必须支持 ←/→ 在行内及跨行边界移动光标。
- FR-8: 系统必须支持 ↑/↓ 在多行缓冲区内移动光标，并在单行/空行场景保留既有历史浏览。
- FR-9: 系统必须支持 Home/End（或 Ctrl+A/Ctrl+E）移动到行首/行尾。
- FR-10: 系统必须正确渲染多行缓冲区并把终端光标定位到逻辑光标处。
- FR-11: 系统必须在重绘时清除上一次渲染的残留字符与多余行。
- FR-12: 系统必须支持在任意光标位置插入字符（含多字节 UTF-8）。
- FR-13: 系统必须支持行首退格合并至上一行、行中退格删除左侧 rune。
- FR-14: 系统必须把提交的多行块作为单条历史记录存储并可复原为多行缓冲区。
- FR-15: 系统必须把多行提交文本（含 `\n`）完整传给 agent 一轮。

## Non-Goals (Out of Scope)

- 不实现全屏 TUI 编辑器；仍是行内编辑层的增强（保持 `line_editor.go` 的定位）。
- 不实现语法高亮、自动缩进、括号匹配等富文本编辑能力。
- 不改变管道/非终端（`e.terminal == nil`）路径：继续用 `bufio.ReadString('\n')` 逐行读取，不引入续行/多行逻辑。
- 不为每种终端逐一适配私有序列；仅支持标准 modifyOtherKeys/CSI-u 上报，其余靠 `\` 续行兜底。
- 不实现鼠标定位光标、不实现多行内的选区/复制。
- 不改动 agent 侧对消息内容的处理（多行文本对上层透明）。

## Design Considerations

- 复用现有 `readLine` 的 raw 模式进出、stty 状态保存/还原（`line_editor.go:147-164`）与转义序列消费框架（`case 27`）。
- 组合键上报启用/还原序列应与 stty 还原一起 `defer`，避免异常路径下终端残留在 CSI-u 模式。
- 续行标识与对齐应与现有 prompt `pigo(%s)> `（`repl.go:191`）视觉协调；续行行可用简短前缀（如 `...> ` 或缩进对齐）。
- 补全/建议逻辑（`suggestions`/`visible`/dim 渲染）仅适用于单行末尾场景，多行时抑制以避免渲染冲突。

## Technical Considerations

- Shift+Enter 的可区分性依赖终端协议：需先发送启用序列（如 xterm `\x1b[>4;1m` modifyOtherKeys，或 CSI-u `\x1b[>1u`），并在退出时发送对应关闭序列；无法启用/不上报的终端自动退回 `\` 续行。
- 测试策略：交互逻辑用可编程 `io.Reader` 喂字节序列驱动 `readLine`（`e.terminal == nil` 时仍走 buffered 分支，需要为 raw 分支的按键处理抽出可单测的纯函数，或用注入的伪终端标志），避免依赖真实 TTY——沿用 `line_editor_test.go` 现有测试风格。
- UTF-8 多字节处理需沿用现有 `utf8.DecodeLastRuneInString` / 变长字节读取逻辑（`line_editor.go:228-299`）。
- 多行渲染需要跨行光标控制（`\r`、`\033[2K` 清行、`\033[<n>A/B` 上下移、列定位），注意不同终端对光标序列的一致支持。

## Success Metrics

- 在支持的终端（如 iTerm2/kitty，开启 CSI-u）Shift+Enter 换行、Enter 提交按预期工作。
- 在不支持的终端（如默认 Terminal.app）`\` 续行可完成等价多行输入。
- 现有单行 REPL 交互（历史、补全、Ctrl+C/D、管道输入）无回退，`go test ./cmd/pigo/` 全绿。
- 多行 prompt 提交后 agent 收到完整含换行的消息。

## Open Questions

- 续行行的视觉前缀用哪种：`...> ` 风格，还是按首行 prompt 宽度纯缩进对齐？（影响 US-005 渲染）
- 是否需要一个可配置开关（flag/env）在个别终端强制启用或禁用 CSI-u 上报，以防误判？
- 历史存储多行记录时，是否需要在 `/history` 类展示中对换行做折叠或转义显示？（本 PRD 默认原样存储，不改展示）
- Ctrl+A/Ctrl+E 与既有按键是否有冲突需确认（当前 editor 未占用，倾向直接支持）。
