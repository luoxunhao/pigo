# 所有工具先发 pending tool_call

## Description

pigo 在工具执行前、权限判断前对所有工具发出 `tool_call status=pending`，事件状态单调：pending -> in_progress -> completed/failed。被 `beforeToolCall` 拦截的工具也必须先有 pending 事件。对应 PRD US-005 / FR-7、FR-8。

## Acceptance Criteria

- [ ] 所有工具调用先收到 `sessionUpdate=tool_call, status=pending`
- [ ] 权限通过并开始执行后收到 `status=in_progress`
- [ ] 执行结束收到 `status=completed` 或 `status=failed`
- [ ] 被 `beforeToolCall` 拦截的工具仍然先有 pending 事件
- [ ] 事件状态保持单调
- [ ] 对应 Go 与事件映射测试通过

## Dependencies

None

## Type

backend

## Priority

high
