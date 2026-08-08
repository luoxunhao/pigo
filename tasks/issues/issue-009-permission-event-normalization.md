# 权限事件标准化与 optionId 回包

## Description

ash-workbench 主进程向 renderer 发送标准化权限事件，包含 `permissionId`、`sessionId`、`toolCall`、`options(optionId/name/kind)`；`PermissionBroker` 按用户选择的 `optionId` 精确回包，60 秒无响应自动 cancelled。对应 PRD US-007 / FR-10、FR-11。

## Acceptance Criteria

- [ ] 主进程权限事件包含 `permissionId`、`sessionId`、`toolCall`、`options`
- [ ] `permissionId` 与 JSON-RPC request id 一一对应
- [ ] `PermissionBroker` 按 `optionId` 回包，不退回第一个选项
- [ ] 60 秒无响应自动 cancelled
- [ ] 对应 vitest 单元测试通过

## Dependencies

None

## Type

fullstack

## Priority

high
