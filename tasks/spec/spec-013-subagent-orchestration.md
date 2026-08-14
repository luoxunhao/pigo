# SPEC: 通用 Subagent 编排与实时状态

> Technical specification derived from: `tasks/prd/prd-030-subagent-orchestration.md`
> Generated: 2026-07-31 | Target branch: master | Commit: 4f919af

## 1. Summary

### 1.1 What This SPEC Covers
新增一个通用 `task` 工具（复用并泛化 `internal/runtime` 现有 `SubAgentTool`），让模型能以自定义 prompt 派发通用子 agent，从而使 `/graph` 能在一条消息内并行 fan-out。同时新增 `SubAgentProgressEvent` 事件类型，让子 agent 的结构化进度经运行级 emitter 上报到父事件流，供交互式 TUI（多行状态面板）与 headless 模式（stderr 行）实时展示。范围限于 goroutine 隔离模式的通用 agent；不涉及 `subagent_type`/技能人格、多层嵌套、进程隔离行为变更。

### 1.2 PRD Reference
- Source: `tasks/prd/prd-030-subagent-orchestration.md`
- User Stories covered: US-001 ~ US-008
- Functional Requirements covered: FR-1 ~ FR-10

### 1.3 Design Decisions Summary
| Decision | Choice | Rationale |
|----------|--------|-----------|
| D-1 执行核心 | 泛化复用 `SubAgentTool` | 避免第二套执行路径；`executeGoroutine` 已实现子 loop 驱动 |
| D-2 子 agent 人格 | 只做通用 agent（固定通用 system prompt） | PRD 明确本轮不做 `subagent_type` |
| D-3 进度事件 | 新增专门 `SubAgentProgressEvent` | 与 `ToolExecutionUpdateEvent`（文本增量）解耦，语义清晰 |
| D-4 进度传输 | loop 把运行级 `emit` 注入 ctx，task 工具取出直接 emit | `AgentTool.Execute` 只有 `onUpdate`，改签名代价大；ctx 注入零侵入 |
| D-5 并发上限 | 工具实例级共享信号量，默认 4，`PIGO_MAX_SUBAGENTS` 覆盖 | 呼应 `/graph` max concurrency，防速率打爆 |
| D-6 嵌套护栏 | 子 RunConfig 的工具集剔除 `task` | 天然一层深度上限，最简单安全 |
| D-7 上报时机 | 按子 agent 工具执行/轮边界 | 活动变化才发，事件量可控 |
| D-8 活动粒度 | 工具名/阶段（Editing / Running bash / Thinking …） | 简洁；不含参数摘要，免截断/脱敏 |
| D-9 headless 输出 | 进度写 stderr | 不污染 stdout 最终结果/JSON |

---

## 2. Architecture

### 2.1 System Context
```
主 agent loop (runLoop, loop.go)
  └─ ExecuteToolCalls (并行 goroutine, batch_executor.go)
       └─ task 工具.Execute(id, {description, prompt})   ← parallel 执行模式
            ├─ 信号量 acquire (cap=4)
            ├─ 构建子 AgentContext(通用 systemPrompt + prompt)
            ├─ StartRun + DrainStream(子 loop)
            │     └─ 子 StreamHandler.OnEvent: 子工具/轮边界
            │           → 经 ctx 取出的父 emit 发 SubAgentProgressEvent{ToolCallID=id,…}
            └─ 返回子最终文本作为 tool result
父事件流 → bridge → TUI 多行面板 (subagentpanel.go)
             → headless.go → stderr 行
```

### 2.2 Component Design
- **`task` 工具**：`SubAgentTool` 的一个「通用 spec」实例（固定通用 system prompt，prompt 来自调用参数）。`ExecutionMode()=parallel`。
- **运行级进度 emitter**：`emitFrom`（loop.go:158，`agentcore.EmitFunc`）在调用 `ExecuteToolCalls`（loop.go:229）前注入 ctx；task 工具在子 `DrainStream.OnEvent` 里取出并发 `SubAgentProgressEvent`。
- **信号量**：task 工具实例持有的带缓冲 channel，`Env` 组装时按 cap 创建，一个进程一个实例即天然共享。
- **子 RunConfig 工厂**：`run.go` 组装 `Env` 时构造，捕获同一 provider stream/model，子工具注册表由 `BuiltinTools` 去掉 `task` 生成。

