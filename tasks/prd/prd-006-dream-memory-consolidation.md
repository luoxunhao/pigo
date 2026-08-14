# PRD: `/dream` — 记忆固结（Memory Consolidation）

## Introduction

pigo 已经具备一套持久记忆体系（`internal/memory/`）：按 scope（global / projects / sessions）与 type（user / feedback / project / reference / checkpoint / progress / notes）组织的 Markdown 记忆文件、`MEMORY.md` 索引、FTS 全文检索、以及会话 `checkpoint.md`。随着使用时间变长，记忆会**碎片化**：同一事实被多次记录、彼此矛盾、引用的文件路径已失效、单个 scope 下积累大量零散条目。检索质量下降，注入上下文的预算被浪费。

`/dream` 引入一个**周期性记忆固结**机制，模仿 MiMo Code 的同名能力。它由一个**独立子进程 Agent** 读取「当前项目 + 全局」的现有记忆文件与近期会话原始记录（session JSONL），执行**合并、去重、路径有效性校验、压缩，并主动剔除过期或矛盾的旧记忆**，把分散的记忆收敛成一份紧凑的「当前状态」，回写到 global 与当前 project 的记忆文件。触发方式为：会话启动时若距上次 dream ≥ 配置间隔（默认 7 天）则**静默后台触发**；也可手动 `/dream` 立即运行。目标是让 pigo「越用越懂你」——不必每次从零开始，而是带着对用户与项目的理解持续成长。

面向读者：实现本功能的开发者或 AI Agent。

## Goals

- 提供 `/dream` slash 命令，手动即时触发一次记忆固结。
- 会话启动时自动检测：距上次成功 dream ≥ 配置间隔（默认 7 天）则在后台静默触发，不阻塞用户当前会话。
- 固结范围覆盖**当前项目记忆 + 全局记忆**，并读取**近期会话 JSONL 原始记录**提炼此前未沉淀的新记忆。
- 固结操作包含：**合并**同类记忆、**去重**重复条目、**校验并清理**失效的文件/路径引用、**压缩**为紧凑当前态、**主动剔除**过期或与新信息矛盾的旧记忆。
- 固结在**独立子进程**（`pigo --subagent-rpc`）中执行，与主会话崩溃隔离。
- 触发间隔可通过 `config.toml` 配置（默认 7 天），并可关闭自动触发。
- 每次固结后回写 `MEMORY.md` 索引并重建 FTS 检索，保证后续 `memory_search` 命中固结后的紧凑记忆。

## User Stories

### US-001: `[dream]` 配置表与间隔状态记录
**Description:** As a pigo user, I want to configure the dream interval and have pigo track when dream last ran, so that auto-trigger fires at the right cadence and I can disable it.

**Acceptance Criteria:**
- [ ] `config.toml` 支持 `[dream]` 表，键：`enabled`（bool，默认 true）、`interval_days`（int，默认 7）；`enabled=false` 时永不自动触发。
- [ ] 上次成功 dream 的时间戳（RFC 3339 UTC）持久化到记忆根下的固定位置（如 `<memoryRoot>/global/dream/state.json`），字段含 `last_run_at`、`last_status`。
- [ ] 提供函数判定「是否到期」：`now - last_run_at >= interval_days`；无历史记录（首次）视为到期。
- [ ] 配置缺省时全部走默认值，解析不因缺少 `[dream]` 表而报错。
- [ ] Typecheck / `go vet` / 相关单测通过。

### US-002: 独立子进程 dream Agent 骨架与固结提示词
**Description:** As a developer, I need a dream Agent that runs in an isolated subprocess with a dedicated consolidation prompt, so consolidation never corrupts or blocks the parent session.

**Acceptance Criteria:**
- [ ] 复用现有 `pigo --subagent-rpc` 子进程机制启动一个 dream Agent，拥有全新上下文与文件读写工具。
- [ ] dream Agent 使用专用系统提示词，明确其任务边界：仅在「当前 project + global」scope 内合并/去重/校验路径/压缩/剔除过期矛盾条目；禁止写入用户源码目录。
- [ ] 子进程崩溃或超时不影响父会话；父会话捕获错误并记录 `last_status=failed`。
- [ ] dream Agent 完成后返回一份结构化变更报告（合并 N、去重 N、清理失效路径 N、剔除过期 N、压缩前后条目/字节数）。
- [ ] Typecheck / `go vet` / 相关单测通过。

### US-003: 读取现有记忆文件并执行合并/去重/压缩
**Description:** As a user, I want scattered duplicate and overlapping memories merged into a compact current state, so retrieval stays sharp and context budget isn't wasted.

**Acceptance Criteria:**
- [ ] dream Agent 枚举当前 project scope 与 global scope 下的全部 `*.md` 记忆文件（不含 sessions/checkpoint）。
- [ ] 语义相同或高度重叠的条目被合并为单条，保留最新且信息量最大的表述。
- [ ] 完全重复的条目被去重，仅保留其一。
- [ ] 合并后按 type/topic 组织，产出比输入更紧凑的记忆文件集（总字节数不增加，除非有新提炼记忆加入）。
- [ ] 回写后 `MEMORY.md` 索引条目与实际记忆文件一一对应，无悬挂链接。
- [ ] Typecheck / `go vet` / 相关单测通过。

