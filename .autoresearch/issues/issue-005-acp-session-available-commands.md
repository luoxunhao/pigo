# ACP session 响应携带 availableCommands

## Description

消除 `available_commands_update` 异步通知与 `session/new` 响应的竞态，让客户端不依赖通知是否刚好晚到。

## Acceptance Criteria

- [ ] `session/new` 响应包含 `availableCommands`
- [ ] `session/load` 响应包含 `availableCommands`
- [ ] 客户端不依赖异步通知也能拿到命令列表
- [ ] 测试断言响应 payload

## Dependencies

None

## Type

backend

## Priority

high
