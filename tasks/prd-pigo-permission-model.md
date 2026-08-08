# PRD: pigo 三级权限模型（Permission Model）

> 状态：draft，待用户 review 确认
> 来源：grilling 设计树结论（2026-08-07）

## 1. Introduction/Overview

pigo 目前的安全交互由三块拼成：目录 trust（`trust.json` 三态 + `/trust`）、
`--approve` 免确认开关、ACP `session/request_permission` 四选项审批。它们能回答
"这个目录信不信"和"这次调用放不放行"，但没有一个用户可理解、可切换的权限等级模型。

本 PRD 增加三个显式权限等级：

- `ask`（请求批准）：副作用与网络工具逐次请求批准。
- `auto_approve_edits`（替我审批）：文件写入自动放行，命令与网络仍请求批准。
- `full_access`（完全访问权限）：全部放行，不请求批准。

等级是进程级全局状态，启动时由 CLI / config.toml 决定，运行时可从 TUI、REPL 或
ACP 客户端切换并广播给所有会话。未显式设置等级时，pigo 完全保持今天的行为：
目录 trust 仍然生效，首次进入目录的信任提示与逐次确认原样保留。

## 2. Goals

- 提供三档显式权限等级：`ask` / `auto_approve_edits` / `full_access`。
- 三档在 TUI、REPL、ACP、headless 四条运行路径上语义一致。
- 运行时支持切换：`/permission`、TUI 状态栏入口、ACP `session/set_config_option`。
- 等级切换立即生效并广播 `config_options_update` 给所有 ACP 会话。
- 未显式设置等级时零行为回归，目录 trust 与 `--approve` 保持兼容。
- 显式等级优先于目录 trust，但不能突破 `allowed_tools` / `disallowed_tools` 与
  PreToolUse hooks 的既有硬边界。
- 文档与测试完整，提供真实客户端端到端验证。

## 3. User Stories

### US-001: 权限等级类型与启动配置
**Description:** As a pigo 用户, I want 用 `--permission-mode` 或
`permission_mode` 在启动时声明权限等级 so that 我能按本次运行或默认配置决定审批强度。

**Acceptance Criteria:**
- [ ] 新增内部类型 `PermissionMode`，枚举值为 `ask` / `auto_approve_edits` / `full_access`
- [ ] `pigo --permission-mode ask|auto_approve_edits|full_access` 可解析，大小写不敏感
- [ ] `config.toml` 新增 `permission_mode` 键，`applyFileConfig` 遵循 CLI > 文件 > 默认
- [ ] `--approve` / `-a` 与 `approve = true` 等价于 `full_access`；显式
      `permission_mode` 存在时优先于 `approve`
- [ ] 未知档位输出 `pigo: --permission-mode: unknown permission mode "xxx"` 并以
      exit code 2 退出
- [ ] `go test ./cmd/... ./internal/cli/config/...` 通过

### US-002: ACP permission_mode 配置项与全局状态
**Description:** As an ACP 客户端用户, I want 通过标准配置项查看和切换权限等级 so that
pi-web 等客户端不必发明私有协议。

**Acceptance Criteria:**
- [ ] `session/new` 返回的 `configOptions` 新增 `permission_mode` select，选项为
      三档，未显式设置时 `currentValue` 为 `"default"`
- [ ] `session/set_config_option` 接受 `configId=permission_mode` 与三档之一，
      修改进程级全局等级并返回最新 `configOptions`
- [ ] 设置 `"default"` 或未知值返回 invalid params，不改变当前等级
- [ ] 切换后向所有已打开会话发送 `config_options_update`，`permission_mode`
      currentValue 为新等级
- [ ] `session/set_mode` 行为不变，仍只用于 thinking level
- [ ] `go test ./internal/acp/...` 通过

### US-003: 三档权限门
**Description:** As a pigo 用户, I want 权限门按当前等级决定哪些工具需要确认 so that
三档有真实可感知的差异。

**Acceptance Criteria:**
- [ ] `ask` 下 `bash` / `write` / `edit` / `webfetch` / `websearch` 均触发权限请求
- [ ] `auto_approve_edits` 下 `write` / `edit` 自动放行，其余五个工具触发权限请求
- [ ] `full_access` 下五个工具均不触发权限请求
- [ ] 未显式等级时仍只按现有 trust 行为门控 `bash` / `write` / `edit`，
      `webfetch` / `websearch` 不新增确认，避免行为回归
