# PRD: Skill 模型自动调用（Model-Invoked Skills）

## Introduction

今天 pigo 把每个 skill 只注册成了 `/skill-name` 斜杠命令（见 `cmd/pigo/interactive.go` 中 `loadSkillCommands` 只调用 `s.SlashCommand()`）。模型的系统提示里从不出现 skill 的存在，因此模型无法根据任务自主选用 skill——用户必须手动敲 `/weather` 才能触发 weather skill。

pi agent 已经解决了这个问题：它把每个 skill 的 `name / description / location`（SKILL.md 的绝对路径）注入系统提示的 `<available_skills>` 块，并告诉模型"当任务匹配某个 skill 的描述时，用 read 工具加载它的文件并按其指示执行"。这是一种**渐进式披露（progressive disclosure）**——只在提示里放元数据，正文由模型按需读取，上下文开销最小。

本 PRD 的目标是让 pigo 对齐 pi 的这套机制：模型能自动发现并调用 skill，同时保留现有的 `/skill-name` 显式调用能力。

参考实现（pi，位于 `/Users/chaoyuepan/ai/pi`）：
- `packages/coding-agent/src/core/skills.ts` — `formatSkillsForPrompt`、`loadSkills`、frontmatter 校验、`disable-model-invocation`
- `packages/coding-agent/src/core/system-prompt.ts` — 第 63-67 / 154-157 行把 skills 注入系统提示（仅当 read 工具可用时）

## Goals

- 模型无需用户手动输入命令，即可根据任务自主发现并调用匹配的 skill。
- 采用 pi 的渐进式披露：系统提示只注入 `name / description / location`，正文由模型经 read 工具按需加载，避免上下文膨胀。
- 保留现有 `/skill-name` 斜杠命令作为显式调用路径。
- 支持 `disable-model-invocation: true` frontmatter 开关：该 skill 不进系统提示，仅可显式调用。
- 对齐 pi 的目录/校验规范（SKILL.md 布局、name/description 校验）。
- 尊重 `--no-skills`：既不注入提示，也不注册命令。
- 仅当 read 工具在当前工具集中可用时才注入 skills（否则模型无法加载正文）。

## User Stories

### US-001: 扩展 skill frontmatter 支持 disable-model-invocation
**Description:** 作为 skill 作者，我希望能标记某个 skill 只允许显式调用，这样它不会被模型自动触发。

**Acceptance Criteria:**
- [ ] `SkillFrontmatter`（`internal/runtime/skills.go`）新增 `DisableModelInvocation bool`，映射 YAML 键 `disable-model-invocation`
- [ ] frontmatter 中缺省该键时，字段为 `false`
- [ ] `disable-model-invocation: true` 解析为 `true`
- [ ] `Skill` 结构体暴露该值，供提示注入时过滤使用
- [ ] 新增单测覆盖：缺省、`true`、`false` 三种情况
- [ ] Typecheck/lint（`go build ./...` 与 `go vet ./...`）通过

### US-002: 对齐 pi 的 name / description 校验规范
**Description:** 作为维护者，我希望 skill 的 name 与 description 遵循 Agent Skills 规范，避免非法名字进入系统提示或命令表。

**Acceptance Criteria:**
- [ ] name 校验：仅允许小写 `a-z`、`0-9`、连字符；长度 ≤ 64；不以连字符开头/结尾；不含连续 `--`
- [ ] description 必填且长度 ≤ 1024
- [ ] 校验失败按 pigo 现有"部分解析失败不致命"策略处理：跳过该 skill 并把原因累加进返回的 error（与当前 `LoadSkillsDir` 行为一致），不影响其它 skill 加载
- [ ] name 缺省时回退为父目录名或文件基名（保持现有回退逻辑）
- [ ] 新增单测覆盖每条校验规则的通过与失败用例
- [ ] Typecheck/lint 通过

### US-003: 渲染 <available_skills> 系统提示块
**Description:** 作为开发者，我需要一个把 skill 列表格式化成 `<available_skills>` XML 块的函数，供注入系统提示。

