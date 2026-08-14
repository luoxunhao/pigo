# SPEC: pigo Hooks(用户可扩展的生命周期钩子)

> Technical specification derived from: `tasks/prd/prd-017-pigo-hooks.md`
> Generated: 2026-07-30 | Target branch: master | Commit: 48e3a37

## 1. Summary

### 1.1 What This SPEC Covers
本 SPEC 定义 pigo 用户级 Hook 系统的技术实现:一个新的 `internal/hooks` 叶子包(配置类型、matcher、shell runner、协议解析、事件分发),以及把它**组合(compose)** 进现有 seam 的装配逻辑。范围覆盖 PRD 的 9 个 hook 点与隔离/协议要求。不覆盖沙箱、非 shell 类型、远程分发(见 PRD Non-Goals)。

### 1.2 PRD Reference
- Source: `tasks/prd/prd-017-pigo-hooks.md`
- User Stories: US-001 ~ US-015
- Functional Requirements: FR-1 ~ FR-18

### 1.3 Design Decisions Summary
| Decision | Choice | Rationale |
|----------|--------|-----------|
| 执行载体 | shell 命令,stdin 传 JSON | 对齐 Claude Code,零编译 |
| 新包位置 | `internal/hooks`(叶子,仅依赖 agentcore) | 避免与 runtime/plugin 循环依赖 |
| PreToolUse 与 trust 共存 | 链式包裹:trust→hook | `BeforeToolCall` 已被 `trust.BeforeToolCall` 占用,改动最小 |
| PostToolUse | 组合进 `AfterToolCall` | 复用现有 field-level override seam |
| UserPromptSubmit 注入 | 一次性 system-reminder(经 `ReminderRegistry`) | 不污染持久化历史 |
| Stop / SubagentStop | 组合进 `ShouldStopAfterTurn` | 现有 seam 语义即"是否结束" |
| 观察型 hook(SessionEnd/PreCompact/Notification) | 复用 `OnEvent` 事件流(仿 `plugin.EventNotifier`) | 事件已在流中,低侵入 |
| SessionStart(需注入) | 在 run-start seam **同步**分发 | 保证注入赶上第一个 turn(见 §11.2) |
| 多 driver 装配 | 单一 helper `InstallHooks(*RunConfig,…)` at cli 层 | 避免 7 处 RunConfig 组装漂移 |
| 配置 | 扩展 `ConfigLayer.Hooks`,按事件类型追加合并 | 复用分层 config.json |
| 失败策略 | 可阻断 hook fail-open + 告警 | 一个坏 hook 不拖垮 agent |

---

## 2. Architecture

### 2.1 System Context
Hook 系统横切三个既有子系统:配置层(`internal/runtime/config.go`)、工具执行器(`internal/agenttool/tool_executor.go`)、agent 循环与事件流(`internal/runtime/loop.go` + `internal/agentcore/event.go`)。它以 `internal/plugin` 的"只暴露可观测字段"纪律为范本,但比 plugin 更进一步:除观察外还能**阻断/改写/注入**。

### 2.2 Component Design
新增叶子包 `internal/hooks`(仅依赖 `internal/agentcore`),职责:
- **config**:`HookConfig` / `HookMatcherConfig` 类型 + 校验。
- **matcher**:`MatchHooks(eventType, toolName) []HookConfig`。
- **protocol**:`HookInput`(→stdin JSON)、`HookOutput`(←stdout JSON)、`HookDecision`(解析后内部结构)。
- **runner**:`Runner.Run` — `sh -c` 执行、写 stdin、超时 kill、输出截断、退出码语义。
- **dispatch**:`Dispatcher` — 对一个事件匹配并顺序运行全部 hook,合并为单一 `HookDecision`。
- **notifier**:`HookNotifier` — 桥接 `AgentEvent` → 观察型 hook(仿 `plugin.EventNotifier`)。

装配 helper `InstallHooks(*runtime.RunConfig, *hooks.Dispatcher, deps)` 放在 **cli 层**(`internal/cli/run`),因为它同时依赖 runtime 与 hooks,放此处可避免 `runtime → hooks → runtime` 循环。

