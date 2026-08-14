# SPEC: `/dream` — 记忆固结（Memory Consolidation）

> Technical specification derived from: `tasks/prd/prd-006-dream-memory-consolidation.md`
> Generated: 2026-08-01 | Target branch: `master` | Base commit: `81c2e7d`

## 1. Summary

### 1.1 What This SPEC Covers
本 SPEC 描述 `/dream` 记忆固结功能的技术实现：一个在**独立子进程**中运行的、由 LLM 驱动的记忆固结 Agent，读取「当前 project + global」scope 的记忆文件与近期会话 JSONL，执行去重、路径校验、合并、剔除过期/矛盾条目、压缩，并回写紧凑当前态、重建 FTS 索引。覆盖手动 `/dream`（含 `--dry-run`）、会话启动时的到期后台触发、`[dream]` 配置表、状态与并发锁，以及文档更新。不涉及记忆云同步、embedding 检索、备份回滚栈。

### 1.2 PRD Reference
- Source: `tasks/prd/prd-006-dream-memory-consolidation.md`
- User Stories covered: US-001 ~ US-009
- Functional Requirements covered: FR-1 ~ FR-18

### 1.3 Design Decisions Summary
| Decision | Choice | Rationale |
|----------|--------|-----------|
| 子进程入口（Q1） | 新增 `pigo --dream` 内部标志，以自包含 headless agent 运行到完成、向 stdout 输出报告 JSON | `--subagent-rpc` 是父进程编排的 JSON-RPC 服务，需要驱动方常驻；周期性独立作业更适合"跑完即退"的一次性子进程，复用 headless 会话基建，天然崩溃隔离 |
| 固结执行分工（Q2） | 混合：Go 确定性做去重候选/路径校验/索引重建；LLM 做合并、过期/矛盾判定、JSONL 提炼 | 确定性部分可单测、可控；语义部分交给 LLM。降低 LLM 误删风险 |
| dream 模型（Q3） | 固定沿用主会话模型，不单独配置 | 保持一致的记忆理解质量；避免新增配置面。成本在 Non-Goals 之外由周期性触发天然摊薄 |
| 状态与锁（Q4） | `state.json`（状态）+ 独立 `dream.lock`（`O_EXCL` 原子创建，含 PID+started_at，超时接管陈旧锁） | 锁与状态分离，崩溃不污染状态；`O_EXCL` 保证互斥；超时接管避免死锁遗留 |
| JSONL 提炼窗口（Q5） | 优先"自上次 dream 以来"，无记录时回退"最近 N 条"（N 可配，默认 20） | 增量提炼；首次运行有合理上界 |
| 合并/剔除范围 | 仅 global + 当前 project scope 的 `*.md`（排除 sessions/checkpoint） | 与 PRD 3D 一致；checkpoint 是会话瞬时态，不作长期记忆 |

---

## 2. Architecture

### 2.1 System Context
```
交互式会话 (TUI/REPL)
  │  会话启动
  ├─► DreamScheduler.CheckDue()  ── 到期 & enabled ─► spawn 子进程（后台，非阻塞）
  │                                                      │
  │  用户输入 /dream[ --dry-run]                          │
  └─► DreamScheduler.RunNow() ───────────────────────────┤
                                                          ▼
                                    子进程: `pigo --dream [--dry-run] --cwd <proj>`
                                                          │
                                    ┌─────────────────────┴─────────────────────┐
                                    │ dream runner (internal/dream)              │
                                    │  1. acquire lock (dream.lock, O_EXCL)      │
                                    │  2. Go: 枚举记忆文件 + 去重候选 + 路径校验  │
                                    │  3. LLM agent: 合并/剔除/JSONL 提炼         │
                                    │  4. 写回 (dry-run 时跳过) + memory.Reconcile│
                                    │  5. 更新 state.json + 释放锁                │
                                    │  6. stdout ← Report JSON                    │
                                    └────────────────────────────────────────────┘
                                                          │
                          父进程读取 stdout 报告 → 手动: 打印全表 / 后台: 一行摘要
```

