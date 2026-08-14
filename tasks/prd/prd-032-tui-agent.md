# PRD: TUI 形式 Agent 交互界面

## Introduction

pigo 目前的交互建立在一个**行式 REPL**（`internal/cli/repl`）之上：读一行 → 跑 agent → 流式打印文本 → 循环。它简洁可靠，但工具调用、状态信息只能以纯文本平铺输出，缺少结构化、可扫读的呈现。

本 PRD 要为 pigo 引入一个**全屏 TUI（Terminal User Interface）**交互界面，对标参考截图的观感：工具调用（LSP references/rename、Read、Search 等）以带边框的卡片 + 树状结构呈现，卡片头部有 `✓`/`!` 状态与配色；底部有一条常驻状态栏（模型名、thinking level、工作目录、git 分支/dirty、context 使用率、累计花费、当前任务）；assistant 输出与工具结果实时流式追加，可滚动回看历史；底部是支持多行与快捷键的输入框。

技术栈选定 **Bubble Tea + Lipgloss**（charmbracelet 生态，Go TUI 事实标准，Elm 架构）。TUI 复用现有 agent 内核，通过订阅 `internal/agentcore` 已有的 `AgentEvent` 事件流驱动渲染，不重写内核逻辑。

> **历史背景（重要）：** pigo 曾有一个基于 `charm.land/bubbletea/v2` 的全屏 TUI（`internal/tui`，约 3000 行），因逻辑复杂在 `tasks/prd/prd-026-remove-tui.md` 中被整体删除，回退为行式 REPL。删除时记录的两个致命缺陷（见 `tasks/prd/prd-033-tui-enhancement.md`）本次必须从设计上规避：
> 1. **中文/CJK 无法输入**（`handleKey` 只在 `len(s)==1` 时追加按键，多字节 UTF-8 被静默丢弃）。
> 2. **无宽度感知渲染**（拼字符串不区分双宽字符，换行/截断/光标对齐错位）。
>
> 因此本次 TUI 从第一版起就要求：基于 bubbles 组件做输入、用 `lipgloss.Width`（底层 go-runewidth）做宽度感知渲染。

面向读者可能是初级开发者或 AI agent，下文尽量避免术语堆砌。

## Goals

- 在 TTY 环境下，`pigo`（无 `-p`）默认进入全屏 TUI 交互；行式 REPL 降级为 `--no-tui` 显式选项与非 TTY 自动 fallback。
- 工具调用以带边框卡片呈现：头部含工具名 + `✓`/`!` 状态图标 + 配色，主体含调用输入参数与树状结构的 Response。
- 底部常驻状态栏展示：模型名、thinking level、cwd、git 分支与 dirty 状态、context 使用率百分比、累计花费、当前任务描述。
- assistant 文本与工具结果**流式**追加渲染，长对话可视口滚动回看历史。
- 输入框支持多行、中文/CJK/emoji 输入、按 rune 移动光标与退格，以及快捷键（提交、中断、展开详情、退出）。
- TUI 复用现有 agent 内核，仅通过订阅 `agentcore.AgentEvent` 事件流驱动 UI，不改动内核决策逻辑。
- 斜杠命令、技能调用、会话持久化（`--resume`/`--continue`/`--list-sessions`）在 TUI 下与 REPL 功能对齐。
- 所有新增渲染/映射/输入逻辑有对应单元测试；`go build`/`go vet`/`go test` 通过。

## User Stories

### US-001: 建立 TUI 骨架与默认入口接线
**Description:** As a pigo 用户, I want TTY 下默认进入一个可启动、可退出的全屏 TUI 空壳, so that 后续渲染能力有承载容器且不破坏无界面路径。

**Acceptance Criteria:**
- [ ] 新增 `internal/cli/tui` 包，引入 `github.com/charmbracelet/bubbletea` 依赖，定义实现 `tea.Model` 的根 `Model`（`Init/Update/View`）
- [ ] `dispatch`（`cmd/pigo/main.go`）在「无 prompt + stdout 为 TTY + 未指定 `--no-tui`」时启动 TUI；否则走行式 REPL
- [ ] 新增 `--no-tui` 布尔 flag，置位时强制行式 REPL
- [ ] 非 TTY（管道/CI）自动 fallback 到现有行式 REPL 或既有 no-prompt 错误路径，退出码不变
- [ ] TUI 启动后显示空壳（状态栏占位 + 空 transcript + 空输入框），`Ctrl+C`/`Ctrl+D` 可正常退出并恢复终端
- [ ] `go build ./...`、`go vet ./...` 通过
- [ ] 在终端手动验证：`pigo` 进入 TUI，`pigo --no-tui` 与 `echo x | pigo -p ...` 走原路径