### 2.3 Module Interactions
1. 模型在一条 assistant 消息里发多个 `task` 调用 → `ExecuteToolCalls` 判定全 parallel → 各自 goroutine 并发。
2. 每个 task `Execute` 先 `sem <- struct{}{}` 占位（阻塞排队），`defer <-sem` 释放。
3. task 构建子 `AgentContext`，`StartRun` 子 loop，`DrainStream` 时 `OnText` 仍走 `onUpdate`（保留文本增量），`OnEvent` 额外把子工具/轮边界翻译成 `SubAgentProgressEvent` 并用 ctx 里的父 emit 发出。
4. 父事件流：`bridge.go` 映射为 `subagentProgressMsg`；`headless.go` 打印 stderr 行。
5. task 返回子最终文本；失败（子 `StopReason` error/aborted）按现有逻辑转 tool error。

### 2.4 File Structure
```
internal/agentcore/
├── event.go                 [MODIFY] 新增 SubAgentProgressEvent + EventType 常量
└── progress_ctx.go          [NEW]    WithProgressEmitter / ProgressEmitterFromContext
internal/runtime/
├── subagent.go              [MODIFY] executeGoroutine 接收运行时 prompt；子 OnEvent 上报进度
├── task.go                  [NEW]    NewTaskTool(factory, sem)：通用 spec + 信号量封装
└── loop.go                  [MODIFY] ExecuteToolCalls 前把 emitFrom 注入 ctx
internal/cli/run/
└── run.go                   [MODIFY] 组装 task 工具(子 RunConfig 工厂+信号量)、append tools、系统提示广告
internal/cli/tui/
├── msgs.go                  [MODIFY] subagentProgressMsg
├── bridge.go                [MODIFY] 映射 SubAgentProgressEvent
├── model.go                 [MODIFY] activeSubagents 有序集合 + start/progress/end 处理 + View 插入面板
└── subagentpanel.go         [NEW]    多行渲染 + 宽度截断
internal/runtime/
└── headless.go              [MODIFY] SubAgentProgressEvent → stderr 行
```

---

## 3. Data Model / 事件与工具契约

### 3.1 SubAgentProgressEvent（`internal/agentcore/event.go`）
```go
// SubAgentProgressEvent 携带一个运行中子 agent（由 task 工具派发）的结构化进度。
// 按子 agent 的工具执行/轮边界上报，供 TUI/headless 实时展示。
type SubAgentProgressEvent struct {
    ToolCallID  string // 父 task 调用的 tool-call id，作为状态行的键
    Description string // task 调用的 description，展示用（可空）
    Activity    string // 当前活动：工具名/阶段，如 "Editing" / "Running bash" / "Thinking"
    Tokens      int    // 子 agent 输出 token 估计（0 = 未知）
}

func (SubAgentProgressEvent) isAgentEvent()      {}
func (SubAgentProgressEvent) EventType() string  { return EventSubAgentProgress }
// const EventSubAgentProgress = "subagent_progress"
```
`Elapsed` 不入事件：由消费端（TUI 从 toolStart 时间、headless 从首次见到该 id 的时间）自行计算，避免每帧发事件。

### 3.2 进度 emitter 的 ctx 载体（`internal/agentcore/progress_ctx.go`）
```go
type progressEmitterKey struct{}

func WithProgressEmitter(ctx context.Context, emit EmitFunc) context.Context
func ProgressEmitterFromContext(ctx context.Context) EmitFunc // 无则返回 nil
```
`EmitFunc = func(ctx, AgentEvent) error`（已存在，helpers.go:34）。

