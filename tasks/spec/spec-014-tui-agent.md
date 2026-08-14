# SPEC: TUI 形式 Agent 交互界面

> Technical specification derived from: `tasks/prd/prd-032-tui-agent.md`
> Generated: 2026-07-28 | Target branch: master | Commit: bdc9071

## 1. Summary

### 1.1 What This SPEC Covers
本 SPEC 描述如何为 pigo 新增一个基于 **Bubble Tea v2** 的全屏 TUI 交互界面，作为 TTY 下的默认交互模式，并保留行式 REPL 作为 `--no-tui` 与非 TTY 的 fallback。核心策略是**复用现有 agent 内核**：TUI 不重写运行循环，而是复用 `runtime.StartRun` + `runtime.DrainStream` 这一既有运行/流式消费 seam，通过一个把 `agentcore.AgentEvent` 桥接为 `tea.Msg` 的适配器驱动 UI。范围包括：入口接线、主题与宽度感知渲染、状态栏、事件桥、流式 transcript、富工具卡片、CJK 输入框、斜杠命令复用、会话恢复。

### 1.2 PRD Reference
- Source: `tasks/prd/prd-032-tui-agent.md`
- User Stories covered: US-001 ~ US-009（全部）
- Functional Requirements covered: FR-1 ~ FR-17（全部）

### 1.3 Design Decisions Summary
| Decision | Choice | Rationale |
|----------|--------|-----------|
| TUI 框架 | 上游 `github.com/charmbracelet/bubbletea/v2` + `bubbles/v2` + `lipgloss` v2 | 用户选定 v2；需将现有 lipgloss v1.1.x 同步升级到 v2 |
| 运行 seam | 复用 `runtime.StartRun`/`DrainStream`，自定义 `StreamHandler` 桥接 | 已是 REPL/headless/subagent 共享的唯一 drain 点，零内核改动 |
| 事件→UI 投递 | goroutine 内 drain，经缓冲 channel + `tea.Cmd` 投递 `tea.Msg` | bubbletea 单 goroutine 模型，回调不能直接改 Model |
| 工具卡片状态 | 用 `OnEvent` 消费 `ToolExecutionStart/Update/End` 驱动 running→✓/! | `DrainStream` 的 `OnTurnEnd` 只在回合末给结果，拿不到实时进行态 |
| 状态栏花费 | 显示 token 用量（`ContextTokens`/`ContextWindow`/`ContextUtilization`），不做 $ | 现有代码无 cost 数据源，token 统计已存在于 `TelemetryEvent` |
| git 段 | 异步 shell out `git`（`rev-parse`/`status --porcelain`） | 现无 git 数据源；shell out 零新依赖 |
| 斜杠命令复用 | 将 `buildSlashRegistry`/`loadPromptPaths`/`promptTemplateSources` 下沉到共享包 `internal/cli/prompts` | 现为 repl 包私有；repl 与 tui 共用避免重复 |
| CJK 输入 | 用 `bubbles/v2/textarea` 组件 | 天然按 rune 处理多字节，规避旧 TUI `len==1` 丢字 bug |
| 宽度渲染 | 统一走 `lipgloss.Width`（底层 go-runewidth） | 规避旧 TUI 双宽错位缺陷 |

---

## 2. Architecture

### 2.1 System Context
```
cmd/pigo/main.go (dispatch)
  │  无 -p + TTY + 未 --no-tui
  ▼
internal/cli/tui.Run(tui.Options)         ← 新增，镜像 repl.Run 的装配
  │  组装 session/live/slash/trust（复用 run.SetupEnv、prompts 共享包）
  ▼
tea.NewProgram(rootModel).Run()           ← Bubble Tea 主循环（单 goroutine）
  │  用户提交 prompt
  ▼
runBridge：goroutine 内
  runtime.StartRun(ctx, agentCtx, cfg) → *LoopEventStream
  runtime.DrainStream(ctx, stream, StreamHandler{OnEvent/OnText/OnTurnEnd})
  │  每个回调 → 组装 tea.Msg → 送入 bufChan
  ▼
rootModel.Update(tea.Msg)  ← 由 waitForEvent tea.Cmd 从 bufChan 读出
  更新 transcript / toolCards / statusBar / telemetry
```

行式 REPL 路径（`--no-tui`/非 TTY）保持不变，仍走 `repl.Run`。

### 2.2 Component Design
新增包 `internal/cli/tui`，Elm 架构，根 `Model` 组合以下子组件（各自封装状态与 `View`，`Update` 由根分发）：

