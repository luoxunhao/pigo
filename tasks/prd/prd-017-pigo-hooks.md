# PRD: pigo Hooks(用户可扩展的生命周期钩子)

## 1. Introduction/Overview

为 pigo 增加一套**用户可扩展的 Hook(钩子)系统**,借鉴 Claude Code 的 hook 思想:用户在配置文件中声明 shell 命令,pigo 在 agent 生命周期的关键节点触发这些命令,把事件数据通过 stdin 以 JSON 传给命令,并读取命令的退出码与 stdout 来决定后续行为。

pigo 目前已经有两类"内部"扩展能力:Go 层的函数钩子(`internal/agenttool/tool_executor.go` 的 `BeforeToolCall`/`AfterToolCall`、`internal/runtime/loop.go` 的 `ShouldStopAfterTurn` 等),以及一条单向的插件事件总线(`internal/plugin/events.go`,插件只能"观察"事件,不能改变行为)。二者都不能让**普通用户在不写 Go、不编译插件的前提下**注入自己的逻辑。本特性填补这个空白:让用户仅靠配置 + 一段 shell 脚本,就能在工具执行前拦截、在用户提交提示词时注入上下文、在 agent 结束前阻止退出等。

目标读者假设为初级开发者或 AI agent,因此本文尽量用平实语言并给出可验证的判定标准。

## 2. Goals

- 让用户**无需写 Go、无需编译**,仅通过配置文件中的 shell 命令即可扩展 pigo 行为。
- 尽可能多地暴露生命周期 hook 点,覆盖工具调用、提示词提交、会话起止、压缩、子 agent、通知等节点。
- Hook 不仅能**观察**事件,还能**阻断 / 改写 / 注入**(对齐 Claude Code:PreToolUse 可拒绝工具、UserPromptSubmit 可注入上下文、Stop 可阻止结束)。
- 复用现有分层 config.json,新增 `hooks` 段并支持按事件类型 + 工具名 matcher 匹配。
- Hook 失败、超时、输出异常时必须**隔离**,不能拖垮或崩溃主 agent 循环。
- 提供清晰的 hook 输入/输出协议文档与示例,为未来用户编写 hook 提供便利。

## 3. User Stories

### US-001: 在分层配置中定义 hooks 结构
**Description:** As a pigo 用户, I want 在 config.json 中声明 hooks so that 我可以把 shell 命令绑定到生命周期事件上而无需改动 pigo 源码。

**Acceptance Criteria:**
- [ ] `ConfigLayer` 新增 `Hooks map[string][]HookMatcherConfig` 字段(key 为事件类型,如 `PreToolUse`),`json:"hooks,omitempty"`
- [ ] 每个 `HookMatcherConfig` 含 `matcher`(字符串,可选,用于匹配工具名等)与 `hooks`([]HookConfig)
- [ ] 每个 `HookConfig` 含 `type`(当前固定 `"command"`)、`command`(shell 命令字符串)、`timeout`(秒,可选,默认值见 FR-11)
- [ ] 分层合并:用户级 / 项目级 / 命令行层的 hooks **按事件类型追加合并**(而非整体覆盖),合并顺序与现有 config 层级一致
- [ ] 提供 `hooks` 段缺失时的零值安全:无 hooks 时系统行为与当前完全一致
- [ ] 新增单元测试覆盖多层合并与空配置
- [ ] Typecheck/`go build ./...` 通过,`go test ./internal/runtime/...` 通过

### US-002: Hook 匹配引擎
**Description:** As a pigo 用户, I want 用 matcher 精确指定哪些事件触发哪个 hook so that 我可以只对特定工具(如 `bash`、`write`)挂钩,而不影响其它工具。

**Acceptance Criteria:**
- [ ] 实现 `MatchHooks(eventType string, toolName string) []HookConfig`,返回该事件下所有匹配的 hook
- [ ] matcher 为空或 `"*"` 时匹配该事件的所有触发
- [ ] matcher 支持精确匹配单个工具名(如 `bash`)
- [ ] matcher 支持 `|` 分隔的多工具(如 `write|edit`)与简单正则(如 `Edit.*`),匹配规则需在文档中明确
- [ ] 对不携带工具名的事件(如 `SessionStart`),matcher 被忽略,所有 hook 均触发
- [ ] 单元测试覆盖:空 matcher、精确、多值、正则、无工具名事件
- [ ] Typecheck/lint 通过

