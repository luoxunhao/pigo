# TUI 解耦直连 runtime（Phase 2）

## Description

本地 fork 的 TUI 通过 in-process ACP（`RunACP`）驱动 agent，继承了 ACP 队列语义；upstream 的 TUI 是 `runSession` + `runtime.StartRun` + 事件桥的 zero-transport 架构。本 Issue 将 TUI 改回直连 runtime，`internal/acp` 只保留给 `--acp`。

对应 PRD：US-005、FR-10/12。

## Acceptance Criteria

- [ ] `tui.Run` 不再调用 `acp.StartInProcessWithHooks`
- [ ] TUI 通过 `runSession` + `runtime.StartRun` + 事件桥驱动 agent
- [ ] TUI 会话持久化、斜杠命令、权限确认行为与现状一致
- [ ] `--acp` 模式无回归，Zed 仍可使用
- [ ] 对应 Go 测试通过

## Dependencies

None（Phase 2，独立 PR）

## Type

ui

## Priority

medium