### 3.3 task 工具参数 Schema
```json
{
  "type": "object",
  "properties": {
    "description": { "type": "string", "description": "3-5 词的任务简述，用于状态展示" },
    "prompt":      { "type": "string", "description": "交给子 agent 的完整任务（子 agent 上下文全新，需自包含）" }
  },
  "required": ["prompt"],
  "additionalProperties": false
}
```
`description` 可选但推荐（状态面板行首展示）。

### 3.4 泛化 SubAgentSpec
新增一个「运行时 prompt 来自调用」的通用 spec 形态：`SystemPrompt` 固定为通用子 agent 提示；`Name="task"`；`Tools` 为不含 task 的内置集；`NewRunConfig` 由 `run.go` 注入的工厂提供。`executeGoroutine` 现已从 `subAgentArgs.Prompt` 取 prompt——泛化点在于让该 prompt 走通用 spec，而非绑定固定描述。

---

## 4. 关键接口与控制流

### 4.1 loop 注入运行级 emitter（`loop.go`，约 229 行）
```go
// 现状: toolResults, allTerminate := agenttool.ExecuteToolCalls(ctx, cfg.Batch, calls, emitFrom)
// 改为:
toolCtx := agentcore.WithProgressEmitter(ctx, emitFrom)
toolResults, allTerminate := agenttool.ExecuteToolCalls(toolCtx, cfg.Batch, calls, emitFrom)
```
`emitFrom` 是运行级 emit（发进父事件流），随每次 run 新建，天然 run-scoped。

### 4.2 task 工具执行（`internal/runtime/task.go` + `subagent.go`）
```
Execute(ctx, id, args, onUpdate):
  a := decode(args); require a.prompt
  sem <- {} ; defer <-sem                      // D-5 并发上限
  parentEmit := agentcore.ProgressEmitterFromContext(ctx)   // D-4
  childCtx := {SystemPrompt: 通用提示, Messages:[user(prompt)], Tools: 子集(无 task)}
  h := StreamHandler{
        OnText: 转 onUpdate(文本增量),          // 保留现状，不回归
        OnEvent: func(ev):                       // D-7 按工具/轮边界
          act := activityOf(ev)                  // D-8 工具名/阶段
          if act != "" && parentEmit != nil:
             parentEmit(ctx, SubAgentProgressEvent{
                ToolCallID: id, Description: a.description,
                Activity: act, Tokens: lastTokenEstimate})
      }
  final := DrainStream(childCtx, h)
  失败(StopReason error/aborted) → tool error（现有逻辑）
  return final.text
```
`activityOf(ev)` 映射：子 `ToolExecutionStartEvent`→该工具的展示动词（read→Reading, edit/write→Editing, bash→"Running bash", grep/find→Searching, webfetch→Fetching, task 不会出现）；子 `TurnStartEvent`→"Thinking"。`lastTokenEstimate` 从子 `TelemetryEvent`/文本增量累计。

### 4.3 子 RunConfig 工厂（`run.go` 组装 Env 时）
```go
childTools := BuiltinToolsExcept(cwd, disabled, "task")   // D-6
factory := func() runtime.RunConfig {
    return buildChildRunConfig(prov, model, ToolRegistry(childTools))
    // 复用主 run 构建 RunConfig 的路径；子 agent 不装 hooks/reminders（保持简单）
}
sem := make(chan struct{}, maxSubagents())  // 默认 4，PIGO_MAX_SUBAGENTS 覆盖
tools = append(tools, runtime.NewTaskTool(factory, sem))
```

### 4.4 TUI 状态面板（`model.go` + `subagentpanel.go`）
- model 新增 `activeSubagents` 有序结构（切片保序 + map 索引 by ToolCallID）与每行 `{id, desc, activity, tokens, start}`。
- 处理：`toolStartMsg`(name=="task") 加行并记 start；`subagentProgressMsg` 更新 activity/tokens；`toolEndMsg` 删行。
- spinner tick 顺带刷新（elapsed 实时）。
- View：在 spinner 行上方插入面板；每行 `⏺ {desc} · {activity} ({elapsed} · ↓{tokens})`，`TruncateToWidth`；无行则不占高度（同时更新 model.go:953 的高度预留计算）。

