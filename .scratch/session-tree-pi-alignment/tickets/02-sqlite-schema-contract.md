# 02 - SQLite schema 与 writer lease 契约

**Type:** grilling
**Status:** resolved
**Blocked by:** 01

## Question

逐字复刻 pi SQLite schema 时，pigo 的 DDL、索引、`session_sequences`、`session_stats`、branch cache 重建、facts（name/label）、writer lease TTL/heartbeat 如何落到单库 `$PIGO_HOME/sessions.db`？FTS 会话搜索是否纳入首版？

## Answer

- writer lease：per-operation 模型。写操作开始时 claim，操作期间 heartbeat 续租，结束 release；默认 `ttlMs=30000`、`heartbeatIntervalMs=10000`，takeover 递增 fence，renew/release 校验 owner+fence，语义照搬 pi。
- FTS：首版纳入。同库建 `session_search_fts`（FTS5 trigram + content triggers + bm25），搜索按 entry payload、facts name、cwd 过滤。
- ID：session id 用 uuidv7；entry id 用 pi 的 8-hex；branch cache id 用 uuidv7。
- session_stats：保留 pi 完整列；pigo 只填充 `message_count`、`total_tokens` 等可得字段，`cached_tokens/uncached_tokens/cost_total` 默认 0。
- 迁移与连接：`migrations` 表 + 编号 SQL 迁移；PRAGMA 对齐 pi（WAL、`synchronous=FULL`、`busy_timeout=5000`）；进程内单 lazy DB 连接，写操作走事务。
- sessions.metadata：JSON 只放 pigo 扩展字段（workspaceHost、tags、status、subagent 信息等）；name/label 走 `facts`，不重复存储。
