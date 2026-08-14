# allow_always 持久化 trust

## Description

pigo 所有 `allow_always` 路径将目录写为持久化 Trusted 到 `~/.pigo/trust.json`；`allow_once` 不持久化；`reject_always` 写入 Untrusted；remote confirm 与工具卡路径行为一致。对应 PRD US-010 / FR-14。

## Acceptance Criteria

- [ ] `allow_always` 写入 trust.json 的 Trusted 决策
- [ ] 重新加载 trust manager 后目录仍为 Trusted
- [ ] `allow_once` 不写入持久化 trust
- [ ] `reject_always` 写入 Untrusted
- [ ] remote confirm 的 allow_always 与工具卡路径行为一致
- [ ] 对应 Go 单元测试通过

## Dependencies

None

## Type

backend

## Priority

high
