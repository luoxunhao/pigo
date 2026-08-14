# /sandbox 命令与验收回归

## Description

按 `tasks/spec/spec-017-sandbox.md` 切片 8 实施：`/sandbox` 斜杠命令（REPL/TUI/serve/ACP available_commands 通知：显示当前模式 + 切换三档，写 lane.config）与三层验收收尾（行为测试矩阵 / 兼容回归 / 全量构建）。

## Acceptance Criteria

- [ ] `/sandbox` 命令：显示当前模式与 workspace 根；`/sandbox <mode>` 切换并写 lane.config；ACP available_commands 通知
- [ ] 行为测试矩阵全绿：模式切换与 lane.config 持久化（resume 直读）、fence 包含（别名/临时区）、进程沙箱拒绝与 SANDBOX_UNAVAILABLE、escalation 成对/严格变宽/审批 allowed-once/非变宽不打扰、PolicyReminder 注入内容与时效、各平台后端探针
- [ ] 兼容回归全绿：默认 workspace-write 下现有工作流（工作区内写、trust 询问、hooks 拦截、observed-state）行为不变，既有测试无破坏
- [ ] `go build ./...` 与相关包 `go test` 通过
- [ ] REPL / TUI / headless / serve / ACP 回归（bash 前后台、文件工具、escalation 交互）

## Dependencies

issue-053 ~ issue-059 全部完成。

## Type

backend

## Priority

high
