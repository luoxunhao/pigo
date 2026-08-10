# PRD: pigo turn 卡死与 ACP 消息队列阻塞修复

## 1. Introduction

Zed 通过 `pigo.exe --acp` 使用 pigo 时，长 bash 命令（例如 `bun install`、`cd node_modules/bun && node install.js`）被取消后，turn 无法正常结束，后续消息全部进入 pending 队列且队列永久阻塞。同一现象也出现在 desktop-cc-gui 的 task/todo 工具取消链路中。

根因分三层：

- 工具层：Windows 下 `exec.CommandContext` 取消时只杀直接子进程 bash.exe，不杀进程树；孙进程（node、electron-builder）持有 stdout/stderr 管道句柄，`cmd.Run()` 永久等待。
- 循环层：`runLoop` 在 `ExecuteToolCalls` 返回后不检查 `ctx.Err()`，把取消错误当普通 tool result 喂回模型，继续下一轮 LLM 调用。
- 会话层：ACP 的 turn 槽位只在 `SessionManager.Run` 返回后释放，且没有超时/看门狗兜底；任何工具卡死都会让 `turnActive` 永远为 true，后续 `session/prompt` 无限排队。

另外，本地 fork 把 TUI/REPL 也改成了 in-process ACP（`RunACP`），而上游 `smallnest/pigo` 的 TUI/REPL 是直连 `runtime.StartRun` + 事件桥的 zero-transport 架构。TUI/REPL 不应依赖 ACP，应在本 PRD 的 Phase 2 对齐 upstream。

## 2. Goals

- Windows 下取消 bash 时杀掉整棵进程树，工具调用及时返回。
- 取消后 agent loop 以 cancelled/aborted 终态收尾，不再向模型重试。
- ACP turn 槽位增加 idle 看门狗，任何工具/子代理卡死都不能永久阻塞队列。
- TUI/REPL 移除 in-process ACP，直连 runtime，与 upstream 架构一致。
- 修复覆盖单测、集成测试和 Zed 手动端到端验证。

## 3. User Stories

### US-001: Windows bash 取消杀进程树
**Description:** As a Zed/pigo 用户, I want 取消长 bash 命令时整个进程树被杀死, so that turn 能及时结束，后续消息不再排队阻塞。

**Acceptance Criteria:**
- [ ] Windows 下取消 bash 后，bash 及其孙进程（如 `node scripts/postinstall.js`）都退出
- [ ] `BashTool.Execute` 在 context 取消后 N 秒内返回，不阻塞在 `cmd.Run()`
- [ ] 新增 Windows 取消测试不 Skip，验证进程树被杀
- [ ] 对应 `go test ./internal/agenttool` 通过

### US-002: runLoop 取消终态收尾
**Description:** As a pigo runtime 开发者, I want 工具执行后立即检查 context 取消, so that 取消不会变成下一轮 LLM 调用。

**Acceptance Criteria:**
- [ ] `ExecuteToolCalls` 返回后检查 `ctx.Err()`
- [ ] 已取消时追加 `StopReasonAborted` 终态 assistant 消息，不调 provider
- [ ] 取消错误（`bash: command canceled`、`tool call aborted`）不被当作普通 tool result 喂回模型
- [ ] 新增 loop 取消测试通过

### US-003: ACP turn 看门狗释放队列
**Description:** As a Zed/pigo 用户, I want turn 卡死时有兜底, so that 排队消息不会永久等待。

**Acceptance Criteria:**
- [ ] 默认 5 分钟无任何 AgentEvent/工具输出心跳时，强制 `finishTurn`
- [ ] 当前 turn 以 cancelled/error 结束，槽位释放
- [ ] 队列中的下一条消息继续执行，不清空队列
- [ ] 看门狗阈值可配置
- [ ] 新增 ACP 看门狗集成测试通过

### US-004: Zed 端到端恢复
**Description:** As a Zed 用户, I want 在 Zed 中取消 `bun install` 后新消息能继续, so that 会话不再卡死。

**Acceptance Criteria:**
- [ ] 手动在 Zed 中运行 `bun install 2>&1` 并取消
- [ ] 取消后发送新消息，不再显示永久排队
- [ ] 会话状态恢复为可继续对话

### US-005: TUI 直连 runtime（Phase 2）
**Description:** As a pigo TUI 开发者, I want TUI 不经过 in-process ACP, so that 不继承 ACP 队列语义且与 upstream 架构一致。

**Acceptance Criteria:**
- [ ] `tui.Run` 不再调用 `acp.StartInProcessWithHooks`
- [ ] TUI 通过 `runSession` + `runtime.StartRun` + 事件桥驱动 agent
- [ ] TUI 会话持久化、斜杠命令、权限确认行为与现状一致
- [ ] 对应 Go 测试通过

### US-006: REPL 直连 runtime（Phase 2）
**Description:** As a pigo REPL 开发者, I want REPL 不经过 in-process ACP, so that SIGINT 直接取消 run context。

**Acceptance Criteria:**
- [ ] `repl.Run` 不再调用 `acp.StartInProcessWithHooks`
- [ ] REPL 通过 `replDeps` + `runtime.StartRun`/`DrainStream` 直连
- [ ] SIGINT 直接 cancel run context 并回到提示符
- [ ] 对应 Go 测试通过

