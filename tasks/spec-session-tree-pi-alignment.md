# SPEC: pigo 会话树对齐 pi 迁移设计

> 来源：`.scratch/session-tree-pi-alignment/map.md`、ticket 01-09 决议、research 01-02。
> 生成日期：2026-08-12 | 目标分支：`research/session-tree-pi-alignment`
> 交付形态：设计文档，不含实现代码；实现范围由 `.scratch/session-tree-pi-alignment/issues/` 承接。

## 1. Summary

### 1.1 目标

把 pigo 的会话持久化与运行时树模型从“项目级 JSONL 文件 + 进程内 curLeaf + 文件序切片”对齐为 pi 的“运行时树模型 + 逐字复刻 SQLite schema 作为 canonical 存储 + v4 typed JSONL 仅作导出/导入”。迁移完成后：

- `$PIGO_HOME/sessions.db` 是会话唯一 canonical 存储；`internal/sessionstore` 重写为 SQLite 唯一存储入口。
- `internal/session` 瘦身为 v4 typed JSONL 编解码、树渲染与 leaf 投影。
- `main` lane 的 `lanes.leaf_id` 是 resume / `session/load` / 所有前端的 leaf 权威来源；进程内 `curLeaf` 降级为游标，不再作为权威。
- compaction 使用 `retainedTail` 自包含 checkpoint，完整实现 split-turn、迭代 summary、overflow 单次重试；`memory/checkpoint.md` 集成移除。
- ACP 只保留标准方法 + 斜杠命令 + `_meta.pigo.sessionTree` v1 扩展，不新增非标准 `session/*` 方法。
- 旧 v1/v2/v3 JSONL 与旧平铺目录运行时不可读，不迁移、不向后兼容；由仓库脚本隔离到 `$PIGO_HOME/legacy-sessions/`。
- 子 agent 成为 SQLite 中的 first-class 子会话（`parent_session_id` + metadata），内置 `task` 工具移除，改为插件声明式注册。
- TUI/REPL 提供与 pi 对齐的树导航、label、branch summary、compaction 状态与 queue 交互。

### 1.2 输入决议