### US-004: 校验并清理失效路径引用
**Description:** As a user, I want memories that reference files/paths no longer existing to be flagged and cleaned, so stale pointers don't mislead future sessions.

**Acceptance Criteria:**
- [ ] 对记忆正文中出现的本地文件路径引用（绝对路径或相对项目根的路径）逐一校验是否仍存在。
- [ ] 失效路径所在条目：若整条已失去意义则剔除；若仍有部分价值则删除失效引用并保留其余内容。
- [ ] 校验不触碰非路径文本（URL、外部系统引用不因本地不存在而删除）。
- [ ] 变更报告列出被清理的失效路径数量。
- [ ] Typecheck / `go vet` / 相关单测通过。

### US-005: 从近期会话 JSONL 提炼新记忆
**Description:** As a user, I want dream to distill durable facts from recent session transcripts that were never saved as memory, so the agent keeps learning from actual usage.

**Acceptance Criteria:**
- [ ] dream Agent 读取自上次 dream 以来（或最近 N 条）当前项目相关的 session JSONL 记录。
- [ ] 从中提炼符合记忆类型标准的**持久事实**（user/feedback/project/reference），忽略一次性任务态与瞬时上下文。
- [ ] 新提炼记忆与既有记忆去重后合并，不产生与既有条目重复的新文件。
- [ ] 若近期无可提炼的持久事实，此步为 no-op 且报告标注「无新增」。
- [ ] Typecheck / `go vet` / 相关单测通过。

### US-006: 主动剔除过期与矛盾的旧记忆
**Description:** As a user, I want outdated or contradicted memories removed during dream, so the agent doesn't act on stale beliefs.

**Acceptance Criteria:**
- [ ] 当新信息与旧记忆矛盾时，保留新信息、剔除或订正旧条目，并在报告中标注。
- [ ] 明显时效性已过的条目（如已完成的临时计划、已废弃的配置）被剔除。
- [ ] 剔除决策保守：无法判定是否过期时保留条目（宁留勿误删）。
- [ ] 变更报告列出剔除条数及原因摘要。
- [ ] Typecheck / `go vet` / 相关单测通过。

### US-007: `/dream` 手动命令与 `--dry-run` 预演
**Description:** As a user, I want to run `/dream` on demand and preview changes with `--dry-run` before anything is written, so I stay in control of my memory.

**Acceptance Criteria:**
- [ ] `/dream` 在 TUI 中即时触发一次固结，运行期间显示进度提示，完成后打印变更摘要报告。
- [ ] `/dream --dry-run` 执行完整固结分析但**不写入任何文件**，仅打印将要发生的变更报告。
- [ ] `/dream` 出现在 `/help` 列表，附简短说明与 `--dry-run` 用法。
- [ ] 手动成功运行后更新 `last_run_at`（`--dry-run` 不更新）。
- [ ] headless 模式下 `--dream` 或等价入口可触发同一流程（供脚本/CI 调用）。
- [ ] Typecheck / `go vet` / 相关单测通过。

### US-008: 启动时到期检测与后台静默触发
**Description:** As a user, I want dream to run automatically in the background when it's due, so consolidation happens without me remembering to trigger it.

**Acceptance Criteria:**
- [ ] 会话启动时，若 `[dream].enabled=true` 且已到期，则在后台启动 dream 子进程，不阻塞用户输入或首条响应。
- [ ] 同一时刻仅允许一个 dream 运行（加锁/持有标记），防止并发覆盖。
- [ ] 后台 dream 完成时以非侵入方式提示（如状态行一行摘要），失败则静默记录 `last_status=failed` 不打扰用户。
- [ ] `[dream].enabled=false` 或未到期时，启动路径零额外开销、无子进程启动。
- [ ] Typecheck / `go vet` / 相关单测通过。

### US-009: 固结后重建索引与文档
**Description:** As a user, I want search and docs to reflect the consolidated memory, so `/dream` is discoverable and its output is immediately usable.

**Acceptance Criteria:**
- [ ] 固结写回后触发 `memory.Reconcile`（重建 FTS 索引、清理已删除文件的行）。
- [ ] `memory_search` 在固结后命中的是紧凑后的条目，不再返回已合并/剔除的旧碎片。
- [ ] `docs/web/slash-commands.html` 内置命令表新增 `/dream` 行（中/EN 双语）。
- [ ] `docs/web/features.html` 与/或 `configuration.html` 记忆相关章节补充 dream 固结与 `[dream]` 配置说明（中/EN 双语）。
- [ ] Typecheck / `go vet` 通过；在浏览器验证文档页渲染（例如通过 `run` skill）。

## Functional Requirements

