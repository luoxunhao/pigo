# ACP turn idle 看门狗释放队列

## Description

ACP 的 turn 槽位只在 `SessionManager.Run` 返回后释放，工具卡死会让 `turnActive` 永远为 true，后续 `session/prompt` 无限排队。本 Issue 为 ACP 会话增加 turn idle 看门狗：默认 5 分钟无任何 AgentEvent/工具输出心跳时强制 `finishTurn`，当前 turn 以 cancelled/error 结束，队列保留并执行下一条；`session/cancel` 幂等且最终释放槽位。

对应 PRD：US-003、FR-5/6/7/8。

## Acceptance Criteria

- [ ] 默认 5 分钟无 AgentEvent/工具输出心跳时触发看门狗
- [ ] 触发后强制 `finishTurn`，当前 turn 以 cancelled/error 结束
- [ ] turn 槽位释放，队列下一条消息继续执行，不清空队列
- [ ] 看门狗阈值可配置（环境变量或配置项），默认 5 分钟
- [ ] `session/cancel` 幂等，且无论取消发生在哪个阶段最终都释放槽位
- [ ] 新增 ACP 看门狗集成测试通过
- [ ] `go test ./internal/acp` 通过

## Dependencies

None

## Type

backend

## Priority

high
