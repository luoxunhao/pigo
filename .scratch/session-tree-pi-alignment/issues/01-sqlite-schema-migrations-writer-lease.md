# 01 - SQLite schema + migrations + writer lease

**What to build:** 建立 `$PIGO_HOME/sessions.db` 的 SQLite 地基：`schema_migrations`、pi `001_initial.sql` 的完整表结构、`session_search_fts`（FTS5 trigram + triggers）、PRAGMA（WAL/synchronous=FULL/busy_timeout=5000/foreign_keys）、进程内单 lazy `*sql.DB`，以及 per-operation writer lease（claim/renew/release/takeover/fence/expiry）。

**Blocked by:** None

**Status:** ready-for-agent

## Acceptance Criteria

- [ ] `sessionstore.Open(pigoHome)` 幂等创建 `sessions.db` 与全部表/索引/虚拟表
- [ ] `schema_migrations` 记录已应用版本，SQL 迁移在事务内执行
- [ ] PRAGMA 生效且有测试可观测
- [ ] FTS5 虚拟表可创建，`entries` / `facts` trigger 保持索引同步
- [ ] writer lease 默认 `ttlMs=30000`、`heartbeatIntervalMs=10000`，heartbeat 严格小于 TTL
- [ ] takeover 递增 fence；renew/release 校验 owner+fence+未过期
- [ ] 丢失 lease 的写操作报错，不静默重写
- [ ] 对应包测试通过

**Type:** backend

**Priority:** high

**Spec Reference:** `tasks/spec-session-tree-pi-alignment.md` §4.1-4.4

