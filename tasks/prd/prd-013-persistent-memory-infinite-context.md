# PRD: 持久记忆系统 + 无限上下文

## Introduction

本功能为 pigo 增加两项能力，参考 MiMo-Code（opencode 的分支）的实现：

1. **持久记忆系统（Persistent Memory）** — 让 agent 把跨会话有价值的知识（用户偏好、项目决策、经验反馈、外部资源指针、检查点摘要）写入磁盘上的 Markdown 文件，并通过一个 **SQLite FTS5 全文索引**支持按相关度检索，在会话开始或需要时自动注入相关记忆，从而不必每次重新学习项目上下文。

2. **无限上下文（Infinite Context）** — 当对话逼近模型上下文窗口上限时，不再简单地做一次性有损压缩，而是结合两种机制：(a) **检查点重建**——在最近一个成功检查点处插入边界，早期消息坍缩为检查点摘要、近期消息逐字保留；(b) **检索召回**——按需从持久记忆库检索相关片段回注上下文。二者叠加，使 agent 能在超长/多会话任务中持续保持关键上下文而不溢出。

pigo 当前已有：基于文件的 auto-memory 约定（`MEMORY.md` 索引 + 类型化记忆文件，见系统提示词）、`internal/compaction` 的有损摘要式压缩、`internal/session` 的会话持久化、`internal/runtime` 的 per-turn system-reminder 注入机制。本功能在这些既有基础上**增量增强**，默认开启（可通过配置关闭）。

本文档面向实现者（可能是初级工程师或 AI agent），术语尽量直白。

## Goals

- 提供一个跨会话持久、可全文检索（SQLite FTS5 + BM25 排序）的记忆库，复用 pigo 现有 `MEMORY.md` + 类型化记忆文件的磁盘布局。
- 记忆写入由 agent 通过工具主动完成（对齐 pigo 现状），系统提示词引导其行为；无需引入后台自动写入子系统。
- 在检索前对磁盘记忆文件做懒重建索引（reconcile），覆盖工具外的手工编辑；用 `size-mtime` 指纹避免重复索引。
- 会话开始/恢复时自动注入相关记忆；agent 也可显式检索。
- 实现"无限上下文"：检查点重建（分层摘要坍缩 + 近期逐字保留）与检索召回二者结合，在逼近窗口上限时保留关键上下文，无可用检查点时回退到现有有损压缩。
- 压缩/重建进行中向用户显示英文进行态提示（复用已实现的 "Compacting conversation…" 机制，新增 "Preparing conversation context…"）。
- 默认开启，且对现有用户零迁移成本（复用现有记忆目录与约定）；提供配置项可关闭或调参。

## User Stories

### US-001: 引入 SQLite FTS5 记忆存储层
**Description:** As a pigo 开发者, I want 一个纯 Go 的 SQLite + FTS5 存储层, so that 记忆文件可被全文索引与按相关度检索，且不引入 CGO 依赖。

**Acceptance Criteria:**
- [ ] 新增 `internal/memory` 包，选用纯 Go SQLite 驱动（如 `modernc.org/sqlite`，精确 pin 版本）。
- [ ] 建表：`memory_index`（path 主键、scope、scope_id、type、body、fingerprint、last_indexed_at）+ FTS5 虚拟表 `memory_fts`（tokenize=`unicode61 remove_diacritics 1`，`content=memory_index`）。
- [ ] 建立 INSERT/DELETE/UPDATE 同步触发器，保持 FTS 索引与内容表一致。
- [ ] 数据库文件落在 pigo 数据目录下（如 `~/.claude/projects/<slug>/memory.db` 或全局数据目录），路径可配置。
- [ ] 迁移在首次访问时幂等创建；已存在则跳过。
- [ ] 单元测试覆盖建表、触发器同步、重复迁移安全。
- [ ] `go build ./...` / `go vet ./...` / `go test ./...` 通过。

