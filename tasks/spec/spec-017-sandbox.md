# SPEC: pigo sandbox 体系（对齐 deepseek-harness）

> 状态：待实施。决策源为 grilling 共识（13 项）与 `docs/adr/0013-sandbox-hierarchy.md`。

## 目标

为 pigo 引入 sandbox 体系：三档模式（read-only / workspace-write / danger-full-access）作为文件边界策略，执行级强制（进程内 file fence + 进程级进程沙箱），模式经 PolicyReminder 每轮注入模型上下文，escalation 升级审批闭环；默认 workspace-write，现有工作流行为兼容。

## 范围

涉及：`internal/sandbox`（新建：policy 解析、fence、进程沙箱后端、escalation）、`internal/agenttool`（bash/pwsh/文件工具接入）、`internal/runtime`（PolicyReminderProvider）、`internal/session` 与 `internal/sessionstore`（lane.config SandboxMode）、`internal/trust`（escalation 审批通道）、`internal/cli`（/sandbox 命令）、`internal/cli/config` 与 `cmd/pigo`（[sandbox] 配置、--sandbox-mode）。

排除：网络沙箱（websearch/webfetch 不受文件沙箱约束）；plugin 进程外工具的沙箱强制（外部进程自带边界）；容器/微VM 类后端（本轮仅同主机进程沙箱）。

## 策略层（mode）

- 三档：`read-only` / `workspace-write` / `danger-full-access`；默认 `workspace-write`。
- 模式与 trust 并存、职责分离：sandbox = 文件边界，trust = 工具前审批（allow_once/allow_always/reject_always 语义保留）。
- 会话级覆盖存 `session.LaneConfig.SandboxMode`（`SetLaneConfig`，resume 直读）；部署默认来自 `[sandbox] default_mode`。
- workspace root：workspace-write 的可写根 = workspaceRoot + 平台临时区（`os.TempDir()`），canonical 化，所有执行方言共用同一 `writableRoots` 定义。
- 配置：`[sandbox]` 表（`default_mode` / `workspace_root` / backend 开关 / `escalation_enabled` 默认 true）；CLI `--sandbox-mode` 覆盖；优先级 CLI > config.toml > 默认。

## 执行层（enforcement）

- **File fence**（纯 Go 进程内）：read/write/edit/search 等文件工具的路径包含检查——canonical fast path + filesystem identity fallback（处理 Windows 8.3/大小写别名）；目标必须是可写根或其下。
- **进程沙箱后端**（`internal/sandbox`）：
  - Windows：`CreateRestrictedToken` + Job Object（x/sys/windows 自研受限令牌 runner，含 workspace/temp 写 SID）。
  - Linux：landlock Go 绑定（内核 5.13+），不可用时降级 bwrap（需安装）；均不可用 → fail-closed。
  - macOS：`sandbox-exec` 包装（外部命令）。
  - 无可用后端 → `SANDBOX_UNAVAILABLE` 错误拒绝执行（不静默降级、不自动回落 full-access）。
- **接入**：bash 前台 + 后台 job 都经进程沙箱包装；文件工具经 fence；网络工具不约束。
- 信任链时序：hooks PreToolUse → trust 询问 → sandbox 执行强制。

## 模型可见（policy injection）

- 新增 `PolicyReminderProvider`（ReminderRegistry 常驻）：每轮注入当前模式 + workspace 根 + escalation 通道说明到请求副本；不进持久历史、不碰 system prompt 指纹缓存；模式切换下轮即生效。

## Escalation（升级闭环）

- 工具 schema 增加 `sandbox_permissions` + `justification`（**必须成对**，justification 非空句子）。
- 拒绝统一文案：`[sandbox: file access denied under <mode> mode]` + escalation hint（"retry this exact command once with sandbox_permissions (the narrowest wider mode that suffices) + justification"）。
- 严格变宽阶梯：read-only → workspace-write → danger-full-access（执行时校验，不可跳级、不可降级）；非变宽请求不打扰人。
- 升级请求走 trust 审批通道（新增 allowed-once 升级决策）；拒绝/取消/无通道 → fail-closed 错误结果；批准仅该次调用生效。

## 用户入口

- `/sandbox` 斜杠命令（REPL/TUI/serve/ACP available_commands 通知）：显示当前模式 + 切换三档，写 lane.config。
- `--sandbox-mode` headless flag。

## 验收与兼容

- 三层验收：
  1. **行为测试矩阵**：模式切换与 lane.config 持久化（resume 直读）、fence 路径包含（含别名/临时区）、进程沙箱拒绝与 SANDBOX_UNAVAILABLE、escalation 成对校验/严格变宽/审批 allowed-once/非变宽不打扰、PolicyReminder 注入内容与时效、各平台后端探针。
  2. **兼容回归**：默认 workspace-write 下现有工作流（工作区内写、trust 询问、hooks 拦截、observed-state）行为不变；`go test ./...` 既有用例无破坏。
  3. **全量构建**：`go build ./...`。
- 后端探针测试按平台 tag 组织（windows/linux/darwin），CI 平台覆盖。

## 建议实施切片

1. 策略层：`internal/sandbox` policy 类型、writableRoots、lane.config `SandboxMode`、[sandbox] 配置与 --sandbox-mode。
2. PolicyReminderProvider 注入 + 模式解析服务（per-session resolve）。
3. File fence：fence 实现 + 文件工具接入。
4. 进程沙箱后端：Windows 受限令牌 runner（先行，开发环境）。
5. 进程沙箱后端：Linux landlock/bwrap + macOS sandbox-exec + SANDBOX_UNAVAILABLE 统一错误。
6. bash 前台 + 后台接入进程沙箱。
7. Escalation：参数成对、marker/hint 文案、trust 审批通道（allowed-once）。
8. /sandbox 命令 + 兼容回归 + 三层验收。

## 验证

- `go build ./...`。
- 行为测试矩阵、兼容回归、平台 tag 探针测试。
- REPL / TUI / headless / serve / ACP 回归（bash、文件工具、后台 job、escalation 交互）。
