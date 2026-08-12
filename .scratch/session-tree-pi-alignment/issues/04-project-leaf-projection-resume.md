# 04 - ProjectLeaf 统一投影与 resume 收敛

**What to build:** 新增 `ProjectLeaf` / `BuildProjection`，默认取 `lanes.main.leaf_id`，支持显式 leaf，按 root→leaf 路径 + compaction retainedTail 构造 `AgentContext.Messages`；把所有前端（REPL/TUI/headless/serve/HTTP load/ACP load）收敛到同一入口；`/tree` 改用 `moveLane`；删除 metadata `curLeaf` 权威。

**Blocked by:** 01 - SQLite schema + migrations + writer lease, 02 - SQLite repo

**Status:** resolved

## Acceptance Criteria

- [x] `leaf = explicitLeafID ?? lanes.main.leaf_id`；null 返回空会话
- [x] 路径投影只保留最新 compaction 及其后 entries，并展开 retainedTail
- [x] model/thinkingLevel 从路径上最新 model_change/thinking_level_change/assistant 推导
- [x] 只有该入口能构建 `AgentContext.Messages`
- [x] REPL/TUI/headless/serve/ACP resume 不再用文件末行推断 leaf
- [x] `/tree N` 持久化 `moveLane("main", target)`，不再写 `customMetadata["curLeaf"]`
- [x] `lanes.leaf_id` 悬空 fail-closed
- [x] 跨前端恢复同一分支的集成测试通过

**Type:** backend

**Priority:** high

**Spec Reference:** `tasks/spec-session-tree-pi-alignment.md` §7