- **rootModel**（`model.go`）：持有 `Host`/session 装配、运行状态机（idle/running/interrupting）、子组件与事件桥。
- **statusBar**（`statusbar.go`）：渲染模型/thinking/cwd/git/token 用量/当前任务。
- **transcript**（`transcript.go`）：`bubbles/v2/viewport` 承载消息流（user/assistant/system + 工具卡片），管理自动贴底。
- **toolCard**（`toolcard.go`）：单次工具调用的卡片渲染（header 状态图标、输入参数、树状 Response、Ctrl+O 展开）。
- **input**（`input.go`）：`bubbles/v2/textarea` 输入框 + `/` 补全弹层。
- **theme**（`theme.go`）：集中 `lipgloss.Style` 与宽度/换行辅助。
- **bridge**（`bridge.go`）：`StreamHandler` → `tea.Msg` 适配 + `waitForEvent` Cmd。
- **git**（`gitinfo.go`）：异步 `git` 探测 Cmd。

共享改造包 `internal/cli/prompts`（新增）：承接从 repl 下沉的 `BuildSlashRegistry`/`LoadPromptPaths`/`PromptTemplateSources`（导出）。

### 2.3 Module Interactions
运行一轮的时序（提交 prompt 后）：
1. `input` 提交 → rootModel 置 `running`，禁用输入，将 user 消息加入 transcript。
2. rootModel 返回 `startRunCmd`：在新 goroutine 内 `StartRun`+`DrainStream`；`StreamHandler` 各回调把事件封成 `tea.Msg` 写入 `bufChan`（带缓冲，满时 drain 侧阻塞即天然背压，不丢事件）。
3. rootModel 的 `waitForEvent` Cmd 从 `bufChan` 读一条 → 交给 `Update` → 处理后再次调度 `waitForEvent`（经典 bubbletea channel 泵模式）。
4. `runEndMsg`（源自 `AgentEnd`/drain 返回）到达 → rootModel 回 `idle`，重启输入，持久化 session（复用 repl 既有保存逻辑）。

### 2.4 File Structure
```
cmd/pigo/
└── main.go                         [MODIFY: dispatch 增加 TUI 分支 + --no-tui flag]

internal/cli/tui/                    [NEW 包]
├── doc.go
├── run.go                          [NEW: Run(Options) 装配 + tea.NewProgram]
├── options.go                      [NEW: Options（镜像 repl.Options 字段）]
├── model.go                        [NEW: rootModel + Update 分发 + 运行状态机]
├── statusbar.go                    [NEW: US-003]
├── transcript.go                   [NEW: US-005 viewport]
├── toolcard.go                     [NEW: US-006]
├── input.go                        [NEW: US-007 textarea + 补全]
├── theme.go                        [NEW: US-002 样式 + 宽度辅助]
├── bridge.go                       [NEW: US-004 StreamHandler→tea.Msg]
├── gitinfo.go                      [NEW: US-003 git 异步探测]
├── msgs.go                         [NEW: tea.Msg 类型定义]
└── *_test.go                       [NEW: 各组件单测]

internal/cli/prompts/                [NEW 包，下沉自 repl]
├── registry.go                     [NEW: BuildSlashRegistry/LoadPromptPaths/PromptTemplateSources 导出]
└── registry_test.go                [MOVE: repl 的 prompts_*_test.go 相关用例]

internal/cli/repl/
├── interactive.go                  [MODIFY: 改调用 prompts.BuildSlashRegistry]
└── prompts_*_test.go               [MODIFY/MOVE: 指向共享包]

go.mod / go.sum                      [MODIFY: +bubbletea/v2 +bubbles/v2; lipgloss v1→v2]
```

---

## 3. Data Model

### 3.1 Schema Changes
无持久化 schema 变更。会话仍用现有 `session.Store`（`~/.pigo/sessions`）。

