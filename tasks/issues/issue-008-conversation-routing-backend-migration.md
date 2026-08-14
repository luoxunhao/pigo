# ash-workbench conversation 路由与 backend 回迁 shared

## Description

pigo conversation 记录回迁到单一共享语义，迁移幂等；conversation 保存 `acpSessionId`、项目 `path` 与 `sourceFolders`，重启后通过共享 gateway 执行 `session/load` 恢复。对应 PRD US-009 / FR-16、FR-17。

## Acceptance Criteria

- [ ] pigo conversation 记录迁移为 `backend='shared'`（或等价单一语义），迁移幂等
- [ ] conversation 保存 `acpSessionId`、项目 `path` 与 `sourceFolders`
- [ ] 重启后通过共享 gateway 执行 `session/load` 恢复
- [ ] 代码中不再存在 `backend='project'` 的 pigo 专用分支
- [ ] 对应 vitest 单元测试通过

## Dependencies

Issue #022

## Type

backend

## Priority

high