### US-002: Lipgloss 主题层与宽度感知渲染基础
**Description:** As a 开发者, I want 集中的主题定义与宽度感知的渲染工具, so that 各类消息样式一致且中文双宽字符不错位。

**Acceptance Criteria:**
- [ ] 新增 `internal/cli/tui/theme.go`，定义 `Theme` 结构，持有 user/assistant/tool-header/tool-body/status/accent/error/success 等元素的 `lipgloss.Style`
- [ ] 提供换行/截断辅助函数，使用 `lipgloss.Width` 计算显示宽度（CJK 记为 2 列）
- [ ] 单元测试：含中文与 emoji 的字符串按显示宽度换行/截断，断言不在多字节边界内切断、对齐正确
- [ ] Typecheck/lint 通过

### US-003: 底部常驻状态栏
**Description:** As a 用户, I want 屏幕底部有一条状态栏, so that 我随时能看到当前模型、思考等级、目录、git 状态、context 占用与花费。

**Acceptance Criteria:**
- [ ] 状态栏渲染以下字段：模型名、thinking level、cwd（可缩写 `~`）、git 分支与 dirty 标记（如 `master *3 +4`）、context 使用率百分比、累计花费（如 `$0.32`）、当前任务文本
- [ ] 字段数据来源于现有 `LiveConfig`/session/telemetry，不新造数据源；无 git 仓库时隐藏 git 段
- [ ] 状态栏始终固定在底部输入框上方，随窗口宽度自适应（过窄时按优先级截断而非溢出换行）
- [ ] 单元测试：给定一组字段值，`View` 输出包含各字段且总宽度不超过给定终端宽度
- [ ] Typecheck/lint 通过
- [ ] 在终端手动验证：状态栏字段与真实运行状态一致

### US-004: 订阅 agentcore 事件流并映射为 TUI 消息
**Description:** As a 开发者, I want TUI 消费现有 `AgentEvent` 事件流, so that UI 由内核事件驱动而无需重写 agent 逻辑。

**Acceptance Criteria:**
- [ ] TUI 以 `tea.Cmd` 形式监听 agent 运行产生的 `agentcore.AgentEvent`（`MessageStart/Update/End`、`ToolExecutionStart/Update/End`、`TurnStart/End`、`AgentEnd`、`Telemetry`、`Compaction`）
- [ ] 每类事件转换为对应的 bubbletea 消息类型，驱动 transcript/状态栏更新；不阻塞 UI 主循环（事件在 goroutine 中读取，通过 channel/`tea.Cmd` 投递）
- [ ] 运行结束（`AgentEnd`）后 UI 回到可输入状态；运行期间输入被排队或提示忙碌
- [ ] 单元测试：构造一串伪事件序列，断言 `Model` 状态按序转换（如工具从 running→success/error）
- [ ] Typecheck/lint 通过

### US-005: 流式 assistant 文本渲染与视口滚动
**Description:** As a 用户, I want assistant 回复边生成边显示且历史可滚动, so that 我能实时跟读并回看长对话。

**Acceptance Criteria:**
- [ ] `MessageUpdate` 事件增量追加 assistant 文本到 transcript，而非等待完整结果一次性输出
- [ ] transcript 用视口（viewport）承载，内容超过屏幕高度时可上下滚动（PgUp/PgDn 或方向键）
- [ ] 新内容到达时默认自动贴底；用户手动上滚后暂停自动贴底，回到底部后恢复
- [ ] user/assistant/system 消息有可区分的样式前缀或配色
- [ ] 单元测试：多次 `MessageUpdate` 后 transcript 文本按序拼接正确
- [ ] Typecheck/lint 通过
- [ ] 在终端手动验证：长回复流式滚动、手动上滚不被打断

### US-006: 富工具调用卡片组件
**Description:** As a 用户, I want 工具调用以带边框卡片与树状结构呈现, so that 我能快速扫读每次调用的输入与结果。

**Acceptance Criteria:**
- [ ] 定义通用工具卡片渲染：带边框；头部为「工具名 + `✓`(成功)/`!`(失败/警告) 图标 + 配色」；主体分「调用输入参数」与「Response」两段
- [ ] Response 支持树状缩进结构（对标截图：文件 → `line X, col Y` → `… N more`），并按状态着色（成功绿、失败/无结果红/黄）
- [ ] 运行中卡片显示进行态（spinner 或 `…`），结束后据结果切换为 `✓`/`!`
- [ ] `Ctrl+O` 展开/收起当前（或最近）工具卡片的完整详情（对标截图 `(Ctrl+O for more)`）
- [ ] 至少覆盖通用文本型工具结果；LSP/Read/Search 等具体工具复用同一卡片、按结果结构填充（无需每工具定制渲染器）
- [ ] 单元测试：给定一次工具调用的输入/结果与状态，卡片 `View` 含工具名、状态图标、参数与 Response 关键行
- [ ] Typecheck/lint 通过
- [ ] 在终端手动验证：真实工具调用卡片与截图观感接近