**Acceptance Criteria:**
- [ ] 新增函数（如 `runtime.FormatSkillsForPrompt(skills []*Skill) string`）
- [ ] 输出对齐 pi 格式：引导语（"…Use the read tool to load a skill's file when the task matches its description…" 及相对路径解析说明）+ `<available_skills>`，每个 skill 含 `<name>` `<description>` `<location>`（`location` 为 SKILL.md 的绝对路径）
- [ ] 过滤掉 `DisableModelInvocation == true` 的 skill
- [ ] 可见 skill 为 0 时返回空字符串
- [ ] XML 特殊字符（`& < > " '`）被转义
- [ ] 新增单测：含/不含 disable 项、空列表、转义
- [ ] Typecheck/lint 通过

### US-004: 把 skills 注入系统提示
**Description:** 作为用户，我希望模型的系统提示里包含可用 skill 列表，这样模型能自主选用。

**Acceptance Criteria:**
- [ ] `PromptConfig`（`internal/runtime/prompt.go`）新增承载 skills 的字段（如 `Skills []*Skill`）与"read 工具是否可用"的信号（如 `ReadToolAvailable bool`）
- [ ] `BuildSystemPrompt` 在 base + 环境块 + AGENTS.md + append 之后追加 `FormatSkillsForPrompt` 的输出
- [ ] 仅当 read 工具可用且存在可见 skill 时才注入；否则系统提示与现状完全一致
- [ ] `--no-skills` 时不注入（skills 列表为空）
- [ ] 新增单测：注入内容正确、无 read 工具时不注入、无 skill 时不注入
- [ ] Typecheck/lint 通过

### US-005: 在 run/repl 装配路径打通 skills 到提示
**Description:** 作为用户，我在实际运行 pigo 时，加载到的 skills 能真正出现在系统提示里并被模型调用。

**Acceptance Criteria:**
- [ ] `cmd/pigo/run.go` 的 `setupAgentEnv` 在构建工具集后，把已加载的 skills 与 read 工具可用性传入 `BuildSystemPrompt`（注意当前 `BuildSystemPrompt` 调用早于 `builtinTools`，需调整装配顺序或先探测工具集）
- [ ] skills 从 `skillsDir()`（`~/.agents/skills` 或 `PIGO_SKILLS_DIR`）经 `LoadSkillsDir` 加载，与斜杠命令共用同一份加载结果，避免重复读盘
- [ ] 手动验证：在 skills 目录放一个 weather skill，不输入 `/weather`，向模型提出天气相关请求，模型自主 read 该 SKILL.md 并按其指示执行
- [ ] 手动验证：`--no-skills` 时系统提示不含 `<available_skills>`
- [ ] Typecheck/lint 通过

### US-006: /skill-name 显式调用与 disable-model-invocation 并存
**Description:** 作为老用户，我希望 `/weather` 依然可用；被标记 disable 的 skill 虽不进系统提示，但仍能显式调用。

**Acceptance Criteria:**
- [ ] 所有 skill（含 `disable-model-invocation: true`）仍注册为 `/skill-name` 斜杠命令，行为与现状一致（body 展开为下一轮 prompt，支持 `$ARGUMENTS`）
- [ ] `disable-model-invocation: true` 的 skill 不出现在 `<available_skills>`，但 `/skill-name` 仍能调用
- [ ] 新增单测：disable 项被斜杠命令注册、同时被提示注入排除
- [ ] Typecheck/lint 通过

### US-007: 文档与帮助更新
**Description:** 作为用户，我希望文档说明模型自动调用 skill 的机制与 `disable-model-invocation` 开关。

**Acceptance Criteria:**
- [ ] README 或 skills 相关文档说明：skill 现可被模型自动调用（渐进式披露），以及如何用 `disable-model-invocation` 关闭
- [ ] 说明仅当 read 工具可用时自动调用才生效
- [ ] 与现有文档风格一致

## Functional Requirements