- [ ] `read` / `grep` / `find` / `ls` / `todo` / `bash_output` / `kill_bash` 不进入
      权限门
- [ ] `write` / `edit` 的既有工具层 Root 边界保持不变，越界路径仍由工具拒绝
- [ ] 行为矩阵单测覆盖 3 档 × 5 个门控工具，另覆盖未显式档

### US-004: 显式模式下的工具级 allow / reject 记忆
**Description:** As a pigo 用户, I want 在显式 ask 模式里"总是允许/总是拒绝"只作用于
单个工具 so that 放行 webfetch 不会连带放行 bash。

**Acceptance Criteria:**
- [ ] 显式 ask 下 `allow_always` 按工具名记录会话内放行，仅该工具后续跳过确认
- [ ] 显式 ask 下 `reject_always` 按工具名记录会话内拒绝，仅该工具后续直接 block
- [ ] `allow_once` / `reject_once` 只影响当前调用
- [ ] 未显式模式下四选项保留今天语义：`allow_always`=目录会话信任，
      `reject_always`=持久目录 `untrusted`
- [ ] 工具级记忆不落盘，重启即消失
- [ ] `go test ./internal/acp/... ./internal/trust/...` 通过

### US-005: 等级切换语义
**Description:** As a pigo 用户, I want 切换等级时旧的工具级决定不残留、正在等待的
权限请求按新等级处理 so that 切换结果可预测。

**Acceptance Criteria:**
- [ ] 切换等级清空会话内工具级 allow/reject 记忆
- [ ] 切换时已挂起的权限请求按新等级重评估：切到 `full_access` 直接放行；
      切到 `auto_approve_edits` 且请求是 `write` / `edit` 直接放行；其余保持等待
- [ ] 重评估结果通过既有权限响应通道返回，不向模型注入额外事件
- [ ] 并发挂起多个权限请求时全部按同一新等级处理，无竞态漏判
- [ ] `go test -race` 不适用时以并发单测 + 顺序断言覆盖（Windows 本机无 CGO）

### US-006: headless 显式等级
**Description:** As a pigo 用户, I want `-p` 模式尊重显式权限等级 so that 脚本里选了
ask 不会悄悄执行本应确认的调用。

**Acceptance Criteria:**
- [ ] 显式 `ask` / `auto_approve_edits` 下，headless 中需要批准的工具调用直接
      block，结果以工具错误回填模型
- [ ] 显式 `full_access` 下 headless 全部放行
- [ ] 未显式等级时 headless 保持今天无权限门行为
- [ ] `--approve` 等价 `full_access`，现有 headless 脚本不回归
- [ ] `go test ./internal/cli/... ./cmd/...` 通过

### US-007: /permission 斜杠命令
**Description:** As a pigo 用户, I want 在 TUI 和 REPL 里用 `/permission` 查看与切换
等级 so that 我不必重启进程。

**Acceptance Criteria:**
- [ ] `/permission` 无参数显示当前等级与用法
- [ ] `/permission ask|auto|full` 切换进程级全局等级，TUI 与 REPL 都注册
- [ ] 非法参数返回用法提示，不改变当前等级
- [ ] 切换后通过 ACP 通道广播，状态栏同步更新
- [ ] `/trust` 行为不变，只在未显式等级时参与决策
- [ ] `go test ./internal/cli/tui/... ./internal/cli/repl/...` 通过

### US-008: TUI 状态栏权限段
**Description:** As a TUI 用户, I want 状态栏常驻显示当前权限等级 so that 我随时知道
这次会话处于哪一档。

**Acceptance Criteria:**
- [ ] 状态栏新增权限段，未显式时显示"默认(trust)"，显式后显示三档之一
- [ ] `/permission` 或 ACP 配置项切换后权限段立即刷新
- [ ] 窄终端下按既有优先级截断，不破坏状态栏布局
- [ ] TUI 单测覆盖初始渲染与切换刷新
- [ ] 手动验证：`pigo.exe` 启动 TUI 后权限段可见、可切换

### US-009: trust 默认层与显式等级互斥
**Description:** As a pigo 用户, I want 显式设置等级后不再弹首次 trust 提示 so that
不会出现"已经选了 ask 还被问要不要信任目录"的双重确认。

