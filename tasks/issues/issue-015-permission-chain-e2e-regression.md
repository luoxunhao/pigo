# 权限链路桌面端回归

## Description

共享进程改造后，权限审批链路保持完整：pending 工具卡、rawInput、permissionId/optionId 回包、trust 持久化、被拦截 bash 的 UI 工具卡均需回归验证。对应 PRD US-012 / FR-21、FR-22、FR-23、FR-24。

## Acceptance Criteria

- [ ] 所有工具先发 `tool_call status=pending`，再 `in_progress`，最后 `completed|failed`
- [ ] `tool_call_update` 携带 `rawInput`
- [ ] 权限事件包含 `permissionId/sessionId/toolCall/options`
- [ ] 审批按 `optionId` 精确回包，60 秒无响应 cancelled
- [ ] `allow_always` 重启后不再弹审批
- [ ] 被拦截 bash 在 UI 上显示工具卡而不是静默失败
- [ ] 现有 `permission-flow` e2e 继续通过

## Dependencies

Issue #022

## Type

fullstack

## Priority

high
