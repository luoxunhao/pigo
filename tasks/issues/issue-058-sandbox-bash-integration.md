# bash 前台与后台接入进程沙箱

## Description

按 `tasks/spec/spec-017-sandbox.md` 切片 6 实施：bash 工具（前台 + 后台 job）经进程沙箱包装——`exec.CommandContext` 改为经 SandboxProvider confine 后的 enforcing 执行；后台 job 同样受限（含 kill_bash 经 Job Object 终止路径兼容）；denial 以 marker 呈现。

## Acceptance Criteria

- [ ] bash 前台：执行经进程沙箱（Windows 受限令牌 / Linux landlock/bwrap / macOS seatbelt），模式按当前会话 resolve
- [ ] bash 后台（run_in_background）：job 进程同样经沙箱包装；bash_output/kill_bash 兼容
- [ ] 沙箱拒绝 → `[sandbox: file access denied under <mode> mode]` + 升级 hint（escalation_enabled 时）
- [ ] SANDBOX_UNAVAILABLE 路径：后端不可用拒绝执行并提示
- [ ] 单测：前台/后台沙箱包装、denial 呈现、与超时/取消（WaitDelay/Job Object）共存

## Dependencies

issue-056、issue-057（进程沙箱后端）。

## Type

backend

## Priority

high
