# 02 - SQLite repo：entries/lanes/facts/branch cache/FTS

**What to build:** 在 schema 之上实现 `internal/sessionstore` 的 repo 层：`create/open/delete`、`appendEntry`、`moveLane`、`appendRecord`、`setName/setLabel`、branch cache 重建、session_stats、list/query、FTS 搜索（bm25 + cwd 过滤）。

**Blocked by:** 01 - SQLite schema + migrations + writer lease

**Status:** resolved

## Acceptance Criteria

- [x] `create()` 同一事务写 sessions/sequences/stats/main lane 并 claim lease
- [x] `appendEntry` 以 lane head 为 parent，分配 seq，推进该 lane，更新 branch cache 与 message_count
- [x] `moveLane` 持久化 leaf 并追加 `lane_moves`；`leaf_id=null` 表示 reset
- [x] `appendRecord` 维护 `lanes.open_operation_id` 与 `session_stats`
- [x] `setName/setLabel` 走 facts，`undefined` 写 NULL 清除
- [x] branch cache 可从 entries 重建；append 冲突返回 `Branch tip changed during append`
- [x] `delete()` 按依赖序清理并释放 storage/lease
- [x] FTS 搜索命中 message/custom_message/branch_summary/compaction/name/label，支持 cwd 过滤
- [x] 未知 session/entry 返回标准 not found

**Type:** backend

**Priority:** high

**Spec Reference:** `tasks/spec-session-tree-pi-alignment.md` §4.2-4.5