### US-002: 记忆文件路径解析与作用域
**Description:** As a 记忆系统, I need 从磁盘路径解析出记忆的 scope / scope_id / type, so that 索引与检索能按维度过滤。

**Acceptance Criteria:**
- [ ] 支持 scope：`global`（跨项目用户偏好）、`projects`（项目级）、`sessions`（会话级），可选 `cc`（Claude Code 兼容目录）。
- [ ] 支持 type：`user` / `feedback` / `project` / `reference`（对齐 pigo 现有类型化记忆）+ `checkpoint` / `progress` / `notes` / `free`。
- [ ] 从路径模式与 YAML frontmatter（`metadata.type`）推断 type；无法判定时归为 `free`。
- [ ] `resolveProjectId(absRepoPath)` 用 sha256(路径) 前 12 位生成稳定 project id。
- [ ] 路径构造有防目录穿越保护（拒绝 `..` 与前导 `/`）。
- [ ] 单元测试覆盖各 scope/type 的解析与非法路径拒绝。
- [ ] Typecheck/lint 通过。

### US-003: 索引重建（reconcile）与懒同步
**Description:** As a 记忆系统, I want 扫描磁盘记忆目录并把新增/变更文件索引进 FTS、删除已消失文件的索引行, so that 索引始终与磁盘一致，且覆盖工具外的手工编辑。

**Acceptance Criteria:**
- [ ] 递归遍历记忆根目录（及可选 cc 根）收集所有 `.md` 文件。
- [ ] 用 `size-mtime` 指纹判断是否需要重新索引（命中指纹则跳过）。
- [ ] 磁盘上不存在的索引行被剪除（prune）。
- [ ] 检索前按配置开关执行懒 reconcile（默认开启）。
- [ ] reconcile 返回 `{indexed, pruned}` 计数。
- [ ] 单元测试覆盖：新增文件被索引、变更文件被更新、删除文件被剪除、指纹命中跳过。
- [ ] Typecheck/lint 通过。

### US-004: 相关度检索（BM25 + 分数下限）
**Description:** As an agent, I want 用自由文本查询检索记忆并得到按相关度排序的片段, so that 我能召回与当前任务最相关的历史知识。

**Acceptance Criteria:**
- [ ] `Search(query, {scope?, scope_id?, type?, limit?})` 返回 `[{path, snippet, score, scope, scope_id, type}]`。
- [ ] 查询构造：将自由文本按 Unicode 分词（`\p{L}\p{N}_`，含 CJK），每词转为短语引用并 OR 连接，避免 FTS5 特殊字符崩溃、保持召回。
- [ ] 用 BM25 排序（转换为越大越好），并应用相对分数下限（默认 0.15，保留第一名，剔除只匹配常见词的噪声），过取 3x 再截断。
- [ ] 空/无有效 token 的查询返回空结果，不发 SQL。
- [ ] 支持 scope/scope_id/type 过滤。
- [ ] 单元测试覆盖：多词 OR 召回、分数下限剔除、过滤条件、空查询。
- [ ] Typecheck/lint 通过。

### US-005: 记忆读写工具（agent 工具驱动）
**Description:** As an agent, I want 通过工具读取、写入、检索记忆, so that 我能按系统提示词引导主动维护记忆库。

**Acceptance Criteria:**
- [ ] 在 `internal/agenttool` 注册记忆工具：至少 `memory_search`（检索）；写入复用现有 Write/Edit（写到记忆目录）或提供 `memory_write`。
- [ ] 写入的文件带规范 frontmatter（name/description/metadata.type），与 pigo 现有类型化记忆格式一致。
- [ ] 写入后索引在下次检索的懒 reconcile 中自动更新（无需显式重建调用）。
- [ ] 系统提示词更新：说明记忆工具的存在、何时写/读、类型语义（复用现有 auto-memory 提示词，补充检索工具用法）。
- [ ] 写入路径受防目录穿越保护。
- [ ] 单元测试覆盖工具的检索/写入行为。
- [ ] Typecheck/lint 通过。

