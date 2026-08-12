# 08 - 子 agent SQLite 会话图 + 插件注册

**What to build:** 删除内置 `task` 工具与默认注册；插件 manifest `subagents[]` 声明式注册；子 agent 调用 eager 创建 SQLite 子会话；确定性子会话 id；`parent_session_id` 血缘 + metadata 契约；删除 `subagents.json`；`session/list` 过滤与生命周期/删除语义。

**Blocked by:** 01 - SQLite schema + migrations + writer lease, 02 - SQLite repo

**Status:** resolved

## Acceptance Criteria

- [x] 核心不再默认注册 `task`；`taskGuide` 删除
- [x] 插件 manifest `subagents[]` 注册子 agent 工具，未知工具名 fail
- [x] 子会话 id 为 `subagent-<sha256(parent + "\x00" + toolCallId)[:16]>`
- [x] 调用开始 eager 建行：sessions/sequences/stats/main lane + lease 同一事务
- [x] metadata 只写 `sessionKind/subagentType/plugin/parentToolCallId`
- [x] `subagents.json` 删除，`Registry` 只保留 live map
- [x] `session/list` 默认隐藏子会话；`includeSubagents` / `_meta.pigo.sessionList` 返回血缘字段
- [x] 状态 active/completed/archived；running 由 records/lanes 表达
- [x] 父删除不级联；running child 删除冲突；child 可单独删除
- [x] isolation 默认 goroutine，process 显式声明且只用 builtin 工具；嵌套默认禁止

**Type:** backend

**Priority:** medium

**Spec Reference:** `tasks/spec-session-tree-pi-alignment.md` §10

