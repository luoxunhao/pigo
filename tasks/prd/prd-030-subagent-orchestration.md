# PRD: 通用 Subagent 编排与实时状态

## Introduction

pigo 目前虽然内部有完整的子 agent 执行机制（`internal/runtime/subagent.go` 的 `SubAgentTool`，支持 goroutine/进程隔离、并行执行），但**模型没有任何工具可以调用它**：内置工具集（`internal/cli/run/run.go` 的 `BuiltinTools`）里没有通用的 Task/Agent 工具，技能也只是被当作提示词在主循环里原地展开，而非真正的子 agent。

这带来两个问题：

1. **`/graph` 无法真正并发** —— graph 技能要求「一条消息发 N 个 subagent 调用实现 fan-out」，但模型无工具可调，只能串行自己做。
2. **看不到多个 subagent 的实时状态** —— 子 agent 内部活动被丢弃，且 TUI 只有单行 spinner，没有「每个并发 subagent 占一行」的状态区（Claude Code 的做法）。

本 PRD 描述新增一个**通用 `task` 工具**（复用并泛化现有 `SubAgentTool`），并新增 **`SubAgentProgressEvent`** 事件类型，让 TUI 与 headless 模式都能展示多个并发 subagent 的实时状态。

## Goals

- 让模型能通过一个通用 `task` 工具，以自定义 prompt 派发通用子 agent。
- 让 `/graph` 能在一条消息里派发多个 `task` 调用，实现真正的并行 fan-out / fan-in。
- 通过共享信号量限制子 agent 并发数（默认 4），避免速率限制与资源打爆。
- 禁止子 agent 再派发子 agent（无限嵌套护栏）。
- 新增 `SubAgentProgressEvent`，让父层拿到每个子 agent 的结构化进度（当前活动、token、耗时）。
- 在交互式 TUI 中以多行面板实时展示每个运行中的 subagent。
- 在 headless / 非交互模式中把 subagent 进度打印成行。

## User Stories

### US-001: 泛化 SubAgentTool 支持运行时自定义 prompt
**Description:** 作为开发者，我需要把现有 `SubAgentTool` 泛化，使单次调用能接收模型提供的任务 prompt 与（可选）system prompt，而不是绑死在一个固定 spec 上，以便复用它作为通用 task 工具的执行核心。

**Acceptance Criteria:**
- [ ] `executeGoroutine` 逻辑可接受每次调用传入的 prompt 构建子 `AgentContext`（现有行为不回归）
- [ ] 保留现有 `SubAgentSpec` 固定 system prompt 的用法，泛化路径为叠加而非替换
- [ ] 现有 `subagent_test.go` 全部通过
- [ ] `go build ./... && go vet ./... && go test ./internal/runtime/...` 通过

### US-002: 新增通用 task 工具并注册进工具集
**Description:** 作为使用 pigo 的模型，我需要一个 `task` 工具，接收 `{description, prompt}` 派发一个通用子 agent，返回其最终文本，以便实现委派与 fan-out。

**Acceptance Criteria:**
- [ ] `task` 工具 Schema 为 `{description: string, prompt: string}`，`prompt` 必填
- [ ] `ExecutionMode()` 返回 parallel
- [ ] 子 agent 使用通用 system prompt + 内置工具集（不含 task，见 US-004），跑完返回最终 assistant 文本作为 tool result
- [ ] 在 `run.go` 组装 Env 时构造 task 工具（注入子 RunConfig 工厂：同一 provider stream + 子工具注册表），append 进 tools
- [ ] 子 agent 运行失败时以 tool error 形式返回，父循环不受影响
- [ ] `go build ./... && go vet ./... && go test ./...` 通过

### US-003: 子 agent 并发信号量
**Description:** 作为运维/使用者，我希望同时运行的子 agent 数量有上限，以免一次 fan-out 打爆 provider 速率或本地资源。

**Acceptance Criteria:**
- [ ] task 工具共享一个信号量，默认最大并发 4
- [ ] 超出上限的 task 调用阻塞排队，直到有空位，不报错、不丢弃
- [ ] 上限可通过配置/环境变量覆盖（如 `PIGO_MAX_SUBAGENTS`）
- [ ] 单元测试验证：派发 N>上限 个 task，任一时刻运行中的不超过上限
- [ ] `go test ./...` 通过

### US-004: 禁止子 agent 再起子 agent（嵌套护栏）
**Description:** 作为开发者，我需要确保子 agent 的工具集不包含 `task`，从而杜绝无限嵌套 fan-out。

**Acceptance Criteria:**
- [ ] 子 RunConfig 工厂构建的子工具注册表中不含 `task` 工具
- [ ] 单元测试验证子 agent 的工具集里没有 `task`
- [ ] `go test ./...` 通过

### US-005: 新增 SubAgentProgressEvent 并由子 agent 上报
**Description:** 作为父层（TUI/headless），我需要一个专门的 `SubAgentProgressEvent` 事件，携带每个子 agent 的结构化进度，以便实时展示。

**Acceptance Criteria:**
- [ ] `internal/agentcore/event.go` 新增 `SubAgentProgressEvent`，字段含 `ToolCallID`、当前活动（如工具名/turn 阶段）、token 估计、耗时
- [ ] 泛化后的子 agent 执行在其 `DrainStream` 中捕获子事件，转译为 `SubAgentProgressEvent` 上报到父事件流
- [ ] 现有 `ToolExecutionUpdateEvent`（文本增量）行为不回归
- [ ] 事件实现 `AgentEvent` 接口并有对应 `EventType()`
- [ ] `go test ./internal/agentcore/... ./internal/runtime/...` 通过

