# SPEC: 持久记忆系统 + 无限上下文

> Technical specification derived from: tasks/prd/prd-013-persistent-memory-infinite-context.md
> Generated: 2026-08-01 | Target branch: master | Commit: d3100e8

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 描述在 pigo 中新增两项能力的技术实现：(1) 基于纯 Go SQLite + FTS5 的**持久记忆系统**（磁盘 Markdown 记忆文件 + 全文索引 + BM25 检索 + 会话注入），(2) **无限上下文**（复用现有 `internal/compaction` 的 `Compact()`/`RebuildContext()` 并将检查点摘要持久化为可检索的 `checkpoint.md`，叠加检索召回，并支持跨会话上下文继承）。实现范围覆盖新包 `internal/memory`、`internal/agenttool` 记忆工具、`internal/runtime` 的检查点/重建/注入集成、`internal/session` 的继承水位、config 的嵌套 TOML 表、以及 TUI/REPL 的 `/rebuild` `/memory` 命令。

### 1.2 PRD Reference

- Source: `tasks/prd/prd-013-persistent-memory-infinite-context.md`
- User Stories covered: US-001 ~ US-011
- Functional Requirements covered: FR-1 ~ FR-20

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| SQLite 驱动 | `modernc.org/sqlite`（纯 Go，内置 FTS5） | 无 CGO，跨平台构建；FTS5 由 amalgamation 转译内置 |
| 数据库位置 | 单个全局库 `$PIGO_HOME/memory.db`（默认 `~/.pigo/memory.db`） | 便于跨项目 global 记忆检索；scope/scope_id 区分维度 |
| 检查点重建 | 复用现有 `Compact()`/`RebuildContext()`，额外把摘要持久化为 `checkpoint.md` | 代码量最小，复用已验证的压缩/重建机制 |
| 配置形态 | 嵌套 TOML 表 `[memory]` / `[checkpoint]` | 对齐 MiMo；结构清晰，便于扩展 |
| 跨会话继承 | 纳入本期，复用会话 ParentID 树 + 新增水位字段 | pigo 会话已是 fork 树，继承成本可控 |
| 写入触发 | agent 工具驱动（`memory_search` + 写入路径） | 对齐 pigo 现状，不引入后台写入子系统 |
| 记忆注入 | 复用 `ReminderRegistry` per-turn 注入通道 | 注入内容不进入持久历史，受 token 预算约束 |
| 默认开关 | 全部默认开启，`memory.enabled=false` 完全回退 | 零迁移，复用现有记忆目录约定 |

---
## 2. Architecture

### 2.1 System Context

记忆系统是一个独立的存储/检索层，被 agent 工具、运行时注入、检查点重建三处消费：

```
┌─────────────┐   写入(.md)   ┌──────────────────────┐
│ agent tools │──────────────▶│  memory dir (磁盘)    │
│ (Write/     │◀──检索─────── │  MEMORY.md/checkpoint │
│  memory_*)  │               │  notes/typed/*.md     │
└─────────────┘               └──────────┬───────────┘
                                          │ reconcile(懒同步)
                                          ▼
                              ┌──────────────────────┐
   注入(reminder) ◀───Search──│ internal/memory       │
   ┌──────────────┐  BM25     │  SQLite memory.db     │
   │ runtime loop │           │  memory_index+_fts    │
   │ maybeAutoCompact────────▶│ (FTS5 虚拟表+触发器)  │
   │ rebuild      │           └──────────────────────┘
   └──────────────┘
```

### 2.2 Component Design

- **`internal/memory`（新包）**：存储层（DB 打开/迁移）、路径解析（scope/type/project-id）、reconcile（懒索引/剪枝）、Search（BM25 + 分数下限）。对外暴露 `Store` 类型。
- **`internal/memory/inject`（或 runtime 内）**：把 Search 结果与 `MEMORY.md` 组装为受预算约束的注入块。
- **`internal/agenttool` 扩展**：注册 `memory_search` 工具；写入复用现有 Write/Edit（约束到记忆目录）。
- **`internal/runtime` 扩展**：`maybeAutoCompact` 附近增加"检查点持久化 + 重建 + 检索召回"；`ReminderRegistry` 增加记忆注入 provider；新增 `/rebuild` 触发。
- **`internal/session` 扩展**：`SessionHeader` 增加 `ContextFrom`/`ContextWatermark`，继承父会话检查点。
- **`internal/compaction` 复用**：`Compact()`/`RebuildContext()`/`CompactionMessage` 不改核心；新增把 `CompactionResult.Summary` 落盘为 `checkpoint.md` 的钩子。
- **config 扩展**：`FileConfig` 增加 `[memory]`/`[checkpoint]` 嵌套表 + overlay。

