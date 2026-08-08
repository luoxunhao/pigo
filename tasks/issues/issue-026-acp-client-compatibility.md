# pigo Zed 与第三方 ACP 客户端兼容回归

## Description

共享进程改造不改变协议方法面，不新增客户端专属字段；Zed 每项目一进程的行为保持不变，per-session 隔离对单项目进程幂等。对应 PRD US-011 / FR-25。

## Acceptance Criteria

- [ ] 协议方法名、参数与事件形状不新增客户端专属字段
- [ ] Zed 发送的 `session/new` 带 `cwd`，per-session 构建逻辑按该 cwd 工作
- [ ] Zed 每项目一个进程时，单会话行为与改造前一致
- [ ] `agent-client-protocol` 兼容性测试通过
- [ ] 对应 Go 单元测试通过

## Dependencies

Issue #016、#017、#018、#019、#020

## Type

backend

## Priority

medium
