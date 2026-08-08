# pigo session/load 支持 additionalDirectories

## Description

pigo `session/load` 增加 `additionalDirectories` 参数，并把附加目录重新合并进 read/write/edit 工具的 ExtraRoots，保证进程重启后恢复会话仍保留多目录边界。对应 PRD US-012 / FR-19。

## Acceptance Criteria

- [ ] `session/load` 请求结构包含 `additionalDirectories`
- [ ] 加载后 read/write/edit 工具可访问附加目录
- [ ] 空数组或缺失字段时行为与现有加载一致
- [ ] 对应 Go 单元测试通过

## Dependencies

None

## Type

backend

## Priority

high
