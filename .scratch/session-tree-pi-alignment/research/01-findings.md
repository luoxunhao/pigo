# wayfinder ticket 01：pi 会话树参照面事实清单

> 调研基准：`E:\project\pi`，`main @ 666d8972ff0b6da5067e05973249760964194769`。
> 除“与 pigo 当前实现的差异点”外，本文件只陈述 pi 侧事实，不给出实现方案。

## 1. SessionManager：entry 类型、树、leaf 与迁移

- 会话文件版本常量为 `CURRENT_SESSION_VERSION = 3`；`SessionHeader` 的 `version` 对 v1 会话可选（`packages/coding-agent/src/core/session-manager.ts:30`、`:32-39`）。
- `SessionEntry` 联合类型包含 9 种：`message`、`thinking_level_change`、`model_change`、`compaction`、`branch_summary`、`custom`、`custom_message`、`label`、`session_info`（`packages/coding-agent/src/core/session-manager.ts:46-153`）。
- `CompactionEntry` 保存 `summary`、`firstKeptEntryId`、`tokensBefore`、可选 `details/usage/fromHook`，没有 `retainedTail`（`packages/coding-agent/src/core/session-manager.ts:69-80`）。
- `CustomEntry` 不参与 LLM 上下文；`CustomMessageEntry` 通过 `buildSessionContext()` 投影为用户消息，`display` 只控制 TUI 渲染（`packages/coding-agent/src/core/session-manager.ts:94-141`）。
- `SessionTreeNode` 在防御性树副本上带解析后的 `label` 与 `labelTimestamp`（`packages/coding-agent/src/core/session-manager.ts:158-166`）。
- v1→v2 迁移为每条 entry 生成 `id/parentId`，并把 compaction 的 `firstKeptEntryIndex` 转成 `firstKeptEntryId`；v2→v3 把 `hookMessage` role 改名 `custom`；迁移原地修改并在加载时自动重写文件（`packages/coding-agent/src/core/session-manager.ts:230-291`、`:895-928`）。
- `buildSessionPath()`：`leafId === null` 返回空路径；未传 `leafId` 时取物理文件最后一条；否则从 leaf 沿 `parentId` 走到 root，再反转为 root→leaf（`packages/coding-agent/src/core/session-manager.ts:334-360`）。
- 加载文件时 `_buildIndex()` 按文件顺序遍历，最终把 `leafId` 指向最后一条物理 entry；label 按最后一次 set/clear 投影（`packages/coding-agent/src/core/session-manager.ts:958-977`）。
- 所有 `appendXXX()` 都生成 `parentId: this.leafId` 的子 entry 并把 leaf 前移；`appendMessage()` 明确禁止直接写 `CompactionSummaryMessage/BranchSummaryMessage`（`packages/coding-agent/src/core/session-manager.ts:1044-1119`、`:1051-1056`）。
- `appendLabelChange()` 校验目标存在，把 label 作为真实树 entry 追加，并在内存 map 中更新/清除（`packages/coding-agent/src/core/session-manager.ts:1232-1253`）。
- `branch()` 只移动 leaf 指针，不修改历史；`resetLeaf()` 把 leaf 置 null，使下一次 append 创建新的根（`packages/coding-agent/src/core/session-manager.ts:1354-1374`）。
- `branchWithSummary()` 先设 `leafId = branchFromId`（允许 null），再在该位置追加 `branch_summary` entry；`fromId` 为 null 时记 `"root"`（`packages/coding-agent/src/core/session-manager.ts:1376-1405`）。
- `getBranch()` 从 `fromId` 或当前 leaf 沿 parent 链返回 root→leaf；包含所有 entry 类型（`packages/coding-agent/src/core/session-manager.ts:1255-1270`）。
- `buildContextEntries()` 只取当前 leaf 路径；若路径有 compaction，返回 `[compaction, firstKeptEntryId 起的 kept entries, compaction 之后的 entries]`；旧消息被省略（`packages/coding-agent/src/core/session-manager.ts:410-454`）。
- `sessionEntryToContextMessages()`：`message/custom_message/branch_summary/compaction` 会生成上下文消息；`custom/label/session_info/model_change/thinking_level_change` 返回空（`packages/coding-agent/src/core/session-manager.ts:379-408`）。
- `buildSessionContext()` 从完整路径推导 `thinkingLevel/model`，再对 `buildContextEntries()` 展平消息（`packages/coding-agent/src/core/session-manager.ts:461-470`）。
- `createBranchedSession()` 提取 root→leaf 路径，移除 label 后重链 parentId，把仍有效的 label 作为新 label entry 追加到新文件；新 header 记录 `parentSession`；若没有 assistant 消息则延迟写文件（`packages/coding-agent/src/core/session-manager.ts:1407-1512`）。
- `forkFrom()` 复制源文件全部非 header entry（整棵树的物理顺序），写入新 id、新 cwd、`parentSession=sourcePath` 的新文件（`packages/coding-agent/src/core/session-manager.ts:1572-1630`）。
- `newSession()` 生成 v3 header，清空 byId/labels/leaf；`setSessionFile()` 切换并加载另一文件（`packages/coding-agent/src/core/session-manager.ts:930-956`、`:890-928`）。