### 2.3 Module Interactions
- **同步阻断类**(PreToolUse/PostToolUse/UserPromptSubmit/Stop/SubagentStop):`InstallHooks` 用**装饰器**包裹 `RunConfig` 中已有的函数 seam;每次触发点 Dispatcher 同步调用 Runner。
- **观察类**(SessionEnd/PreCompact/Notification):`HookNotifier.Handle` 挂到 `RunConfig.OnEvent`,与 `plugin.EventNotifier` 并存(两者都是 OnEvent 观察者,现有代码已支持链式)。
- **SessionStart**:同步在 run-start seam 触发以保证注入顺序(§11.2)。

依赖方向:`agentcore ← hooks ← runtime`;`cli → {runtime, hooks, plugin}`。无环。

### 2.4 File Structure
```
internal/hooks/                         [NEW]
├── config.go        HookConfig, HookMatcherConfig, Validate
├── matcher.go       MatchHooks(eventType, toolName)
├── protocol.go      HookInput, HookOutput, HookDecision, parse
├── runner.go        Runner.Run (shell exec, stdin, timeout, cap)
├── dispatch.go      Dispatcher.Dispatch(event, toolName, input) HookDecision
├── notifier.go      HookNotifier (OnEvent bridge, observe-only)
├── config_test.go / matcher_test.go / runner_test.go
├── protocol_test.go / dispatch_test.go / notifier_test.go
internal/runtime/config.go              [MODIFY: ConfigLayer.Hooks + resolved Config.Hooks + merge]
internal/cli/run/run.go                 [MODIFY: load hooks layer, build Dispatcher, InstallHooks]
internal/cli/run/hooks_install.go       [NEW: InstallHooks compose helper]
internal/runtime/loop.go                [MODIFY: UserPromptSubmit/Stop seam hooks callable; SessionStart dispatch]
internal/cli/{repl,goal,btw,headless,tui}/…  [MODIFY: call InstallHooks (single helper)]
README.md                               [MODIFY: Hooks 章节 + 示例]
```

---

## 3. Data Model

### 3.1 Config Types (`internal/hooks/config.go`)
```go
// HookConfig 是单个 hook 命令。
type HookConfig struct {
    Type    string `json:"type"`              // v1 固定 "command"
    Command string `json:"command"`           // 交给系统 shell 执行
    Timeout *int   `json:"timeout,omitempty"` // 秒;nil = 默认 60
}

// HookMatcherConfig 把一组 hook 绑定到一个 matcher。
type HookMatcherConfig struct {
    Matcher string       `json:"matcher,omitempty"` // 空/"*"=全部;工具名;"a|b";正则
    Hooks   []HookConfig `json:"hooks"`
}

// HookSet: 事件类型 → matcher 列表。用于 ConfigLayer 与 resolved Config。
type HookSet map[string][]HookMatcherConfig
```

### 3.2 ConfigLayer 变更 (`internal/runtime/config.go`)
```go
type ConfigLayer struct {
    // …既有字段…
    Hooks hooks.HookSet `json:"hooks,omitempty"`
}
type Config struct {
    // …既有字段…
    Hooks hooks.HookSet // 合并后的只读结果
}
```
`ResolveConfig` 中对 Hooks 做**按事件类型追加合并**(FR-2):
```
for each layer (升序):
  for eventType, matchers := range layer.Hooks:
    cfg.Hooks[eventType] = append(cfg.Hooks[eventType], matchers...)
```
不同层同事件的 matcher 累加,不覆盖。

### 3.3 Protocol Types (`internal/hooks/protocol.go`)
```go
// HookInput → stdin JSON。仅可观测、非敏感字段(FR-17)。
type HookInput struct {
    EventType    string          `json:"event_type"`
    SessionID    string          `json:"session_id,omitempty"`
    ProjectDir   string          `json:"project_dir,omitempty"`
    ToolName     string          `json:"tool_name,omitempty"`     // Pre/PostToolUse
    ToolInput    json.RawMessage `json:"tool_input,omitempty"`    // Pre/PostToolUse
    ToolResponse json.RawMessage `json:"tool_response,omitempty"` // PostToolUse
    Prompt       string          `json:"prompt,omitempty"`        // UserPromptSubmit
    StopReason   string          `json:"stop_reason,omitempty"`   // Stop/SessionEnd
    Source       string          `json:"source,omitempty"`        // SessionStart(startup/resume)
    Trigger      string          `json:"trigger,omitempty"`       // PreCompact(manual/auto)
    Message      string          `json:"message,omitempty"`       // Notification
}

// HookOutput ← stdout JSON(可选;非 JSON 视为无操作)。
type HookOutput struct {
    Decision          string          `json:"decision,omitempty"` // "block"|"approve"|""
    Reason            string          `json:"reason,omitempty"`
    AdditionalContext string          `json:"additionalContext,omitempty"`
    Continue          *bool           `json:"continue,omitempty"`
    UpdatedInput      json.RawMessage `json:"updatedInput,omitempty"` // PreToolUse 改写参数
}

// HookDecision 是 Dispatcher 合并多个 hook 后的内部结果。
type HookDecision struct {
    Block             bool
    Reason            string
    AdditionalContext string          // 多 hook 时按顺序拼接
    UpdatedInput      json.RawMessage // 最后一个提供者胜出(见 §5.4)
}
```

