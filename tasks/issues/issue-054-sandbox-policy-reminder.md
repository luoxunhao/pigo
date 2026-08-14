# PolicyReminder 模式注入

## Description

按 `tasks/spec/spec-017-sandbox.md` 切片 2 实施：新增 `PolicyReminderProvider`（ReminderRegistry 常驻 provider），每轮把当前模式 + workspace 根 + escalation 通道说明注入请求副本（复用 contextbuild ReminderTransform 路径）；不进持久历史、不碰 system prompt 指纹缓存；模式切换下轮即生效。

## Acceptance Criteria

- [ ] `runtime.ReminderRegistry` 注册 `PolicyReminderProvider`（sandbox 策略来源注入）
- [ ] 注入内容：当前模式、workspace 根、escalation 通道说明（对齐 DSH 的 "Current DSH file policy: ..." 语义）
- [ ] 注入经 TransformContext/ReminderTransform 只进请求副本，持久历史无变化，system prompt 指纹不受影响
- [ ] 模式切换后下一轮注入即更新
- [ ] 单测：注入内容与时效、历史不变、指纹缓存不受影响

## Dependencies

issue-053（策略层）。

## Type

backend

## Priority

high