### 与 pigo 当前实现的差异点

- pigo 的 entry 是“消息 wrapper”，没有 entry type 联合；没有 `custom/custom_message/label/session_info/branch_summary/compaction` 独立 entry，compaction 只是 `agentcore.CompactionMessage` 这类消息角色（`E:\project\pigo\internal\session\session.go:96-121`、`E:\project\pigo\internal\agentcore\message.go:100-114`）。
- pigo 本地 REPL/TUI 的 leaf 只在进程内存中维护；resume 时取文件最后一条 entry，HTTP serve 额外把 `curLeaf` 放进 metadata 的 `customMetadata`（`E:\project\pigo\internal\cli\repl\repl.go:947-970`、`E:\project\pigo\internal\cli\tui\session.go:162-173`、`E:\project\pigo\cmd\pigo\prompt_runner.go:159-176`）。
- pigo `Fork` 复制的是 root→leaf 的线性路径，不是 pi `forkFrom` 的整树复制；pigo 没有 label 重链逻辑（`E:\project\pigo\internal\session\session.go:681-700`）。

## 2. agent-session 与 interactive-mode：compaction 触发、事件、/tree、branch summary、fork/clone

- `AgentSessionEvent` 定义 `compaction_start/compaction_end`（reason 为 `manual|threshold|overflow`）、`auto_retry_*`、`summarization_retry_*`，以及 `entry_appended/session_info_changed/thinking_level_changed`（`packages/coding-agent/src/core/agent-session.ts:140-183`）。
- `isCompacting` 同时覆盖手动 compaction、auto compaction 与 branch summarization 的 abort controller（`packages/coding-agent/src/core/agent-session.ts:945-952`）。
- 手动 `compact()`：先 `abort()`，发 `compaction_start(manual)`；调 `prepareCompaction()`；`session_before_compact` 扩展可取消或提供 `CompactionResult`；默认走 `compact()`；成功后 `appendCompaction()` 并重建 `agent.state.messages`，发 `session_compact` 与 `compaction_end`（`packages/coding-agent/src/core/agent-session.ts:1785-1933`）。
- `_checkCompaction()` 在 agent_end 后和下一次 prompt 提交前调用；默认跳过 aborted 消息；跳过模型不匹配的 overflow；跳过 compaction 边界之前的旧 assistant（`packages/coding-agent/src/core/agent-session.ts:1950-1986`）。
- overflow/length 恢复：同模型下 `isContextOverflow || recoverableLength` 时，`stopReason !== "stop"` 则 compact 后自动重试一次；`_overflowRecoveryAttempted` 保证只试一次；重试前把最后一条 assistant 从 agent state 移除（`packages/coding-agent/src/core/agent-session.ts:1988-2021`）。
- threshold 触发：用最后有效 assistant usage 或 `estimateContextTokens()` 估算；若 usage 早于最新 compaction 则忽略，避免刚 compact 后误触发；超阈值时 `_runAutoCompaction("threshold", false)`，不自动重试（`packages/coding-agent/src/core/agent-session.ts:2024-2053`）。
- `_runAutoCompaction()`：先发 `compaction_start`，再跑 `session_before_compact` 扩展，成功后 `appendCompaction`、重建状态、发 `session_compact` 与 `compaction_end`；`willRetry` 时清理最后的 error/length assistant 并返回 true 让 loop 继续；失败只发事件不中断（`packages/coding-agent/src/core/agent-session.ts:2058-2223`）。
- prompt 提交前检查：手动 compaction 进行中直接拒绝提交；正常提交前会对最后 assistant 调 `_checkCompaction(lastAssistant, false)`，以覆盖 aborted 响应；`preflightResult` 只在接受/拒绝时回调（`packages/coding-agent/src/core/agent-session.ts:1133-1137`、`:1205-1210`、`:1262-1272`）。
- `navigateTree()`：选择 user/custom_message 时新 leaf 为目标 entry 的 parent，并把消息文本放入编辑器；选择其他 entry 时 leaf 就是目标 entry；有 summary 时用 `branchWithSummary()` 在目标位置生成 summary 并可给 summary 打 label，无 summary 时用 `branch()/resetLeaf()`（`packages/coding-agent/src/core/agent-session.ts:3028-3091`）。
- 分支总结只收集 old leaf 到 common ancestor 的条目，不因 compaction 边界停止；compaction/branch_summary 本身也会成为被总结上下文（`packages/coding-agent/src/core/compaction/branch-summarization.ts:96-146`、`:152-180`）。
- interactive `/tree` 会先展示 tree selector，再询问 `No summary / Summarize / Summarize with custom prompt`，必要时中止当前流式响应，最后调 `session.navigateTree()`（`packages/coding-agent/src/modes/interactive/interactive-mode.ts:4948-5070`）。
- interactive `/fork` 用 user message selector，`runtimeHost.fork(entryId)`；`/clone` 用 `runtimeHost.fork(leafId, { position: "at" })`（`packages/coding-agent/src/modes/interactive/interactive-mode.ts:2932-2945`、`:4889-4945`）。
- `runtimeHost.fork()`：`position:"before"` 对 user message 取 parent 作为新 leaf；`position:"at"` 取 entry 本身；持久化模式下用 `SessionManager.createBranchedSession()` 生成新文件，并替换当前 runtime（`packages/coding-agent/src/core/agent-session-runtime.ts:262-352`）。

