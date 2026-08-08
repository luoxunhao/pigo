# pigo 会话级 eventMapper cwd

## Description

`eventMapper` 按会话持有 cwd，不再使用进程级 `SetCwd`；bash 工具事件与相对文件路径按会话 cwd 解析。对应 PRD US-005 / FR-7。

## Acceptance Criteria

- [ ] `eventMapper` 按会话持有 cwd，不再使用进程级 `SetCwd`
- [ ] bash pending/in_progress/completed 事件的 `_meta.terminal_info.cwd` 为会话 cwd
- [ ] 相对文件路径按会话 cwd 解析为绝对路径
- [ ] 两个项目交替产生工具事件时，事件中的 cwd 不串
- [ ] 对应 Go 单元测试通过

## Dependencies

Issue #019

## Type

backend

## Priority

high