| 来源 | 决议摘要 |
|---|---|
| ticket 01 | pi 参照面事实清单；9 种 entry、leaf 投影、compaction、SQLite schema、`/tree`/`/fork`/`/clone` 语义 |
| ticket 02 | SQLite schema + writer lease 契约；FTS 首版纳入；uuidv7 session id；完整 session_stats |
| ticket 03 | v4 JSONL typed entry 契约；header、9 种 entry、seq 物理序、无损往返、facts 表达 |
| ticket 04 | leaf 持久化与 resume 语义；`lanes.leaf_id` canonical；统一 `ProjectLeaf` 投影入口 |
| ticket 05 | compaction 对齐设计；`retainedTail` checkpoint、split-turn、迭代 summary、overflow 重试、UI queue/abort |
| ticket 06 | ACP 树 surface；`_meta.pigo.sessionTree` v1、结构化 `/tree`、`session_info_update`、不新增 session/* 方法 |
| ticket 07 | 子 agent 会话图；删除内置 `task`、插件声明式注册、`subagent-` id、`parent_session_id`、生命周期 |
| ticket 08 | 旧格式移除与入口清理；运行时不读 v1/v2/v3、旧目录隔离、`/export` v4 + HTML、`/import` v4 only |
| ticket 09 | TUI/REPL 树交互线框；tree selector、label、branch summary、compaction queue、REPL fallback |

### 1.3 关键决策

| 决策 | 选择 | 理由 |
|---|---|---|
| canonical 存储 | 单库 `$PIGO_HOME/sessions.db`，逐字复刻 pi schema | 与 pi 语义一致，支持跨进程 writer lease 与查询 |
| 运行时文件格式 | v1/v2/v3 JSONL 完全废弃，不做兼容 shim | AGENTS.md 约定“移除即移除”；避免双实现 |
| 导出/导入格式 | v4 typed JSONL，仅 portability | 可读、可 diff、可验收 round-trip；不承担运行时性能 |
| leaf 权威 | SQLite `lanes.main.leaf_id` | 跨进程/跨前端一致，取代 metadata curLeaf 与文件末行推断 |
| compaction checkpoint | compaction entry 内嵌 `retainedTail` | 自包含，不依赖被压缩 entry 仍存在 |
| ACP 扩展 | 标准方法 + 斜杠命令 + `_meta.pigo.sessionTree` v1 | 保持标准 ACP 面，不复活 `pigo/*` 私有方法 |
| 子 agent | SQLite 子会话 + 插件注册 | 可恢复、可过滤、可审计；核心不再内置 task |
| memory | 移除 `checkpoint.md` 会话集成 | retainedTail 成为唯一压缩边界；记忆系统另起 effort |

### 1.4 Out of Scope

- 与 pi JSONL/SQLite 的字节级互通。
- 旧 v1/v2/v3 会话迁移与向后兼容。
- 新增非标准 `session/*` ACP 方法。
- 整体移植 pi extension 系统。
- pi-web / ash-workbench 客户端 UI 改造。
- 树搜索、过滤、折叠、复制、label 时间戳、TUI `/fork` `/clone` 选择器。
- memory 能力本身与 checkpoint.md 之外的设计（另起 effort）。

## 2. 当前实现与差距

### 2.1 存储

- `internal/session`：单文件 JSONL，`SchemaVersion=3`，`SessionHeader` + `Entry{ID,ParentID,Timestamp,Message}`，`PathToLeaf` 按 parent 链走，`RenderTreeLines` 线性编号。
- `internal/sessionstore`：`$PIGO_HOME/projects/<slug>/sessions/` 下 metadata + index + transcript；`curLeaf` 存在 `metadata.customMetadata["curLeaf"]`；写路径用整文件原子重写。
- 没有 SQLite、lanes、facts、branch cache、writer lease、跨进程锁。

### 2.2 运行时 leaf

- REPL/TUI/headless resume 用 `entries[len(entries)-1].ID`。
- serve `prompt_runner.go` / `goal_host.go` 用 metadata `curLeaf` 或末条 entry。
- HTTP `/tree` 切换后写回 metadata。
- 没有统一 `ProjectLeaf`；serve 与本地前端可能恢复不同分支。

### 2.3 Compaction

- `compaction.CompactionResult` 用 `FirstKeptIndex`（消息下标），持久化的是单条 `CompactionMessage`。
- `FindCutPoint` 已返回 `TurnStartIndex/IsSplitTurn`，但 `Compact` 不消费；无 turn prefix summary。
- 所有调用传 `prevCompactionIndex=-1/prevDetails=nil/previousSummary=""`，无迭代 summary。
- overflow 只有事件枚举，`maybeAutoCompact` 无识别/单次重试。
- `runtime/checkpoint.go` 把 checkpoint 写到 `<memoryRoot>/sessions/<id>/checkpoint.md`；`/rebuild` 优先读 checkpoint。

### 2.4 ACP / HTTP

- ACP 只暴露标准方法；`initialize` 无 `_meta` 扩展；事件映射无 tree 元数据。
- `session/load` replay 只带标准 message；HTTP `/tree` 返回文本编号，`PromptResponse` 无 structured 字段。
- `/resume`、`/fork`、`/tree` 均走 HTTP 斜杠命令，数据源仍是 JSONL store。

### 2.5 子 agent

- `internal/runtime` 已有 `SubAgentTool`、`Registry`、`ChildSession`、`SessionID(parent, toolCallID)` 与 `subagent-<sha256>[:16]`。
- 子会话仍通过 `sessionstore` JSONL 持久化；`subagents.json` 索引存在（ticket 07 决定删除）。
- 内置 `task` 工具、`taskGuide`、`NewTaskTool` 仍注册；嵌套守卫默认排除 task。

### 2.6 UI

- REPL `/tree N` 只移动进程内 leaf 并重建平铺消息；无 label、branch summary、回填编辑器。
- TUI 无全屏树选择器；compaction 在 run loop 内同步执行，无 queue/abort。

## 3. 目标架构

```text
                    +----------------------------------------+
                    | internal/session (codec + projection)  |
                    |  v4 JSONL, Entry, ProjectLeaf, tree    |
                    +-------------------+--------------------+
                                        |
                    +-------------------+--------------------+
                    | internal/sessionstore (SQLite canonical)|
                    |  migrations, repo, lanes, facts, FTS,  |
                    |  writer lease, Store API               |
                    +-------------------+--------------------+
                                        |
        +-------------------------------+-------------------------------+
        |                               |                               |
   internal/runtime               internal/httpapi               internal/acp
   loop/compaction/subagent       sessions/commands/status        adapter/events/meta
        |                               |                               |
        +-------------------------------+-------------------------------+
                                    TUI / REPL / headless / Zed
```

模块边界：

- `internal/session`：只做格式与投影。提供 v4 JSONL codec、typed entry 类型、`ProjectLeaf`、`RenderTreeLines`、`TreeLines`。不打开 SQLite，不做运行时持久化。
- `internal/sessionstore`：SQLite canonical。提供 `Open(pigoHome)`、`Store`、`Session` repo 方法、migrations、FTS、writer lease、`List/Delete/Fork/Import/Export`。
- `internal/compaction`：只计算摘要与边界。`Compact` 返回 `retainedTail`、split-turn 合并、迭代 summary；不再写 checkpoint 文件。
- `internal/runtime`：消费 `ProjectLeaf` 与 `AppendCompaction`；overflow 重试；queue/abort 事件；subagent 子会话。
- `internal/httpapi`：所有 session API 数据源换成 SQLite；`/tree` 返回 `structured`；`/label`、`/export`、`/import` 语义收敛。
- `internal/acp`：`initialize` 双向声明 v1；事件和 replay 附加 `_meta.pigo.sessionTree`；`/tree` 结构化映射到 `_meta.pigo.structured`。
- `internal/cli`：TUI/REPL 通过共享 runtime/projection 使用同一 leaf；不再各自从 JSONL 末行推断。

## 4. SQLite Canonical 存储

### 4.1 数据库位置与连接

- 单库：`$PIGO_HOME/sessions.db`（默认 `~/.pigo/sessions.db`）。
- 进程内单 lazy `*sql.DB`；写操作走事务；`busy_timeout=5000`。
- PRAGMA：`journal_mode=WAL`、`synchronous=FULL`、`foreign_keys=ON`、`busy_timeout=5000`。
- migrations：`schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`；按编号 SQL 顺序执行，事务内应用。
- `sessions.cwd` 区分项目；session id 为 uuidv7；entry id 为 pi 风格 8-hex；branch cache id 为 uuidv7。

### 4.2 表结构（逐字复刻 pi `001_initial.sql` 核心）

```sql
CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  created_at TEXT NOT NULL,
  cwd TEXT NOT NULL,
  parent_session_id TEXT NULL,
  metadata TEXT NULL
) WITHOUT ROWID;

CREATE TABLE entries (
  session_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  id TEXT NOT NULL,
  parent_id TEXT NULL,
  type TEXT NOT NULL,
  timestamp TEXT NOT NULL,
  payload TEXT NOT NULL,
  PRIMARY KEY (session_id, id),
  UNIQUE (session_id, seq)
);

CREATE TABLE session_sequences (
  session_id TEXT PRIMARY KEY,
  next_seq INTEGER NOT NULL
) WITHOUT ROWID;

CREATE TABLE session_stats (
  session_id TEXT PRIMARY KEY,
  message_count INTEGER NOT NULL,
  cached_tokens REAL NOT NULL,
  uncached_tokens REAL NOT NULL,
  total_tokens REAL NOT NULL,
  cost_total REAL NOT NULL
) WITHOUT ROWID;

CREATE TABLE branch_entries (
  session_id TEXT NOT NULL,
  branch_id TEXT NOT NULL,
  entry_id TEXT NOT NULL,
  entry_seq INTEGER NOT NULL,
  entry_type TEXT NULL,
  custom_type TEXT NULL,
  PRIMARY KEY (session_id, branch_id, entry_id)
) WITHOUT ROWID;

CREATE TABLE lanes (
  session_id TEXT NOT NULL,
  lane TEXT NOT NULL,
  leaf_id TEXT NULL,
  open_operation_id TEXT NULL,
  PRIMARY KEY (session_id, lane)
) WITHOUT ROWID;

CREATE TABLE records (
  session_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  id TEXT NOT NULL,
  lane TEXT NOT NULL,
  run_id TEXT NULL,
  type TEXT NOT NULL,
  op_kind TEXT NULL,
  timestamp TEXT NOT NULL,
  payload TEXT NOT NULL,
  PRIMARY KEY (session_id, id),
  UNIQUE (session_id, seq)
) WITHOUT ROWID;

CREATE TABLE lane_moves (
  session_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  lane TEXT NOT NULL,
  leaf_id TEXT NULL,
  PRIMARY KEY (session_id, seq)
) WITHOUT ROWID;

CREATE TABLE facts (
  session_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  kind TEXT NOT NULL,
  key TEXT NULL,
  value TEXT NULL,
  PRIMARY KEY (session_id, seq)
) WITHOUT ROWID;

CREATE TABLE branch_tips (
  session_id TEXT NOT NULL,
  tip_id TEXT NOT NULL,
  branch_id TEXT NOT NULL,
  PRIMARY KEY (session_id, tip_id),
  UNIQUE (session_id, branch_id)
) WITHOUT ROWID;

CREATE TABLE writer_leases (
  session_id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  fence INTEGER NOT NULL,
  expires_at_ms INTEGER NOT NULL
) WITHOUT ROWID;
```

索引与 pi 一致（`idx_sessions_*`、`idx_entries_*`、`idx_branch_entries_*`、`idx_records_*`、`idx_lane_moves_*`、`idx_facts_*`、`idx_branch_tips_*`）。pigo 额外增加：

```sql
CREATE VIRTUAL TABLE session_search_fts USING fts5(
  session_id UNINDEXED,
  entry_id UNINDEXED,
  seq UNINDEXED,
  kind,
  text,
  tokenize = 'trigram'
);
```

`session_search_fts` 由 `entries`（`message` / `custom_message` / `branch_summary` / `compaction`）与 `facts`（`name` / `label`）的 trigger 维护；搜索按 `bm25(session_search_fts)` 排序，并用 `sessions.cwd` 过滤。

### 4.3 写入语义

- `create()`：同一事务插入 `sessions`、`session_sequences`、`session_stats`、`main` lane（`leaf_id=NULL`）并 claim writer lease。
- `open()`：要求 session 行存在，claim writer lease；同进程已有 storage 时复用。
- `appendEntry(entry, lane)`：读 lane head 作 `parent_id`，校验全局唯一 id，分配 `seq`，写 `entries`，`setLaneLeaf()` 推进该 lane，更新 branch cache 与 message count，advance sequence。
- `moveLane(lane, leafId|null)`：校验 entry 存在，写 `lanes` 并追加 `lane_moves` 审计；这是 `/tree`、`session/load` 显式 leaf 的持久化入口。
- `appendRecord()`：`operation_started` 写 `lanes.open_operation_id`，`operation_finished` 清空；`usage` record 累加 `session_stats`。
- `setName/setLabel`：只写 `facts`，不写 `entries`；`setLabel(undefined)` 写 `NULL` 清除。
- `delete()`：先释放本进程 storage，再 claim lease，按依赖序删除 branch/facts/lanes/records/entries/lease/stats/sequence/session row。
- branch cache：`branch_entries` 是派生 cache，canonical 是 `entries.parent_id`；append 时若 parent 已是 tip 则扩展，否则复制到 parent seq 再建新 branch；`branch_tips` 乐观更新，冲突返回 `Branch tip changed during append`。

### 4.4 Writer Lease

- 模型：per-operation。写操作开始时 claim，操作期间 heartbeat 续租，结束时 release；短写操作也可只在事务前 claim、事务内 renew、结束后 release。
- 默认 `ttlMs=30000`、`heartbeatIntervalMs=10000`，heartbeat 必须严格小于 TTL。
- 语义照 pi：`claim` 插入 `fence=1`；冲突且已过期才 takeover 并 `fence+1`；`renew` / `release` 校验 `owner+fence+未过期`。
- 同进程内由 `Store` 互斥串行写，避免同一进程两个 session 对象自抢 lease；跨进程由 lease 保证。
- `open_operation_id` 与 writer lease 分离：前者是运行期操作标记，后者是写入保护。

### 4.5 sessions.metadata

- JSON 只放 pigo 扩展字段：`workspaceHost`、`tags`、`status`、subagent 信息等。
- `name` / label 走 `facts`，不重复存 metadata。
- 旧 `customMetadata["curLeaf"]` 不再写入；读取时忽略并视为迁移残留。

## 5. 会话树领域模型

### 5.1 Typed Entry

域模型对齐 pi 的 9 种 entry：

| type | 关键字段 | 上下文投影 |
|---|---|---|
| `message` | `message`（agentcore JSON） | 直接作为 agentcore message |
| `model_change` | `provider`、`modelId` | 不生成消息，推导当前 model |
| `thinking_level_change` | `thinkingLevel` | 不生成消息，推导当前 thinking level |
| `compaction` | `summary`、`retainedTail`、`tokensBefore`、`details?`、`usage?` | `CompactionMessage(summary)` + `retainedTail` |
| `branch_summary` | `fromId`、`summary`、`details?`、`usage?` | 生成 branch summary 上下文消息 |
| `custom` | `customType`、`data?` | 不生成消息（扩展投影器可注入） |
| `custom_message` | `customType`、`content`、`display` | 投影为用户消息 |
| `label` | `targetId`、`label` | 不生成消息，映射到 facts |
| `session_info` | `name?` | 不生成消息，映射到 facts |

SQLite `entries` 表只存 7 种树 entry（不含 `label` / `session_info`）；`facts` 表是 name/label 的 canonical。v4 JSONL 与 ACP 结构化输出识别全部 9 种。

### 5.2 Lanes

- 同一会话可有多个命名 lane，每个 lane 持有独立 `leaf_id`。
- `appendEntry` 只推进调用方所在 lane 的 leaf；其他 lane 不变。
- 所有 lane 共享同一棵 `entries` 树。
- `main` 是默认游标；`side` / `remote` 等用于侧线程与多客户端并发位置。
- lane 移动是审计事件，写 `lane_moves`，不修改历史 entry。

### 5.3 ID 规则

- session id：uuidv7；子会话特例 `subagent-<sha256(parentSessionId + "\x00" + toolCallId)[:16]>`。
- entry id：8-hex，碰撞时重新生成。
- branch cache id / record id：uuidv7。
- v4 JSONL 导入总是创建新 uuidv7 session id，原 id 只作血缘记录。

## 6. v4 Typed JSONL（仅导出/导入）

### 6.1 Header

```json
{
  "type": "session",
  "version": 4,
  "id": "0196f1c6-...",
  "createdAt": "2026-08-12T10:30:00+08:00",
  "updatedAt": "2026-08-12T10:31:00+08:00",
  "cwd": "E:/project/pigo",
  "model": "deepseek/deepseek-v4-pro",
  "provider": "deepseek",
  "systemPrompt": "...",
  "additionalDirectories": ["E:/project/pi"],
  "parentSessionId": "",
  "leafId": "00000002"
}
```

规则：

- `type/version/id/createdAt/cwd` 必填；其余可选，空值 `omitempty`。
- `leafId` 为 `main` lane 在导出时刻的 leaf；`null` 表示空会话（reset）。
- 旧 v1/v2/v3 header 一律拒绝，不自动迁移。

### 6.2 Entry 行

外层字段照 pi：`type/id/parentId/timestamp`（ISO 字符串）。行序即 SQLite `seq` 物理序，父 entry 必须先于子 entry；不写 `seq` 字段。

```json
{"type":"message","id":"00000001","parentId":null,"timestamp":"2026-08-12T10:30:00+08:00","message":{"role":"user","content":[{"type":"text","text":"hello"}],"timestamp":1780000000000}}
{"type":"message","id":"00000002","parentId":"00000001","timestamp":"2026-08-12T10:31:00+08:00","message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"provider":"deepseek","model":"deepseek-v4-pro","stopReason":"end_turn","timestamp":1780000060000}}
{"type":"compaction","id":"00000003","parentId":"00000002","timestamp":"2026-08-12T10:32:00+08:00","summary":"...","retainedTail":[{"role":"assistant",...}],"tokensBefore":12345}
{"type":"label","id":"00000004","parentId":"00000001","timestamp":"2026-08-12T10:33:00+08:00","targetId":"00000001","label":"任务"}
{"type":"session_info","id":"00000005","parentId":null,"timestamp":"2026-08-12T10:34:00+08:00","name":"API 迁移"}
```

### 6.3 无损往返

- 导出：header + 全部 typed entries + `main.leaf_id` + facts 当前值（name/label 落成 `session_info` / `label` entry）+ 可派生统计。
- 不导出 `lanes/records/lane_moves/writer_leases`；side lanes 不保留，导入后重建默认 `main` lane。
- 导入：总是新会话；按文件行序分配新 `seq`；校验 header、entry type、唯一 id、`parentId` 存在、`leafId` 存在或为 null、facts 目标存在；任何非法输入整体失败，不产生部分会话。
- label/session_info 按 seq 重放并写入 `facts` 最新值。
- `/import` 遇 v1/v2/v3 返回明确错误，不探测旧目录、不输出 legacy 专用提示。

## 7. Leaf 投影与 Resume

### 7.1 `ProjectLeaf`

新增统一投影入口（建议 `internal/session` 的 `ProjectLeaf` 类型 + `BuildProjection(repo, sessionID, explicitLeafID)`）：

1. `leaf = explicitLeafID ?? lanes.main.leaf_id`；`null` 返回空路径。
2. `path = PathToLeaf(entries, leaf)`，按 `parent_id` 链 root→leaf。
3. 找路径上最新 `compaction`：
   - 存在时上下文条目为 `[compaction, ...compaction 之后的 entries]`；
   - `compaction.retainedTail` 是自包含 checkpoint，不再依赖 `firstKeptEntryId` 重放被压缩 entry。
4. `messages = flatten(path/contextEntries)`：
   - `message` → 原 agentcore message；
   - `compaction` → `CompactionMessage(summary, tokensBefore)` + `retainedTail`；
   - `branch_summary` → branch summary 上下文消息；
   - `custom_message` → 用户消息；
   - `model_change` / `thinking_level_change` / `custom` / `label` / `session_info` → 不产生消息。
5. `model/thinkingLevel` 从路径上最新 `model_change` / `thinking_level_change` / assistant `provider/model` 推导。
6. 只有该函数能构建 `AgentContext.Messages`；所有前端禁止自行从文件末行推断 leaf。

### 7.2 调用方收敛

| 调用方 | 现状 | 目标 |
|---|---|---|
| REPL/TUI `--resume` | `entries[len-1].ID` | `ProjectLeaf`，默认 `lanes.main.leaf_id` |
| headless `--resume/--continue` | `entries[len-1].ID` | `ProjectLeaf` |
| serve promptRun | `LoadEntries` + metadata curLeaf | `ProjectLeaf` |
| HTTP `session/load` | `LoadEntries` 全量再切片 | `ProjectLeaf` 路径 + pagination |
| HTTP `/tree N` | metadata curLeaf | `moveLane("main", target)` + `ProjectLeaf` |
| ACP `session/load` | replay 标准消息 | `ProjectLeaf` + `_meta` |

### 7.3 一致性

- `lanes.leaf_id` 指向不存在 entry 视为存储损坏，fail-closed 拒绝打开。
- 旧 v1-v3 文件即使临时用于展示，也只能把末条 entry 视为临时 leaf，不得写入 `lanes`。
- 进程内 `curLeaf` / `persisted` 只作游标，初始化自 `ProjectLeaf`，只能由 `AppendTurn/MoveLeaf` 更新。

## 8. Compaction 对齐

### 8.1 Entry 形态

- compaction entry 采用 harness 风格：`summary`、`retainedTail: AgentMessage[]`、`tokensBefore`、`details?`、`usage?`、`fromHook?`。
- 不保留 `firstKeptEntryId`；上下文投影只读 compaction entry 与它之后的 entry。
- 上一次 compaction 的 `retainedTail` 虚拟成消息 entry 纳入下一次可压缩范围。

### 8.2 Split Turn

- `FindCutPoint` 已识别 `TurnStartIndex/IsSplitTurn`。
- 切点落在单轮中间时：先对完整历史生成 history summary，再对 `[turnStart, firstKept)` 生成 turn prefix summary，最后合并为一个 compaction entry。
- toolResult 永远不可切；合法切点包括 user / assistant / custom_message / branch_summary。

### 8.3 迭代 Summary

- `Compact` 接收上一次 compaction entry 的 `retainedTail`、`details`、`summary`。
- 汇总范围从上一次 `retainedTail` 末尾之后开始；文件操作从上次 `details` 累积。
- 有 `previousSummary` 时走 update prompt，不再每次全量总结。

### 8.4 Overflow 恢复

- 同模型 `isContextOverflow || recoverableLength` 且 `stopReason != "stop"` 时：从 agent state 移除失败的 assistant 消息，压缩后自动重试一次（`_overflowRecoveryAttempted` 保证只试一次）。
- 每次 prompt 提交前检查上一次 aborted 响应；跳过模型不匹配的 overflow 与 compaction 边界之前的旧 usage。

### 8.5 落盘与 UI

- 压缩永远作为新 `compaction` entry 追加到当前 lane 并推进 leaf；REPL/TUI/serve/headless 全部走同一 `AppendCompaction`。
- 删除压缩后 `Save()` 线性重写压平树的路径。
- TUI：compaction 期间输入进 pending queue；`compaction_end` 后整批作为 steering 注入；`Alt+Up` 取回编辑器；失败/中止恢复队列。
- REPL：compaction 期间阻塞读输入，Ctrl+C 中止，typed-ahead 保留。
- 不再有 `memory/checkpoint.md` 写读路径；`/rebuild` 语义改为从 compaction entry 重建。

## 9. ACP 与 HTTP 树 Surface

### 9.1 版本协商

```json
// client initialize
{"protocolVersion":1,"clientCapabilities":{"_meta":{"pigo":{"sessionTree":{"version":1}}}}}

// server initialize result
{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"_meta":{"pigo":{"sessionTree":{"version":1}}}}}
```

- 命名空间固定 `_meta.pigo.sessionTree`；旧 `pigo/*` 方法与事件不复活。
- pigo 总是声明 v1，但只对声明 v1 的客户端发送树元数据。
- 客户端版本 >1 或未知：pigo 响应 v1，客户端可降级；非法声明视为未声明，不报错。
- 启用状态绑定连接级，`initialize` 时确定。
- TUI 与 `sdk/node/pigo-acp` 默认声明 v1。

### 9.2 消息元数据

所有 `session/update` 消息（chunk、tool_call、tool_call_update、permission）对声明客户端附加：

```json
"_meta": {"pigo": {"sessionTree": {"version":1,"entryId":"00000002","parentId":"00000001","entryType":"message","seq":2,"lane":"main"}}}
```

- 标准 `messageId` 始终发送且等于 entry id。
- `tool_call` / `tool_call_update` 的 `entryId` 指向所属 assistant entry，不带独立 `messageId`。
- `entryId` 在流式开始前分配；user entry 在 prompt 开始时 append，assistant entry 在 turn 结束时 append；失败/取消不 append 最终态。
- live user 消息只对声明客户端回发 `user_message_chunk`，只含文本；slash 命令不创建 entry；hybrid prompt 用展开后文本创建 user entry。

### 9.3 Leaf 通知

每次 entry append 推进 leaf 与每次 lane move 后发送 `session_info_update`：

```json
{"sessionUpdate":"session_info_update","_meta":{"pigo":{"sessionTree":{"version":1,"entryId":"abc123","entryType":"message","currentLeafId":"abc123","currentLane":"main","lanes":[{"lane":"main","leafId":"abc123"},{"lane":"side","leafId":"def456"}]}}}}
```

### 9.4 session/load

- 只回放当前 main lane leaf 的 root→leaf 路径。
- 回放 user/assistant/thought 文本与历史工具最终态；compaction 等非消息 entry 不流式。
- 历史工具只回放最终 `tool_call_update`（completed/failed），带 `rawInput/rawOutput/entryId`。
- parent 链断裂时回放可解析前缀；`lanes.main.leaf_id` 无效时报错。
- 响应 `_meta` 带 `currentLeafId/currentLane/lanes`；`session/new` 同样带空 main lane。
- 若请求显式带 `leafId/entryId`（ticket 06 扩展字段），先 `moveLane("main", leafId)` 再投影。

### 9.5 结构化 /tree

- `/tree` 仍是斜杠命令；HTTP `POST /api/v1/session/{id}/command` 的 `PromptResponse` 增加可选 `structured = {version, kind, data}`；ACP 映射为 `_meta.pigo.structured`。
- `data`：

```json
{
  "nodes": [
    {"id":"00000001","parentId":null,"kind":"user","summary":"...","timestamp":"2026-08-12T10:30:00+08:00","label":"任务"}
  ],
  "currentLeafId":"00000002",
  "currentLane":"main",
  "activePathIds":["00000001","00000002"],
  "labels":{"00000001":"任务"},
  "lanes":[{"lane":"main","leafId":"00000002"}]
}
```

- `nodes` 只含逻辑树 entry（不含 `label/session_info` 节点），`kind` 枚举保留 pi-web `SessionTreeNodeKind` 全集。
- `nodes` 不带 seq，pre-order 输出，children 按 seq 升序；孤 child 作为根输出，不加标记。
- `summary` 按 pi-web 投影规则生成；`labels` 是 string map；`currentLeafId=null` 时 `activePathIds=[]`；`lanes` 中 main 第一，其余按名称字母序。
- `/tree N` 返回更新后完整快照 + 文本确认，并触发 `session_info_update`；空树返回空快照；非法参数/越界走命令错误，不返回 structured。
- `available_commands_update` 的 tree 命令对象带 `_meta.pigo.structuredKinds: ["sessionTree"]`，只发给声明客户端。
- 文本 fallback 继续使用 `RenderTreeLines` 编号文本。

### 9.6 session/status

- `GET /api/v1/session/{id}/status` 返回 `currentLeafId/currentLane/lanes`；`null` 表示空会话。
- ACP 不新增 `session/status`；`/status` 文本显示 `leaf: <id> (lane: main)`。
- 本次不引入 `_meta.pigo.structured.kind="sessionStatus"`。

### 9.7 命令面

- 不新增 `session/tree`、`session/status`、`pigo/tree`。
- 所有能力保持“标准 ACP 方法 + 斜杠命令 + `_meta` 扩展数据”。
- `tree`、`label` 通过 `available_commands_update` 暴露；未知命令仍按普通文本交给模型。

## 10. 子 Agent 会话图

### 10.1 插件化方向

- 删除内置 `task` 工具、`taskGuide`、`NewTaskTool` 的默认注册。
- 核心保留 `SubAgentTool` / `ChildSession` / SQLite 原语作为执行承载体。
- 插件通过 manifest `subagents[]` 声明式注册子 agent 工具；核心负责子会话创建、血缘、持久化与恢复。
- 子 agent 调用不走 `tools/call` 往返；插件声明定义与编排，核心执行。

### 10.2 ID 与创建

- 子会话 id 保留确定性派生：`subagent-<sha256(parentSessionId + "\x00" + toolCallId)[:16]>`，SQLite 中是非 uuidv7 特例。
- 调用开始时 eager 创建 SQLite 行：同一事务写 `sessions`、`session_sequences`、`session_stats`、`main lane` 并 claim writer lease。
- 崩溃/取消后仍可重跑，不延迟建行。

### 10.3 血缘与 metadata

- `sessions.parent_session_id` 是唯一血缘权威。
- `sessions.metadata` JSON 只写扩展字段：`sessionKind:"subagent"`、`subagentType`（工具名）、`plugin`（插件名）、`parentToolCallId`。
- 不重复写 `parentSessionId` 到 customMetadata；读取方从列重建父 id。
- 删除 `subagents.json`；`Registry` 只保留 live map，持久化关系从 SQLite 派生。
- 默认子会话名使用工具名，task description 可覆盖。

### 10.4 列表与生命周期

- `session/list` 默认隐藏子会话；`/resume` 继续过滤；HTTP `?includeSubagents=true`、ACP `_meta.pigo.sessionList` v1 时返回。
- 子会话 summary 带 `parentSessionId/parentToolCallId/subagentType/plugin`。
- 状态：`active` / `completed`（可继续）/ `archived`（终态）；running 不落 metadata，由 `records` / `lanes.open_operation_id` 表达。
- 子会话完成后仍是标准会话，`session/prompt` 走 `ChildSession.Continue()`，`session/cancel` 取消当前回合。
- 父删除不级联，child 保留，`parent_session_id` 允许悬挂；running child 的 `session/delete` 返回冲突，必须先 cancel；child 可单独删除。
- `isolation` 默认 `goroutine`，`process` 显式声明；process 模式只用 builtin 工具。
- `tools` 按 builtins + 全部插件工具合并注册表解析，未知名称 fail。
- 嵌套默认禁止；agent 声明 `nested/maxDepth` 才允许，core 用 context depth 守卫；child 工具集默认剔除 subagent 工具。

## 11. 旧格式移除与入口清理

### 11.1 运行时

- v1/v2/v3 JSONL 与 `$PIGO_HOME/sessions` / `$PIGO_HOME/projects/*/sessions` 旧平铺目录运行时完全不可读。
- 不迁移、不向后兼容；旧 id 统一返回标准 not found。
- `internal/session` 删除 `Store/Load/LoadEntries/Append/AppendBranch/Fork/PathToLeaf` 等 JSONL 运行时 API，保留 codec/projection/rendering。
- `internal/sessionstore` 删除 `.metadata.json` / `index.json` / `*.jsonl` 运行时路径，重写为 SQLite canonical。

### 11.2 磁盘隔离

- pigo 不移动旧文件。
- 新增 `scripts/quarantine-legacy-sessions.ps1` 与 `.sh`，把 `$PIGO_HOME/sessions` 与 `$PIGO_HOME/projects/*/sessions` 移到 `$PIGO_HOME/legacy-sessions/`。
- 只移动、不删除、不转换；升级文档要求手动执行一次。

### 11.3 入口语义

- `--list-sessions` / `--resume` / `--continue` / `/resume` 保留，语义不变，数据源换成 SQLite canonical。
- `/export` 从 SQLite 生成 v4 JSONL（默认）或自包含 HTML（只读分享，不参与导入）。
- `/import` 只接受 v4 JSONL；v1/v2/v3 明确报错。

## 12. TUI/REPL 树交互

线框素材：`research/02-tree-interaction-wireframes.md`。实现范围：

- TUI 仅 `/tree` 进入全屏树选择器；输入框隐藏，Esc 返回主界面，主编辑器草稿不丢失。
- 默认选中当前 leaf；当前 leaf 是 no-op，Enter 关闭并提示 `Already at this point`。
- 当前分支（含 `<- current`）优先，其余按时间顺序。
- 默认视图隐藏 label/custom/model_change/thinking_level_change/session_info 与纯工具 assistant；保留 user、assistant 文本、tool result、compaction、branch_summary。
- 选中 user/custom_message：leaf 指向其 parent，主编辑器为空时回填消息文本；选中其他 entry：leaf 指向该 entry；根 user 等效 reset。
- `Shift+L` 编辑当前行 label；空值保存即删除；REPL `/label <树行号> [文本]` 与 `/tree` 共用编号。
- 每次非当前 leaf 导航询问 branch summary 三选一：No summary / Summarize / Summarize with custom prompt；`[tree] branch_summary_skip_prompt=false` 默认询问，`true` 跳过，`--summary` 可覆盖。
- branch summary 生成中可 Esc/Ctrl+C 取消，成功才提交导航，失败回到树选择器并显示原因。
- compaction 状态显示触发原因（manual/threshold/overflow）；TUI queue + `Alt+Up` 取回 + 失败恢复；REPL 阻塞 + Ctrl+C 中止。
- 后置项不进入本 spec 验收：树搜索、过滤、折叠、复制、label 时间戳、TUI `/fork` `/clone` 选择器。

## 13. 配置

```toml
[tree]
branch_summary_skip_prompt = false

[compaction]
reserve_tokens = 16384
keep_recent_tokens = 20000
```

- `[tree].branch_summary_skip_prompt` 为本次新增。
- compaction 增加 pi 风格的 `reserve_tokens` / `keep_recent_tokens`（默认值与 pi 一致）。
- `[memory]` / `[checkpoint]` 中仅 checkpoint.md 会话集成相关项在本 effort 移除；memory 能力本身另起 effort 设计。

## 14. 文件结构变化

```text
internal/session/
  entry.go                 [REWRITE: typed entry + 9 types]
  v4jsonl.go               [NEW: Write/ReadV4JSONL]
  projection.go            [NEW: ProjectLeaf/BuildProjection]
  tree.go                  [REWRITE: RenderTreeLines + labels]
  export.go / export_html.go / inherit.go
                           [REWRITE: v4 export/import]
internal/sessionstore/
  store.go                 [REWRITE: SQLite Store]
  migrations.go            [NEW]
  migrations/001_initial.sql [NEW: pi schema + FTS + triggers]
  repo.go                  [NEW: create/open/append/move/delete]
  writer_lease.go          [NEW]
  branch_cache.go          [NEW]
  facts.go                 [NEW]
  fts.go                   [NEW]
internal/compaction/
  compact.go               [REWRITE: retainedTail + split turn + iterative]
  cutpoint.go              [REWRITE: expose split turn payload]
  summary.go               [REWRITE: turn prefix merge]
internal/runtime/
  checkpoint.go            [DELETE or reduce to compaction entry bridge]
  rebuild.go               [REWRITE: compaction entry rebuild]
  loop.go                  [REWRITE: overflow retry + AppendCompaction]
  subagent_child.go        [REWRITE: SQLite child sessions, drop subagents.json]
  task.go                  [DELETE]
internal/acp/
  http_adapter.go          [MODIFY: initialize meta, load meta, structured]
  events.go                [MODIFY: attach sessionTree meta]
internal/httpapi/
  sessions.go              [REWRITE: SQLite + ProjectLeaf]
  session_commands.go      [REWRITE: /tree /label /export /import]
  gen/api.gen.go           [REGENERATE: structured + leaf fields]
internal/cli/
  repl/                    [MODIFY: /tree /label, summary, compaction abort]
  tui/                     [MODIFY: tree modal, queue]
cmd/pigo/
  prompt_runner.go         [REWRITE: ProjectLeaf + SQLite]
  goal_host.go             [REWRITE: same projection]
scripts/
  quarantine-legacy-sessions.ps1 [NEW]
  quarantine-legacy-sessions.sh  [NEW]
docs/adr/
  0006-*.md ... 0010-*.md  [NEW]
```

## 15. 错误与验收边界

| 场景 | 行为 |
|---|---|
| 未知 session id | 标准 not found，不探测旧目录 |
| `lanes.leaf_id` 悬空 | 视为存储损坏，拒绝打开 |
| v1/v2/v3 导入 | 明确错误，整体失败 |
| v4 import 非法 entry | 整体失败，不产生部分会话 |
| `/tree N` 越界 | 命令错误，无 structured 返回 |
| running child 删除 | 冲突，需先 cancel |
| writer lease 丢失 | 写操作报 `writer lease was lost`，不静默重写 |
| compaction 失败 | 非致命事件，TUI/REPL 恢复输入；不切换 leaf |

硬验收：

- 代码 grep 无旧运行时路径引用（`LoadEntries`、`customMetadata["curLeaf"]`、`sessions/<id>/checkpoint.md` 写读等）。
- `--resume/--continue/--list-sessions//resume` 全部来自 SQLite。
- v4 round-trip、v1/v2/v3 拒绝、旧 id not found、隔离脚本幂等均有测试。
- `go build ./...` 与相关包测试通过；完整 `go test ./...` 遵循 AGENTS.md Windows 环境性失败说明。

## 16. 测试策略

| 层 | 覆盖 |
|---|---|
| sessionstore | migrations、writer lease takeover/renew/release、append/move/delete、branch cache 重建、facts、FTS、fork、跨进程语义 |
| session codec | 9 种 entry 编解码、v4 round-trip、header/leaf 校验、非法输入拒绝 |
| projection | root→leaf、compaction retainedTail、split turn、model/thinking 推导、空会话 |
| compaction | split turn 合并、迭代 summary、overflow 单次重试、append 推进 lane、queue 失败恢复 |
| acp | initialize 双向声明、meta 附加、session/load replay、structured /tree、无新增方法 |
| subagent | eager 建行、确定性 id、列表过滤、父删除不级联、running child 删除冲突 |
| cli | TUI/REPL 树交互、label、branch summary、compaction abort/queue |
| e2e | Zed 手动验收、serve + ACP 跨前端 leaf 一致性、legacy 隔离脚本 |

## 17. 实施阶段

1. **地基**：SQLite schema/repo/writer lease/FTS（实现 issue 01-02）。
2. **格式**：v4 JSONL codec 与投影（issue 03-04）。
3. **语义**：compaction 对齐与子 agent 图（issue 05-08）。
4. **接口**：ACP/HTTP 树 surface 与旧格式清理（issue 09-12）。
5. **前端**：TUI/REPL 树交互（issue 13-14）。
6. **收口**：集成验收、文档、ADR、map 收尾（issue 15-16）。

依赖顺序必须满足：SQLite > codec/projection > compaction/子 agent > ACP/旧格式清理 > UI > 验收。

## 18. 实现 Ticket 映射

完整 issue 见 `.scratch/session-tree-pi-alignment/issues/`：

| Issue | 标题 | 依赖 | 优先级 |
|---|---|---|---|
| 01 | SQLite schema + migrations + writer lease | - | high |
| 02 | SQLite repo：entries/lanes/facts/branch cache/FTS | 01 | high |
| 03 | v4 typed JSONL codec | 01, 02 | high |
| 04 | ProjectLeaf 统一投影与 resume 收敛 | 01, 02 | high |
| 05 | Compaction retainedTail + split-turn + 迭代 + overflow | 03, 04 | high |
| 06 | Compaction 落盘统一 + UI queue/abort 事件 | 05 | high |
| 07 | ACP/HTTP 树 surface v1 | 04 | high |
| 08 | 子 agent SQLite 会话图 + 插件注册 | 01, 02 | medium |
| 09 | sessionstore SQLite 重写 + 旧 JSONL 运行时删除 | 01-08 | high |
| 10 | 旧目录隔离脚本 + 入口语义 + 文档/ADR | 09 | high |
| 11 | TUI 树选择器 + label + branch summary | 04, 05, 06 | medium |
| 12 | REPL 树 fallback + compaction 中止 | 04, 05, 06 | medium |
| 13 | sdk/node/pigo-acp 默认声明 v1 | 07 | medium |
| 14 | OpenAPI / generated client 更新 | 07, 09 | medium |
| 15 | 集成验收 + 回归 + 文档 | 01-14 | high |
| 16 | map/tracker 收尾 | 15 | low |

## 19. 未决问题与风险

### 19.1 未决

- `/rewind` 与新树模型的关系（另开 ticket）。
- 多 serve 进程共享单库时的 open/close 生命周期（本 spec 只要求 lease 保护写，不做进程级 storage 生命周期契约）。
- 树搜索、过滤、折叠、复制、label 时间戳。
- TUI `/fork` / `/clone` 选择器。

### 19.2 风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| 大规模重写破坏现有 serve/ACP 兼容 | 高 | 阶段化落地，先 SQLite+投影，再删旧路径；每阶段保留测试 |
| `modernc.org/sqlite` FTS5/trigger 行为差异 | 中 | 地基 ticket 加 FTS 冒烟测试；pin 版本 |
| writer lease 与现有同步 run loop 冲突 | 高 | per-operation claim 只包 DB 写，不包 LLM；同进程互斥 |
| compaction retainedTail 使 JSONL 行变大 | 中 | 仅在导出/导入中出现 retainedTail；SQLite payload 可大行 |
| 子 agent 插件化破坏现有 task 用户 | 中 | 一次移除，无兼容 shim；文档明确升级路径 |
| `go test ./...` Windows 既有环境失败 | 低 | 相关包单测优先，AGENTS.md 已记录 |