### US-007: 多行输入框、CJK 输入与快捷键
**Description:** As a 用户, I want 一个支持中文与多行的输入框及一组快捷键, so that 我能顺畅输入并控制会话。

**Acceptance Criteria:**
- [ ] 使用 bubbles `textarea`/`textinput` 组件承载输入，正常键入中文/CJK/emoji，按 rune 移动光标、退格删除整个字符（不再有 `len==1` 丢字问题）
- [ ] 支持多行输入（如 `Shift+Enter` 或 `Alt+Enter` 换行，`Enter` 提交，具体键位在实现中确定并记录）
- [ ] `Esc` 或 `Ctrl+C` 两段式：运行中首次中断当前 agent 运行，空闲时（或连续两次）退出程序
- [ ] 提交后输入框清空并禁用直至该轮运行结束
- [ ] 单元测试：注入含中文的按键序列，断言输入缓冲区内容与光标位置正确
- [ ] Typecheck/lint 通过
- [ ] 在终端手动验证：中文输入、换行、两段式中断、退出均正常

### US-008: 斜杠命令、技能与自动补全集成
**Description:** As a 用户, I want 在 TUI 输入框里用斜杠命令与技能并有补全提示, so that TUI 的能力不低于行式 REPL。

**Acceptance Criteria:**
- [ ] 复用现有斜杠命令注册表（`buildSlashRegistry` 等），`/model`、`/help`、用户自定义命令、`~/.agents/skills` 的 `/skill-name` 在 TUI 中可用
- [ ] 输入以 `/` 开头时展示候选命令补全列表，可用方向键/Tab 选择
- [ ] 命令执行结果渲染进 transcript（复用 US-005/US-006 的呈现）
- [ ] 单元测试：输入 `/` 前缀得到候选集合；执行一个内置命令得到预期输出
- [ ] Typecheck/lint 通过
- [ ] 在终端手动验证：`/help`、一个技能命令在 TUI 内正常执行

### US-009: 会话持久化在 TUI 下对齐
**Description:** As a 用户, I want TUI 也能列出/恢复/续接会话, so that 无界面与 TUI 两种模式的会话行为一致。

**Acceptance Criteria:**
- [ ] `pigo --resume <id>` 与 `pigo --continue` 在 TUI 模式下加载对应会话历史并渲染进 transcript 后进入交互
- [ ] `pigo --list-sessions` 行为与退出码不变（仍是打印并退出，不进入 TUI）
- [ ] TUI 会话在退出/结束时按现有机制持久化到 `~/.pigo/sessions`
- [ ] 单元测试：恢复一个含若干消息的会话后，transcript 初始内容包含这些消息
- [ ] Typecheck/lint 通过
- [ ] 在终端手动验证：`--continue` 进入 TUI 且历史可见

## Functional Requirements

- FR-1: 系统必须在「无 `-p` prompt + stdout 为 TTY + 未指定 `--no-tui`」时启动 Bubble Tea 全屏 TUI。
- FR-2: 系统必须提供 `--no-tui` flag，置位时强制使用行式 REPL。
- FR-3: 系统必须在非 TTY（管道/CI）环境自动 fallback 到现有无界面路径，且退出码与当前一致。
- FR-4: TUI 必须通过订阅 `agentcore.AgentEvent` 事件流驱动渲染，不复制或改写内核决策逻辑。
- FR-5: TUI 必须将 `MessageUpdate` 事件增量渲染为流式 assistant 文本。
- FR-6: TUI 必须以带边框卡片呈现工具调用，头部含工具名与 `✓`/`!` 状态图标及配色。
- FR-7: 工具卡片 Response 必须支持树状缩进结构并按状态着色。
- FR-8: 系统必须提供 `Ctrl+O` 展开/收起工具卡片完整详情。
- FR-9: TUI 必须在底部渲染常驻状态栏，包含模型名、thinking level、cwd、git 分支/dirty、context 使用率、累计花费、当前任务。
- FR-10: transcript 必须支持视口滚动，且新内容默认贴底、用户上滚时暂停自动贴底。
- FR-11: 输入框必须正确处理中文/CJK/emoji 的输入、光标移动与删除（按 rune 而非字节）。
- FR-12: 所有面向终端宽度的渲染必须用 `lipgloss.Width` 做宽度感知，避免双宽字符错位。
- FR-13: 输入框必须支持多行输入与提交/换行的键位区分。
- FR-14: `Esc`/`Ctrl+C` 必须实现两段式：运行中中断当前运行，空闲时退出程序。
- FR-15: TUI 必须复用现有斜杠命令注册表，支持命令、技能与 `/` 前缀补全。
- FR-16: TUI 必须支持 `--resume`/`--continue` 加载并渲染既有会话历史。
- FR-17: `--list-sessions` 必须保持「打印并退出、不进入 TUI」的现有行为。