- FR-1: 系统必须支持 `config.toml` 中的 `[dream]` 表，含 `enabled`（默认 true）与 `interval_days`（默认 7）。
- FR-2: 系统必须持久化上次成功 dream 的时间戳与状态到记忆根下的固定状态文件。
- FR-3: 系统必须提供到期判定：`now - last_run_at >= interval_days`，无记录视为到期。
- FR-4: 系统必须在会话启动且 `enabled=true` 且到期时，于后台独立子进程启动一次 dream。
- FR-5: 系统必须提供 `/dream` slash 命令以手动即时触发固结。
- FR-6: 系统必须支持 `/dream --dry-run`，执行分析但不写入文件。
- FR-7: dream Agent 必须在 `pigo --subagent-rpc` 子进程中运行，与父会话崩溃隔离。
- FR-8: dream Agent 必须仅在当前 project scope 与 global scope 的记忆文件范围内读写。
- FR-9: dream Agent 必须合并语义重叠的记忆条目为单条。
- FR-10: dream Agent 必须去除完全重复的记忆条目。
- FR-11: dream Agent 必须校验记忆正文中的本地路径引用，并清理失效引用。
- FR-12: dream Agent 必须把记忆压缩为不大于输入总量的紧凑当前态（新增提炼记忆除外）。
- FR-13: dream Agent 必须从上次 dream 以来的当前项目相关 session JSONL 中提炼新的持久记忆。
- FR-14: dream Agent 必须剔除过期或与新信息矛盾的旧记忆，判定不确定时保留。
- FR-15: 系统必须在固结写回后重建 FTS 索引（调用 `memory.Reconcile`）并更新 `MEMORY.md`。
- FR-16: 系统必须在每次固结（含后台）完成后产出结构化变更报告；手动模式打印给用户，后台模式以一行摘要非侵入提示。
- FR-17: 系统必须保证同一时刻仅有一个 dream 运行。
- FR-18: 系统必须在成功的非 dry-run 运行后更新 `last_run_at`。

## Non-Goals (Out of Scope)

- 不做跨机器/云端记忆同步。
- 不固结其他项目的记忆（仅当前 project + global）；不消化 sessions/checkpoint 的 checkpoint 内容作为长期记忆。
- 不提供记忆的图形化编辑 UI。
- 不引入向量/embedding 语义检索（沿用现有 FTS）。
- 不做记忆的版本历史管理或撤销栈（本 PRD 依用户选择 5C，只提供 `--dry-run` 预演，不做自动备份/回滚）。
- 不在每次会话结束时增量固结（仅按周期/手动触发）。
- 不改变现有 `memory_search`、`checkpoint`、`/memory`、`/rebuild` 的既有行为。

## Design Considerations

- `/dream` 的进度与报告展示应复用 TUI 现有 subagent panel / 状态行风格，保持视觉一致。
- 后台自动触发的完成提示必须低打扰（一行、可忽略），不得抢占用户正在进行的对话。
- 变更报告建议为紧凑表格式：`merged / deduped / paths-cleaned / pruned / distilled / bytes before→after`。
- 文档改动沿用现有双语 `data-en`/`data-zh` 静态站点约定。

## Technical Considerations

- 复用 `internal/memory`：scope/type 布局（`paths.go`）、`Reconcile`（`reconcile.go`，FTS 索引 + 删除文件剪枝）、`store.go`/`search.go`。dream 的「语义合并/剔除」是 Reconcile 之上的一层新逻辑，二者职责不同：Reconcile 负责索引一致性，dream 负责内容收敛。
- 复用子进程 subagent 机制（`internal/runtime/subagent.go`、`pigo --subagent-rpc` over stdio JSON-RPC）与嵌套守卫（dream Agent 不应再 spawn 子 Agent）。
- session JSONL 读取需定位当前项目相关会话文件（参考现有 session store 路径约定）。
- dream 状态文件与记忆根解析需与现有 `memoryRoot` 解析保持一致（见 `store.go` 注释）。
- 到期判定与启动触发挂载在 CLI run assembly（`cmd/pigo`）会话初始化路径，注意不拖慢冷启动。
- 并发锁：可用记忆根下的 lockfile 或状态文件中的 `running` 标记 + PID/时间戳，避免死锁遗留。

## Success Metrics

- 固结后同一 scope 的记忆文件总字节数与条目数下降（在有冗余时），报告可量化。
- 固结后 `memory_search` 对代表性查询不再返回重复/失效碎片。
- 后台自动触发对会话冷启动的额外延迟 < 用户可感知阈值（首条响应不被阻塞）。
- 连续多次 dream 幂等：对已固结、无新会话的记忆再跑一次，产出「无变更」。

## Open Questions

- session JSONL 提炼的时间窗口取「自上次 dream 以来」还是「最近 N 条会话」更合适？两者是否都要支持？
- 后台 dream 消耗 LLM token/费用，是否需要对模型或预算做限制（如仅用轻量模型 deepseek-v4-flash / haiku）？
- 记忆矛盾判定完全交给 LLM 判断，还是需要一层确定性规则（如按 `updated` 时间取新）兜底？
- `--dry-run` 报告是否需要精确到「逐条 diff」，还是聚合统计即可满足控制需求？
- 首次运行（无 `last_run_at`）在启动时是否也应静默触发，还是首次仅提示用户手动跑一次？
