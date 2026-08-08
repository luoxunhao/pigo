# 项目内会话复用与懒恢复

## Description

同一项目内的历史会话复用同一个 pigo 进程；切换/激活对话时才确保进程启动并执行 `session/load`，不在应用启动时恢复所有项目。对应 PRD US-002 / FR-2、FR-3。

## Acceptance Criteria

- [ ] 应用启动时不自动恢复所有项目进程
- [ ] 切换/激活某项目对话时，确保该项目进程已启动
- [ ] 切换时调用 `session/load` 恢复对应 acpSessionId
- [ ] 同一项目多个历史会话复用同一进程
- [ ] 对应 vitest 单元测试通过

## Dependencies

Issue #1

## Type

backend

## Priority

high