### US-006: 会话恢复时自动注入相关记忆
**Description:** As a 用户, I want 恢复会话或开新会话时相关记忆被自动注入, so that agent 不必重学项目上下文。

**Acceptance Criteria:**
- [ ] 通过现有 per-turn system-reminder / system-prompt 注入机制（`internal/runtime/reminder.go` / `prompt.go`）注入。
- [ ] 注入内容受 token 预算约束（可配置的 per-section 上限），避免挤占窗口。
- [ ] `MEMORY.md` 索引优先注入；相关类型化记忆按检索相关度补充。
- [ ] 注入内容标注为记忆来源，不污染最终输出或持久历史。
- [ ] 单元测试覆盖注入内容与预算裁剪。
- [ ] Typecheck/lint 通过。

### US-007: 检查点写入（无限上下文之一）
**Description:** As a 记忆系统, I want 在上下文填充达到阈值时把对话蒸馏为检查点摘要写入 `checkpoint.md`, so that 后续可在检查点边界坍缩早期消息。

**Acceptance Criteria:**
- [ ] 在填充阈值（可配置，如 `["40%","60%","80%"]`）触发检查点摘要生成。
- [ ] 检查点由摘要 LLM 调用产生（可复用 `internal/compaction/summary.go` 的摘要能力），写入会话 scope 的 `checkpoint.md`。
- [ ] 检查点记录其覆盖的消息水位（watermark），供重建时定位边界。
- [ ] 检查点失败为非致命：记录并回退，不中断主运行。
- [ ] 单元测试覆盖阈值触发与 watermark 记录。
- [ ] Typecheck/lint 通过。

### US-008: 上下文重建（无限上下文之二）
**Description:** As a 记忆系统, I want 逼近窗口上限时在最近检查点处插入边界、早期消息坍缩为检查点摘要、近期消息逐字保留, so that 上下文不溢出且保留关键近期细节。

**Acceptance Criteria:**
- [ ] 触发条件基于模型可用窗口（`limit.input` 或 `limit.context`）减去 reserve，可被 `compaction.max_context` 进一步下调。
- [ ] 重建在最近成功检查点处插入压缩边界；边界前坍缩为摘要，边界后（近期若干 turn / preserve_recent_tokens）逐字保留。
- [ ] 若检查点写入仍在进行，重建等待其完成，并显示 "Preparing conversation context…" 进行态提示。
- [ ] 无可用检查点时，回退到现有 `internal/compaction` 的有损压缩。
- [ ] 提供 `/rebuild` slash 命令手动触发重建（TUI + REPL 均可用）。
- [ ] 发出对应事件（复用/扩展 `CompactionEvent` 或新增 `RebuildEvent`），headless stream-json 可见。
- [ ] 单元测试覆盖：有检查点走重建、无检查点回退压缩、边界与近期保留正确。
- [ ] Typecheck/lint 通过。

### US-009: 检索召回增强压缩（无限上下文之三）
**Description:** As a 记忆系统, I want 在压缩/重建后按需从持久记忆库检索相关片段回注上下文, so that 被坍缩的关键信息仍可在需要时被召回。

**Acceptance Criteria:**
- [ ] 压缩/重建后，基于近期查询/任务上下文调用 `memory_search` 召回相关片段。
- [ ] 召回片段以受预算约束的形式注入（复用 US-006 注入通道）。
- [ ] 召回不重复注入已在上下文中的内容。
- [ ] 单元测试覆盖召回注入与去重。
- [ ] Typecheck/lint 通过。

### US-010: 配置项与默认值
**Description:** As a 用户, I want 通过配置调节记忆与上下文行为并能整体关闭, so that 我可以按需裁剪。