**Acceptance Criteria:**
- [ ] 启动时显式设置等级（CLI 或 config）跳过首次 trust 提示
- [ ] 运行时从默认切到任一等级后，不再因 trust 状态发起额外确认
- [ ] 未显式等级时首次 trust 提示、`/trust`、`trust.json` 行为与今天一致
- [ ] 显式等级下目录 trust 不参与权限决策，但 `trust.json` 不被改写
- [ ] `go test ./internal/trust/... ./internal/acp/...` 通过

### US-010: 文档更新
**Description:** As a pigo 维护者, I want 文档描述新权限模型 so that 用户和后续 agent
不会继续按旧行为理解 pigo。

**Acceptance Criteria:**
- [ ] README 新增"权限模型"章节，说明三档、配置、切换、与 trust/hooks 的优先级
- [ ] `config.toml.example` 新增 `permission_mode` 与 `--approve` 别名说明
- [ ] CHANGELOG `[Unreleased]` 增加 Added 条目
- [ ] 新增 `tasks/spec-permission-model.md` 技术设计文档
- [ ] `docs/harness-capability-matrix.md` 安全护栏现状更新，移除"无统一策略入口"过期描述

### US-011: 真实客户端端到端验证
**Description:** As a pigo 开发者, I want 在真实客户端验证三档与切换广播 so that
协议层行为不是只在单测里成立。

**Acceptance Criteria:**
- [ ] pi-web 连接 `pigo --acp`，验证三档下权限请求出现/消失符合矩阵
- [ ] pi-web 中切换 `permission_mode` 后，其他已打开会话收到 `config_options_update`
- [ ] Zed 若支持 agent config option UI，则用 Zed 重复上述验证；若不支持，记录
      [Assumption] 并以 pi-web 为准
- [ ] 端到端结果记录到 PRD 或 issue，注明环境、命令、观察结果

## 4. Functional Requirements

- FR-1: 系统必须支持三个权限等级，内部 ID 为 `ask` / `auto_approve_edits` /
  `full_access`，显示名为 请求批准 / 替我审批 / 完全访问权限。
- FR-2: 系统必须解析 `--permission-mode` CLI 参数，值为三档之一。
- FR-3: 系统必须读取 `config.toml` 的 `permission_mode` 作为启动默认。
- FR-4: 系统必须遵循 CLI > config.toml > 默认的优先级。
- FR-5: 系统必须把 `--approve` / `approve = true` 映射为 `full_access`，且显式
  `permission_mode` 优先。
- FR-6: 系统必须在 `permission_mode` 值未知时以 exit code 2 报错并列出合法值。
- FR-7: `ask` 模式下，系统必须对 `bash` / `write` / `edit` / `webfetch` /
  `websearch` 发起权限请求。
- FR-8: `auto_approve_edits` 模式下，系统必须自动放行 `write` / `edit`，并对
  `bash` / `webfetch` / `websearch` 发起权限请求。
- FR-9: `full_access` 模式下，系统必须放行全部门控工具且不发起权限请求。
- FR-10: 未显式等级时，系统必须保持现有 trust 行为，只门控 `bash` / `write` /
  `edit`，且 `webfetch` / `websearch` 不新增确认。
- FR-11: 系统必须保持 `read` / `grep` / `find` / `ls` / `todo` / `bash_output` /
  `kill_bash` 不进入权限门。
- FR-12: 系统必须保证 `allowed_tools` / `disallowed_tools` 在注册层生效，任何权限
  等级都不能绕过。
- FR-13: 系统必须保证 PreToolUse hooks 先于权限确认执行，被 hook block 的调用不发起
  权限请求，任何权限等级都不能绕过 hooks。
- FR-14: 显式等级下，系统必须跳过首次 trust 提示。
- FR-15: 显式等级下，系统必须让等级优先于目录 trust。
- FR-16: 系统必须支持运行时的进程级等级切换，入口为 `/permission` 与
  `session/set_config_option`。
- FR-17: 系统必须把等级切换广播为所有会话的 `config_options_update`。
- FR-18: 系统必须把运行时切换保持在进程内，不写回 `config.toml`。
- FR-19: 系统必须不提供运行时 `default` 档；显式切换后只有三档，重启才恢复启动默认。
- FR-20: 显式 ask 模式下，系统必须把 `allow_always` / `reject_always` 实现为工具级、
  会话内记忆，只影响该工具。