### 与 pigo 当前实现的差异点

- pigo 没有 `session_before_compact` / `session_compact` 扩展事件；`PreCompact` 只是 hooks 对 `CompactionEvent` 的只读通知（`E:\project\pigo\internal\hooks\notifier.go:26-63`）。
- pigo 自动 compaction 只有 `threshold` 路径；事件结构预留 `overflow` reason，但 `maybeAutoCompact()` 没有 overflow 识别、单次恢复或重试逻辑（`E:\project\pigo\internal\runtime\loop.go:465-521`、`E:\project\pigo\internal\agentcore\event.go:109-135`）。
- pigo 没有 compaction queue / steer / followUp 队列语义；compaction 直接在 run loop 内同步执行（`E:\project\pigo\internal\runtime\loop.go:450-513`）。
- pigo `/tree N` 只把 leaf 移到所选节点并重建扁平消息列表，没有 branch summary、没有把 user 消息回填编辑器、没有 label（`E:\project\pigo\internal\cli\repl\repl.go:842-896`）。
- pigo `/fork` 是行式编号选择并 `store.Fork()`，不是 TUI tree/user selector；pigo 没有 `navigateTree()` 这类独立运行时方法（`E:\project\pigo\internal\cli\repl\repl.go:744-840`）。

## 3. compaction 算法与 retainedTail 差异

