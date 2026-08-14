# 进程沙箱后端：Windows 受限令牌

## Description

按 `tasks/spec/spec-017-sandbox.md` 切片 4 实施（先行，开发环境）：Windows 进程沙箱后端——`CreateRestrictedToken` + Job Object 的受限令牌 runner（x/sys/windows），给 workspace 根与临时目录授予写 SID（对齐 DSH 的 AclSandbox / workspaceWriteSid / tempWriteSid 语义）；`confine` 返回 enforcing argv 或 fail-closed。

## Acceptance Criteria

- [ ] Windows runner：受限令牌创建、workspace/temp 写 SID 授予、Job Object 关联（含进程树终止）
- [ ] `internal/sandbox` SandboxProvider 抽象：`confine()` 返回 enforcing argv 或抛 `SANDBOX_UNAVAILABLE`（静默未沙箱放行禁止）
- [ ] read-only 模式下无写 SID；workspace-write 仅 workspace/temp 可写；full-access 直通
- [ ] 探针测试（windows tag）：受限进程实际写越界被拒
- [ ] 错误路径：令牌创建失败 → SANDBOX_UNAVAILABLE 拒绝执行

## Dependencies

issue-053（策略层）。

## Type

backend

## Priority

high
