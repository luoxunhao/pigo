# Escalation 升级审批闭环

## Description

按 `tasks/spec/spec-017-sandbox.md` 切片 7 实施：工具 schema 增加 `sandbox_permissions` + `justification`（必须成对、justification 非空句子）；拒绝时统一 denial marker + escalation hint；执行时严格变宽校验（read-only → workspace-write → danger-full-access，不可跳级/降级，非变宽请求不打扰人）；升级请求走 trust 审批通道（新增 allowed-once 升级决策），批准仅该次调用生效。

## Acceptance Criteria

- [ ] 工具 schema：`sandbox_permissions` + `justification` 成对校验（对齐 DSH validateEscalationArgs）
- [ ] 统一文案：denial marker + escalation hint（"retry this exact command once with sandbox_permissions (the narrowest wider mode that suffices) + justification"）
- [ ] 严格变宽校验在执行时做（非 schema enum）；非变宽请求直接拒绝不询问
- [ ] trust 审批通道：升级请求 → 用户 allowed-once 批准 → 该次调用以更宽模式执行；拒绝/取消/无通道 → fail-closed 错误
- [ ] 批准不持久化为 allow_always（单次语义，与 trust 的 allow_once/allow_always 不混淆）
- [ ] 单测：成对校验、严格变宽矩阵、审批各结局、单次生效

## Dependencies

issue-055、issue-058（执行层拒绝路径）。

## Type

backend

## Priority

high