### US-007: headless 取消语义正确
**Description:** As a pigo headless 使用者, I want 取消的 run 报 aborted 而非静默 end_turn, so that 脚本能区分取消与正常完成。

**Acceptance Criteria:**
- [ ] canceled/aborted run 返回非零退出码或 `ErrRunFailed{Reason:"aborted"}`
- [ ] 不再因最后消息缺失而把取消伪装成 `end_turn`
- [ ] 新增 headless 取消测试通过

## 4. Functional Requirements

- FR-1: Windows 下 bash 工具必须把子进程放入 Job Object，取消/超时时杀整棵进程树。
- FR-2: bash 工具必须设置 `cmd.WaitDelay`，避免孙进程持有管道句柄导致 `cmd.Wait` 永久阻塞。
- FR-3: `bash: command canceled` 必须作为终态处理，禁止重试。
- FR-4: `runLoop` 必须在 `ExecuteToolCalls` 返回后检查 `ctx.Err()`，已取消则追加 `StopReasonAborted` 终态消息并结束 turn，不调 provider。
- FR-5: ACP 会话必须提供 turn idle 看门狗：默认 5 分钟无任何 AgentEvent/工具输出心跳时强制 `finishTurn`。
- FR-6: 看门狗触发后，当前 turn 以 cancelled/error 结束，turn 槽位释放，队列保留并执行下一条消息。
- FR-7: 看门狗阈值必须可配置（环境变量或配置项），默认 5 分钟。
- FR-8: `session/cancel` 必须幂等，且无论取消是否在工具执行中，最终都要释放 turn 槽位。
- FR-9: headless run 被取消时必须以 aborted 终态返回，不能静默回 `end_turn`。
- FR-10: TUI（Phase 2）必须直连 runtime，不启动 in-process ACP server。
- FR-11: REPL（Phase 2）必须直连 runtime，不启动 in-process ACP server。
- FR-12: `--acp` 模式必须保持现有 ACP 协议行为不变，Zed 集成不回归。
- FR-13: 所有新增修复必须有对应单元/集成测试，Windows 专用测试不得 Skip。

## 5. Non-Goals

- 不做 OS 级 sandbox / 权限隔离。
- 不做自动化 Zed e2e 测试床，Zed 验收为手动执行。
- 不改 provider transport 的 idle/stall 看门狗。
- 不新增 TUI/REPL 用户功能。
- 不重写子代理、remote-control、权限模型。
- Phase 2 的 TUI/REPL 解耦不并入 Phase 1 修复 PR。

## 6. Design Considerations

### Phase 1（当前修复）

- `internal/agenttool/bash_tool.go`：Windows 下创建 Job Object，将子进程加入 Job；`cmd.Cancel` 触发 Job 终止；`cmd.WaitDelay` 兜底管道句柄。
- `internal/runtime/loop.go`：工具执行后检查取消并终态收尾。
- `internal/acp/session.go` + `internal/acp/dispatch.go`：turn idle 看门狗，基于事件心跳重置。

### Phase 2（TUI/REPL 解耦）

- 对齐 upstream `smallnest/pigo`：TUI 使用 `internal/cli/tui/session.go` + `bridge.go`，REPL 使用 `internal/cli/repl/repl.go`，两者都通过 `cli.Host` 契约访问会话。
- `internal/acp` 只保留给 `--acp`（Zed、外部客户端）。

## 7. Technical Considerations

- `golang.org/x/sys v0.47.0` 已在 go.mod（间接依赖），Windows Job Object API 可直接使用。
- 项目 go 版本 `1.27rc1`，`os/exec.Cmd.WaitDelay` 可用。
- 看门狗实现位置：`SessionManager.Run` 或 `AcpSession`，通过包装 `onEvent` 重置心跳计时器。
- 取消错误判定：`isDegenerateToolError` 应包含 `bash: command canceled`、`tool call aborted`，或由 runLoop 的取消检查直接短路。
- 验收环境：Windows 10 + Zed `agent_servers.pigo`（`E:\project\pigo\pigo.exe --acp`）。

## 8. Success Metrics

- `go test ./internal/agenttool ./internal/runtime ./internal/acp ./internal/cli/...` 全部通过。
- 新增 Windows 取消测试不再 Skip 且通过。
- Zed 手动复现：取消 `bun install` 后，新消息在看门狗阈值内恢复，不再永久排队。
- 会话记录中不再出现 `bash: command canceled` 后无终态、`turnActive` 不释放的情况。

## 9. Open Questions

- 看门狗配置项命名与位置：建议 `PIGO_TURN_IDLE_TIMEOUT` 环境变量或 config 中 `turn.idle_timeout`，默认 5 分钟。[Assumption]
- Phase 2 是否直接移植 upstream 的 `runSession`/`replDeps` 文件，还是按本地 fork 现状重构。[Assumption]
- 看门狗触发后当前 turn 的 stop reason 是 `cancelled` 还是 `error`：建议 `cancelled`。[Assumption]