### 3.4 Migration Plan
纯新增字段,向后兼容。无 hooks 配置时 `Config.Hooks` 为 nil,`InstallHooks` 短路(不包裹任何 seam),行为与引入前完全一致(FR-18)。无数据库、无 schema 迁移。

---

## 4. Hook Contract (对外协议,替代传统 API 层)

本特性不引入 HTTP API;"接口"是 pigo 与用户 shell 命令之间的进程契约。

### 4.1 触发点一览
| Hook | 触发位置 | 机制 | 能力 | 携带工具名 |
|------|----------|------|------|-----------|
| PreToolUse | `tool_executor.go` `BeforeToolCall` | 同步装饰(trust→hook) | block / updatedInput | 是 |
| PostToolUse | `tool_executor.go` `AfterToolCall` | 同步装饰 | additionalContext / reason | 是 |
| UserPromptSubmit | 提示词入口(REPL+headless) | 同步 | block / 注入(reminder) | 否 |
| Stop | `loop.go` `ShouldStopAfterTurn`/`finish()` 前 | 同步装饰 | block(强制续跑) | 否 |
| SubagentStop | `subagent.go` 结束路径 | 同步 | block | 否 |
| SessionStart | run-start seam | 同步 | 注入(reminder) | 否 |
| SessionEnd | `finish()` → agent_end 事件 | OnEvent 观察 | 无(仅观察) | 否 |
| PreCompact | compaction 事件 | OnEvent 观察 | 无 | 否 |
| Notification | trust 确认/等待 → 事件 | OnEvent 观察 | 无 | 否 |

### 4.2 输入(stdin)
Dispatcher 把 `HookInput`(§3.3)序列化为单行 JSON 写入命令 stdin;并注入环境变量 `PIGO_SESSION_ID` / `PIGO_PROJECT_DIR` / `PIGO_EVENT_TYPE`(FR-4)。命令 cwd = 项目工作目录。

### 4.3 输出(退出码 + stdout)
| 退出码 | 含义 | stdout | stderr |
|--------|------|--------|--------|
| 0 | 放行 | 若为合法 JSON → 按 `HookOutput` 解析 | 忽略 |
| 2 | 阻断 | 解析(可选) | 作为 `reason` 反馈 |
| 其它非 0 | 执行失败 | 忽略 | 记入告警 |

`HookOutput.decision="block"` 等价于 exit 2 的阻断。非 JSON stdout 且 exit 0 视为无操作(仅 debug 日志)。

### 4.4 各 hook 的输出语义
- **PreToolUse**:block ⇒ 工具不执行,`reason` 作为错误结果回填模型(复用 `BeforeToolCallDecision{Block, Content}`);`updatedInput` ⇒ 替换工具参数后继续。
- **PostToolUse**:`additionalContext`/`reason` ⇒ 复用 `AfterToolCallResult.Content` 追加到结果回填;不撤销已执行工具。
- **UserPromptSubmit**:block ⇒ 提示词不提交(REPL 回输入态 / headless 非零退出);`additionalContext` ⇒ 注册一次性 reminder。
- **Stop / SubagentStop**:block ⇒ 不结束,`reason` 作为引导消息续跑;连续 block 达上限强制结束(FR-12)。
- **观察型**:忽略 decision,仅执行失败告警。

### 4.5 破坏性变更
无。所有变更为新增可选字段与可选包裹,默认关闭。

---

## 5. Business Logic

