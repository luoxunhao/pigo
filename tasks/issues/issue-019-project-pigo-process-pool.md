# 按项目懒启动 pigo 进程池

> 已废弃：由 `issue-022` 等新 Issue 取代，方向改为单个共享 pigo 进程 + per-session 隔离。

## Description

将 ash-workbench 的单例 `PigoGateway` 改为按 `project.path` 管理的进程池。首次在项目内创建对话或发消息时启动一个 `pigo.exe --acp` 进程，cwd 为项目主目录；同一项目复用进程，不同项目不共享；应用退出时统一清理。对应 PRD US-001 / FR-1、FR-2、FR-3。

## Acceptance Criteria

- [ ] 项目 A 首次使用时启动独立 pigo 进程，cwd 为项目 A 主目录
- [ ] 项目 B 使用独立进程，不复用项目 A 进程
- [ ] 同一项目后续对话复用已启动进程
- [ ] 应用退出时统一清理全部 pigo 进程
- [ ] 对应 vitest 单元测试通过

## Dependencies

None

## Type

backend

## Priority

high