## Non-Goals（不做）

- 不做鼠标交互（点击/拖拽/滚轮之外的复杂操作）。
- 不做主题的运行时可视化配置面板（可预留一处主题定义即可，切换 UI 非本期目标）。
- 不做多面板/分屏布局（如侧边文件树、独立日志面板）。
- 不做 Markdown 全量富渲染的高级特性（表格、图片）——assistant 文本以基本样式呈现即可，代码块高亮为可选增强。
- 不改动 agent 内核的决策、工具执行或 provider 逻辑。
- 不做 stream-json/headless 输出格式的改动。
- 不追求与已删除的旧 `internal/tui` 逐特性等价（如模型 picker 弹窗）——超出本期范围的旧特性列入 Open Questions。

## Design Considerations

- 采用 Elm 架构：单一根 `Model`，子组件（状态栏、transcript viewport、工具卡片列表、输入框）各自封装 `Update`/`View`，根 `Model` 组合。
- 复用已在 `go.mod` 的 `charmbracelet/lipgloss` 与 `glamour`（如需 Markdown）；新增 `charmbracelet/bubbletea` 与 `charmbracelet/bubbles`。
- 工具卡片为通用组件，按「输入参数 map + Response 树节点」数据结构渲染，避免每种工具写一套渲染器（对标截图中 LSP/Read/Search 共用同一视觉语言）。
- 配色对标截图：成功绿、失败/警告红黄、文件名蓝、次要信息灰。
- 复用 `internal/cli/repl` 已有的斜杠命令注册、会话装配（`SetupEnv`/`ResolveThinkingLevel`）逻辑，抽取可共享部分而非复制。

## Technical Considerations

- **事件流即接口边界**：`agentcore` 已有 `EventStream[T,R]` 与 12 类 `AgentEvent`（`event.go`/`event_stream.go`）。TUI 作为该流的消费者，在 goroutine 中 `range` 事件、经 channel 投递给 bubbletea 的 `tea.Cmd`。需确认现有运行入口能把事件流暴露给 TUI（可能需在 `run`/`repl` 层新增一个「以 TUI 消费者启动一轮运行」的接口）。
- **依赖命名空间**：已删除的旧 TUI 用的是 `charm.land/bubbletea/v2` 分支命名空间；本期用户选择「Bubble Tea + Lipgloss」，需确认统一采用上游 `github.com/charmbracelet/bubbletea`（与现有 `github.com/charmbracelet/lipgloss` 一致），避免两套命名空间混用。见 Open Questions。
- **终端恢复**：程序退出/panic 时必须恢复终端（关闭 alt-screen、显示光标），避免污染用户 shell。
- **窗口尺寸**：监听 `tea.WindowSizeMsg`，各组件按新宽高重算布局。
- **测试策略**：`Model.Update` 为纯函数式转换，可用「输入消息序列 → 断言状态/`View` 子串」做单元测试，无需真实 TTY；宽度渲染用固定宽度断言。

## Success Metrics

- 用户可在 TUI 中正常输入中文并完成一轮多工具调用对话（零丢字）。
- 工具调用卡片、状态栏、流式滚动三要素在真实运行中呈现，观感接近参考截图。
- 非 TTY/`--no-tui` 路径行为与退出码与引入前完全一致（无回归）。
- 新增代码 `go build`/`go vet`/`go test` 全绿。

## Open Questions

- Bubble Tea 版本与命名空间：统一用上游 `github.com/charmbracelet/bubbletea`（v1）还是 v2？是否与现有 lipgloss 版本兼容？（倾向上游 v1 以对齐现有 lipgloss）
- 多行输入的换行键位选择：`Shift+Enter` 在多数终端不可区分，是否改用 `Alt+Enter` 或 `Ctrl+J`？
- context 使用率百分比的数据来源：是否已有可读的「已用 token / 上限」统计，还是需要从 telemetry 事件推导？
- 是否需要恢复旧 TUI 的模型 picker（`/model` 弹窗式选择）？本期是否只保留命令式 `/model <name>`？
- assistant 文本是否第一版就用 glamour 做 Markdown 渲染，还是先纯文本、后续增强？
- 工具卡片历史是否全部保留在 transcript（可能很长），还是折叠旧卡片仅留摘要？