### 5.1 Dispatcher.Dispatch(核心算法)
```
func Dispatch(eventType, toolName string, input HookInput) HookDecision:
    matched = MatchHooks(eventType, toolName)          // §5.2
    dec = HookDecision{}
    for h in matched (按 config 层级+声明顺序):
        out, err = Runner.Run(ctx, h, input)           // §5.3
        if err != nil:                                  // 超时/启动失败/非0非2
            warn(err); continue                         // fail-open(FR-15)
        if out.Block: dec.Block = true; dec.Reason += out.Reason
        if out.AdditionalContext != "":
            dec.AdditionalContext = join(dec.AdditionalContext, out.AdditionalContext)
        if out.UpdatedInput != nil: dec.UpdatedInput = out.UpdatedInput  // 后者胜出
        if dec.Block and eventType==PreToolUse: break   // 阻断短路,不再改写
    return dec
```

### 5.2 MatchHooks 规则(matcher.go)
- matcher 空 或 `"*"` → 匹配该事件全部触发。
- 精确工具名(如 `bash`)→ 仅该工具。
- `|` 分隔多值(如 `write|edit`)→ 任一命中。
- 其余按 Go `regexp` 编译并 `MatchString(toolName)`;编译失败记告警并跳过该 matcher。
- 不携带工具名的事件(SessionStart 等):matcher 被忽略,全部命中。

### 5.3 Runner.Run(runner.go)
```
1. 构造 exec.CommandContext(ctx_timeout, shell, "-c", h.Command)
2. 设 cwd=projectDir;env=os.Environ()+PIGO_*
3. stdin = json(input);stdout/stderr = 各自 bounded buffer(上限 1MB,FR-13)
4. 超时(h.Timeout 或默认 60s,FR-11)由 CommandContext 触发 kill
5. 按退出码分类返回 (HookOutput, error);exit 2 → 从 stderr 取 reason
```

### 5.4 Edge Cases
- 多个 PreToolUse 都返回 `updatedInput`:最后一个胜出(§5.1),文档明示;未来可加严格模式报错(Open Q)。
- 多 hook 同时 block:合并 reason,PreToolUse 首个 block 即短路。
- Stop 连续 block:Dispatcher 无状态;计数由 loop 装饰器维护(§5.5),达 FR-12 上限后忽略 block 并告警。
- UserPromptSubmit 注入 + block 同时出现:block 优先,忽略注入。
- 空 command / type≠"command":`Validate` 阶段拒绝并告警,跳过。

### 5.5 Stop 死循环防护
`InstallHooks` 包裹 `ShouldStopAfterTurn` 时持有一个 per-run 计数器闭包:每次 hook block 递增,`>= 上限(默认5)`时装饰器返回 true(允许结束)并告警,计数在自然结束时归零。

---

## 6. Error Handling

### 6.1 Error Taxonomy
| 情形 | 分类 | 处理 |
|------|------|------|
| exit 0 | 成功/放行 | 解析 stdout(可选) |
| exit 2 | 阻断 | block=true,stderr→reason |
| exit 其它非 0 | 执行失败 | 告警;可阻断 hook fail-open |
| 命令无法启动(ENOENT) | 执行失败 | 告警;fail-open |
| 超时被 kill | 执行失败 | 告警;fail-open |
| stdout 非法 JSON(exit 0) | 无操作 | debug 日志 |
| 输出超 1MB | 截断 | 截断后尝试解析 + 告警 |
| 配置非法(空 command 等) | 配置错误 | `Validate` 拒绝,加载期告警,跳过该 hook |

### 6.2 Retry Strategy
Hook **不重试**(副作用未知,重试不安全)。失败即按 §6.1 分类处理。

### 6.3 Failure Modes(隔离,FR-15)
告警统一走 `io.Writer`(默认 `os.Stderr`,复用 `plugin.EventNotifier.warnLog` 风格),不打断 agent。可阻断 hook 失败时默认 **fail-open**(放行),避免坏 hook 冻结 agent;文档提醒安全敏感场景需在 hook 内自校验。

---

## 7. Security

### 7.1 执行权限
Hook 命令以**当前用户身份**运行(与 `bash` 工具同权),不做额外提权/降权。风险通过信任边界 + 文档约束(PRD Non-Goals)。

