# ash-workbench 启动即拉起共享 pigo

## Description

取消懒启动：应用启动时立即拉起共享 pigo 进程，启动 cwd 使用 `os.homedir()`，不绑定任何项目目录；渲染进程启动阶段不主动读 pigo 配置决定是否拉起。对应 PRD US-008 / FR-13、FR-14。

## Acceptance Criteria

- [ ] 主进程 ready 后立即 spawn 共享 pigo，不等首次对话
- [ ] 共享 pigo 启动 cwd 为 `os.homedir()`，不绑定任何项目目录
- [ ] 启动时渲染进程不主动读 pigo 配置来决定是否拉起
- [ ] 无任何项目/对话时进程仍然存在
- [ ] 启动失败时显示可恢复错误状态
- [ ] 对应 vitest 单元测试通过

## Dependencies

Issue #022

## Type

backend

## Priority

high
