# runLoop 取消终态收尾

## Description

`runLoop` 在 `ExecuteToolCalls` 返回后不检查 `ctx.Err()`，把取消错误当普通 tool result 喂回模型，继续下一轮 LLM 调用。本 Issue 让 runLoop 在工具执行后立即检查取消，已取消则以 `StopReasonAborted` 终态收尾，不再调用 provider。

对应 PRD：US-002、FR-4。

## Acceptance Criteria

- [ ] `ExecuteToolCalls` 返回后检查 `ctx.Err()`
- [ ] 已取消时追加 `StopReasonAborted` 终态 assistant 消息并结束 turn
- [ ] 不向 provider 发起下一轮调用
- [ ] 取消错误（`bash: command canceled`、`tool call aborted`）不再作为普通 tool result 喂回模型
- [ ] 新增 loop 取消测试通过
- [ ] `go test ./internal/runtime` 通过

## Dependencies

None

## Type

backend

## Priority

high
