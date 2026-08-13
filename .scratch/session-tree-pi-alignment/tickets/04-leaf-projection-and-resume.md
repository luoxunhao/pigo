# 04 - leaf 投影与 resume 语义

**Type:** grilling
**Status:** resolved
**Blocked by:** 01, 02

## Question

main lane 的 leaf 如何持久化；resume / `session/load` 如何选 leaf；pigo 的 `buildSessionContext` 等价算法是什么；REPL/TUI/serve/ACP 如何共享同一套 leaf 投影，避免 serve 仍按文件顺序读全部 entry。

## Answer

已确认（2026-08-12）。

- **main lane leaf 持久化**：canonical 是 SQLite `lanes` 行。创建会话时写入 `lanes(main, leaf_id=NULL)`；`appendEntry()` 在同一事务内读取 lane head 作为新 entry 的 `parent_id`、写入 `entries`、再用 `setLaneLeaf()` 把 `lanes.leaf_id` 推进到新 entry；`/tree` 切换调用 `moveLane("main", leafId|null)` 持久化选择并追加 `lane_moves` 审计。pi 参照：`packages/session-backends/sqlite-node/src/sqlite/repo.ts`（`appendEntry` 463-491、`moveLane` 450-461）与 `packages/session-backends/sqlite-node/src/sqlite/storage/lanes.ts`（`setLaneLeaf` 83-86）。pigo 现有的 `metadata.customMetadata["curLeaf"]` 与进程内 `curLeaf` 降级为缓存或一次性迁移输入，不再作为权威。
- **多 lane 语义**：同一会话可有多个命名 lane，每个 lane 持有独立 `leaf_id`；`appendEntry` 只推进调用方所在 lane 的 leaf，其他 lane 的 leaf 不变；所有 lane 共享同一棵 `entries` 树。`main` 是默认游标；side/remote 等附加 lane 用于侧线程、远程客户端等并发位置。`session.getLanes()`/`Session.view(lane)` 为 pi 侧参照（`packages/session-backends/sqlite-node/src/sqlite/repo.ts` 436-461、`packages/agent/src/harness/session/session.ts` view 83-94）。
- **resume / `session/load` 选 leaf**：默认取 `session.getLeafId()`，即 `lanes.main.leaf_id`，不是文件最后一行，也不是 metadata。`session/load` 若带显式 `leafId/entryId`（ticket 06 的扩展字段），先 `moveLane("main", leafId)` 再投影；`leafId=null` 表示空会话（reset）。`session/load` 返回的窗口来自投影路径，消息带 `entryId/parentId`；字段形状留给 ticket 06，本 ticket 只锁定选择规则。
- **`buildSessionContext` 等价算法**：新增唯一投影入口（建议 `session.ProjectLeaf` 或 `sessionstore.Project`）：
  1. `leaf = explicitLeafID ?? lanes.main.leaf_id`；`null` 返回空路径。
  2. `path = PathToLeaf(entries, leaf)`，即 root→leaf 的 `parent_id` 链。
  3. compaction 投影：路径上有 compaction 时保留最新 compaction 及其后 entries；pi harness 的 `retainedTail` 变体由 ticket 05 对齐。
  4. `messages = flatten(path/contextEntries)`；`model/thinking` 从路径上最新的 `model_change/thinking_level_change/assistant provider/model` 推导。
  5. 只有该函数能构建 `AgentContext.Messages`。
  pi 参照：`packages/agent/src/harness/session/session.ts`（`getLeafId` 134-136、`findEntriesOnBranch` 170-179、默认 start 取 lane leaf 的 `queryBranchEntries` 240-248）与 `packages/agent/src/harness/session/context.ts`（`buildSessionContext` 90-98）。coding-agent 的 `SessionManager.buildSessionPath/buildContextEntries`（`session-manager.ts` 334-360、418-470）是同一语义的 JSONL 侧实现，但其 `_buildIndex` 用“最后一条物理 entry”作 leaf（958-977），是进程内不持久化行为，pigo 不照搬。
- **共享投影与入口收口**：REPL/TUI/headless 的 `--resume`、serve 的 `promptRun`、HTTP `session/load`/`messages`、ACP `session/load` 全部走同一投影入口；`curLeaf/persisted` 只作为进程内游标，初始化自 `Project().LeafID`，且只能由统一 `AppendTurn/MoveLeaf` 更新。serve 不再 `LoadEntries` 后按文件序切片，也不再读 `customMetadata["curLeaf"]`。
- **现状缺口**：`cmd/pigo/prompt_runner.go`（157-226）与 `internal/httpapi/sessions.go`（166-219）都按 `LoadEntries` 全量读再切窗口；`internal/cli/repl/interactive.go`（120-135）、`internal/cli/tui/session.go`（160-174）、`internal/cli/headless/session.go`（219-231）都用 `entries[len(entries)-1].ID` 作为 resume leaf；`internal/httpapi/session_commands.go` 的 `/tree`（102-145）把 leaf 写进 metadata。上述路径全部收敛到共享投影后，serve 与本地前端恢复同一分支。
- **一致性**：未知 leaf 报错；`lanes.leaf_id` 指向不存在的 entry 视为存储损坏（fail-closed 或按 pi `readLanes` 语义拒绝打开）；旧 v1-v3 不进入新 canonical（map 已锁定废弃，ticket 08 负责入口清理），若临时读取旧文件仅用于展示，可把最后一条物理 entry 视作临时 leaf，但不能写入 `lanes` 作为持久状态。
