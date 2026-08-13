# 03 - v4 typed JSONL codec

**What to build:** `internal/session` 瘦身为 v4 codec：`SessionHeader` v4、9 种 typed entry、`WriteV4JSONL/ReadV4JSONL`、严格校验（header/entry type/唯一 id/parentId/leafId/facts）、v1/v2/v3 拒绝、export/import round-trip。

**Blocked by:** 01 - SQLite schema + migrations + writer lease, 02 - SQLite repo

**Status:** resolved

## Acceptance Criteria

- [x] header 单行 `{"type":"session","version":4,...}`，pigo 扩展字段 `omitempty`
- [x] 9 种 entry 编解码完整；外层字段照 pi，`message` 保留 agentcore JSON
- [x] 行序即 seq 物理序，父 entry 先于子 entry；不写 seq 字段
- [x] label/session_info 在 JSONL 中表达 name/label facts
- [x] export 携带 `main.leafId` 与 facts 当前值；不导出 lanes/records/lane_moves/writer_leases
- [x] import 总是创建新 uuidv7 会话，按行序分配 seq，校验 parentId/leafId
- [x] 任何非法输入整体失败，不产生部分会话
- [x] v1/v2/v3 明确拒绝；round-trip 测试通过

**Type:** backend

**Priority:** high

**Spec Reference:** `tasks/spec-session-tree-pi-alignment.md` §5, §6

