# submit_acp_permission_response 落地真实 IPC

## Description

实现 `submit_acp_permission_response` 桌面 IPC，`ACPClientAPI.submitPermissionResponse` 从 unsupported 改为真实调用；四种审批结果正确回包给 pigo。对应 PRD US-008 / FR-12。

## Acceptance Criteria

- [ ] `submit_acp_permission_response` 不再是 unsupported
- [ ] allow once / allow always / reject once / reject always 返回 pigo 可识别的 outcome
- [ ] 审批按钮点击后工具卡状态更新为 confirmed 或 rejected
- [ ] 对应 vitest 单元测试通过

## Dependencies

Issue #9

## Type

fullstack

## Priority

high