### US-003: Hook 执行器(shell 命令运行 + stdin JSON)
**Description:** As a pigo 用户, I want pigo 把事件数据以 JSON 通过 stdin 传给我的 shell 命令 so that 我的脚本能读取上下文(工具名、参数、session_id、cwd 等)做判断。

**Acceptance Criteria:**
- [ ] 新增 `internal/hooks` 包,提供 `Runner.Run(ctx, hook HookConfig, input HookInput) (HookOutput, error)`
- [ ] 命令通过系统 shell 执行(`sh -c` / 平台等价),事件 payload 作为 JSON 从 stdin 写入
- [ ] 注入环境变量:至少 `PIGO_SESSION_ID`、`PIGO_PROJECT_DIR`、`PIGO_EVENT_TYPE`
- [ ] payload 只包含**可观测、非敏感**字段(对齐现有 `events.go` 的 "never secrets" 纪律),明确不包含 API key/凭证
- [ ] 命令 cwd 默认为项目工作目录
- [ ] 单元测试:用一个把 stdin 回显到临时文件的脚本,断言 pigo 传入的 JSON 结构正确
- [ ] Typecheck/lint 通过

### US-004: Hook 决策协议(退出码 + JSON stdout)
**Description:** As a pigo 用户, I want 用退出码和 stdout JSON 告诉 pigo "放行/阻断/注入什么" so that 我的 hook 能真正影响 agent 行为。

**Acceptance Criteria:**
- [ ] 退出码 `0` = 放行;stdout(若为合法 JSON)按协议解析
- [ ] 退出码非 `0` 且非 `2` = hook 执行失败(告警但不阻断,见 US-014)
- [ ] 退出码 `2` = **阻断**,stderr 内容作为反馈信息传回 agent(对齐 Claude Code 语义)
- [ ] stdout JSON 支持字段:`decision`(`"block"` | `"approve"` | 空)、`reason`(字符串)、`additionalContext`(字符串,用于注入)、`continue`(bool)
- [ ] 解析结果映射为内部 `HookDecision` 结构,供各 hook 点消费
- [ ] 非 JSON stdout 在退出码 0 时视为无操作(仅日志)
- [ ] 单元测试覆盖:exit 0 空输出、exit 2、JSON decision=block、JSON additionalContext、非法 JSON
- [ ] Typecheck/lint 通过

### US-005: PreToolUse hook(工具执行前,可拒绝/改写参数)
**Description:** As a pigo 用户, I want 在工具执行前用 hook 拦截或修改参数 so that 我可以阻止危险命令(如 `rm -rf`)或强制规范化参数。

**Acceptance Criteria:**
- [ ] 在 `internal/agenttool/tool_executor.go` 的 `BeforeToolCall` 现有 seam 上接入 PreToolUse hook 分发
- [ ] payload 含 `tool_name`、`tool_input`(参数 JSON)、`session_id`
- [ ] hook 返回 `decision=block`(或 exit 2)时,工具**不执行**,`reason` 作为错误内容回填给模型(复用现有 `BeforeToolCallDecision.Block`)
- [ ] hook 可通过 stdout 返回改写后的工具参数(可选字段 `updatedInput`),若提供则替换原参数
- [ ] 多个匹配 hook 中任一 block 即阻断;改写按声明顺序依次应用
- [ ] 集成测试:配置一个对 `bash` 且命令含 `rm -rf` 返回 block 的 hook,断言该工具调用被拦截且模型收到 reason
- [ ] Typecheck/lint 通过,`go test ./internal/agenttool/...` 通过

### US-006: PostToolUse hook(工具执行后,可反馈/改写结果)
**Description:** As a pigo 用户, I want 在工具执行后对结果做检查并给模型反馈 so that 我可以在写文件后自动跑 lint 并把结果反馈给 agent。

**Acceptance Criteria:**
- [ ] 在 `tool_executor.go` 的 `AfterToolCall` seam 上接入 PostToolUse hook 分发
- [ ] payload 含 `tool_name`、`tool_input`、`tool_response`(结果内容 + isError)、`session_id`
- [ ] hook 可通过 `additionalContext` / `reason` 追加反馈,复用现有 `AfterToolCallResult` 回填给模型
- [ ] exit 2 / decision=block 时,把 stderr/reason 作为附加上下文注入(不撤销已执行的工具,仅提示模型)
- [ ] 集成测试:配置一个 PostToolUse hook 对 `write` 追加一段反馈,断言反馈出现在回填内容中
- [ ] Typecheck/lint 通过