### 4.5 headless（`headless.go`）
在事件 switch 增加：
```go
case agentcore.SubAgentProgressEvent:
    fmt.Fprintf(os.Stderr, "  ⏺ %s · %s\n", e.Description, e.Activity) // D-9 stderr
```
stream-json 的 stdout 事件包不含该类型（普通/JSON 结果输出不受影响）。

---

## 5. Business Logic

### 5.1 并发信号量
- `maxSubagents()`：读 `PIGO_MAX_SUBAGENTS`，非法/缺省→4，下限 1。
- `Execute` 起始 `sem <- struct{}{}`（阻塞直到有空位），`defer func(){ <-sem }()` 保证 panic/err 也释放。
- 信号量在 task 工具实例上，一个 `Env` 一个实例 → 同一 run 内所有 task 调用共享。

### 5.2 嵌套护栏
子工具集由 `BuiltinToolsExcept(..., "task")` 生成，子 agent 拿不到 task 工具，无法再 fan-out。深度上限恒为 1。

### 5.3 活动映射（activityOf）
| 子事件 | Activity |
|--------|----------|
| ToolExecutionStart read | Reading |
| ToolExecutionStart edit/write | Editing |
| ToolExecutionStart bash | Running bash |
| ToolExecutionStart grep/find/ls | Searching |
| ToolExecutionStart webfetch | Fetching |
| TurnStart（无工具进行中） | Thinking |
| 其它 | "" (不发) |

### 5.4 Edge Cases
- `parentEmit==nil`（非经 loop 调用，如测试直调）→ 静默不发进度，功能不受影响。
- 子 agent 无文本输出 → 沿用现有 "(sub-agent produced no text output)"。
- 进度事件被 bridge 缓冲通道（`eventChanCap=64`）背压 → 阻塞子 goroutine，不丢事件（D-7 已限量）。
- task 在 end 后又来迟到 progress → model 找不到行则忽略。
- ctx 取消 → 子 loop 随父 ctx 取消，信号量经 defer 释放。

---

## 6. Error Handling
| 失败 | 处理 |
|------|------|
| prompt 为空 | tool error："empty prompt"（现有） |
| 子 loop 失败(StopReason error/aborted) | tool error，父循环继续（现有 executeGoroutine 逻辑） |
| 子 panic | executor recover → error tool result（tool_executor.go 现有） |
| 信号量持有期间 err/panic | defer 释放，不泄漏配额 |
| 进度 emit 返回 err | 忽略（进度非关键路径） |

---

## 7. Security
- task 的 prompt 是模型生成的委派内容，子 agent 使用与父相同的工具边界（file 工具的 Root/ExtraRoots 不变），无新增越权面。
- headless stderr 输出仅含 description+activity（工具名/阶段），不含参数/文件内容（D-8），无敏感泄漏。
- 无网络外发、无凭据处理变更。

---

## 8. Performance
- 并发受 `maxSubagents`（默认 4）硬性限制。
- 进度事件按工具/轮边界发，量与子 agent 工具调用次数同阶，远低于文本增量频率。
- TUI 面板 O(活跃 subagent 数) 重绘，随 spinner tick（~120ms）刷新，无额外定时器。

---

## 9. Testing Strategy

### 9.1 Unit
- `agentcore`: `SubAgentProgressEvent` 实现 `AgentEvent`、`EventType()=="subagent_progress"`；`WithProgressEmitter`/`FromContext` 往返。
- `runtime`(faux provider): task 派发子 agent 返回文本；子工具执行触发 `SubAgentProgressEvent`（ToolCallID==父 id, Activity 映射正确）；`parentEmit==nil` 时不 panic。
- `runtime`: 信号量——派发 N>cap 个 task，断言任一时刻并发 ≤cap（用可阻塞的假工具 + 计数器）。
- `runtime`: 子工具集不含 "task"（BuiltinToolsExcept）。

### 9.2 Integration / TUI
- `bridge_test`: `SubAgentProgressEvent` → `subagentProgressMsg`。
- model 测试: start→加行, progress→更新, end→删行；面板渲染宽度截断；无活跃时不占高度。