### 7.2 信任边界(FR-14)
- **用户级** hooks(`$PIGO_HOME/config.json`)始终启用。
- **项目级** hooks(`./.pigo/config.json`)**仅在项目受信任时**加载;未信任项目的项目级 hooks 被忽略并提示(复用 `internal/trust`)。
- `InstallHooks` 接收 trust 判定结果,决定是否并入项目层 hooks。

### 7.3 数据保护(FR-17)
`HookInput` 仅含可观测字段;**绝不**包含 `Credentials`/API key。构造 payload 时以白名单字段序列化(与 `plugin/events.go` 同纪律)。命令 stdin 传入的是转义后的合法 JSON,避免注入到 pigo 侧(用户命令自身的注入风险由用户承担)。

---

## 8. Performance

### 8.1 预期负载
Hook 触发频率与工具调用/turn 同量级(每 turn 数次)。每次触发 fork 一个 shell 进程。

### 8.2 优化策略
- **零 hooks 短路**(FR-18):`Config.Hooks` 为空时 `InstallHooks` 不包裹任何 seam,`Dispatcher` 不构造,热路径零额外分配、零 fork。
- 某事件无匹配 matcher 时,Dispatcher 在 fork 前返回空 decision。
- 观察型 hook 经 OnEvent,现有 `EventBuffer` 决定同步/异步背压;不改变既有性能特性。

### 8.3 并发
同一事件多个 hook **顺序**执行(保证 decision 合并确定性,§5.1)。不同工具的 PreToolUse 随批量工具的并发调度并行(与现有 `BatchConfig` 并行度一致,装饰器是每调用独立的)。

---

## 9. Testing Strategy

### 9.1 Unit Tests(`internal/hooks/*_test.go`)
- config:多层追加合并、空配置、非法配置校验。
- matcher:空/`*`/精确/`|`多值/正则/无工具名事件/正则编译失败。
- runner:stdin JSON 正确性(回显脚本)、超时 kill、命令不存在、输出超限截断、exit 0/2/非0 分类。
- protocol:合法 JSON、空输出、非法 JSON、各字段解析。
- dispatch:多 hook 合并、首个 block 短路、fail-open、updatedInput 后者胜出。
- notifier:仅订阅事件被投递、nil 安全。

### 9.2 Integration Tests
- PreToolUse:对 `bash` 且含 `rm -rf` 的命令 block,断言工具未执行且模型收到 reason(`internal/agenttool` 或 cli 层)。
- PostToolUse:`write` 后追加反馈,断言反馈进入回填内容。
- UserPromptSubmit:注入文本进入发往 provider 的上下文;block 拦截提示词。
- Stop:前 N 次 block 后放行,断言续跑且在上限内结束。
- SessionStart/End、PreCompact、Notification:各断言触发一次且 payload 字段正确。
- 隔离:超时/报错 hook 不导致 crash 或 hang。

### 9.3 Edge Case Tests
覆盖 §5.4 全部条目 + §6.1 全部分类。

### 9.4 Acceptance Criteria Mapping
| US/FR | Test | Type | Description |
|-------|------|------|-------------|
| US-001/FR-1,2 | config_test | unit | Hooks 分层追加合并 |
| US-002/FR-3 | matcher_test | unit | matcher 全规则 |
| US-003/FR-4 | runner_test | unit | stdin JSON + env |
| US-004/FR-5,6 | protocol/dispatch_test | unit | 退出码 + JSON 协议 |
| US-005/FR-7 | pretooluse_test | integration | 阻断 + 改写参数 |
| US-006/FR-8 | posttooluse_test | integration | 追加反馈 |
| US-007/FR-9 | userprompt_test | integration | 注入 + 阻断 |
| US-008/FR-10 | stop_test | integration | 阻止结束 + 上限 |
| US-009 | subagentstop_test | integration | 子 agent 触发 |
| US-010 | sessionstart_test | integration | 注入初始上下文 |
| US-011 | sessionend_test | integration | natural/abort reason |
| US-012 | precompact_test | integration | manual/auto trigger |
| US-013 | notification_test | integration | 信任确认触发 |
| US-014/FR-11,13,15 | isolation_test | unit | 超时/失败/超限 |
| FR-18 | 现有全量测试 | regression | 无 hooks 行为不变 |

---

## 10. Implementation Plan