- FR-21: 未显式模式下，系统必须保留 `allow_always`=目录会话信任、
  `reject_always`=持久目录 `untrusted` 的既有语义。
- FR-22: 系统必须在切换等级时清空会话内工具级 allow/reject 记忆。
- FR-23: 系统必须在切换等级时重评估挂起的权限请求：新等级不再需要批准的立即放行，
  仍需要批准的保持等待。
- FR-24: 显式等级下，headless 必须对需要批准的调用 fail-closed block；未显式等级
  保持今天无门行为。
- FR-25: 系统必须通过 `session/set_config_option` 暴露 `permission_mode` select，
  且 `session/set_mode` 继续只用于 thinking level。
- FR-26: 系统不得为工具级记忆新增持久化存储。

## 5. Non-Goals (Out of Scope)

- 不做按会话或按目录的权限等级，等级是进程级全局状态。
- 不做运行时切回 trust 默认层的 `default` 档。
- 不做工具级拒绝/放行的跨重启持久化。
- 不做参数级匹配（如 `Bash(git log:*)`）或命令风险分类。
- 不做网络域名级规则；webfetch/websearch 只按工具名门控。
- 不做 OS 级沙箱/隔离；仍以进程内策略门 + 工具 Root 边界为准。
- 不改动只读工具与 `bash_output` / `kill_bash` 的门控现状。
- 不改动 hooks 协议、hooks 加载顺序或 hook 优先级。
- 不把 thinking level 与权限等级合并进 `session/set_mode`。
- 不改动 MCP / 插件工具注册与 `allowed_tools` 校验逻辑。

## 6. Design Considerations

- 复用 `internal/trust.Manager` 作为未显式等级时的默认层，`trust.json` 格式不变。
- 复用 ACP `session/set_config_option` 的既有 select 模式（model / thought_level），
  新增 `permission_mode` 第三个选项。
- 复用 slash 命令注册机制（`/model`、`/think` 同款）实现 `/permission`。
- 复用 TUI 状态栏 segment 机制新增权限段，窄终端按既有优先级截断。
- 权限门仍挂在 `BeforeToolCall` seam；显式等级与未显式等级共用 broker，但门控工具集
  与选项语义按等级切换。
- 显式 ask 与未显式默认的门控集不同（显式含网络工具），这是有意的：未显式零回归，
  显式才收紧网络。

## 7. Technical Considerations

- 进程级当前等级建议由 ACP dispatcher / server 持有；TUI 与 REPL 作为 ACP 客户端通过
  `set_config_option` 切换，headless 直接读取启动配置。
- 未显式状态需要一个哨兵值（如 `unset`），`configOptions` 的 currentValue 显示
  `"default"`，但 `set_config_option` 不接受 `"default"`。
- 工具级记忆是 `map[toolName]decision`，挂在 permission broker 的会话状态上；
  等级切换时整表清空。
- 挂起权限请求需要可枚举注册（请求 ID、工具名、响应 channel），等级切换时统一重评估，
  避免只处理当前请求。
- 广播 `config_options_update` 需要 dispatcher 能枚举全部 live session，沿用现有
  SessionManager 的会话表。
- headless 当前不挂 `BeforeToolCall`；显式等级时需要新增一条 headless 权限门接线，
  未显式时保持 nil。
- Windows 本机无 CGO，`go test -race` 不可用；并发语义用顺序断言与并发单测覆盖，
  并在 CI 上补 `-race`。

## 8. Success Metrics

- 三档 × 五个门控工具的行为矩阵单测全部通过，另有未显式档回归用例。
- `go build ./...` 通过；`internal/acp`、`internal/trust`、`internal/cli/tui`、
  `internal/cli/repl`、`cmd` 相关包测试通过。
- 现有 trust / hooks / tool policy 测试无回归。
- pi-web 端到端可见：三档下权限请求按矩阵出现/消失，切换后其他会话收到
  `config_options_update`。
- README、config 示例、CHANGELOG、spec、harness matrix 五处文档同步更新。

## 9. Open Questions

- Zed 是否暴露 agent config option 的切换 UI 未知；若不支持，Zed 端只能使用启动档，
  端到端验证以 pi-web 为准，并记录 [Assumption]。
- 状态栏未显式时显示"默认(trust)"的文案是否最终接受。
- 是否需要在 `/status` 或 `pigo/status` 中额外展示当前权限等级（可选项，本期不做
  强制要求）。
