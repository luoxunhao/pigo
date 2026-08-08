# 独立配置网关

## Description

新增独立配置网关 pigo 进程，cwd 使用 `os.homedir()`，负责 `pigo/config`、`pigo/models`、`pigo/trust/list`、`pigo/trust/set` 等全局能力；项目进程只服务会话、工具与权限。对应 PRD US-003 / FR-4、FR-5。

## Acceptance Criteria

- [ ] 配置网关使用 `os.homedir()` 作为 cwd
- [ ] `pigo/config`、`pigo/models`、`pigo/trust/*` 通过配置网关调用
- [ ] 无项目会话时设置页仍可读取模型列表与信任目录
- [ ] 配置网关崩溃可重建，不影响已启动的项目进程
- [ ] 对应 vitest 单元测试通过

## Dependencies

Issue #1

## Type

backend

## Priority

high