### 10.1 Phases
1. **基础设施**(无接线,可独立测试):`internal/hooks` 包 — config/matcher/protocol/runner/dispatch + 单测。(US-001~US-004, US-014)
2. **配置接线**:`ConfigLayer.Hooks` + `ResolveConfig` 合并 + `run.go` 加载 hooks 层(受信任判定)。(US-001, FR-14)
3. **单一装配 helper**:`internal/cli/run/hooks_install.go` 的 `InstallHooks`,先接 PreToolUse(链式 trust→hook)与 PostToolUse。(US-005, US-006)
4. **提示词与结束类**:UserPromptSubmit(reminder 注入)、Stop、SubagentStop、SessionStart。(US-007~US-010)
5. **观察型**:`HookNotifier` 挂 OnEvent,接 SessionEnd/PreCompact/Notification;与 `plugin.EventNotifier` 并存。(US-011~US-013)
6. **收敛全 driver**:repl/goal/btw/headless/tui/subagent 统一调用 `InstallHooks`。(FR-16 全覆盖)
7. **文档与示例**:README Hooks 章节 + 3 个示例。(US-015)

### 10.2 Issue Mapping
| Issue | SPEC Sections | US | Priority | Depends On |
|-------|--------------|----|----------|------------|
| #1 | 3.1,3.3,5.2,5.3,6 | US-002~004,014 | high | — |
| #2 | 3.2 | US-001 | high | #1 |
| #3 | 2.2,2.3,4.1,5.1 | (dispatcher+install 骨架) | high | #1,#2 |
| #4 | 4.4,5.1 | US-005,006 | high | #3 |
| #5 | 4.4 | US-007 | high | #3 |
| #6 | 4.4,5.5 | US-008,009 | med | #3 |
| #7 | 4.1,2.3 | US-010 | med | #3 |
| #8 | 2.3(notifier) | US-011~013 | med | #3 |
| #9 | 2.4 | (全 driver 收敛) | med | #4~#8 |
| #10 | 7,§示例 | US-015 | low | #4~#9 |

### 10.3 Incremental Delivery
每个 hook 点默认关闭(无配置即无行为),天然是"隐式 feature flag"。可按 Phase 逐点合并、逐点在 README 标注"可用",无需集中大爆炸上线。

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions
- Stop 上限(FR-12)/超时(FR-11)是否需暴露为全局配置项,而非仅 per-hook?(建议 v1 per-hook + 全局默认常量)
- 多 PreToolUse `updatedInput` 是否需要"严格模式"报错而非后者胜出?
- 是否 v1 就提供 `pigo hooks test <event>` 调试子命令?(建议 v1.1)
- Windows shell(`cmd /c` vs `powershell`)是否纳入 v1?(建议 v1 仅 darwin/linux,`sh -c`)

### 11.2 Technical Risks
| Risk | Impact | Mitigation |
|------|--------|-----------|
| SessionStart 若经异步 OnEvent 投递,注入可能赶不上第一个 turn | 注入失效 | 不走 OnEvent,改在 run-start seam **同步**分发;或要求该点 EventBuffer=0 |
| `BeforeToolCall` 已被 trust 占用,直接替换会破坏信任门 | 安全回退 | 用装饰器链式包裹(trust→hook),不替换 |
| RunConfig 在 7 处组装,易漏接线 | hook 在某模式失效 | 收敛到单一 `InstallHooks`,各 driver 只调用它;加"全 driver 均调用"回归测试 |
| 慢/挂起 hook 阻塞 agent | 冻结 | per-hook 超时 kill + fail-open |
| 项目级 hook 在未信任目录执行任意命令 | 安全 | 仅受信任项目加载项目层 hooks(FR-14) |
| payload 误带凭证 | 泄密 | 白名单序列化 + 单测断言无敏感字段 |

### 11.3 Assumptions
- 现有 `BeforeToolCall`/`AfterToolCall`/`ShouldStopAfterTurn` seam 语义在实现期不变。
- `ReminderRegistry` 可承载一次性(one-shot)reminder 用于 UserPromptSubmit/SessionStart 注入(需实现期确认其 provider 是否支持"仅下一 turn 生效",否则新增 one-shot provider)。
- `OnEvent` 已支持多观察者链式(plugin notifier + hook notifier 并存)——由现有 repl/headless 代码佐证。
- headless 与 REPL 两条提示词入口均可在提交前插入同步 UserPromptSubmit 分发。