### 2.2 Component Design
新增包 `internal/dream`：
- **`Config`**（映射 `[dream]` TOML 表）：`Enabled bool`、`IntervalDays int`、`RecentSessions int`。
- **`State`**：`LastRunAt time.Time`、`LastStatus string`（`ok`/`failed`/`skipped`）、`LastReport Report`。读写 `<memoryRoot>/global/dream/state.json`。
- **`Scheduler`**：`Due(now) bool`、`RunNow(ctx, opts) (Report, error)`、`MaybeRunBackground(ctx)`。父进程侧，负责判定到期、spawn 子进程、解析报告。
- **`Lock`**：基于 `<memoryRoot>/global/dream/dream.lock` 的 `O_EXCL` 文件锁；写入 `{pid, started_at}`；`staleAfter`（默认 30min）后可接管。
- **`Runner`**：子进程侧固结主逻辑。分 `plan`（Go 确定性）与 `apply`（LLM agent + 写回）两阶段。
- **`Report`**：结构化变更统计（见 3.2）。
- **`prompt.go`**：dream Agent 系统提示词常量。

复用既有：`internal/memory`（`Store.Open`/`Root`/`Reconcile`/`paths`）、`internal/session`（`Store` 读 JSONL）、`internal/cli/headless`（agent 运行基建）、`cmd/pigo`（flag 解析、`--cwd`）。

### 2.3 Module Interactions
- **父进程**（`internal/cli/repl` 与 `cmd/pigo` 会话初始化）：调用 `dream.Scheduler`。`/dream` 拦截点与 `/compact`、`/rebuild` 同构（`repl.go` streamPrompt 循环内 `if line == "/dream" || strings.HasPrefix(line, "/dream ")`）。
- **子进程**（`cmd/pigo` 解析 `--dream` → 调 `dream.Runner.Run`）：独立完成固结，`os.Exit(0/1)`，报告经 stdout。
- **记忆写回后** 调 `memory.Reconcile(store)` 重建 FTS 索引 + 剪除已删文件行。

### 2.4 File Structure
```
cmd/pigo/
  main.go                         [MODIFY: 注册 --dream / --dream-dry-run 内部 flag，分派到 dream.Runner]
internal/dream/                   [NEW 包]
  config.go                       [NEW: Config + 默认值]
  state.go                        [NEW: State 读写 state.json]
  lock.go                         [NEW: O_EXCL 锁 + 陈旧接管]
  scheduler.go                    [NEW: Due / RunNow / MaybeRunBackground（父进程 spawn）]
  runner.go                       [NEW: 子进程固结主流程 plan+apply]
  plan.go                         [NEW: Go 确定性 —— 枚举/去重候选/路径校验]
  distill.go                      [NEW: 定位并读取近期项目 session JSONL]
  report.go                       [NEW: Report 结构 + 渲染（全表 / 一行摘要）]
  prompt.go                       [NEW: dream 系统提示词]
  *_test.go                       [NEW]
internal/cli/config/
  config.go                       [MODIFY: FileConfig 增加 Dream DreamConfig `toml:"dream"`]
internal/cli/repl/
  repl.go                         [MODIFY: 拦截 /dream；会话启动调 MaybeRunBackground]
  dream_repl.go                   [NEW: /dream 命令处理 + 报告打印]
internal/cli/tui/
  (会话启动路径)                    [MODIFY: 启动后台 dream 到期检查]
docs/web/
  slash-commands.html             [MODIFY: 新增 /dream 行]
  configuration.html              [MODIFY: [dream] 表说明]
  features.html                   [MODIFY: dream 固结章节]
```

---

## 3. Data Model

