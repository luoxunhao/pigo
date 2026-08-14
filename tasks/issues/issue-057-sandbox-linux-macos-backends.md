# 进程沙箱后端：Linux / macOS + SANDBOX_UNAVAILABLE

## Description

按 `tasks/spec/spec-017-sandbox.md` 切片 5 实施：Linux 后端（landlock Go 绑定，内核 5.13+；不可用时降级 bwrap）与 macOS 后端（sandbox-exec 包装）；统一 `SANDBOX_UNAVAILABLE` 错误（后端不可用拒绝执行，不静默降级、不自动回落 full-access）；后端探针按平台 tag 组织。

## Acceptance Criteria

- [ ] Linux landlock 后端：allow-list grant（read-only 无写、workspace-write 含 workspace/temp）；内核 <5.13 或绑定不可用时降级 bwrap（--ro-bind / + --tmpfs /tmp + --bind workspaceRoot）
- [ ] macOS seatbelt 后端：sandbox-exec profile 包装（writableRoots 同源）
- [ ] 两者均不可用 → `SANDBOX_UNAVAILABLE` 拒绝执行，错误信息提示安装后端或切 full-access
- [ ] 探针测试（linux/darwin tag）：landlock/bwrap/seatbelt 可用性探测与拒绝行为
- [ ] 跨平台：三后端经同一 SandboxProvider 抽象接入

## Dependencies

issue-053、issue-056（SandboxProvider 抽象与 Windows 实现先行）。

## Type

backend

## Priority

high