- `DEFAULT_COMPACTION_SETTINGS`：`enabled=true`、`reserveTokens=16384`、`keepRecentTokens=20000`；coding-agent 与 harness 默认值相同（`packages/coding-agent/src/core/compaction/compaction.ts:126-136`、`packages/agent/src/harness/compaction/compaction.ts:147-162`）。
- `shouldCompact()`：`contextTokens > contextWindow - reserveTokens`（`packages/coding-agent/src/core/compaction/compaction.ts:235-238`、`packages/agent/src/harness/compaction/compaction.ts:247-250`）。
- token 估算：优先用最后有效 assistant usage，之后的 trailing 消息按 chars/4 估算；图片按 4800 chars 计；usage 不计算 cache read/write 的 pigo 与 pi 差异见下文（`packages/coding-agent/src/core/compaction/compaction.ts:202-306`）。
- 合法切点包括 user/assistant/bashExecution/custom/branchSummary/compactionSummary 消息，绝不切在 toolResult；`findCutPoint()` 从新到旧累积 token，达到 `keepRecentTokens` 后吸附到最近的合法切点，并向前包含相邻的非上下文 metadata entry（`packages/coding-agent/src/core/compaction/compaction.ts:308-461`）。
- split turn：若切点不是 user 起始消息，`findTurnStartIndex()` 找到该轮 user；`isSplitTurn=true` 时 `prepareCompaction()` 把 `[turnStart, firstKept)` 单独作为 `turnPrefixMessages`，`compact()` 会生成 history summary + turn prefix summary 并合并（`packages/coding-agent/src/core/compaction/compaction.ts:369-461`、`:747-763`、`:817-902`）。
- `prepareCompaction()` 的边界：若路径最后一条是 compaction 则返回 undefined；否则找最新 compaction，`boundaryStart` 取 `firstKeptEntryId` 的位置，找不到时退回 `prevCompactionIndex+1`；`previousSummary` 用于迭代式 update prompt（`packages/coding-agent/src/core/compaction/compaction.ts:710-789`）。
- `generateSummaryWithUsage()`：summary maxTokens 为 `0.8 * reserveTokens`；有 `previousSummary` 时用 update prompt；summary 请求通过 `completeSummarization()` 统一隔离 cache 并走 retry（`packages/coding-agent/src/core/compaction/compaction.ts:621-686`、`:555-581`）。
- branch summary 用 `collectEntriesForBranchSummary()` 找 common ancestor，`prepareBranchEntries()` 从新到旧按 budget 选取，`generateBranchSummary()` 前缀 preamble 并追加 `<read-files>/<modified-files>`（`packages/coding-agent/src/core/compaction/branch-summarization.ts:108-376`）。
- harness `CompactionEntry` 类型带 `retainedTail: AgentMessage[]`，没有 `firstKeptEntryId`；`CompactResult` 同样只返回 `retainedTail`（`packages/agent/src/harness/session/types.ts:44-51`、`packages/agent/src/harness/compaction/compaction.ts:88-100`）。
- harness `prepareCompaction()` 会把上一 compaction 的 `retainedTail` 虚拟成 message entry 纳入下次可压缩范围；`cutPoint.firstKeptEntryIndex` 之后的全部消息被收集为新的 `retainedTail`（`packages/agent/src/harness/compaction/compaction.ts:624-687`）。
- harness 上下文投影是“自包含 checkpoint”：`defaultContextEntryTransform()` 只保留 `[compaction, compaction 之后的 entries]`；compaction entry 投影为 `compactionSummary + entry.retainedTail`，不再依赖 `firstKeptEntryId` 重放旧 entry（`packages/agent/src/harness/session/context.ts:45-99`）。
- coding-agent JSONL SessionManager 是另一套投影：compaction entry 本身无 retainedTail，`buildContextEntries()` 仍按 `firstKeptEntryId` 把路径中的 kept entries 重新纳入（`packages/coding-agent/src/core/session-manager.ts:69-80`、`:410-454`）；文档也确认两套格式并存（`packages/coding-agent/docs/session-format.md:229-248`、`:319-341`）。

### 与 pigo 当前实现的差异点