**Acceptance Criteria:**
- [ ] 新增配置键（对齐 pigo 现有 config 分层）：`memory.enabled`（默认 true）、`memory.reconcile_on_search`（默认 true）、`memory.search_score_floor`（默认 0.15）、`memory.cc_index`（默认 false）。
- [ ] `checkpoint.thresholds`（默认 `["40%","60%","80%"]`）、`checkpoint.reserved`、`checkpoint.push_caps.*`（per-section 注入上限）。
- [ ] `compaction.max_context`（可为 token 数 / "300K" / "1M" / "50%"，始终被 provider 上限钳制，仅能下调触发点）。
- [ ] 默认全部开启且零迁移（复用现有记忆目录）；`memory.enabled=false` 时完全退回现有行为。
- [ ] 单元测试覆盖默认值与关闭开关。
- [ ] Typecheck/lint 通过。

### US-011: `/memory` 与 `/rebuild` 状态可见性
**Description:** As a 用户, I want 查看记忆库状态并手动触发重建/检索, so that 我能理解与控制记忆行为。

**Acceptance Criteria:**
- [ ] `/rebuild` 手动触发上下文重建（US-008）。
- [ ] `/memory`（或 `/status` 扩展）显示：记忆条目数、当前窗口与触发点、当前上下文占用。
- [ ] TUI 与 REPL 均支持上述命令。
- [ ] 单元测试覆盖命令注册与基本行为。
- [ ] Typecheck/lint 通过。

## Functional Requirements

- FR-1: 系统必须提供一个纯 Go 的 SQLite 存储层，包含 `memory_index` 内容表与 `memory_fts` FTS5 虚拟表及其同步触发器。
- FR-2: 系统必须在首次访问时幂等执行 schema 迁移。
- FR-3: 系统必须从磁盘路径与 frontmatter 解析记忆的 scope、scope_id、type。
- FR-4: 系统必须用 sha256 路径前缀生成稳定 project id。
- FR-5: 系统必须对记忆写入路径施加防目录穿越保护。
- FR-6: 系统必须提供 reconcile：索引新增/变更文件、剪除已删除文件、用 size-mtime 指纹跳过未变更文件。
- FR-7: 系统必须在检索前按配置执行懒 reconcile（默认开启）。
- FR-8: 系统必须提供 BM25 排序的全文检索，支持 scope/scope_id/type 过滤、Unicode（含 CJK）分词、OR 连接短语查询、相对分数下限过滤。
- FR-9: 系统必须提供 agent 可调用的记忆检索工具（`memory_search`）与写入路径。
- FR-10: 系统必须在会话恢复/开始时通过现有注入机制注入相关记忆，且受 token 预算约束。
- FR-11: 系统必须在填充阈值触发时把对话蒸馏为检查点摘要写入 `checkpoint.md` 并记录 watermark。
- FR-12: 检查点失败必须为非致命，不中断主运行。
- FR-13: 系统必须在逼近窗口上限时执行检查点重建（边界坍缩 + 近期逐字保留）。
- FR-14: 当无可用检查点时，系统必须回退到现有有损压缩。
- FR-15: 系统必须在重建等待检查点时显示 "Preparing conversation context…" 进行态提示。
- FR-16: 系统必须提供 `/rebuild` 手动触发重建，TUI 与 REPL 均可用。
- FR-17: 系统必须在压缩/重建后按需检索召回相关记忆片段并去重注入。
- FR-18: 系统必须提供 `memory.*` / `checkpoint.*` / `compaction.max_context` 配置键，默认全部开启且零迁移。
- FR-19: 当 `memory.enabled=false` 时，系统必须完全退回现有文件式 auto-memory + 有损压缩行为。
- FR-20: 系统必须为重建/召回发出可观测事件，headless stream-json 可见。

## Non-Goals (Out of Scope)

- 不实现后台自动记忆写入子系统（如 MiMo 的 checkpoint-writer 子 agent 独立编排、`/dream` 自动巩固、`/distill`）——本期记忆写入由 agent 工具驱动。
- 不实现语音、compose 工作流、goal 自治循环等 MiMo 其他特性。
- 不引入 CGO 版 SQLite（坚持纯 Go 驱动）。
- 不实现跨机器/云端记忆同步。
- 不改变现有会话持久化的磁盘格式（仅新增记忆库与检查点文件）。
- 不实现向量/语义 embedding 检索（本期仅 FTS5 全文 + BM25）。
- 不替换现有 `MEMORY.md` 约定（在其基础上增量增强）。