### 3.1 On-disk Layout
```
<memoryRoot>/global/dream/
  state.json      # DreamState（见 3.2）
  dream.lock      # 运行期存在；{ "pid": 12345, "started_at": "2026-08-01T09:00:00Z" }
```
记忆文件本身沿用现有布局（`<memoryRoot>/global/<type>/*.md`、`<memoryRoot>/projects/<projectId>/<type>/*.md`、各 scope 根的 `MEMORY.md`）。`memoryRoot` 解析复用 `memory` 包既有逻辑。

### 3.2 Entity Definitions
```go
// internal/dream/config.go
type Config struct {
    Enabled        bool `toml:"enabled"`         // 默认 true
    IntervalDays   int  `toml:"interval_days"`   // 默认 7
    RecentSessions int  `toml:"recent_sessions"` // 默认 20（无 last_run 时的回退窗口）
}

// internal/dream/state.go
type State struct {
    LastRunAt  time.Time `json:"last_run_at"`         // 零值表示从未运行
    LastStatus string    `json:"last_status"`         // "ok" | "failed" | "skipped"
    LastReport *Report   `json:"last_report,omitempty"`
}

// internal/dream/report.go
type Report struct {
    Merged        int      `json:"merged"`          // 合并掉的条目数
    Deduped       int      `json:"deduped"`         // 完全重复去重数
    PathsCleaned  int      `json:"paths_cleaned"`   // 清理的失效路径引用数
    Pruned        int      `json:"pruned"`          // 剔除的过期/矛盾条目数
    Distilled     int      `json:"distilled"`       // 从 JSONL 新提炼记忆数
    BytesBefore   int64    `json:"bytes_before"`
    BytesAfter    int64    `json:"bytes_after"`
    FilesBefore   int      `json:"files_before"`
    FilesAfter    int      `json:"files_after"`
    DryRun        bool     `json:"dry_run"`
    Notes         []string `json:"notes,omitempty"` // 剔除原因摘要等
    Reconciled    struct{ Indexed, Pruned int } `json:"reconciled"`
}
```

### 3.3 Config 映射
```go
// internal/cli/config/config.go — FileConfig 追加
Dream DreamConfig `toml:"dream"`

type DreamConfig struct {
    Enabled        *bool `toml:"enabled"`         // 指针区分"未设置"与 false；nil→true
    IntervalDays   int   `toml:"interval_days"`   // 0→默认 7
    RecentSessions int   `toml:"recent_sessions"` // 0→默认 20
}
```
`Enabled` 用 `*bool`：缺省（nil）视为 true，显式 `false` 才关闭。

### 3.4 Migration Plan
无数据库 schema 变更。`internal/memory` 的 SQLite FTS schema 不变（dream 只调用现有 `Reconcile`）。`state.json`/`dream.lock` 首次运行时惰性创建，目录 `global/dream/` 用 `os.MkdirAll`。向后兼容：旧安装无 `[dream]` 表与状态文件 → 首次会话启动视为"从未运行"，按 US-009 Open Question 决议（见 §11.1）处理首次触发。

---

## 4. Interfaces（CLI，非 HTTP）

### 4.1 命令与标志
| 入口 | 形式 | 描述 | 写入 | 触发方 |
|------|------|------|------|--------|
| 手动 | `/dream` | 即时固结，打印全表报告 | 是 | 用户（TUI/REPL） |
| 手动预演 | `/dream --dry-run` | 完整分析不写盘，打印将发生的变更 | 否 | 用户 |
| 后台自动 | 会话启动隐式 | 到期且 enabled 时后台 spawn | 是 | Scheduler |
| 子进程 | `pigo --dream [--dry-run] -C <projDir>` | 内部：真正执行固结，stdout 输出 Report JSON | 视 dry-run | 父进程 spawn |
| headless | `pigo --dream ...`（同上） | 供脚本/CI 直接调用 | 视 dry-run | 用户脚本 |