### 3.2 Entity Definitions
```go
// internal/cli/tui/options.go —— 与 repl.Options 同字段（可后续抽公共结构）
type Options struct {
    Model, ProviderName, BaseURL, APIKey, Protocol string
    Provider      provider.Provider
    ThinkingLevel agentcore.ThinkingLevel
    Tools         []agentcore.AgentTool
    SysPrompt     string
    ResumeID      string
    Approve       bool
    Skills        []*runtime.Skill
    Plugins       *plugin.Manager
    ConfigPrompts, CliPrompts []string
    NoPromptTemplates bool
}

// internal/cli/tui/msgs.go —— 事件桥投递的消息
type textDeltaMsg struct{ delta string }                         // 源自 OnText
type turnEndMsg struct {                                         // 源自 OnTurnEnd
    msg     agentcore.AssistantMessage
    results []agentcore.ToolResultMessage
}
type toolStartMsg struct{ id, name string; input map[string]any } // 源自 ToolExecutionStartEvent
type toolUpdateMsg struct{ id string; partial string }            // ToolExecutionUpdateEvent
type toolEndMsg    struct{ id string; ok bool; result string }    // ToolExecutionEndEvent
type telemetryMsg  struct{ ev agentcore.TelemetryEvent }          // TelemetryEvent
type compactionMsg struct{}                                       // CompactionEvent
type runEndMsg     struct{ err error }                            // drain 返回
type gitInfoMsg    struct{ branch string; ahead, dirty int; ok bool }

// internal/cli/tui/toolcard.go
type toolCard struct {
    id, name string
    input    map[string]any
    response []respNode   // 树状 Response
    state    cardState    // running | success | warn
    expanded bool         // Ctrl+O
}
type cardState int
const ( cardRunning cardState = iota; cardSuccess; cardWarn )
type respNode struct { text string; depth int; kind nodeKind } // 缩进 + 着色
```

### 3.3 Relationships
`rootModel` 持有 `map[string]*toolCard`（按工具调用 id 索引），transcript 按到达顺序保存渲染块（文本块或卡片引用）。`statusBar` 从 `LiveConfig` + `TelemetryHolder` + `gitInfoMsg` 读快照。

### 3.4 Migration Plan
- **prompts 下沉**：`git mv` 语义——把 repl 私有的 `buildSlashRegistry`/`loadPromptPaths`/`promptTemplateSources` 复制到 `internal/cli/prompts` 并导出为 `BuildSlashRegistry`/`LoadPromptPaths`/`PromptTemplateSources`，repl 改为调用共享包，删除私有副本，迁移对应测试。向后兼容：REPL 行为不变（纯重构 + 重命名）。
- **lipgloss v1→v2**：升级依赖，修正 API 差异（`lipgloss.Style` v2 的 renderer/color API 变更），现有使用 lipgloss 的 `internal/cli/ui`、`status` 需一并适配并回归测试。回滚：单独一个 PR 承载依赖升级，出问题可 revert。

---

## 4. API Design

> 本特性为终端交互，无网络 API。此处以「包级函数契约」代替 HTTP 端点。

### 4.1 Endpoints（包级入口）
| 函数 | 签名 | 说明 |
|------|------|------|
| `tui.Run` | `func Run(opts Options) error` | 装配并启动 TUI，阻塞至退出 |
| `prompts.BuildSlashRegistry` | `func BuildSlashRegistry(live *cli.LiveConfig, skills []*runtime.Skill, mgr *plugin.Manager, srcs PromptTemplateSources) (*runtime.SlashRegistry, error)` | 下沉的注册表构建 |
| `prompts.LoadPromptPaths` | `func LoadPromptPaths(paths []string) []runtime.SlashCommand` | 下沉 |
| `dispatch`（改） | 现有签名不变 | 增加 TUI 分支判断 |

### 4.2 Request/Response Schemas
入口判定（`cmd/pigo/main.go` dispatch，无 prompt 分支）：
```
if opts.prompt == "" {
    useTUI := resumeID != "" || ui.StdoutIsTerminal()
    useTUI = useTUI && !opts.noTUI && ui.StdoutIsTerminal()
    if !ui.StdoutIsTerminal() && resumeID == "" {  // 现有：管道无 prompt → 退出码 2
        return 2
    }
    if useTUI { return tui.Run(tui.Options{...}) 的退出码映射 }
    return repl.Run(repl.Options{...}) 的退出码映射   // --no-tui 或非 TTY
}
```
新增 flag：`flag.BoolVar(&opts.noTUI, "no-tui", false, "使用行式 REPL 而非全屏 TUI")`。

### 4.3 Error Responses
| 失败 | 退出码 | 处理 |
|------|--------|------|
| TUI 初始化失败（非 TTY 误入等） | 1 | fallback 到 `repl.Run` 或打印错误 |
| 运行内错误（provider/tool） | 0（交互态） | 渲染进 transcript，不退出程序 |
| `Run` 返回 error | 1 | dispatch 打印 `pigo: %v` 到 errOut |