### US-007: UserPromptSubmit hook(提示词提交时,可注入上下文/阻断)
**Description:** As a pigo 用户, I want 在我提交提示词时自动注入上下文或拦截 so that 我可以自动附加当前 git 分支、时间戳,或拦截含敏感词的输入。

**Acceptance Criteria:**
- [ ] 在 REPL 与 headless 两条提示词入口触发 UserPromptSubmit hook
- [ ] payload 含 `prompt`(用户原文)、`session_id`
- [ ] hook 返回 `additionalContext` 时,该文本在提示词进入 LLM 前被注入(追加到用户消息或作为 system-reminder,实现方式在 SPEC 阶段定)
- [ ] hook 返回 `decision=block`(或 exit 2)时,该提示词**不提交**,`reason` 显示给用户,REPL 回到输入态、headless 以非零码退出
- [ ] 集成测试:配置一个注入固定文本的 hook,断言注入文本出现在发往 provider 的上下文;另配置一个 block hook,断言提示词被拦截
- [ ] Typecheck/lint 通过

### US-008: Stop hook(agent 结束前,可阻止结束)
**Description:** As a pigo 用户, I want 在 agent 打算结束时用 hook 决定是否强制它继续 so that 我可以要求 agent 在未完成 checklist 前不停止。

**Acceptance Criteria:**
- [ ] 在 `internal/runtime/loop.go` 收敛到 `finish()` 之前接入 Stop hook,复用/扩展 `ShouldStopAfterTurn` 语义
- [ ] payload 含 `session_id`、`stop_reason`
- [ ] hook 返回 `decision=block`(或 exit 2)时,agent **不结束**,`reason` 作为新的引导消息注入,外层循环继续
- [ ] 防止无限循环:同一 run 内 Stop hook 连续 block 达到上限(默认见 FR-12)后强制结束并告警
- [ ] 集成测试:配置一个前 N 次 block、之后放行的 hook,断言 agent 被延续且最终在上限内结束
- [ ] Typecheck/lint 通过,`go test ./internal/runtime/...` 通过

### US-009: SubagentStop hook(子 agent 结束时)
**Description:** As a pigo 用户, I want 在子 agent 完成时触发 hook so that 我可以聚合或校验子 agent 的产出。

**Acceptance Criteria:**
- [ ] 在 `internal/runtime/subagent.go` 子 agent 结束路径触发 SubagentStop hook
- [ ] payload 含父 `session_id`、子 agent 标识、结束原因
- [ ] block 语义与 Stop 一致(在子 agent 上下文内生效),并同样有连续 block 上限保护
- [ ] 集成测试:断言子 agent 结束时 hook 被调用一次且 payload 含子 agent 标识
- [ ] Typecheck/lint 通过

### US-010: SessionStart hook(会话/run 开始时)
**Description:** As a pigo 用户, I want 在会话开始时触发 hook so that 我可以加载额外上下文(如从外部系统拉取当前任务)。

**Acceptance Criteria:**
- [ ] 在 run 启动(`agent_start` 对应路径)触发 SessionStart hook,区分 `startup`/`resume` 来源(payload 字段 `source`)
- [ ] payload 含 `session_id`、`source`、`project_dir`
- [ ] hook 返回 `additionalContext` 时注入到初始上下文中
- [ ] 集成测试:断言 SessionStart hook 在 run 开始时被调用且注入文本进入初始上下文
- [ ] Typecheck/lint 通过

### US-011: SessionEnd hook(会话/run 结束时)
**Description:** As a pigo 用户, I want 在会话结束时触发 hook so that 我可以做清理、归档会话或上报统计。

**Acceptance Criteria:**
- [ ] 在 `finish()` 唯一出口(所有终止路径汇合处)触发 SessionEnd hook
- [ ] payload 含 `session_id`、`reason`(natural/error/aborted 等)
- [ ] SessionEnd 为**观察型**:不解析 block(结束不可再被阻止),仅告警级处理执行失败
- [ ] 集成测试:分别在自然结束与 abort 路径断言 hook 被调用且 `reason` 正确
- [ ] Typecheck/lint 通过

### US-012: PreCompact hook(上下文压缩前)
**Description:** As a pigo 用户, I want 在上下文压缩前触发 hook so that 我可以在信息被摘要前先做备份或自定义保留策略。