### 2.3 Module Interactions

自动压缩/重建时序（复用现有 compaction，叠加持久化与召回）：

```
loop.runLoop
  └─ maybeAutoCompact(ctx, agentCtx, cfg)
       ├─ ShouldCompact(tokens, window, settings)?  ── 否 ─▶ 继续
       ├─ emit CompactionStartEvent (已实现, spinner pin "Compacting conversation…")
       ├─ Compact(...) → CompactionResult
       ├─ persistCheckpoint(result.Summary → sessions/<id>/checkpoint.md)   [新增]
       ├─ agentCtx.Messages = result.RebuildContext(msgs, now)
       ├─ recallRelevant(query, memory.Store) → 注入片段(去重, 预算约束)     [新增]
       └─ emit CompactionEvent
```

会话恢复时的记忆注入：

```
run 组装 agentCtx
  └─ ReminderRegistry.memoryProvider(ctx)      [新增]
       ├─ 读取 MEMORY.md 索引(scope=projects/<id> + global)
       ├─ memory.Search(近期意图/项目关键词) 补充 typed/checkpoint 片段
       └─ 组装 <system-reminder> 注入块(受 push_caps 预算)
```

### 2.4 File Structure

```
internal/
├── memory/                       [NEW]
│   ├── store.go                  DB 打开 + 迁移 + Store 类型
│   ├── schema.go                 memory_index + memory_fts + 触发器 DDL
│   ├── paths.go                  scope/type 解析, project-id, 防穿越
│   ├── reconcile.go              walk + 指纹 + 索引/剪枝
│   ├── query.go                  buildFtsQuery (Unicode/CJK 分词 OR)
│   ├── search.go                 Search + BM25 + 分数下限
│   ├── inject.go                 注入块组装(预算裁剪)
│   └── *_test.go
├── agenttool/
│   └── memory_tool.go            [NEW] memory_search 工具 + 写入约束
├── runtime/
│   ├── loop.go                   [MODIFY] maybeAutoCompact: 持久化+召回
│   ├── reminder.go               [MODIFY] 注册 memory 注入 provider
│   ├── checkpoint.go             [NEW] persistCheckpoint / loadCheckpoint
│   └── rebuild.go                [NEW] /rebuild 触发 + 事件
├── session/
│   └── session.go                [MODIFY] SessionHeader 增加继承水位字段
├── compaction/                   [复用, 不改核心]
└── cli/
    ├── config/config.go          [MODIFY] [memory]/[checkpoint] 嵌套表
    ├── tui/                       [MODIFY] /rebuild /memory + 进行态提示
    └── repl/repl.go              [MODIFY] /rebuild /memory
cmd/pigo/                         [MODIFY] overlay 新 config 到 options
go.mod                            [MODIFY] + modernc.org/sqlite (pin)
```

---
## 3. Data Model

### 3.1 Schema Changes

SQLite（`$PIGO_HOME/memory.db`，默认 `~/.pigo/memory.db`）：

