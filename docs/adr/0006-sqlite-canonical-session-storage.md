# SQLite canonical session storage: single DB, pi-compatible schema

pigo 的会话持久化从“项目级 JSONL 文件 + metadata/index + 进程内 curLeaf”迁移为单库 `$PIGO_HOME/sessions.db`，并逐字复刻 pi `001_initial.sql` 的 `sessions/entries/session_sequences/session_stats/branch_entries/lanes/records/lane_moves/facts/branch_tips/writer_leases` schema。`internal/sessionstore` 重写为 SQLite 唯一 canonical 存储入口；`sessions.cwd` 区分项目；`lanes.main.leaf_id` 是 resume 与所有前端的 leaf 权威。

写入保护采用 per-operation writer lease（默认 `ttlMs=30000`、`heartbeatIntervalMs=10000`，takeover 递增 fence，renew/release 校验 owner+fence），支持同一 `sessions.db` 被多个 pigo 进程共享。`entries.parent_id` 仍是树的 canonical 来源，`branch_entries/branch_tips` 只是派生 cache。name/label 走 `facts` 表，不重复存 `sessions.metadata`；metadata JSON 只放 pigo 扩展字段。

首次版本同时纳入 `session_search_fts`（FTS5 trigram + content triggers + bm25）与 `schema_migrations` 版本表。本 ADR 不保留旧 JSONL 运行时兼容，旧格式处理见 `0008`。

完整设计见 `tasks/spec-session-tree-pi-alignment.md` §4。

