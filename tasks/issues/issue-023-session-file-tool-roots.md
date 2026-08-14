# pigo 会话级文件工具根与 additionalDirectories

## Description

每个会话创建/加载时克隆 read/write/edit/grep/find/bash 工具，`Root`/`Dir` 设置为会话 cwd，`additionalDirectories` 合并进 `ExtraRoots`，skills 可读目录继续保留。对应 PRD US-004 / FR-4。

## Acceptance Criteria

- [ ] 每个会话创建时克隆工具，`Root`/`Dir` 设置为会话 cwd
- [ ] `additionalDirectories` 合并进 `ExtraRoots`（read/write/edit）
- [ ] skills 可读目录继续保留在附加根中
- [ ] 会话 A 的文件工具不能解析到会话 B 的目录边界
- [ ] 多目录项目恢复会话后目录边界仍然生效
- [ ] 对应 Go 单元测试通过

## Dependencies

Issue #016

## Type

backend

## Priority

high