```sql
-- 内容表：一行 = 一个磁盘记忆文件
CREATE TABLE IF NOT EXISTS memory_index (
  id              INTEGER PRIMARY KEY,     -- FTS content_rowid
  path            TEXT NOT NULL UNIQUE,    -- 绝对路径
  scope           TEXT NOT NULL,           -- global|projects|sessions|cc
  scope_id        TEXT NOT NULL DEFAULT '',-- project-id 或 session-id
  type            TEXT NOT NULL,           -- user|feedback|project|reference|checkpoint|progress|notes|free
  body            TEXT NOT NULL,           -- 文件全文
  fingerprint     TEXT NOT NULL,           -- "<size>-<mtimeNs>"
  last_indexed_at INTEGER NOT NULL         -- unix ms
);
CREATE INDEX IF NOT EXISTS memory_index_scope_idx ON memory_index (scope, scope_id);
CREATE INDEX IF NOT EXISTS memory_index_type_idx  ON memory_index (type);

-- FTS5 全文索引（外部内容表模式）
CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
  body,
  content='memory_index',
  content_rowid='id',
  tokenize='unicode61 remove_diacritics 1'
);

-- 同步触发器：内容表增删改 → 维护 FTS
CREATE TRIGGER IF NOT EXISTS memory_ai AFTER INSERT ON memory_index BEGIN
  INSERT INTO memory_fts(rowid, body) VALUES (new.id, new.body);
END;
CREATE TRIGGER IF NOT EXISTS memory_ad AFTER DELETE ON memory_index BEGIN
  INSERT INTO memory_fts(memory_fts, rowid, body) VALUES('delete', old.id, old.body);
END;
CREATE TRIGGER IF NOT EXISTS memory_au AFTER UPDATE ON memory_index BEGIN
  INSERT INTO memory_fts(memory_fts, rowid, body) VALUES('delete', old.id, old.body);
  INSERT INTO memory_fts(rowid, body) VALUES (new.id, new.body);
END;
```

会话表继承字段（`internal/session` 的 `SessionHeader`，落在 JSONL 头，非 SQLite）：新增 `context_from`（父会话 id，可空）与 `context_watermark`（继承时父检查点覆盖的消息水位/时间戳）。

### 3.2 Entity Definitions

```go
// internal/memory
type Scope string // "global" | "projects" | "sessions" | "cc"
type Type  string // "user"|"feedback"|"project"|"reference"|"checkpoint"|"progress"|"notes"|"free"

type Locator struct {
    Scope   Scope
    ScopeID string // project-id / session-id / ""(global)
    Type    Type
    Key     string // 相对文件名(无扩展名)
}

type SearchResult struct {
    Path    string
    Snippet string
    Score   float64 // 越大越相关(由 -bm25 转换)
    Scope   Scope
    ScopeID string
    Type    Type
}

type Store struct { /* *sql.DB, root string, ccBase string */ }

func Open(dbPath, root, ccBase string) (*Store, error)
func (s *Store) Reconcile(ctx context.Context) (indexed, pruned int, err error)
func (s *Store) Search(ctx context.Context, q Query) ([]SearchResult, error)
func (s *Store) Close() error

type Query struct {
    Text       string
    Scope      Scope   // 空=不过滤
    ScopeID    string
    Type       Type
    Limit      int     // 默认 10
    ScoreFloor float64 // 默认 0.15；0=保留全部
}
```

### 3.3 Relationships

- `memory_index.path` 唯一，1:1 对应磁盘 `.md` 文件；`memory_fts` 通过 `content_rowid=id` 外部内容关联。
- `scope_id`：`projects` → sha256(repoAbsPath)[:12]；`sessions` → session id；`global` → ""。
- 会话 `context_from` → 父 `SessionHeader.id`（复用现有 ParentID 树语义，但语义是"上下文继承"而非"消息 fork"）。

### 3.4 Migration Plan

- SQLite schema 在 `Open()` 内用 `CREATE ... IF NOT EXISTS` 幂等建立；无版本表即可（DDL 自幂等）。首个版本无需数据迁移。
- 会话头新增字段向后兼容：旧 JSONL 缺字段时解析为零值（`context_from=""` 视为无继承）。
- 回退：`memory.enabled=false` 时不打开 DB、不注入、不持久化 checkpoint.md，行为等同现状；DB 文件可安全删除（下次 reconcile 重建）。

---
## 4. API Design

pigo 是 CLI/agent，无 HTTP。这里的"API"指 (a) agent 工具接口、(b) slash 命令、(c) 包间 Go 接口。

### 4.1 Interfaces

| 类型 | 名称 | 描述 | 使用者 |
|------|------|------|--------|
| Tool | `memory_search` | 按查询检索记忆，返回片段 | agent |
| Tool 写入 | 复用 `write`/`edit` | 写到记忆目录（路径受约束） | agent |
| Slash | `/rebuild` | 手动触发上下文重建 | 用户(TUI/REPL) |
| Slash | `/memory` | 显示记忆条目数/窗口/占用 | 用户(TUI/REPL) |
| Go | `memory.Store.Search/Reconcile` | 检索与索引 | runtime/tool |
| Go | `runtime.persistCheckpoint/loadCheckpoint` | 检查点落盘/读取 | loop |