`--dream` 为内部标志（`flag.BoolVar`，usage 标 `internal:`），与现有 `--subagent-rpc` 同级。父进程用 `os.Executable()` + `--dream -C <projDir>` spawn，继承记忆根/模型相关环境。

### 4.2 子进程 stdout 契约
子进程仅向 **stdout** 写单行 JSON `Report`（`dry_run` 字段标识）；日志与进度走 **stderr**。退出码：`0` 成功、`1` 失败（父进程据此置 `last_status`）。父进程解析失败按 `failed` 处理，不影响会话。

### 4.3 Breaking Changes
无。纯新增。现有 `/memory`、`/rebuild`、`memory_search` 行为不变。

---

## 5. Business Logic

### 5.1 Core Algorithm — Runner.Run（子进程）
```
Run(ctx, opts{DryRun, ProjectDir}):
  1. store = memory.Open(...)；memoryRoot = store.Root()
  2. lock = acquireLock(memoryRoot); 若已被持有且未过期 → 退出码 0, status="skipped"
     defer lock.Release()
  3. plan = BuildPlan(store, projectDir):        # §5.1.1 确定性
       - 枚举 global + project scope 的 *.md（排除 sessions/checkpoint）
       - 计算 BytesBefore/FilesBefore
       - 精确去重候选（内容哈希相同 → dedupe 组）
       - 路径校验：提取正文本地路径引用，标记失效
       - 近重复候选配对（供 LLM 判定合并；用轻量相似度，如 归一化后的 token 集合 Jaccard ≥ 阈值）
  4. distillInput = collectRecentSessions(projectDir, state, cfg.RecentSessions)  # §5.3
  5. apply = LLM agent run（模型=主会话模型，dream 系统提示词）:
       输入 = plan 摘要 + 候选组 + 失效路径清单 + distillInput
       LLM 决策：确认合并、判定过期/矛盾剔除、从 distillInput 提炼新记忆
       产出：对记忆文件的最终写入集（合并后正文 / 删除的条目 / 新增文件）
  6. if DryRun: 仅据 apply 计划计算 Report 计数，不落盘
     else:
       - 应用确定性去重 + 路径清理 + LLM 合并/剔除/新增到记忆文件（原子写）
       - 更新受影响 scope 的 MEMORY.md 索引
       - memory.Reconcile(store) → Report.Reconciled
       - 计算 BytesAfter/FilesAfter
  7. if !DryRun: state.LastRunAt=now; state.LastStatus="ok"; state.LastReport=report; saveState
  8. stdout ← json(report); return
```

#### 5.1.1 确定性 vs LLM 分工（Q2 混合）
- **Go 确定性**：文件枚举、精确去重（内容哈希）、本地路径存在性校验、近重复候选配对、`MEMORY.md` 重写、`Reconcile`、字节/条目计数。
- **LLM**：近重复候选是否真应合并、合并后的紧凑表述、过期/矛盾条目的剔除判定（不确定→保留）、从 JSONL 提炼持久事实并归类 type。

### 5.2 Validation Rules
- 写入路径必须位于 `memoryRoot` 内的 global/project scope；越界写入被 Runner 拒绝（防 LLM 写用户源码）。
- 剔除保守：LLM 无法明确判定过期/矛盾时保留（PRD FR-14）。
- 路径校验只判定"本地文件路径"，不删除 URL / 外部系统引用（PRD US-004）。
- `interval_days <= 0` → 回落默认 7；`recent_sessions <= 0` → 默认 20。

### 5.3 近期会话收集（Q5 组合窗口）
```
collectRecentSessions(projectDir, state, N):
  sessions = sessionStore.List() 过滤 projectDir 匹配
  if state.LastRunAt 非零:
      取 UpdatedAt > LastRunAt 的会话
  else:
      取按 UpdatedAt 降序的前 N 条
  读取其 JSONL 内容作为 distill 输入（截断到预算上限）
```

