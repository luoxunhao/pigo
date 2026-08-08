# 多目录项目传入 sourceFolders

## Description

ash-workbench 在 `session/new` 与 `session/load` 中把 `project.sourceFolders` 作为 `additionalDirectories` 传给 pigo，保证多目录项目恢复会话后文件工具仍可访问附加目录。对应 PRD US-012 / FR-17、FR-18。

## Acceptance Criteria

- [ ] `session/new` 参数包含 `additionalDirectories: project.sourceFolders`
- [ ] `session/load` 参数包含 `additionalDirectories: project.sourceFolders`
- [ ] 主目录 `project.path` 作为项目 pigo 进程 cwd
- [ ] 对应 vitest 单元测试通过

## Dependencies

Issue #1

## Type

backend

## Priority

high
