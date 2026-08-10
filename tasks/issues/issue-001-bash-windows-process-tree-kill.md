# Windows bash 进程树杀灭 + WaitDelay

## Description

Windows 下取消长 bash 命令（如 `bun install`、`node install.js`）时，`exec.CommandContext` 只杀直接子进程 bash.exe，孙进程仍持有 stdout/stderr 管道句柄，`cmd.Run()` 永久阻塞。本 Issue 为 BashTool 增加 Windows Job Object 进程树杀灭，并设置 `cmd.WaitDelay` 兜底管道句柄；`bash: command canceled` 必须作为终态，禁止重试。

对应 PRD：US-001、FR-1/2/3。

## Acceptance Criteria

- [ ] Windows 下 bash 子进程加入 Job Object，取消/超时时终止整个 Job
- [ ] bash 及孙进程（如 `node scripts/postinstall.js`）在取消后全部退出
- [ ] `BashTool.Execute` 设置 `cmd.WaitDelay`，context 取消后 N 秒内返回
- [ ] `bash: command canceled` 不进入工具重试路径
- [ ] 新增 Windows 取消测试不 Skip，验证进程树被杀
- [ ] `go test ./internal/agenttool` 通过

## Dependencies

None

## Type

backend

## Priority

high