**Acceptance Criteria:**
- [ ] 在 `internal/compaction` 触发压缩前接入 PreCompact hook,区分 `manual`(/compact)与 `auto`(达到阈值)来源
- [ ] payload 含 `session_id`、`trigger`(manual/auto)
- [ ] 为观察型(不阻断压缩);执行失败仅告警
- [ ] 集成测试:手动 /compact 与自动压缩路径各断言 hook 被调用且 `trigger` 正确
- [ ] Typecheck/lint 通过,`go test ./internal/compaction/...` 通过

### US-013: Notification hook(需要用户关注/授权时)
**Description:** As a pigo 用户, I want 在 pigo 需要我关注(如信任闸门要求确认副作用工具)时触发 hook so that 我可以把通知转发到桌面/IM。

**Acceptance Criteria:**
- [ ] 在信任闸门(`internal/trust`)要求确认副作用工具、或长时间等待输入时触发 Notification hook
- [ ] payload 含 `session_id`、`message`(通知文案)
- [ ] 为观察型;执行失败仅告警
- [ ] 集成测试:在未信任目录触发 bash/write 确认时,断言 Notification hook 被调用
- [ ] Typecheck/lint 通过

### US-014: Hook 执行隔离(超时 / 失败 / 输出上限)
**Description:** As a pigo 用户, I want hook 出错时不影响主流程 so that 一个写坏的 hook 不会让整个 agent 崩溃或卡死。

**Acceptance Criteria:**
- [ ] 每个 hook 命令受 `timeout` 约束(默认见 FR-11),超时被 kill 并记为失败
- [ ] hook 进程退出码非 0/2、无法启动、超时,均记为"执行失败":对**观察型 hook** 仅告警;对**可阻断 hook** 默认按"放行"处理(fail-open),并告警
- [ ] stdout/stderr 读取有大小上限(默认见 FR-13),超限截断并告警
- [ ] 告警通过现有 warn 日志通道输出(参考 `EventNotifier.warnLog`),不打断 agent
- [ ] 单元测试覆盖:超时、命令不存在、超大输出、exit 非 0/2
- [ ] Typecheck/lint 通过

### US-015: 文档、示例 hook 与 README 章节
**Description:** As a pigo 用户, I want 清晰的文档和可复制的示例 so that 我能快速写出第一个 hook。

**Acceptance Criteria:**
- [ ] README 新增"Hooks"章节:所有 hook 点列表、输入 JSON schema、输出协议(退出码/JSON 字段)、matcher 规则、配置示例
- [ ] 提供至少 3 个可运行示例:PreToolUse 拦截 `rm -rf`、UserPromptSubmit 注入 git 分支、PostToolUse 写文件后跑格式化
- [ ] 文档明确安全须知:hook 命令以用户身份执行、payload 不含凭证、仅在受信任项目中启用项目级 hooks(见 FR-14)
- [ ] 文档与实际字段/行为一致(交叉核对 US-001~US-014)

## 4. Functional Requirements

- FR-1: 系统必须在 `ConfigLayer` 中支持 `hooks` 段,key 为事件类型,value 为 matcher + 命令列表。
- FR-2: 系统必须按现有 config 分层顺序**追加合并** hooks(不同层同一事件的 hook 累加,而非覆盖)。
- FR-3: 系统必须支持 matcher 匹配:空/`*` 匹配全部,精确工具名,`|` 多值,简单正则。
- FR-4: 系统必须在触发 hook 时,将事件 payload 以 JSON 从 stdin 传入命令,并注入 `PIGO_SESSION_ID`/`PIGO_PROJECT_DIR`/`PIGO_EVENT_TYPE` 环境变量。
- FR-5: 系统必须以退出码 `0`=放行、`2`=阻断(stderr 作为反馈)、其它非 0=失败的语义解释 hook 结果。
- FR-6: 系统必须解析 hook 的 stdout JSON,支持 `decision`/`reason`/`additionalContext`/`continue`/`updatedInput` 字段。
- FR-7: 系统必须在 PreToolUse 支持阻断工具执行与改写工具参数。
- FR-8: 系统必须在 PostToolUse 支持向模型追加反馈上下文。
- FR-9: 系统必须在 UserPromptSubmit 支持注入上下文与阻断提交。
- FR-10: 系统必须在 Stop / SubagentStop 支持阻止结束,并设置连续阻断上限以防死循环。
- FR-11: 系统必须为每个 hook 提供默认超时(建议默认 **60 秒**,可在 HookConfig 覆盖)。
- FR-12: 系统必须为 Stop/SubagentStop 的连续 block 设默认上限(建议默认 **5 次**)。
- FR-13: 系统必须对 hook stdout/stderr 读取设大小上限(建议默认 **1 MB**),超限截断。
- FR-14: 系统必须仅在**受信任项目**中启用项目级(`.pigo`)hooks;用户级 hooks 始终启用。
- FR-15: 系统必须在任一 hook 执行失败/超时时隔离错误、记录告警,且对可阻断 hook 默认 fail-open。
- FR-16: 系统必须支持以下 hook 点:PreToolUse、PostToolUse、UserPromptSubmit、Stop、SubagentStop、SessionStart、SessionEnd、PreCompact、Notification。
- FR-17: 系统必须保证 payload 不包含 API key、凭证等敏感字段。
- FR-18: 系统在无任何 hooks 配置时,行为必须与引入本特性前完全一致(零开销、零副作用)。