### 5.4 并发与幂等
- 单机互斥：`dream.lock`（`O_EXCL`）。已运行则新触发直接 `skipped` 退出。
- 陈旧锁：`started_at` 早于 `now - staleAfter`（默认 30min）→ 接管并覆写。
- 幂等：对已固结、无新会话的记忆再跑 → LLM 无合并/剔除、无 distill → `Report` 全 0、字节不变（PRD Success Metric）。

### 5.5 Edge Cases
| 场景 | 处理 |
|------|------|
| 记忆目录为空/不存在 | plan 空，Report 全 0，status="ok"（Reconcile 对缺失 root 安全） |
| 锁被占用 | 退出码 0，status 不变（`skipped`），父进程后台静默 |
| LLM 调用失败/超时 | 子进程退出码 1，不写盘，父进程置 `failed` 静默记录 |
| dry-run | 不写盘、不更新 state、不置锁写状态（仍取锁防并发误导） |
| 无 API key（后台） | 子进程启动即失败 → `failed`，会话无感 |
| 首次运行（无 state） | 见 §11.1 决议 |
| session JSONL 无匹配项目会话 | distill no-op，Report.Distilled=0，Notes 记"无新增" |

---

## 6. Error Handling

### 6.1 Error Taxonomy
| 场景 | 退出码/状态 | 父进程行为 | 用户可见 |
|------|-------------|-----------|----------|
| 成功固结 | exit 0 / ok | 解析报告 | 手动:全表 / 后台:一行摘要 |
| 锁占用 | exit 0 / skipped | 无操作 | 后台:无；手动:提示"已有 dream 在运行" |
| LLM/IO 失败 | exit 1 / failed | 记录 last_status=failed | 后台:无打扰；手动:打印错误 |
| 报告 JSON 解析失败 | — | 视为 failed | 同上 |
| dry-run | exit 0 | 打印预演报告 | 全表，标注 DRY-RUN，不改 state |

### 6.2 Retry Strategy
后台 dream **不自动重试**（避免叠加 token 成本）；下次会话启动到期时自然再试。手动 `/dream` 由用户决定重跑。

### 6.3 Failure Modes
子进程崩溃/超时（父设 `context` 超时，默认 10min）→ kill 子进程，`failed`，会话不受影响。写回阶段采用逐文件原子写（临时文件 + rename），单文件失败不破坏其余文件；`Reconcile` 在部分写入后仍能收敛索引到磁盘现状。

---

## 7. Security

### 7.1 权限边界
dream 子进程仅被授予记忆读写（限定在 `memoryRoot` 的 global/project scope）+ 只读 session JSONL。**禁止**写用户工作目录源码；Runner 在应用写入前校验目标路径前缀。dream Agent 不得 spawn 子 Agent（复用现有嵌套守卫）。

### 7.2 Input Validation
session JSONL 与记忆文件均为本地可信数据，但 distill 提炼的新记忆需经 §5.2 的 type/scope 归类校验后落盘；LLM 产出的写入目标路径逐条前缀校验。

### 7.3 Data Protection
dream 不外发记忆内容到第三方（仅发给已配置的主模型 provider，与常规会话同）。`dream.lock` 含 PID/时间戳，无敏感信息。不新增审计日志（stderr 进度即诊断）。

---

## 8. Performance

### 8.1 Expected Load
每项目每 `interval_days`（默认 7 天）至多一次；手动按需。单次输入规模 = 记忆文件总量 + 近期会话窗口，通常远小于一次常规长会话。

### 8.2 Optimization Strategy
- 启动路径零开销：`Due()` 仅读 `state.json` 一次；未到期/未启用即返回，不 spawn。
- 后台 spawn 非阻塞：goroutine 内启动子进程，不拖慢首条响应（PRD US-008 / Success Metric）。
- distill 输入按 token 预算截断，避免超长 JSONL 撑爆上下文。
- 近重复配对用轻量集合相似度先筛，减少喂给 LLM 的候选量。

