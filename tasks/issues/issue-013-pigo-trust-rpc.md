# pigo trust RPC

## Description

新增 `pigo/trust/list` 与 `pigo/trust/set` RPC，由配置网关调用，作为 trust.json 的唯一客户端读写入口。对应 PRD FR-15。

## Acceptance Criteria

- [ ] `pigo/trust/list` 返回已信任目录列表
- [ ] `pigo/trust/set` 可写入 Trusted / Untrusted / 移除决策
- [ ] trust.json 格式与并发由 pigo 管理
- [ ] 对应 Go 单元测试通过

## Dependencies

None

## Type

backend

## Priority

high