### 4.2 Tool / Command Schemas

`memory_search` 工具输入（JSON schema）：

```jsonc
{
  "query":   "string, required, 自由文本",
  "scope":   "string, optional, global|projects|sessions|cc",
  "type":    "string, optional, user|feedback|project|reference|checkpoint|progress|notes|free",
  "limit":   "integer, optional, 默认 10, 上限 50"
}
```

`memory_search` 输出（文本，供模型消费）：每条一行 `[type/scope] path\n  <snippet>`，按相关度降序；无结果返回明确的空提示。

`/memory` 输出（示例）：

```
Memory: 42 entries (global 5, projects 30, sessions 7)
Context window: 200000 (usable 183616 after reserve)
Current usage: 128340 tokens (70%)  next trigger at 80%
Checkpoint: sessions/<id>/checkpoint.md (watermark msg#87, 2026-08-01T10:12Z)
```

### 4.3 Error Responses

- `memory_search` 空查询/无 token → 返回"no results"文本，不报错、不发 SQL。
- DB 打开失败 → 记录警告，记忆功能降级为不可用（不影响主运行；等同 `enabled=false`）。
- reconcile 遇到布局外路径 → 跳过并 warn，不中断。
- 写入越权路径（`..`/前导 `/`）→ 工具返回 error，拒绝写入。

### 4.4 Breaking Changes

- 无对外破坏性变更。新增 config 表、新增会话头可选字段（向后兼容）、新增工具/命令。
- `go.mod` 新增 `modernc.org/sqlite` 依赖（构建体积增大，纯 Go）。

---

## 5. Business Logic

### 5.1 Core Algorithms

**buildFtsQuery(raw) → string|nil**（`query.go`，移植自 MiMo/openclaw）：
1. 用 `regexp` 按 `[\p{L}\p{N}_]+`（含 CJK）提取 token；无 token 返回 nil。
2. 每 token 去除内部 `"`，包裹为 `"token"` 短语。
3. 以 ` OR ` 连接。OR（非 AND）保召回，噪声由分数下限剔除。

**Search(q)**（`search.go`）：
1. 若 `reconcile_on_search`（默认 true）→ 先 `Reconcile`。
2. `ftsQuery = buildFtsQuery(q.Text)`；nil → 返回 []。
3. 组装 WHERE（scope/scope_id/type 过滤），SQL：
   `SELECT path,scope,scope_id,type, snippet(memory_fts,0,'<<','>>','...',32), bm25(memory_fts) AS score FROM memory_fts JOIN memory_index ON memory_index.id=memory_fts.rowid WHERE memory_fts MATCH ? [AND ...] ORDER BY score LIMIT ?`
4. 过取 `min(limit*3, 50)`；`score = -bm25`（转为越大越好）。
5. 相对分数下限：`cutoff = top*floor`（floor 默认 0.15，0 禁用）；恒保留第一名，剔除低于 cutoff 的尾部；截断到 limit。

**Reconcile()**（`reconcile.go`）：
1. walk `root`（及可选 `ccBase/<slug>/memory`）收集 `.md` 绝对路径 → `diskPaths`。
2. 读取 `memory_index` 全部 `(path,fingerprint)` → `indexed`。
3. 剪枝：`indexed` 中不在 `diskPaths` 的行 DELETE（`pruned++`）。
4. 索引：对每个磁盘文件，`fp="<size>-<mtimeNs>"`；命中旧指纹 → skip；否则读全文、解析 type（mimo 走路径；cc 走 frontmatter，缺省 `free`）、`INSERT ... ON CONFLICT(path) DO UPDATE`（`indexed++`）。

**persistCheckpoint(result, sessionID)**（`checkpoint.go`）：将 `CompactionResult.Summary` 写入 `sessions/<id>/checkpoint.md`（带 frontmatter `type: checkpoint`），并把覆盖水位（`FirstKeptIndex` 对应的消息序号/时间戳）记入 `SessionHeader.ContextWatermark`。写入后由下次检索的懒 reconcile 索引。