## 5. Non-Goals (Out of Scope)

- 不实现内置 GUI / 交互式 hook 管理器(仅配置文件驱动)。
- v1 不提供除 `command`(shell)之外的 hook 类型(如内嵌脚本引擎、WASM)。
- 不替换或废弃现有 plugin 事件总线;二者并存,plugin 继续做"观察型"订阅。
- 不做 hook 的沙箱/权限隔离(hook 以当前用户身份运行,通过信任边界 + 文档约束风险)。
- 不实现 hook 的远程分发/市场(可在包管理中后续考虑)。
- 不实现 PreToolUse 对已并行执行工具的"回滚"(仅拦截尚未执行者)。
- 不实现跨 run 的 hook 状态持久化(hook 自行管理状态)。

## 6. Design Considerations

- 复用现有 seam,尽量少改核心循环:
  - PreToolUse → `internal/agenttool/tool_executor.go` `BeforeToolCall` / `BeforeToolCallDecision`
  - PostToolUse → 同文件 `AfterToolCall` / `AfterToolCallResult`
  - Stop → `internal/runtime/loop.go` `ShouldStopAfterTurn` 与唯一出口 `finish()`
  - SessionEnd → `finish()`
- payload 构造复用 `internal/plugin/events.go` 的 "只暴露可观测字段" 纪律。
- 告警输出复用 `EventNotifier.warnLog` 的通道风格。
- matcher/协议设计尽量对齐 Claude Code,降低用户迁移与学习成本。

## 7. Technical Considerations

- 新增独立包 `internal/hooks`(config 类型、matcher、runner、协议解析),避免与 `internal/plugin` 耦合。
- Hook 分发点需要能访问 session_id、project_dir、trust 状态;注意 headless 与 REPL 两条路径都要接线。
- shell 执行需跨平台(darwin/linux;Windows 若支持则用对应 shell),命令注入风险由"用户自写命令"承担,但 pigo 传入 stdin 的 payload 必须是合法转义的 JSON。
- 性能:无 hooks 时必须短路(nil 检查),不得为每次工具调用引入额外分配或进程创建。
- 并发:同一事件多个 hook 的执行顺序需确定(按配置层级 + 声明顺序),block 采用短路或全跑后合并需在 SPEC 明确。

## 8. Success Metrics

- 用户可仅通过编辑 config.json + 一段 shell 脚本,成功拦截一次危险工具调用(端到端示例可复现)。
- 无 hooks 配置时,现有测试全部通过且无可测得的性能回退。
- 覆盖 FR-16 列出的全部 9 个 hook 点,每个点至少一个通过的集成测试。
- 一个故意超时/报错的 hook 不会导致 agent 崩溃或挂起(隔离测试通过)。

## 9. Open Questions

- Stop hook 的 block 上限(FR-12)与超时默认值(FR-11)是否需要暴露为全局配置项,还是仅 per-hook?
- UserPromptSubmit 注入的上下文应作为独立 user 消息、追加到原提示词,还是 system-reminder?(建议在 `/prd-to-spec` 阶段定)
- 是否需要在 v1 提供 `pigo hooks list` / `pigo hooks test <event>` 之类的调试子命令?(建议 v1 可选,v1.1 补齐)
- 多个 PreToolUse hook 都返回 `updatedInput` 时的合并/覆盖策略?
- Windows 平台是否纳入 v1 支持范围?
