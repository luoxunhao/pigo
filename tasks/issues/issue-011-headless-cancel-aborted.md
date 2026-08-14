# headless 取消语义 aborted

## Description

headless run 被取消且最后没有 assistant 消息时，`RunHeadless` 当前会返回 nil，ACP/CLI 把取消伪装成 `end_turn`。本 Issue 让取消的 run 以 aborted 终态返回（非零退出码或 `ErrRunFailed{Reason:"aborted"}`），脚本能区分取消与正常完成。

对应 PRD：US-007、FR-9。

## Acceptance Criteria

- [ ] canceled/aborted run 返回非零退出码或 `ErrRunFailed{Reason:"aborted"}`
- [ ] 最后消息缺失时不再静默回 `end_turn`
- [ ] 新增 headless 取消测试通过
- [ ] `go test ./internal/runtime ./internal/cli/headless` 通过

## Dependencies

None

## Type

backend

## Priority

medium