- pigo `CompactionResult` 用 `FirstKeptIndex`（内存消息下标）而非 entry id；`RebuildContext()` 只在运行时重建 `[CompactionMessage, tail]`，持久化的是单条 `CompactionMessage`，没有 `firstKeptEntryId/retainedTail/usage/fromHook`（`E:\project\pigo\internal\compaction\compact.go:26-39`、`:104-130`、`E:\project\pigo\internal\agentcore\message.go:100-114`）。
- pigo `FindCutPoint()` 已返回 `TurnStartIndex/IsSplitTurn`，但 `Compact()` 没有消费这两个字段，也不生成 turn prefix summary；只对 `[prev+1, firstKeptIndex)` 做一次 summary（`E:\project\pigo\internal\compaction\cutpoint.go:5-16`、`:86-96`、`E:\project\pigo\internal\compaction\compact.go:41-90`）。
- pigo 当前所有 Compact 调用都传 `prevCompactionIndex=-1, prevDetails=nil, previousSummary=""`，因此没有 pi 的 iterative previous summary / firstKeptEntryId 边界复用（`E:\project\pigo\internal\runtime\loop.go:526-545`、`E:\project\pigo\internal\cli\repl\repl.go:1126-1158`、`E:\project\pigo\cmd\pigo\prompt_runner.go:245-285`）。
- pigo 把可恢复 checkpoint 单独写到 `<memoryRoot>/sessions/<id>/checkpoint.md`，带 watermark，而不是把 retainedTail 存进 session entry；`/rebuild` 优先用 checkpoint，无 checkpoint 时退回 lossy compaction（`E:\project\pigo\internal\runtime\checkpoint.go:81-84`、`E:\project\pigo\internal\runtime\rebuild.go:68-134`、`E:\project\pigo\internal\runtime\loop.go:504-579`）。

## 4. SQLite session backend：表结构、repo、storage、branch cache、writer lease

- `001_initial.sql` 定义 `sessions`、`entries`、`session_sequences`、`session_stats`、`branch_entries`、`lanes`、`records`、`lane_moves`、`facts`、`branch_tips`、`writer_leases`（`packages/session-backends/sqlite-node/src/sqlite/migrations/001_initial.sql:1-123`）。
- `entries` 以 `(session_id, id)` 为主键，`(session_id, seq)` 唯一；`parent_id` 是 canonical 树边；`type/timestamp/payload` 列（`packages/session-backends/sqlite-node/src/sqlite/migrations/001_initial.sql:13-27`）。
- `branch_entries` 是派生 cache，注释明确 parent links in entries 是 canonical；索引覆盖 branch+seq/type/customType（`packages/session-backends/sqlite-node/src/sqlite/migrations/001_initial.sql:43-58`）。
- `lanes` 每 session 多 lane，记录 `leaf_id` 与 `open_operation_id`；`lane_moves` 是 lane 移动日志；`records` 是带 lane/run_id/type/op_kind 的 operation log；`facts` 是 name/label 等键值事实日志；`branch_tips` 映射 tip entry→branch cache id；`writer_leases` 含 owner/fence/expires（`packages/session-backends/sqlite-node/src/sqlite/migrations/001_initial.sql:60-123`）。
- `create()`：插入 session row、创建 sequence/stats/main lane，并在同一事务内 claim writer lease（`packages/session-backends/sqlite-node/src/sqlite/repo.ts:706-729`）。
- `open()`：若同进程已有 active storage 则复用；否则 `claimStorage()` 要求 session row 存在并 `claimWriterLease()`（`packages/session-backends/sqlite-node/src/sqlite/repo.ts:692-704`、`:633-653`）。
- `delete()`：先释放本进程 storage，再 claim lease，按依赖顺序删除 branch/facts/lanes/records/entries/lease/stats/sequence/session row（`packages/session-backends/sqlite-node/src/sqlite/repo.ts:760-781`）。
- `fork()`：`scope:"tree"` 复制全部 entries/lanes/branchTips；否则只复制 root→目标 message 路径（`position:"at"` 取 message，`"before"` 取 parent），只复制落在路径内的 label facts，重建 branch cache，新 session 继承 source id（`packages/session-backends/sqlite-node/src/sqlite/repo.ts:783-895`）。
- `createLane()`/`moveLane()`：校验 lane 与 entry，分配 seq，写 lanes 并追加 `lane_moves`；`setLaneLeaf()` 是 append 内部推进，不写 lane_moves（`packages/session-backends/sqlite-node/src/sqlite/repo.ts:436-461`、`packages/session-backends/sqlite-node/src/sqlite/storage/lanes.ts:71-86`）。
- `appendEntry()`：取 lane head 为 parent，校验 id 全局唯一，分配 seq，写 entries、推进 lane leaf、更新 branch cache、message 计数，最后 advance sequence（`packages/session-backends/sqlite-node/src/sqlite/repo.ts:463-491`）。
- `appendRecord()`：`operation_started` 写 lanes.open_operation_id，`operation_finished` 清除；`usage` record 累加 session_stats（`packages/session-backends/sqlite-node/src/sqlite/repo.ts:493-522`）。
- branch cache 重建：先找“没有子 entry 的 leaf”作为 tips，清空 cache，再逐个 `buildCachedBranch()`（`packages/session-backends/sqlite-node/src/sqlite/branch-cache.ts:19-52`）。
- append 时若 parent 已是 tip，直接扩展该 branch；否则从包含 parent 的 branch 复制到 parent seq，再建新 branch；`branch_tips` 在 append 期间被乐观更新，失败报 `Branch tip changed during append`（`packages/session-backends/sqlite-node/src/sqlite/branch-cache.ts:54-101`）。
- writer lease 默认 `ttlMs=30000`、`heartbeatIntervalMs=10000`，且 heartbeat 必须严格小于 TTL；每个写操作前事务性 renew，失败即 `writer lease was lost`（`packages/session-backends/sqlite-node/src/sqlite/repo.ts:95-137`、`:384-422`）。
- `acquireWriterLease()`：insert fence=1，冲突时仅当旧 lease 已过期才 takeover 并 `fence+1`；`renewWriterLease()` 要求 owner+fence+未过期；`releaseWriterLease()` 要求 owner+fence（`packages/session-backends/sqlite-node/src/sqlite/storage/writer-leases.ts:16-54`）。
- name/label 通过 `facts` 表以 seq 追加；`readLatestFact()`/`readLatestLabelFacts()` 按 seq 投影最新值；`setLabel(undefined)` 写 null 清除（`packages/session-backends/sqlite-node/src/sqlite/repo.ts:599-626`、`packages/session-backends/sqlite-node/src/sqlite/storage/facts.ts:12-53`）。