### 4.4 Breaking Changes
- **默认行为变更**：TTY 下 `pigo`（无 `-p`）从「行式 REPL」变为「TUI」。这是用户批准的默认迁移；`--no-tui` 提供回退。
- **lipgloss v2 升级**：内部依赖破坏性升级，影响 `ui`/`status`，无对外 API 变更。

---

## 5. Business Logic

### 5.1 Core Algorithms

**事件桥泵（bridge.go，US-004）**
```
startRunCmd(ctx, deps, prompt) tea.Cmd:
  go func:
    stream := runtime.StartRun(ctx, deps.agentCtx, buildRunConfig(deps))
    _, err := runtime.DrainStream(ctx, stream, StreamHandler{
      OnEvent: ev → switch ev.(type):
                 ToolExecutionStartEvent  → bufChan <- toolStartMsg
                 ToolExecutionUpdateEvent → bufChan <- toolUpdateMsg
                 ToolExecutionEndEvent    → bufChan <- toolEndMsg
                 TelemetryEvent           → holder.Fold(ev); bufChan <- telemetryMsg
                 CompactionEvent          → bufChan <- compactionMsg
      OnText:    delta → bufChan <- textDeltaMsg{delta}
      OnTurnEnd: (msg,res) → bufChan <- turnEndMsg{msg,res}
    })
    bufChan <- runEndMsg{err}
  return waitForEvent(bufChan)      // 首个泵

waitForEvent(ch) tea.Cmd = func() tea.Msg { return <-ch }
// rootModel.Update 收到任意桥消息后，除 runEndMsg 外都再次 return waitForEvent(ch)
```
背压：`bufChan` 带小缓冲（如 64）；满时 drain goroutine 在 `Emit`/回调处自然阻塞，事件不丢（复用 `DrainStream` 的 no-leak 契约）。

**流式文本累加（transcript.go，US-005）**：`textDeltaMsg` 追加到「当前 assistant 块」builder；`turnEndMsg` 定稿该块（可选 `ui.RenderMarkdown`）。viewport 内容超高时可滚；`AtBottom()` 为真时新内容自动 `GotoBottom()`，用户上滚后暂停。

**工具卡片状态机（toolcard.go，US-006）**：`toolStartMsg`→建卡 `cardRunning`；`toolUpdateMsg`→更新进行态；`toolEndMsg`→据 `ok` 切 `cardSuccess`/`cardWarn` 并解析 `result` 为 `[]respNode`（按行 + 缩进层级）。`Ctrl+O` 翻转最近卡片 `expanded`。

**状态栏（statusbar.go，US-003）**：token 段 = `fmt.Sprintf("%s/%s", humanTokens(holder.LatestContextTokens()), humanTokens(live.ContextWindow))` 或 `%.1f%%`（`ContextUtilization*100`）；git 段来自最近 `gitInfoMsg`。宽度不足时按优先级（task > model > token > git > cwd）截断。

**git 探测（gitinfo.go）**：`fetchGitCmd(cwd) tea.Cmd` 异步跑 `git rev-parse --abbrev-ref HEAD` 与 `git status --porcelain`，解析分支与 dirty/ahead 计数，返回 `gitInfoMsg`。启动时触发一次；每次 `runEndMsg` 后再触发一次（工具可能改动工作树）。

### 5.2 Validation Rules
- `--no-tui` 与非 TTY 二者任一为真 → 绝不进入 TUI。
- 运行中（`running`）提交被拒绝或排队（MVP：拒绝并提示忙碌）。
- `--list-sessions` 保持打印即退出，绝不进入 TUI（FR-17）。

### 5.3 State Machine
rootModel 运行态：`idle → running`（提交）`→ interrupting`（首个 Esc/Ctrl+C，cancel ctx）`→ idle`（`runEndMsg`）。空闲态下 Esc/Ctrl+C 或连续两次 → `tea.Quit`。

### 5.4 Edge Cases
- 窗口过窄/过矮：viewport 高度按 `WindowSizeMsg` 减去状态栏+输入框行数重算；状态栏截断而非换行。
- 非 git 目录：`gitInfoMsg.ok=false` → 隐藏 git 段。
- 运行中窗口 resize：仅重排布局，不打断 drain。
- provider 未产生 `TelemetryEvent`：token 段显示 `-` 或隐藏。
- 程序 panic：`defer` 恢复终端（bubbletea `tea.WithAltScreen` 退出时自动复原；额外 recover 打印堆栈到 stderr）。
- CJK/emoji：输入交给 textarea，渲染一律 `lipgloss.Width`。

