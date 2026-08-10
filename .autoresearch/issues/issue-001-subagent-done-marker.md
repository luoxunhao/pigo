# task 子代理 final 完成标记 <<DONE>> 校验与剥离

## Description

子代理 final message 必须以 `<<DONE>>` 结尾；空白、中途进度不能以成功身份进入父上下文。pigo 负责校验并剥离标记，不信任模型会自行遵守系统提示词。

## Acceptance Criteria

- [ ] task 子代理系统提示要求 final message 以 `<<DONE>>` 结尾
- [ ] `executeGoroutine` / `executeProcess` / `RunSubAgentOnce` 校验文末标记
- [ ] 成功结果剥离文末 `<<DONE>>` 及前面空白
- [ ] 无标记结果不直接返回成功

## Dependencies

None

## Type

backend

## Priority

high