### 9.3 headless
- 注入 `SubAgentProgressEvent`，断言 stderr 出现进度行、stdout（结果/JSON）不含之。

### 9.4 Acceptance Criteria Mapping
| US/FR | Test | Type | 描述 |
|-------|------|------|------|
| US-001/FR-3 | 泛化后现有 subagent_test 全绿 | unit | 无回归 |
| US-002/FR-1,2 | task 派发子 agent 返回文本 | unit | 通用工具可用 |
| US-003/FR-5 | 并发 ≤cap | unit | 信号量 |
| US-004/FR-4 | 子工具集无 task | unit | 嵌套护栏 |
| US-005/FR-6,7 | 事件类型 + 子边界上报 | unit | 进度事件 |
| US-006/FR-8 | 面板 start/progress/end + 截断 | unit | TUI |
| US-007/FR-9 | stderr 有、stdout 无 | unit | headless |
| US-008/FR-10 | 系统提示含 task 描述 | unit | 广告 |

---

## 10. Implementation Plan

### 10.1 Phases（按依赖）
1. **事件与 ctx 载体**：`SubAgentProgressEvent` + `progress_ctx.go`（US-005 基础，无依赖）。
2. **通用 task 工具**：泛化 `SubAgentTool`、`task.go`(含信号量+嵌套护栏)、`run.go` 接线（US-001/002/003/004）。依赖 1（若上报同期做）。
3. **子进度上报**：`loop.go` 注入 emitter + 子 `OnEvent` 翻译（US-005）。依赖 1、2。
4. **TUI 面板**：msgs/bridge/model/subagentpanel（US-006）。依赖 1、3。
5. **headless**：stderr 行（US-007）。依赖 1、3。
6. **系统提示广告**：（US-008）。依赖 2。

### 10.2 Issue Mapping
| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| #A 事件+ctx | 3.1,3.2 | high | — |
| #B task 工具+接线+信号量+护栏 | 3.3,3.4,4.2,4.3,5.1,5.2 | high | — |
| #C 进度上报 | 4.1,4.2,5.3 | high | #A,#B |
| #D TUI 面板 | 4.4,5.4 | med | #A,#C |
| #E headless | 4.5 | med | #A,#C |
| #F 系统提示 | US-008 | low | #B |

### 10.3 Incremental Delivery
#A+#B 合入后 `/graph` 即可真正并发（无进度展示）；#C 起状态可见；#D/#E 分别覆盖两种前端；#F 收尾。每步独立可测、可单独合入。

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions
- 子 RunConfig 工厂是否需要继承父的 provider 覆盖（BaseURL/Protocol/自定义网关）？倾向继承主 run 的解析结果。
- `/graph` 的 worktree 逻辑仍靠子 agent 内 bash 执行；本 SPEC 不改编排，需确认 graph 技能正文里「Agent/subagent 调用」措辞与新 `task` 工具名一致（可能需微调 SKILL.md 用词）。

### 11.2 Technical Risks
| Risk | Impact | Mitigation |
|------|--------|-----------|
| 子 loop 复用主 RunConfig 构建路径耦合 | 中 | 抽一个 `buildChildRunConfig` 收敛构造；子不装 hooks/reminders 降复杂度 |
| 进度事件与文本增量在 bridge 通道竞争背压 | 低 | D-7 限量；必要时给进度单独轻量路径 |
| TUI 高度预留改动引入布局回归 | 中 | model.go:953 高度计算同步更新 + 快照测试 |

### 11.3 Assumptions
- goroutine 隔离模式下子 loop 与父同进程共享 provider client 安全（现状 executeGoroutine 已如此）。
- `/graph` 通过一条消息多 `task` 调用即可获得并行（依赖现有 batch_executor parallel 语义，已验证 batch_executor.go:54-64）。
- 子 agent 使用与父相同的内置工具（除 task）即可完成 `/graph` 节点的 implement→review→ship（技能作为 slash/prompt 仍可用）。


