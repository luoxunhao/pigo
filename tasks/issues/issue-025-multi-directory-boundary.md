# ash-workbench 多目录项目目录边界传递

## Description

多目录项目以 `project.path` 为会话 cwd，`sourceFolders` 作为 `additionalDirectories` 传给 `session/new` 与 `session/load`，保证 agent 可访问全部 sourceFolders 且主目录信任覆盖整个项目边界。对应 PRD US-010 / FR-16。

## Acceptance Criteria

- [ ] `session/new` 参数包含 `cwd=project.path` 与 `additionalDirectories=sourceFolders`
- [ ] `session/load` 参数包含同样的目录信息
- [ ] read/write/edit 可访问全部 sourceFolders
- [ ] 主目录受信任时 sourceFolders 不需要单独审批
- [ ] 对应 vitest 与 Go 单元测试通过

## Dependencies

Issue #022、#019

## Type

backend

## Priority

medium