---

## 6. Error Handling

### 6.1 Error Taxonomy
| 场景 | 退出码/呈现 | 处理 |
|------|------|------|
| TUI 启动失败 | exit 1 | dispatch 打印错误 |
| 单轮运行 error | transcript 内红字，程序续行 | drain 返回 err → `runEndMsg{err}` 渲染 |
| ctx 取消（中断） | transcript 内 `^C interrupted` | 与 REPL 行为一致 |
| git 探测失败 | 静默隐藏 git 段 | `ok=false` |

### 6.2 Retry Strategy
无自动重试（交互式，用户可重发）。provider 层既有重试不变。

### 6.3 Failure Modes
trust store / slash 加载失败 → 与 REPL 相同的「非致命、降级继续」策略（打印到 stderr 前需注意 TUI 已占用屏幕：改为渲染进 transcript 的 system 消息，避免污染 alt-screen）。

---

## 7. Security
### 7.1 Authentication & Authorization
沿用现有 trust 机制：`--approve` 或首启信任提示。**注意**：旧 REPL 的信任提示走 `os.Stdout`+`reader`，TUI 下需在进入 alt-screen 前完成，或改为 TUI 内的确认弹层（MVP：进入 alt-screen 前先在普通终端完成 `EstablishTrust`）。
### 7.2 Input Validation
git 探测只读、参数固定（无用户插值），无注入面。prompt 内容原样交内核。
### 7.3 Data Protection
无新增敏感数据落盘；session 持久化沿用现有路径与权限。

---

## 8. Performance
### 8.1 Expected Load
单用户交互，事件速率受 provider 流式 token 速率限制（通常 <200 msg/s）。
### 8.2 Optimization Strategy
- `bufChan` 缓冲 + 单泵，避免每事件一次 goroutine。
- transcript 用 viewport 惰性渲染可视区；旧工具卡片可折叠为摘要（见 Open Questions）以控内存。
- git 探测异步，不阻塞 UI。
### 8.3 Database Considerations
不适用（无 DB）。session 文件读写沿用现有。

---

## 9. Testing Strategy

### 9.1 Unit Tests
`Model.Update` 为纯转换：构造 `tea.Msg` 序列 → 断言状态/`View()` 子串。无需真实 TTY。
- bridge：伪 `StreamHandler` 回调序列 → 断言 `bufChan` 产出的 msg 顺序与类型。
- toolcard：给定 start/update/end → 断言 state 与 response 节点。
- statusbar：给定字段 + 宽度 → 断言 `View` 含字段且宽度不超限。
- theme：CJK/emoji 换行/截断宽度断言。
- input：注入含中文按键 → 断言缓冲区与光标（rune 级）。

### 9.2 Integration Tests
- prompts 共享包：`BuildSlashRegistry` 迁移用例全绿（沿用 repl 现有断言）。
- dispatch 分支：`--no-tui`/非 TTY 走 repl；退出码与迁移前一致（复用 `main_test.go` 的 dispatch 表驱动）。

### 9.3 Edge Case Tests
窄窗口截断、非 git 目录隐藏、运行中 resize、无 telemetry 时 token 段、中断 `^C` 渲染。

### 9.4 Acceptance Criteria Mapping
| US/FR | Test | Type | Description |
|-------|------|------|-------------|
| US-001/FR-1..3 | `TestDispatchTUIGating` | unit | TTY+无 --no-tui→TUI；否则 repl；退出码不变 |
| US-002/FR-12 | `TestWrapCJKWidth` | unit | 中文/emoji 按显示宽度换行不错位 |
| US-003/FR-9 | `TestStatusBarRender` | unit | 含各字段且宽度不超限；无 git 隐藏 |
| US-004/FR-4 | `TestBridgeMsgOrder` | unit | 事件序列→tea.Msg 有序映射 |
| US-005/FR-5,10 | `TestTranscriptStreaming` | unit | 多 delta 累加正确；贴底/暂停 |
| US-006/FR-6,7,8 | `TestToolCardStates`/`TestCardExpand` | unit | running→✓/!；Ctrl+O 展开 |
| US-007/FR-11,13,14 | `TestTextareaCJK`/`TestTwoStageInterrupt` | unit | 中文输入；两段式中断 |
| US-008/FR-15 | `TestSlashCompletion` | unit | `/` 前缀候选；内置命令执行 |
| US-009/FR-16,17 | `TestResumeRendersHistory`/`TestListSessionsNoTUI` | unit | 恢复渲染历史；list-sessions 不进 TUI |
| 全 UI | 终端手动验证 | manual | 真实运行观感对标截图（无浏览器，改终端验证） |