### 与 pigo 当前实现的差异点

- pigo 没有 SQLite/lane/records/facts/branch_tips/writer_leases；持久化是项目级 JSONL transcript + metadata/index，leaf 通过 `curLeaf` 进程内变量（serve 路径用 `customMetadata["curLeaf"]`）保存（`E:\project\pigo\internal\sessionstore\store.go:1-5`、`E:\project\pigo\internal\sessionstore\store.go:43-104`、`E:\project\pigo\cmd\pigo\prompt_runner.go:169-227`）。
- pigo 写会话用整文件原子重写（`session.atomicWrite`），只有进程内 `sessionstore.Store` 锁，没有跨进程 writer lease/fence（`E:\project\pigo\internal\session\session.go:392-423`、`E:\project\pigo\internal\sessionstore\store.go:435-457`）。
- pigo 没有 branch cache：`PathToLeaf()` 每次从 entries map 沿 parent 链走；`AppendBranch()` 保留整棵树的条目并重写文件（`E:\project\pigo\internal\session\session.go:178-202`、`:624-660`）。
- pigo 没有 per-entry label/facts；会话名与 tags 存在 `sessionstore.Metadata`（`E:\project\pigo\internal\sessionstore\store.go:46-67`）。

## 5. LiveSessionManager：attach/detach、operationCount、maybeDispose、snapshot

- `LiveSession` 状态包含 `connections`、`operationCount`、`ready`、`terminal`、`disposing`（`packages/server/src/sessions.ts:7-16`）。
- create/attach 都经 `acquire()`：复用已 live 的 session；`terminal` 时拒绝；`disposing` 时等待；并发 open 用 `openingSessions` 去重（`packages/server/src/sessions.ts:186-207`）。
- attach 要求 connection 处于 ready 且未 closed，随后把 connection 加入 `live.connections`；detach 从连接集合移除，若仍有连接则广播，否则 `maybeDispose()`（`packages/server/src/sessions.ts:300-307`、`:75-89`）。
- `runOperation()` 在 prompt/steer/abort/set_model/set_thinking 前后维护 `operationCount`，结束后调度 `maybeDispose()`（`packages/server/src/sessions.ts:90-119`、`:171-184`）。
- `normalizedSnapshot()` 总是给 snapshot 加 `phase: runtime.getPhase()`、`attached: live.connections.size > 0`、`locked: true`；`forConnection()` 再按该 connection 是否 attached 覆盖 `attached`（`packages/server/src/sessions.ts:276-291`）。
- `broadcastSnapshot()` 对 `live.connections` 发 `session_snapshot`，并返回同一 snapshot（`packages/server/src/sessions.ts:293-298`）。
- runtime error 时 `terminate()` 置 `terminal=true`，取消订阅，关闭/断开所有 connection，再 dispose（`packages/server/src/sessions.ts:248-274`）。
- `maybeDispose()` 在 `isClosing || !ready || disposing || connections>0 || operationCount>0 || (!terminal && phase !== "idle")` 时不做 dispose；否则 unsub、dispose、从 map 删除并广播 server snapshot（`packages/server/src/sessions.ts:320-345`）。

