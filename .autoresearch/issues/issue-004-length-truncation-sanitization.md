# length 截断 tool call 清洗与连续 length 熔断

## Description

`stopReason=length` 时修复非法 tool call，并防止连续输出超限导致 run 卡死或无限循环。

## Acceptance Criteria

- [ ] 截断 assistant 消息中非法 Arguments 替换为合法 `{}`
- [ ] 保留 tool call id/name，并附加“未执行”错误结果
- [ ] 连续 `length` 达 3 次终止 run 并报明确错误
- [ ] 正常 turn 重置连续计数
- [ ] 测试覆盖坏 JSON 回灌与上限终止

## Dependencies

None

## Type

backend

## Priority

high
