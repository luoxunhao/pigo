# pigo session/load 重建并持久化 system prompt header

## Description

`session/load` 按请求 `cwd` 重建 system prompt，写回会话 header 并持久化到 sessionstore，修复历史会话或换进程启动后携带错误目录的问题。对应 PRD US-002 / FR-2。

## Acceptance Criteria

- [ ] `session/load` 使用请求 `cwd` 重建 system prompt，忽略进程启动 cwd
- [ ] 重建结果写回会话 header，并持久化到 sessionstore
- [ ] 恢复后继续对话使用重建后的 system prompt
- [ ] 已存在错误 system prompt 的历史会话，在 `session/load` 后 header 被修复
- [ ] 对应 Go 单元测试通过

## Dependencies

Issue #016

## Type

backend

## Priority

high