### 8.3 Storage Considerations
`Reconcile` 复用现有 FTS 增量索引（仅索引新增/变更、剪除已删）。写回用原子 rename，避免半写文件被后续读取。

---

## 9. Testing Strategy

### 9.1 Unit Tests
- `config`：`[dream]` 解析、缺省默认、`enabled=false`、`*bool` 语义。
- `state`：读写往返、零值（从未运行）、损坏 JSON 容错。
- `lock`：`O_EXCL` 互斥、陈旧锁接管、正常释放。
- `scheduler.Due`：到期/未到期/无 state/禁用 的边界。
- `plan`：精确去重分组、路径校验（存在/失效/URL 不误删）、近重复配对阈值。
- `distill`：窗口选择（有/无 last_run）、项目过滤、无匹配 no-op。
- `report`：全表渲染 + 一行摘要渲染。
- 路径边界校验：拒绝 memoryRoot 之外的写入目标。

### 9.2 Integration Tests
- `Runner.Run` 端到端（用 fake/stub LLM 或注入的决策函数）：构造含重复/失效路径/可合并条目的临时 memoryRoot，跑固结，断言文件收敛 + Report 计数 + `Reconcile` 生效 + `memory_search` 不再命中旧碎片。
- `--dry-run`：断言磁盘无变化、state 未更新、报告标 DryRun。
- 幂等：连续两次 Run，第二次 Report 全 0、字节不变。
- 子进程契约：`pigo --dream` 输出可解析 Report JSON、退出码正确。
- 后台触发：Scheduler 到期时 spawn；`enabled=false` 或未到期时不 spawn。

### 9.3 Edge Case Tests
覆盖 §5.5 全部行（空目录、锁占用、LLM 失败、无 key、无匹配会话、首次运行）。

### 9.4 Acceptance Criteria Mapping
| US/FR | Test | Type | Description |
|-------|------|------|-------------|
| US-001 / FR-1,2,3 | config + state + Due | unit | `[dream]` 解析、时间戳持久化、到期判定 |
| US-002 / FR-7,8,16 | runner 子进程契约 + 越界拒绝 | integration | 子进程隔离、报告、scope 边界 |
| US-003 / FR-9,10,12 | plan 去重 + runner 合并 | unit+integration | 合并/去重/压缩、MEMORY.md 一致 |
| US-004 / FR-11 | plan 路径校验 | unit | 失效路径清理、URL 不误删 |
| US-005 / FR-13 | distill + runner | integration | JSONL 提炼、去重后合并、无新增 no-op |
| US-006 / FR-14 | runner 剔除（stub 决策） | integration | 过期/矛盾剔除、保守保留 |
| US-007 / FR-5,6,18 | /dream + --dry-run | integration | 手动触发、预演不写盘、state 更新语义 |
| US-008 / FR-4,17 | scheduler + lock | unit+integration | 后台到期 spawn、非阻塞、单实例互斥 |
| US-009 / FR-15 | Reconcile + 文档 | integration+manual | 索引重建、search 收敛、文档浏览器验证 |

---

## 10. Implementation Plan

### 10.1 Phases
1. **基础设施**（无 LLM 依赖，纯确定性、易测）：`config` 表 + `state` + `lock` + `scheduler.Due`。
2. **确定性 plan**：`plan.go`（枚举/去重/路径校验/相似度配对）+ `report.go`。
3. **子进程入口**：`cmd/pigo` `--dream` 分派 + `Runner` 骨架（先支持 dry-run 与确定性部分，LLM 决策用注入接口）。
4. **LLM apply**：接主会话模型，dream 提示词，合并/剔除 + `distill` JSONL 提炼；写回 + `Reconcile`。
5. **父进程集成**：`/dream` 拦截 + 报告打印；会话启动 `MaybeRunBackground` 后台触发 + 一行摘要提示。
6. **文档**：slash-commands / configuration / features 三页双语更新。