**rebuild（无限上下文主流程）**：见 §2.3；复用 `Compact()`+`RebuildContext()`；差异是重建后额外 `persistCheckpoint` + `recallRelevant`。

**inheritContext(newSession)**：新会话若指定 `ContextFrom=parentID`，加载父会话 `checkpoint.md` 作为首个 `CompactionMessage` 注入起点，并置 `ContextWatermark` 为父水位。

### 5.2 Validation Rules

- `assertSafeComponent`：拒绝含 `..` 段或前导 `/` 的 key/路径组件。
- `limit` 钳制到 `[1,50]`；`ScoreFloor` 钳制到 `[0,1]`。
- `scope`/`type` 仅接受枚举值；非法值 → 工具 error。
- 阈值百分比字符串（如 `"80%"`）解析为 `(0,1]`；越界忽略取默认。

### 5.3 State Machine

上下文状态：`Normal → (ShouldCompact) → Compacting → (有checkpoint) Rebuilt / (无) LossyCompacted → Normal`。
检查点写入与重建的并发：重建若发现"检查点写入进行中"，进入 `WaitingCheckpoint`（显示 "Preparing conversation context…"），完成后继续；超时或失败 → 回退 LossyCompacted。

### 5.4 Edge Cases

- 空记忆库：Search 返回 []；注入块省略；`/memory` 显示 0 entries。
- 记忆文件被手工删除：reconcile 剪枝对应索引行。
- 手工编辑记忆文件：指纹变化触发重索引（覆盖工具外写入）。
- CJK-only 查询：Unicode 分词保证可检索。
- 常见词查询（如 "checkpoint"）：分数下限剔除仅匹配常见词的噪声，恒保留 top1。
- 无有效 cut point / 无新内容可摘要：`Compact` 返回 `(nil,nil)`，跳过重建。
- 无 checkpoint 且逼近上限：回退现有 lossy compaction（不丢近期逐字）。
- DB 损坏/不可打开：降级为 `enabled=false` 行为，主运行不受影响。
- 继承的父会话已无 checkpoint.md：`ContextFrom` 视为无效，按新会话处理。

---
## 6. Error Handling

### 6.1 Error Taxonomy

| 错误 | 触发条件 | 处理 | 用户/日志表现 |
|------|----------|------|---------------|
| `ErrMemoryDisabled` | `memory.enabled=false` | 不打开 DB，所有记忆调用返回空/no-op | 静默；`/memory` 显示 disabled |
| DB open fail | 磁盘/权限/损坏 | 降级为 disabled，主运行继续 | warn 日志一次，记忆不可用 |
| reconcile walk error | 目录不可读 | 跳过该子树，继续其余 | warn 日志，`indexed/pruned` 仍返回 |
| path outside layout | reconcile 命中布局外 `.md` | skip 该文件 | warn 日志 |
| 越权写入路径 | key 含 `..`/前导 `/` | 工具返回 error，拒绝写入 | 工具错误文本给模型 |
| empty/no-token query | `buildFtsQuery`→nil | 返回 `[]`，不发 SQL | `memory_search` 返回 "no results" |
| checkpoint 摘要失败 | 摘要 LLM 调用出错 | 非致命：跳过持久化，继续 | warn 日志 + 回退 lossy |
| rebuild 无 cut point | `Compact` 返回 `(nil,nil)` | 跳过重建，维持现状 | 无提示 |
| 继承父 checkpoint 缺失 | `ContextFrom` 指向的文件不存在 | 视为无继承，按新会话 | debug 日志 |

### 6.2 Retry Strategy

- **DB 写入（INSERT/UPDATE via 触发器）**：`modernc.org/sqlite` 单连接串行；遇 `SQLITE_BUSY` 重试 3 次（指数退避 10/40/160ms），仍失败则 warn 并跳过该文件（reconcile 下次补索引）。
- **摘要 LLM 调用**：复用 `internal/compaction` 现有重试语义，不额外包裹；checkpoint 持久化失败不重试（非致命）。
- **Search / Reconcile**：只读或幂等，不重试；错误直接返回调用方（工具层转为文本）。

### 6.3 Failure Modes

