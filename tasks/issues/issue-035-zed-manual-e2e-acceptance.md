# Zed 手动端到端验收

## Description

Phase 1 修复完成后，在 Zed（`pigo.exe --acp`）中手动复现 `bun install` 取消场景，确认 turn 及时结束、后续消息不再永久排队、会话可继续对话。

对应 PRD：US-004。

## Acceptance Criteria

- [ ] 手动在 Zed 中运行 `bun install 2>&1` 并取消
- [ ] 取消后发送新消息，不再显示永久排队
- [ ] 会话状态恢复为可继续对话
- [ ] 复现会话不再出现 `bash: command canceled` 后无终态的情况

## Dependencies

Issue #1、#2、#3

## Type

backend

## Priority

medium
