# sandbox 策略段迁入 contexts

## Description

按 `tasks/spec/spec-018-contextbuild-registry-dsh-alignment.md` 切片 5 实施（修订 issue-054）：sandbox 策略注入从 PolicyReminderProvider 改为内置 context 注册——`sandbox:policy`（动态文本 = 当前模式 + workspace 根 + escalation 通道说明），每轮经 contexts 渲染注入请求副本；ReminderRegistry 保留给 todo/goal/one-shot（非策略注入）。

## Acceptance Criteria

- [ ] `sandbox:policy` context 注册（order 110 区间），文本随会话模式动态渲染
- [ ] issue-054 的 PolicyReminderProvider 移除/改写为 contexts 注册（reminder 机制不再承载策略注入）
- [ ] 注入不进持久历史、不碰 system prompt 指纹
- [ ] 模式切换后下一轮注入更新；scope 感知（子代理自动继承）
- [ ] 单测：注入内容与时效、reminder 机制回归（todo/goal 不受影响）

## Dependencies

issue-061（Registry 分层）、issue-053/054（sandbox 策略层）。

## Type

backend

## Priority

high