- **DB 完全不可用**：整个记忆子系统降级为 `enabled=false` 语义——注入省略、检查点不落盘、检索返回空。主 agent 循环与现有 lossy compaction 不受影响（core 不依赖 memory 包）。
- **检查点写入进行中而重建触发**：进入 `WaitingCheckpoint`（显示 "Preparing conversation context…"），带超时（默认 30s）；超时→回退 lossy compaction，不阻塞主循环。
- **磁盘满/只读**：写入工具与 checkpoint 落盘返回 error；检索仍可用（只读）；warn 提示用户。
- **索引与磁盘漂移**：任何检索前的懒 reconcile 以磁盘为准（指纹重索引 + 剪枝），保证最终一致。

---

## 7. Security

### 7.1 Authentication & Authorization

pigo 是本地单用户 CLI，无多租户/网络鉴权。核心安全边界是**文件系统访问范围**：

- 记忆写入/读取被约束到记忆根目录（`root`）与可选 `ccBase`；`assertSafeComponent` 拒绝含 `..` 段或前导 `/` 的路径组件。
- `memory_search` 与写入工具构造的绝对路径必须以 `root`/`ccBase` 为前缀（`filepath.Clean` 后再校验前缀），否则拒绝。
- `cc` scope（索引 Claude Code 记忆目录）默认关闭（`memory.cc_index=false`）；开启时仅读取索引，不写入。

### 7.2 Input Validation

- `scope`/`type` 仅接受枚举白名单；非法值 → 工具 error。
- `limit` 钳制 `[1,50]`；`ScoreFloor` 钳制 `[0,1]`。
- 查询文本经 `buildFtsQuery` 分词后短语引用，杜绝 FTS5 MATCH 语法注入（特殊字符 `"()*:^-.{}` 被短语包裹中和）。
- 阈值字符串（`"80%"`）解析越界则忽略取默认。
- SQL 全部参数化（`?` 占位符），无字符串拼接用户输入进 SQL。

### 7.3 Data Protection

- 记忆文件为明文 Markdown，可能包含项目上下文、决策、甚至无意写入的敏感信息。**本期不加密**（与现有 auto-memory 磁盘布局一致，零迁移）；文件权限沿用进程 umask。
- 注入块标注为 `<system-reminder>` 记忆来源，不进入持久历史、不污染最终输出。
- 检索片段用 `snippet()` 截断（32 token 窗口），降低整文件敏感内容一次性外泄面。
- 系统提示词应提醒模型：不要把凭证/密钥写入记忆文件（对齐现有 auto-memory 关于 secrets 的约定）。
- `cc` 索引跨读 Claude Code 目录属隐私敏感，默认关闭；文档需明确开启含义。

---

## 8. Performance

### 8.1 Expected Load

- 典型记忆库：数十~数百个 `.md` 文件（单文件 KB 级）。检索与 reconcile 均为亚秒级。
- 检索频率：每次 compaction/rebuild 后一次召回 + 会话恢复注入一次 + agent 显式调用若干次。
- checkpoint 落盘：仅在阈值触发（40/60/80%）或 `/rebuild` 时，低频。

### 8.2 Optimization Strategy

- **指纹跳过**：reconcile 用 `<size>-<mtimeNs>` 指纹命中即 skip，避免全量重读/重索引。
- **过取截断**：Search 过取 `min(limit*3,50)` 行后按相对分数下限裁剪再截到 limit，兼顾召回与噪声控制。
- **懒 reconcile**：仅检索前触发（可配 `reconcile_on_search`），非后台常驻。
- **注入预算**：`push_caps.*` per-section token 上限裁剪注入块，避免挤占窗口。
- **单库单连接**：全局 `memory.db` 单 `*sql.DB`，串行写；读并发由 SQLite 处理。

### 8.3 Database Considerations

- 索引：`memory_index(scope,scope_id)` 与 `(type)` 复合/单列索引服务 WHERE 过滤；`path` UNIQUE 服务 upsert/prune。
- FTS5 外部内容表模式（`content='memory_index'`）避免正文双写，触发器维护倒排。
- 查询 `ORDER BY bm25 LIMIT`，FTS5 内部按分数流式返回，无需全表扫描。
- N+1 规避：Search 单条 JOIN 查询返回全部字段（path/scope/type/snippet/score），无逐行回查。

---

## 9. Testing Strategy

### 9.1 Unit Tests