### US-006: TUI 多行 subagent 状态面板
**Description:** 作为交互式用户，我希望在输入框上方看到每个运行中的 subagent 各占一行的实时状态，类似 Claude Code。

**Acceptance Criteria:**
- [ ] bridge 将 `SubAgentProgressEvent` 映射为新的 TUI 消息（如 `subagentProgressMsg`）
- [ ] model 维护运行中 subagent 的有序集合：start 时加行、progress 时刷新活动/token/耗时、end 时移除
- [ ] 新增渲染函数，在 spinner/输入框上方输出多行，每行形如 `⏺ <description> · <活动> (<耗时> · ↓<tokens>)`
- [ ] 无运行中 subagent 时面板不占行；面板行随终端宽度截断
- [ ] Typecheck/lint 通过
- [ ] 在实际 TUI 中运行 `/graph` 或多 task 调用，肉眼确认多行状态实时刷新（如 `run` skill）

### US-007: headless 模式打印 subagent 进度
**Description:** 作为在非交互/管道场景使用 pigo 的用户，我希望 subagent 进度也能以文本行输出，便于观察与日志。

**Acceptance Criteria:**
- [ ] headless 事件处理（`internal/runtime/headless.go` 或对应 CLI 输出层）识别 `SubAgentProgressEvent` 并打印成行（含 description + 活动 + 耗时）
- [ ] 输出不干扰最终 stdout 结果（进度走 stderr 或明确前缀）
- [ ] `go test ./...` 通过

### US-008: 系统提示广告 task 工具
**Description:** 作为使用 pigo 的模型，我需要在系统提示里知道 `task` 工具存在及何时用它（fan-out 委派），以便 `/graph` 等技能能正确触发并发。

**Acceptance Criteria:**
- [ ] task 工具出现在模型可见的工具列表/能力描述中，说明其「派发独立子 agent 并行完成子任务」的用途
- [ ] 描述提示「一条消息里发多个 task 调用即并行」
- [ ] `go test ./...` 通过

## Functional Requirements

- FR-1: 系统必须提供一个名为 `task` 的通用工具，接收 `{description, prompt}` 并派发一个通用子 agent。
- FR-2: `task` 工具必须以 parallel 执行模式运行，使同一消息内的多个 task 调用能并发执行。
- FR-3: 系统必须复用泛化后的 `SubAgentTool` 作为 `task` 的执行核心，而非引入第二套执行路径。
- FR-4: 子 agent 必须使用不含 `task` 工具的子工具集，禁止再派发子 agent。
- FR-5: 系统必须以共享信号量限制并发子 agent 数量，默认 4，可通过环境变量覆盖。
- FR-6: 系统必须新增 `SubAgentProgressEvent` 事件类型，携带子 agent 的结构化进度。
- FR-7: 子 agent 执行必须在运行过程中周期性上报 `SubAgentProgressEvent`。
- FR-8: 交互式 TUI 必须以多行面板实时展示每个运行中的 subagent。
- FR-9: headless 模式必须把 `SubAgentProgressEvent` 打印成文本行且不污染最终结果输出。
- FR-10: 系统提示必须向模型广告 `task` 工具及其 fan-out 用法。

## Non-Goals

- 不在本轮做 `subagent_type`（选择技能作为子 agent 人格）—— 先只做通用 agent。
- 不做子 agent 的多层嵌套 fan-out（明确禁止）。
- 不改动进程隔离（`--subagent-rpc`）模式的现有行为；本轮聚焦 goroutine 模式的通用工具与进度展示。
- 不重写 `/graph` 技能正文的编排逻辑；仅让其依赖的 task 工具真正可用。
- 不做子 agent 之间的消息互通或共享任务列表。

## Technical Considerations

- 泛化点在 `internal/runtime/subagent.go`：让 `executeGoroutine` 接受运行时 prompt；`task` 工具可作为 `SubAgentTool` 的一个通用 spec（固定通用 system prompt，prompt 来自调用参数）。
- task 工具需要 provider stream 与子工具注册表工厂，`BuiltinTools()` 为无状态纯函数无法提供，须在 `run.go` 组装 `Env`（provider 解析后）时构造并注入。
- 子 RunConfig 工厂构建子工具集时剔除 `task`（US-004），天然形成一层深度上限。
- 信号量在 task 工具实例级共享（一个 Env 一个实例）。
- `SubAgentProgressEvent` 需按 `ToolCallID` 关联到对应的 task 调用，TUI/headless 据此聚合。
- 进度上报频率需节流（如每 turn 或每工具边界一次），避免事件风暴压垮 bridge 的缓冲通道（`eventChanCap=64`）。
- 相关测试位点：`internal/runtime/subagent_test.go`、`internal/agenttool/batch` 相关、`internal/cli/tui/bridge_test.go` 与 model 测试。

## Success Metrics

- 在支持并发的 provider 下运行 `/graph`，一个 wave 内的多个节点子 agent 确实并发执行（墙钟时间显著短于串行）。
- 运行中能同时看到 ≥2 个 subagent 各自的实时状态行。
- 任一时刻并发子 agent 数不超过配置上限。

## Open Questions

- 信号量上限的默认值 4 是否合适？是否需要区分 provider 分别设默认？
- `SubAgentProgressEvent` 的「当前活动」粒度：到工具名即可，还是要包含工具参数摘要？
- 进度上报节流策略：按 turn、按工具边界，还是固定时间间隔？
- headless 进度输出走 stderr 还是带前缀的 stdout？