## Design Considerations

- 复用 pigo 现有记忆目录布局（`~/.claude/projects/<slug>/memory/` 的 `MEMORY.md` + 类型化文件）与系统提示词中已定义的类型语义（user/feedback/project/reference）。
- 复用 per-turn system-reminder 注入通道（`internal/runtime/reminder.go`）做记忆/召回注入，确保注入内容不进入持久历史。
- 进行态提示复用已实现的 spinner pin 机制（"Compacting conversation…"），新增 "Preparing conversation context…"。
- 检查点摘要复用 `internal/compaction/summary.go` 的摘要 LLM 调用能力，避免重复实现。

## Technical Considerations

- **依赖**：新增 `modernc.org/sqlite`（纯 Go，无 CGO），精确 pin 版本；确认与现有 `go.mod` 兼容、跨平台（darwin/linux）构建通过。该驱动由 SQLite amalgamation 转译而来，**已内置 FTS5**，因此 FTS5 全文索引方案在无 CGO 前提下可行——实现时无需额外编译标记，但应在 US-001 加一个"FTS5 虚拟表可创建"的冒烟测试作为依赖去风险。
- **集成点**：
  - `internal/memory`（新包）：存储层、路径解析、reconcile、检索。
  - `internal/agenttool/registry.go`：注册 `memory_search` / 写入工具。
  - `internal/runtime/loop.go`（`maybeAutoCompact` 附近）与 `internal/compaction`：检查点触发、重建、回退。
  - `internal/runtime/reminder.go` / `prompt.go`：记忆注入。
  - `internal/session`：检查点文件与 watermark 关联会话。
  - `internal/cli/tui` 与 `internal/cli/repl`：`/rebuild`、`/memory` 命令 + 进行态提示。
  - config 分层：新增 `memory.*` / `checkpoint.*` / `compaction.max_context`。
- **性能**：reconcile 用指纹避免全量重索引；检索过取 3x 后截断；注入受 per-section token 上限约束。
- **窗口解析**：触发点 = provider 可用窗口（`limit.input` 优先，否则 `limit.context`）− reserve，可被 `max_context` 下调，且始终被 provider 上限钳制。
- **并发**：检查点写入与重建可能并发；重建须等待进行中的检查点写入完成。

## Success Metrics

- 在跨多次会话的同一项目任务中，agent 无需用户重述即可引用先前决策/偏好（人工验证）。
- 超长对话（超过单窗口）能持续运行不因溢出中断；有检查点时走重建、无检查点时回退压缩，均不丢失近期逐字上下文。
- 记忆检索对多词/CJK 查询返回相关结果，常见词噪声被分数下限有效剔除。
- 默认开启后，现有用户无需迁移即可获得记忆注入；`memory.enabled=false` 可完全恢复旧行为。
- 构建/测试/vet 全绿，新增依赖跨平台构建通过。

## Open Questions

- SQLite 数据库文件的确切落盘位置：随项目（`~/.claude/projects/<slug>/memory.db`）还是全局单库多 scope？（倾向后者，便于跨项目 global 记忆检索。）
- `memory_search` 是否需要作为显式工具暴露给 agent，还是仅由运行时在注入阶段内部调用？（本 PRD 假设二者皆有：显式工具 + 内部召回。）
- 检查点摘要与现有 `compaction/summary.go` 的摘要 prompt 是否需要区分（检查点更强调结构化 ledger）？
- `cc` scope（索引 Claude Code 记忆）默认关闭，是否需要隐私提示？
- 是否需要在本期就提供记忆的 TTL/衰减或去重巩固（目前依赖 agent 手工维护 + `/dream` 留作未来）？