- `internal/memory`：
  - `schema_test.go`：建表幂等、FTS5 虚拟表可创建（**依赖去风险冒烟测试**）、三触发器 INSERT/DELETE/UPDATE 同步。
  - `paths_test.go`：各 scope/type 解析、`resolveProjectId` 稳定性、`assertSafeComponent` 拒绝 `..`/前导 `/`。
  - `reconcile_test.go`：新增被索引、变更被更新、删除被剪枝、指纹命中 skip、布局外路径 skip+warn。
  - `query_test.go`：多词 OR、CJK 分词、空/纯标点→nil、内部 `"` 被剥离。
  - `search_test.go`：BM25 降序、相对分数下限剔除尾部且恒保留 top1、scope/type 过滤、空查询返回 []、`floor=0` 保留全部。
  - `inject_test.go`：预算裁剪、去重、MEMORY.md 优先。
- `internal/runtime`：`checkpoint_test.go`（persist/load + watermark 记录）、`rebuild_test.go`（有 checkpoint 走重建 / 无则回退 lossy / 边界与近期保留）。

### 9.2 Integration Tests

- 真实 `modernc.org/sqlite` 临时库端到端：写 `.md` → reconcile → search 命中片段。
- `maybeAutoCompact` 集成：模拟逼近窗口 → Compact → persistCheckpoint 落盘 checkpoint.md → RebuildContext → recall 注入去重。
- 会话继承：父会话有 checkpoint.md → 新会话 `ContextFrom=parent` → 加载为注入起点 + watermark 继承。
- 工具层：`memory_search` 工具输入 schema 校验、越权路径写入被拒。
- headless stream-json：rebuild/recall 事件可见。

### 9.3 Edge Case Tests

- 空记忆库：Search []、注入省略、`/memory` 显示 0。
- 手工删除/编辑记忆文件：reconcile 剪枝/重索引。
- CJK-only 查询命中。
- 常见词查询（"checkpoint"）：分数下限剔噪，保留 top1。
- 无 cut point：`Compact` 返回 `(nil,nil)` 跳过重建。
- DB 不可打开：降级 disabled，主运行不受影响。
- 继承父会话已无 checkpoint.md：按新会话处理。

### 9.4 Acceptance Criteria Mapping

| US/FR | Test | Type | Description |
|-------|------|------|-------------|
| US-001/FR-1,2 | schema_test | unit | 建表/迁移幂等、FTS5 可创建、触发器同步 |
| US-002/FR-3,4,5 | paths_test | unit | scope/type 解析、project-id、防穿越 |
| US-003/FR-6,7 | reconcile_test | unit | 索引/更新/剪枝/指纹跳过 |
| US-004/FR-8 | query_test,search_test | unit | OR 召回、CJK、BM25、分数下限、过滤、空查询 |
| US-005/FR-9 | memory_tool_test | unit/integration | 检索工具 schema、写入约束 |
| US-006/FR-10 | inject_test | unit | 注入预算裁剪、MEMORY.md 优先、来源标注 |
| US-007/FR-11,12 | checkpoint_test | unit | 阈值触发、watermark、失败非致命 |
| US-008/FR-13,14,15,16 | rebuild_test | unit/integration | 有 checkpoint 重建/无则回退/进行态提示/`/rebuild` |
| US-009/FR-17 | recall integration | integration | 召回注入 + 去重 |
| US-010/FR-18,19 | config_test | unit | 默认值、`enabled=false` 完全回退 |
| US-011/FR-20 | command_test | unit | `/memory`/`/rebuild` 注册、事件可见 |

---

## 10. Implementation Plan

### 10.1 Phases

1. **存储层地基**（US-001~004）：`internal/memory` 全部（store/schema/paths/reconcile/query/search）+ 单测。先落地纯 Go SQLite + FTS5 冒烟测试去风险。
2. **工具与注入**（US-005,006）：`memory_tool.go` + `inject.go` + `reminder.go` provider + 系统提示词更新。
3. **配置**（US-010）：`FileConfig` 增 `[memory]`/`[checkpoint]` + cmd/pigo overlay + 默认值/回退测试。
4. **无限上下文**（US-007~009）：`checkpoint.go`（persist/load + watermark）→ `loop.go` maybeAutoCompact 集成持久化+召回 → `rebuild.go`（`/rebuild` + 事件 + WaitingCheckpoint）→ 回退验证。
5. **会话继承**（SPEC §5 inheritContext）：`session.go` 头字段 + 继承加载。
6. **命令可见性**（US-011）：TUI/REPL `/memory`/`/rebuild` + 进行态提示。

