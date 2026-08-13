# 07 - ACP/HTTP 树 surface v1

**What to build:** `initialize` 双向声明 `_meta.pigo.sessionTree` v1；所有 `session/update` 消息/工具事件附加 `_meta.pigo.sessionTree`；`session/load` 回放 leaf 路径并携带 meta；`/tree` 返回结构化 `structured`；`session_info_update` 通知 currentLeaf/lanes；`available_commands_update` 声明 `structuredKinds`；不新增非标准 `session/*` 方法。

**Blocked by:** 04 - ProjectLeaf 统一投影

**Status:** resolved

## Acceptance Criteria

- [x] 客户端声明 v1 才发送树元数据；未知/缺失声明回退标准字段
- [x] 每个消息 chunk 与 tool_call/tool_call_update 带完整 sessionTree meta
- [x] `session/load` 只回放 root→leaf 路径；compaction 等非消息 entry 不流式
- [x] 历史工具只回放最终态并带 `rawInput/rawOutput/entryId`
- [x] `/tree` 的 `PromptResponse.structured` 与 ACP `_meta.pigo.structured` 映射正确
- [x] `session_info_update` 在 append 推进 leaf 与 lane move 后发送
- [x] `GET /session/{id}/status` 返回 currentLeafId/currentLane/lanes
- [x] 不新增 `session/tree`/`session/status`/`pigo/tree`
- [x] 自动化测试覆盖方法面与通知面仍是标准面

**Type:** fullstack

**Priority:** high

**Spec Reference:** `tasks/spec-session-tree-pi-alignment.md` §9

