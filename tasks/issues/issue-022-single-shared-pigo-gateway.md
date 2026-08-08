# ash-workbench 单一共享 PigoGateway，删除进程池与配置网关

## Description

删除 `PigoGatewayPool` 与独立配置网关，恢复单个共享 `PigoGateway`。配置、模型与 trust 操作全部通过共享 gateway 调用，所有 pigo conversation 按 `acpSessionId` 路由。对应 PRD US-007 / FR-11、FR-12、FR-15、FR-18。

## Acceptance Criteria

- [ ] 代码中不再存在 `PigoGatewayPool`
- [ ] 不再为配置/模型/trust 启动独立配置网关进程
- [ ] `pigo/config`、`pigo/models`、`pigo/trust/*` 通过共享 gateway 调用
- [ ] 所有 pigo conversation 按 `acpSessionId` 路由到共享 gateway
- [ ] 共享 gateway 崩溃后可重建，不影响已持久化会话
- [ ] 对应 vitest 单元测试通过

## Dependencies

None

## Type

backend

## Priority

high