### 10.2 Issue Mapping

| Issue | SPEC Sections | US/FR | Priority | Depends On |
|-------|---------------|-------|----------|------------|
| #1 存储层 schema + Store.Open + FTS5 冒烟 | 3.1,3.2,5.2 | US-001/FR-1,2 | high | — |
| #2 路径解析 + scope/type + 防穿越 | 3.3,5.2,7.1 | US-002/FR-3,4,5 | high | #1 |
| #3 reconcile 懒同步 + 指纹 | 5.1,8.2 | US-003/FR-6,7 | high | #1,#2 |
| #4 Search BM25 + buildFtsQuery + 分数下限 | 5.1,4.2 | US-004/FR-8 | high | #1,#3 |
| #5 memory_search 工具 + 写入约束 | 4.1,4.2,7.1 | US-005/FR-9 | high | #4 |
| #6 记忆注入 provider + 预算裁剪 | 2.3,4,8.2 | US-006/FR-10 | high | #4 |
| #7 config `[memory]`/`[checkpoint]` + overlay | 3,4 | US-010/FR-18,19 | high | — |
| #8 checkpoint persist/load + watermark | 5.1,3.1 | US-007/FR-11,12 | medium | #7 |
| #9 loop 集成：持久化 + 召回 | 2.3,5.1 | US-009/FR-17 | medium | #6,#8 |
| #10 rebuild + `/rebuild` + 回退 + 进行态 | 5.1,5.3,6 | US-008/FR-13~16 | medium | #8 |
| #11 会话继承 context_from/watermark | 3.1,5.1 | — | low | #8 |
| #12 `/memory`/`/rebuild` TUI+REPL + 事件 | 4.2 | US-011/FR-20 | medium | #9,#10 |

### 10.3 Incremental Delivery

- 每 Phase 独立可合并、`go build/vet/test ./...` 全绿。
- `memory.enabled=false` 作为总开关：前 3 个 Phase 合入后即可默认开启记忆检索/注入而不触碰 compaction 路径。
- 无限上下文（Phase 4~5）通过 `checkpoint.*` 配置渐进启用；无 checkpoint 时自动回退现有 lossy compaction，零回归风险。
- 命令可见性（Phase 6）最后叠加，不阻塞核心能力。

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

- 检查点摘要 prompt 是否需与现有 `compaction/summary.go` 区分（更结构化的 ledger）？本期先复用，观察效果再决定是否分化。
- `cc` scope 隐私提示的呈现方式（首次开启时 warn？文档说明？）。
- 记忆 TTL/衰减/去重巩固（`/dream`/`/distill`）明确留待未来，本期依赖 agent 手工维护。

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|-----------|
| `modernc.org/sqlite` FTS5 不可用/构建差异 | 高（核心依赖） | US-001 加 FTS5 冒烟测试；pin 精确版本；darwin/linux CI 构建 |
| 单全局库并发写竞争 | 中 | 单连接串行 + `SQLITE_BUSY` 退避重试；写频率低 |
| 注入挤占上下文窗口 | 中 | per-section `push_caps` 预算裁剪 + snippet 截断 |
| 复用 Compact 语义与检查点持久化耦合出错 | 中 | 持久化为 Compact 之后的独立钩子，失败非致命回退 lossy |
| 构建体积增大（纯 Go SQLite） | 低 | 可接受，换取无 CGO 跨平台 |

### 11.3 Assumptions

- pigo 现有 `Compact()`/`RebuildContext()` 的检查点坍缩语义可直接承载"无限上下文"重建，无需改核心（仅叠加持久化+召回钩子）。
- 记忆磁盘布局沿用现有 `~/.claude/projects/<slug>/memory/`（MEMORY.md + 类型化文件），零迁移。
- 会话 JSONL 头新增可选字段向后兼容，旧会话解析为零值。
- `$PIGO_HOME`（默认 `~/.pigo`）为全局 DB 落盘位置，与配置目录一致。



