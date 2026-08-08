# backend 字段迁移

> 已废弃：迁移方向反转，由 `issue-024` 取代（旧 `project` 记录迁回 `shared`）。

## Description

启动时读取 `conversations.json`，将 pigo 对话的 `backend='shared'` 迁移为 `backend='project'`；移除代码中 `backend==='shared'` 的全局单例路由分支。对应 PRD US-004 / FR-6。

## Acceptance Criteria

- [ ] 启动时把 pigo 记录的 `backend` 迁移为 `project`
- [ ] 迁移幂等，重复启动不重复改写
- [ ] `clientFor` 与 `capabilitiesFor` 不再存在 `backend==='shared'` 的全局单例分支
- [ ] 对应 vitest 单元测试通过

## Dependencies

Issue #1

## Type

backend

## Priority

medium
