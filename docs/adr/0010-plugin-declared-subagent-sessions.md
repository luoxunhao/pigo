# Sub-agents: plugin-declared tools backed by SQLite child sessions

pigo 删除内置 `task` 工具与 `taskGuide` 默认注册，核心不再内嵌任何子 agent 工具；插件通过 manifest `subagents[]` 声明式注册子 agent 工具，核心保留 `SubAgentTool` / `ChildSession` / SQLite 原语作为执行承载体。

子 agent 调用开始时 eager 创建 SQLite 子会话，id 确定性派生为 `subagent-<sha256(parentSessionId + "\x00" + toolCallId)[:16]>`；`sessions.parent_session_id` 是唯一血缘权威，metadata 只写 `sessionKind/subagentType/plugin/parentToolCallId`。删除 `subagents.json`，`Registry` 只保留 live map。`session/list` 默认隐藏子会话，`includeSubagents` / `_meta.pigo.sessionList` 显式返回；状态为 `active/completed/archived`，running 由 records/lanes 表达。父删除不级联，running child 删除返回冲突。

isolation 默认 goroutine，process 显式声明且只用 builtin 工具；嵌套默认禁止，由 agent 声明 `nested/maxDepth` 并由 core depth 守卫。完整设计见 `tasks/spec-session-tree-pi-alignment.md` §10。