### 10.2 Issue Mapping
| Issue | SPEC Sections | US | Priority | Depends On |
|-------|--------------|----|----------|-----------|
| #1 config `[dream]` + state + 到期判定 | 3.2,3.3,5.1,5.4 | US-001 | high | — |
| #2 lock 单实例互斥 | 2.2,5.4 | US-008(部分) | high | #1 |
| #3 确定性 plan（去重/路径/配对）+ Report | 5.1.1,3.2 | US-003(部分),US-004 | high | #1 |
| #4 `--dream` 子进程入口 + Runner 骨架 + dry-run | 4.1,4.2,5.1 | US-002,US-007(dry-run) | high | #1,#3 |
| #5 LLM apply：合并/剔除 + 写回 + Reconcile | 5.1,5.1.1,6.3 | US-003,US-006,US-009 | high | #4 |
| #6 distill 近期 session JSONL 提炼 | 5.3 | US-005 | medium | #4 |
| #7 `/dream` 手动命令 + 报告打印 | 2.3,4.1,6.1 | US-007 | high | #4,#5 |
| #8 启动后台到期触发 + 一行摘要 | 2.1,5.4,8.2 | US-008 | high | #2,#4,#7 |
| #9 文档三页双语更新 | 2.4 | US-009 | medium | #7 |

### 10.3 Incremental Delivery
Phase 1–3 可先合入（无行为暴露，纯内部）。Phase 4–5 完成后 `pigo --dream --dry-run` 即可脚本化验证（headless）。Phase 7 暴露 `/dream`。Phase 8 打开自动触发——此前 `[dream].enabled` 即使为 true 也因无 scheduler 挂载而不生效，天然等价 dark launch。

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions
- **首次运行触发策略**（PRD US-009 Open Q）：本 SPEC 拟定 —— 首次（无 `state.json`）**不在启动时静默后台触发**，而是首个会话以一行提示建议用户手动跑一次 `/dream`；避免新用户冷启动即产生意外 token 消耗。待用户确认；若倾向"首次也自动"，仅需在 `Due()` 中把零值 `LastRunAt` 视为到期即可。
- dry-run 报告是否需要逐条 diff：当前定为聚合统计 + `Notes` 摘要。若需逐条 diff，`Report` 增加 `Changes []ChangeEntry`。
- 矛盾判定是否加确定性兜底（按 frontmatter `updated` 取新）：当前全交 LLM，保守保留。是否需要规则兜底待定。

### 11.2 Technical Risks
| Risk | Impact | Mitigation |
|------|--------|-----------|
| LLM 误删有价值记忆 | 记忆丢失 | 混合分工 + 保守剔除 + `--dry-run` 预演；确定性去重不依赖 LLM |
| 后台 dream token 成本 | 费用 | 周期触发（默认 7 天）+ 首次不自动 + 单实例锁；成本上界可控 |
| 子进程超时/悬挂 | 资源占用 | 父进程 context 超时 kill（默认 10min）+ 陈旧锁接管 |
| 写回中途崩溃 | 记忆文件半更新 | 逐文件原子 rename + 崩溃后 Reconcile 收敛索引到磁盘现状 |
| 项目会话过滤不准 | distill 引入无关记忆 | 按 projectDir + UpdatedAt 过滤；token 预算截断；LLM 归类校验 |

### 11.3 Assumptions
- `memory` 包对外可获取 `memoryRoot` 且 `Reconcile` 可独立调用（已由 `Store.Root()`/`reconcile.go` 证实）。
- `session.Store` 可枚举并按项目/时间过滤会话（`List()`/`SessionHeader` 提供 UpdatedAt；项目归属字段若缺失需在 header 补充，作为 #6 的前置小改动）。
- 主会话模型配置在子进程可经环境/config 复用（`cmd/pigo` 现有解析路径覆盖）。
- 后台 spawn 采用 `os.Executable() + --dream -C <projDir>`，继承必要环境变量。
