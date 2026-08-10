# 子代理缺失标记的纠正循环与最终错误

## Description

无 `<<DONE>>` 时给子代理纠正机会，最多 2 次；仍缺失则返回真实错误，避免主 agent 把半成品当结论。

## Acceptance Criteria

- [ ] 缺失标记时追加纠正 user 消息并继续子代理 run
- [ ] 纠正消息同时提示“未完成”和“需要 <<DONE>>”
- [ ] 最多 2 次纠正；仍缺失返回 `IsError=true`
- [ ] 最终失败 Content 只放错误文案，半成品放 Details
- [ ] goroutine / process 两种模式行为一致

## Dependencies

Issue #1

## Type

backend

## Priority

high