### 与 pigo 当前实现的差异点

- pigo 没有等价的 live-session registry / locked / attached / phase snapshot；HTTP prompt 路径直接打开 sessionstore、执行 run，并返回 `PromptResponse`，不维护连接级会话快照（`E:\project\pigo\cmd\pigo\prompt_runner.go:141-240`）。

## 6. 文档事实：命令、树导航、branch summary、settings

- 交互命令：`/resume`、`/new`、`/name`、`/session`、`/tree`、`/fork`、`/clone`、`/compact`、`/export`、`/share`（`packages/coding-agent/docs/sessions.md:22-35`）。
- `/tree` 选择语义：选 user/custom message 时 leaf 移到其 parent 并把文本放入编辑器；选 assistant/tool/compaction 等非 user entry 时 leaf 移到该 entry 且编辑器留空；选根 user message 会 reset leaf（`packages/coding-agent/docs/sessions.md:102-116`）。
- `/tree`、`/fork`、`/clone` 对照：`/tree` 同文件内导航，`/fork` 从历史 user message 建新会话，`/clone` 复制当前活动分支到新会话，只有 `/tree` 可选 branch summary（`packages/coding-agent/docs/sessions.md:118-127`）。
- branch summary 挂在导航目标位置，而不是被放弃分支末尾；目的是在不重放整条分支时保留上下文（`packages/coding-agent/docs/sessions.md:129-139`）。
- 会话格式版本：v1 线性、v2 tree id/parentId、v3 `hookMessage`→`custom`；加载时自动迁移（`packages/coding-agent/docs/session-format.md:19-27`）。
- 文档明确两种 compaction 格式：旧式 `firstKeptEntryId`；harness 新式 compaction entry 内嵌 `retainedTail`，作为自包含 checkpoint；`retainedTail` 仅用于向后兼容可选（`packages/coding-agent/docs/session-format.md:229-248`、`:319-341`）。
- 自动 compaction 公式与默认值：`contextTokens > contextWindow - 16384`，`keepRecentTokens` 默认 20000；设置位于 `~/.pi/agent/settings.json` 或项目 `.pi/settings.json`（`packages/coding-agent/docs/compaction.md:29-45`、`:359-379`）。
- 重复 compaction 时，汇总区间从上一 compaction 的 `firstKeptEntryId` 开始而不是 compaction entry 本身；`tokensBefore` 从重建后的上下文重新估算（`packages/coding-agent/docs/compaction.md:65`）。
- split turn：单轮超过 `keepRecentTokens` 时可在 assistant 消息处切；先总结完整历史，再单独总结 turn prefix，最终合并（`packages/coding-agent/docs/compaction.md:67-88`）。
- 合法切点：user/assistant/bashExecution/custom message/branch summary；toolResult 不可切（`packages/coding-agent/docs/compaction.md:89-98`）。

### 与 pigo 当前实现的差异点

- pigo 的 `/tree` 文档/实现是行式编号导航，不提供 user 文本回填、可选 branch summary 或 label；pigo 的 `/fork` 是编号列表而不是图形 tree selector（`E:\project\pigo\internal\cli\repl\repl.go:744-896`）。
- pigo compaction 配置表在 TOML `[compaction]`，当前只接线 `max_context`，默认阈值仍来自 `compaction.DefaultCompactionSettings`；没有 pi 文档中的 `reserveTokens/keepRecentTokens` 配置项（`E:\project\pigo\internal\cli\config\memory.go:162-177`、`E:\project\pigo\internal\compaction\tokens.go:31-36`）。