---

## 10. Implementation Plan

### 10.1 Phases
1. **依赖与重构地基**：lipgloss v1→v2 升级（含 ui/status 适配）；prompts 下沉共享包。（无 UI，先绿）
2. **骨架与接线**：tui 包 + `Run` + dispatch 分支 + `--no-tui`（空壳可启停）。
3. **地基组件**：theme/宽度辅助、statusBar（token 段先接 telemetry，git 段接 shell）。
4. **事件驱动核心**：bridge + transcript 流式渲染。
5. **富呈现**：toolCard（含 Ctrl+O）。
6. **输入与命令**：textarea + CJK + 两段式中断 + `/` 补全（接 prompts 共享包）。
7. **会话对齐**：resume/continue 渲染历史 + 退出持久化。

### 10.2 Issue Mapping
| Issue | US | SPEC Sections | Priority | Depends On |
|-------|----|--------------|----------|------------|
| A | 依赖/重构 | 3.4, 2.4(prompts) | high | — |
| B | US-001 | 2.1, 4.2, 5.2 | high | A |
| C | US-002 | 2.2(theme), 5.1 | high | B |
| D | US-003 | 5.1(status/git), 3.2 | high | C |
| E | US-004 | 5.1(bridge), 3.2 | high | B |
| F | US-005 | 5.1(transcript) | high | E |
| G | US-006 | 5.1(toolcard) | high | E,F |
| H | US-007 | 5.1(input), 5.3 | high | F |
| I | US-008 | 4.1(prompts), 5.1 | med | A,H |
| J | US-009 | 5.1(resume) | med | F |

### 10.3 Incremental Delivery
每个 Issue 一个聚焦 PR（与本项目既有节奏一致）。TUI 在 Phase 1-2 期间不成为默认（可先隐藏在 `--tui` 显式 flag 后，最后一个 PR 再翻转默认 + 改名 `--no-tui`），从而中途不影响现有用户。

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions
- 多行换行键位：`Shift+Enter` 多数终端不可区分，实现时定为 `Alt+Enter` 或 `Ctrl+J`（PRD Open Question）——需在 US-007 实现时定稿并记入 note。
- assistant 文本是否首版即用 glamour 渲染 Markdown，还是纯文本先行？（倾向纯文本 + 后续增强，避免 v2 下 glamour 兼容风险）
- 工具卡片历史是否折叠旧卡片仅留摘要，以控长会话内存？
- 是否恢复旧 TUI 的 `/model` 弹窗 picker？（MVP 仅命令式 `/model <name>`）
- 信任提示在 TUI 下：进入 alt-screen 前先普通终端确认（MVP 方案）是否可接受，还是需 TUI 内弹层？

### 11.2 Technical Risks
| Risk | Impact | Mitigation |
|------|--------|-----------|
| lipgloss v1→v2 破坏 `ui`/`status` 渲染 | 中：现有输出回归 | 独立 PR（Phase 1），完整跑 ui/status 测试 + 手动 diff |
| bubbletea v2 API 与文档/示例（多为 v1）不一致 | 中：开发摩擦 | 先做最小 spike 验证 textarea/viewport v2 用法 |
| 事件桥背压/顺序错乱导致 UI 状态漂移 | 中：卡片状态错 | 单泵 + 有序 channel + `TestBridgeMsgOrder` |
| CJK 宽度在不同终端不一致 | 低-中：对齐偏差 | 统一 `lipgloss.Width`；关键路径单测 |
| 默认行为变更影响脚本用户 | 中 | 非 TTY 自动 fallback + `--no-tui`；分阶段翻转默认 |

### 11.3 Assumptions
- `runtime.StartRun`/`DrainStream` 的 `StreamHandler`（`OnEvent`/`OnText`/`OnTurnEnd`）足以驱动全部 UI；`OnEvent` 能拿到 `ToolExecutionStart/Update/End`（已在 `agentcore` 事件类型中确认）。
- `TelemetryEvent` 的 `ContextTokens`/`ContextWindow`/`ContextUtilization` 在运行中会被 provider/loop 填充（现 `/status` 依赖同源）。
- `session.Store.LoadEntries` 足以还原 resume 的消息历史（repl 已用同一路径）。
- 现有 lipgloss 使用点仅在 `internal/cli/ui`、`status`（升级面可控）。