- FR-1: 系统必须解析 skill frontmatter 中的 `disable-model-invocation` 布尔键，缺省为 `false`。
- FR-2: 系统必须按 Agent Skills 规范校验 skill 的 name（小写字母/数字/连字符、≤64、无首尾及连续连字符）。
- FR-3: 系统必须要求 description 非空且 ≤1024 字符。
- FR-4: 校验失败时，系统必须跳过该 skill 并把原因累加进非致命 error，不影响其它 skill 加载。
- FR-5: 系统必须提供一个把 skill 列表渲染为 `<available_skills>` 块（含 name/description/location）的格式化函数。
- FR-6: 格式化函数必须排除 `disable-model-invocation: true` 的 skill。
- FR-7: 格式化函数必须转义 XML 特殊字符。
- FR-8: 系统必须在 read 工具可用且存在可见 skill 时，把 `<available_skills>` 块追加到系统提示末尾。
- FR-9: 当 read 工具不可用或无可见 skill 时，系统必须保持系统提示与现状一致（不注入）。
- FR-10: 系统必须在 `--no-skills` 下既不注入提示也不注册斜杠命令。
- FR-11: 系统必须继续把所有 skill（含 disable 项）注册为 `/skill-name` 斜杠命令，行为与现状一致。
- FR-12: 系统必须在 `<location>` 中给出 SKILL.md 的绝对路径，供模型用 read 工具加载。

## Non-Goals

- 不引入 pigo 已有但未使用的 `SkillTool` / `SubAgentTool` 作为自动调用路径（本次采用 pi 的渐进式披露，不启用子 agent 工具方案）。
- 不改变 skill body 的执行语义（body 仍作为斜杠命令展开或由模型读取后遵循，不做二次编排）。
- 不实现项目级 `.pi/skills` 与用户级目录的多源合并/冲突检测（pi 有，但超出本次范围，可后续迭代）。
- 不实现 `.gitignore` 式忽略规则（pi 的 ignore matcher 不在本次范围）。
- 不改变 skills 的加载目录（仍为 `~/.agents/skills` / `PIGO_SKILLS_DIR`）。
- 不做遥测、指标或 UI 层的 skill 调用可视化。

## Technical Considerations

- **注入位置**：`internal/runtime/prompt.go` 的 `BuildSystemPrompt` 是唯一的系统提示装配点，在 append 之后追加 skills 块最贴合 pi。
- **装配顺序**：`cmd/pigo/run.go:59` 当前先 `BuildSystemPrompt` 再 `builtinTools`（第 68 行）。要判断 read 工具是否可用，需要先确定工具集，或用一个轻量探测（检查是否 `noTools` 且工具名单含 `read`）。read 工具名为 `"read"`（`internal/agenttool/read_tool.go`）。
- **加载复用**：`LoadSkillsDir`（`internal/runtime/skills.go`）已返回 `[]*Skill`，斜杠命令与提示注入应共用同一次加载结果，避免重复 IO。
- **对齐 pi 的引导语**：建议直接沿用 pi 的措辞与相对路径解析说明（`formatSkillsForPrompt`，skills.ts:342-360），保证模型行为一致。
- **绝对路径**：`Skill.Path` 已保留源文件路径；`<location>` 用其绝对路径。
- **非致命解析**：现有 `LoadSkillsDir` 已用 `errors.Join` 累加跳过原因，新增校验应沿用该模式而非中断加载。

## Success Metrics

- 在 skills 目录放置一个 weather skill 后，用户用自然语言（不敲 `/weather`）提出天气请求，模型能自主 read 并执行该 skill。
- 系统提示中 `<available_skills>` 正确列出可见 skill，且排除 disable 项。
- `--no-skills` 与无 read 工具场景下，系统提示与改造前逐字节一致（除 skills 块外无回归）。
- 所有新增单测与现有测试通过，`go build ./...` / `go vet ./...` 无错误。

## Open Questions

- `BuildSystemPrompt` 与工具集构建的顺序调整，是就地探测 read 工具、还是把工具集构建提前？（实现时按最小改动决定）
- 是否需要在 skill 首次被模型自动调用时给用户一条可见提示（pi 有 `skill-invocation-message` 组件）？本次默认不做，留待迭代。
- 未来是否支持项目级 `.pigo/skills` 目录与多源合并？
