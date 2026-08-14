# tool_call_update 结束事件携带 rawInput

## Description

pigo 的 `tool_call_update` completed/failed 事件携带 `rawInput`，使 ash-workbench 在被拦工具没有 Started 事件时仍能创建工具卡并展示原始命令。对应 PRD US-006 / FR-9。

## Acceptance Criteria

- [ ] `tool_call_update` completed/failed 事件包含 `rawInput`
- [ ] bash 被拦时 `rawInput.command` 保留原命令
- [ ] 对应 Go 单元测试通过

## Dependencies

Issue #7

## Type

backend

## Priority

medium
