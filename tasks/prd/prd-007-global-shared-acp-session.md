# PRD: 全局共享 ACP 进程与会话级上下文隔离

## 1. Introduction

ash-workbench 以 pigo 作为核心 ACP agent。之前 pigo 的 `--acp` 进程把启动时的 cwd 当作整个进程的项目上下文：system prompt、slash registry、文件工具根、eventMapper 的 bash cwd 都绑定进程启动目录。当 ash-workbench 用一个共享进程服务多个项目时，`E:\project\ams` 的会话会被模型当成 `E:\project\ash-workbench\apps\desktop-electron`，导致目录定位错误。

前一轮方案改为“按项目启动 pigo 进程池 + 独立配置网关”，但引入了多个 pigo 进程、配置/模型/trust 走旁路网关、进程生命周期复杂的问题，与“ash-workbench 是 pigo 的普通 ACP 客户端”的目标不符。

本 PRD 采用新方向：**ash-workbench 全局只启动一个共享 pigo 进程，pigo 提供完整的 per-session 上下文隔离**。每个 ACP 会话独立持有 system prompt、slash registry、文件工具根、eventMapper cwd 与信任边界；Zed 仍保持每项目一个进程，不受影响。ash-workbench 在应用启动时立即拉起共享 pigo，不再懒启动。

## 2. Goals

- ash-workbench 任意数量 pigo 会话只对应一个 `pigo.exe --acp` 共享进程。
- pigo 的 `session/new` 与 `session/load` 按请求 `cwd` 构建/重建 system prompt，并写回持久化 header。
- 每个 ACP 会话独立持有 system prompt、slash registry、read/write/edit 根、eventMapper cwd、trust 与权限上下文。
- 删除 ash-workbench 的 `PigoGatewayPool` 与独立配置网关，恢复单一 `PigoGateway`。
- ash-workbench 启动即拉起共享 pigo，进程 cwd 使用 `os.homedir()`，不绑定任何项目目录。
- 多目录项目通过 `additionalDirectories` 继续获得完整目录边界。
- 保留并回归验证已完成的权限链路修复（pending tool call、rawInput、optionId 回包、trust 持久化、信任管理 UI）。
- 不改变 ACP 协议方法面，Zed / pi-web 等外部客户端继续兼容。

## 3. User Stories

### US-001: session/new 按请求 cwd 构建 system prompt
**Description:** As a pigo ACP 服务开发者, I want `session/new` 使用请求中的 `cwd` 构建会话 system prompt, so that 共享进程服务多个项目时每个新会话的项目上下文都是正确的。

**Acceptance Criteria:**
- [ ] `session/new` 使用请求 `cwd` 作为 `BuildSystemPrompt` 的 `WorkingDir` 与 `Root`
- [ ] 新会话 header 的 `SystemPrompt` 包含该 `cwd` 的 Environment 工作目录
- [ ] 项目 AGENTS.md 按 `cwd` 路径链注入，不读取进程启动目录
- [ ] 同一共享进程创建 `E:\project\ams` 与 `E:\project\ash-workbench` 两个会话时，两个 header 的 Working directory 各自正确
- [ ] 对应 Go 单元测试通过

### US-002: session/load 重建 system prompt 并写回 header
**Description:** As a user, I want 恢复历史会话时 system prompt 按会话自身的 cwd 重建, so that 旧会话或换进程启动后的会话不会带着错误目录。

**Acceptance Criteria:**
- [ ] `session/load` 使用请求 `cwd` 重建 system prompt，忽略进程启动 cwd
- [ ] 重建结果写回会话 header，并持久化到 sessionstore
- [ ] 恢复后继续对话使用重建后的 system prompt
- [ ] 已存在错误 system prompt 的历史会话，在 `session/load` 后 header 被修复
- [ ] 对应 Go 单元测试通过

### US-003: 会话级 slash registry
**Description:** As a pigo ACP 服务开发者, I want 每个会话按自身 cwd 与信任状态解析 slash registry, so that 项目级 prompt 模板只在对应项目可见且信任变化后及时失效。

**Acceptance Criteria:**
- [ ] `AcpSession` 持有自己的 slash registry，不共享进程级单例
- [ ] 项目 `.pigo/prompts` 只在会话 cwd 受信任时加载
- [ ] registry 以 `(cwd, trust fingerprint)` 为键缓存，同一 cwd 复用
- [ ] trust 决策变化后，对应 cwd 的 registry 缓存失效并重建
- [ ] 两个项目同名模板在各自会话中解析到各自内容
- [ ] 对应 Go 单元测试通过

### US-004: 会话级文件工具根
**Description:** As a user, I want 每个会话的 read/write/edit/grep/find/bash 以会话 cwd 为根, so that 共享进程不会让 A 项目会话操作 B 项目文件。

**Acceptance Criteria:**
- [ ] 每个会话创建时克隆工具，`Root`/`Dir` 设置为会话 cwd
- [ ] `additionalDirectories` 合并进 `ExtraRoots`（read/write/edit）
- [ ] skills 可读目录继续保留在附加根中
- [ ] 会话 A 的文件工具不能解析到会话 B 的目录边界
- [ ] 多目录项目恢复会话后目录边界仍然生效
- [ ] 对应 Go 单元测试通过

### US-005: 会话级 eventMapper cwd
**Description:** As an ash-workbench 开发者, I want 工具事件的 terminal cwd 与文件路径按会话解析, so that 两个项目同时运行时 UI 显示各自的真实目录。

**Acceptance Criteria:**
- [ ] `eventMapper` 按会话持有 cwd，不再使用进程级 `SetCwd`
- [ ] bash pending/in_progress/completed 事件的 `_meta.terminal_info.cwd` 为会话 cwd
- [ ] 相对文件路径按会话 cwd 解析为绝对路径
- [ ] 两个项目交替产生工具事件时，事件中的 cwd 不串
- [ ] 对应 Go 单元测试通过

### US-006: 会话级 trust 与权限
**Description:** As a user, I want 权限审批和信任决策按会话 cwd 判断, so that 未信任项目仍会弹审批，信任项目不被重复打扰。

**Acceptance Criteria:**
- [ ] `BeforeToolCall` 继续使用会话 cwd 判断 `IsTrusted`
- [ ] `allow_always` 持久化写入 `~/.pigo/trust.json`
- [ ] `reject_always` 写入 Untrusted 并正确拦截
- [ ] 信任变更后对应会话的 slash registry 缓存失效
- [ ] 主目录受信任时，项目 `sourceFolders` 视为同一信任边界
- [ ] 对应 Go 单元测试通过

### US-007: 单一共享 PigoGateway
**Description:** As an ash-workbench 开发者, I want 删除 `PigoGatewayPool` 与配置网关，恢复单个共享 `PigoGateway`, so that pigo 进程生命周期和调用面回归简单一致。

**Acceptance Criteria:**
- [ ] 代码中不再存在 `PigoGatewayPool`
- [ ] 不再为配置/模型/trust 启动独立配置网关进程
- [ ] `pigo/config`、`pigo/models`、`pigo/trust/*` 通过共享 gateway 调用
- [ ] 所有 pigo conversation 按 `acpSessionId` 路由到共享 gateway
- [ ] 共享 gateway 崩溃后可重建，不影响已持久化会话
- [ ] 对应 vitest 单元测试通过

### US-008: workbench 启动即拉起共享 pigo
**Description:** As a user, I want ash-workbench 一启动就拉起共享 pigo, so that 打开应用后立即可以对话，无需等待首次输入才启动进程。

**Acceptance Criteria:**
- [ ] 主进程 ready 后（renderer 启动阶段）立即 spawn 共享 pigo，不等首次对话
- [ ] 共享 pigo 启动 cwd 为 `os.homedir()`，不绑定任何项目目录
- [ ] 启动时渲染进程不主动读 pigo 配置来决定是否拉起（取消懒启动路径）
- [ ] 无任何项目/对话时进程仍然存在
- [ ] 启动失败时显示可恢复错误状态
- [ ] 对应 vitest 单元测试通过

### US-009: conversation 路由与 backend 迁移
**Description:** As an ash-workbench 开发者, I want 持久化记录统一到共享语义并保留会话标识, so that 重启后能正确路由和恢复历史会话。

**Acceptance Criteria:**
- [ ] pigo conversation 记录迁移为 `backend='shared'`（或等价单一语义），迁移幂等
- [ ] conversation 保存 `acpSessionId`、项目 `path` 与 `sourceFolders`
- [ ] 重启后通过共享 gateway 执行 `session/load` 恢复
- [ ] 代码中不再存在 `backend='project'` 的 pigo 专用分支
- [ ] 对应 vitest 单元测试通过

### US-010: 多目录项目目录边界
**Description:** As a user with a multi-directory project, I want 会话 cwd 为主目录且附加目录通过 additionalDirectories 传入, so that agent 能访问全部 sourceFolders 且边界清晰。

**Acceptance Criteria:**
- [ ] `session/new` 参数包含 `cwd=project.path` 与 `additionalDirectories=sourceFolders`
- [ ] `session/load` 参数包含同样的目录信息
- [ ] read/write/edit 可访问全部 sourceFolders
- [ ] 主目录受信任时 sourceFolders 不需要单独审批
- [ ] 对应 vitest 与 Go 单元测试通过

### US-011: Zed 与第三方客户端兼容
**Description:** As a pigo ACP 服务开发者, I want 共享进程改造不改变协议面且对单项目进程幂等, so that Zed 每项目一进程的行为保持不变。

**Acceptance Criteria:**
- [ ] 协议方法名、参数与事件形状不新增客户端专属字段
- [ ] Zed 发送的 `session/new` 带 `cwd`，per-session 构建逻辑按该 cwd 工作
- [ ] Zed 每项目一个进程时，单会话行为与改造前一致
- [ ] `agent-client-protocol` 兼容性测试通过
- [ ] 对应 Go 单元测试通过

### US-012: 权限链路回归
**Description:** As a user, I want 共享进程改造后权限审批链路完整可用, so that bash 被安全策略拦截时 UI 能看到工具卡并完成审批。

**Acceptance Criteria:**
- [ ] 所有工具先发 `tool_call status=pending`，再 `in_progress`，最后 `completed|failed`
- [ ] `tool_call_update` 携带 `rawInput`
- [ ] 权限事件包含 `permissionId/sessionId/toolCall/options`
- [ ] 审批按 `optionId` 精确回包，60 秒无响应 cancelled
- [ ] `allow_always` 重启后不再弹审批
- [ ] 被拦截 bash 在 UI 上显示工具卡而不是静默失败
- [ ] 现有 `permission-flow` e2e 继续通过

### US-013: 多项目并发会话隔离
**Description:** As a pigo ACP 服务开发者, I want 两个项目会话并发或交替运行时上下文互不污染, so that 共享进程可以安全服务多个项目。

**Acceptance Criteria:**
- [ ] 项目 A 与项目 B 的 system prompt 互不覆盖
- [ ] 项目 A 的工具根与 eventMapper cwd 不因项目 B 的活动改变
- [ ] 项目 A 的 slash registry 不加载项目 B 的模板
- [ ] 集成测试覆盖“交替会话”与“并发运行”两种场景
- [ ] 对应 Go 集成测试通过

## 4. Functional Requirements

- FR-1: pigo `session/new` 必须用请求 `cwd` 构建 system prompt，并写入新会话 header
- FR-2: pigo `session/load` 必须按请求 `cwd` 重建 system prompt，更新并持久化 header
- FR-3: `AcpSession` 必须持有独立的 system prompt、eventMapper、文件工具根与 slash registry
- FR-4: 每个会话必须克隆文件工具，`Root`/`Dir` 为会话 cwd，`additionalDirectories` 合并进 `ExtraRoots`
- FR-5: slash registry 必须按 `(cwd, trust fingerprint)` 缓存，并在信任变化后失效
- FR-6: 项目级 prompt 模板只在会话 cwd 受信任时加载
- FR-7: eventMapper 必须按会话解析 cwd，bash 事件携带会话 cwd
- FR-8: 信任与权限判断必须使用会话 cwd
- FR-9: `allow_always` 与 `reject_always` 必须持久化到 `~/.pigo/trust.json`
- FR-10: hooks 必须继续按会话 cwd 解析，保持既有 per-session 行为
- FR-11: ash-workbench 必须只维护一个共享 pigo ACP 进程，删除 `PigoGatewayPool`
- FR-12: ash-workbench 不得再启动独立配置网关进程
- FR-13: 共享 pigo 进程启动 cwd 必须为 `os.homedir()`
- FR-14: ash-workbench 应用启动时必须立即拉起共享 pigo，不做懒启动
- FR-15: 所有 pigo conversation 必须通过 `acpSessionId` 路由到共享 gateway
- FR-16: `session/new` 与 `session/load` 必须携带项目 `cwd` 与 `additionalDirectories`
- FR-17: conversation 持久化记录必须迁移到单一共享语义，迁移幂等
- FR-18: 共享 gateway 崩溃后必须可重建并恢复会话
- FR-19: 全局配置、模型与 trust 操作必须通过共享 gateway 调用
- FR-20: 第三方 ACP agent 按会话启动独立进程的行为必须保持不变
- FR-21: 工具事件必须保持 `pending -> in_progress -> completed/failed` 顺序
- FR-22: `tool_call_update` 必须携带 `rawInput`
- FR-23: 权限审批必须支持四选项并通过 `optionId` 精确回包
- FR-24: 被 `beforeToolCall` 拦截的工具必须仍然先产生 pending 工具卡
- FR-25: ACP 协议方法面必须保持与 Zed / pi-web 兼容

## 5. Non-Goals

- 不回退到“按项目进程池 + 配置网关”架构
- 不新增 ACP 客户端专属协议字段
- 不把 Zed 改为全局共享进程；Zed 保持每项目一进程
- 不重建每个会话的 provider、plugins、memory store 等进程级环境，除非测试证明必须隔离
- 不自动信任新项目目录；首次 bash 仍必须走审批
- 不迁移或改写历史会话正文；仅通过 `session/load` 重建 system prompt header
- 不做多用户、账号、云同步或远程会话管理
- 不重写第三方 ACP agent 的进程生命周期

## 6. Design Considerations

- pigo 侧以 `AcpSession` 为上下文边界：`SysPrompt`、`Mapper`、`Roots`、`Registry` 全部挂在会话对象上；`Dispatcher` 不再持有可被会话覆盖的全局 `sysPrompt/cwd/mapper/registry`。
- 工具克隆采用“模板 + 按会话替换根”的方式：启动装配保留一份工具模板，创建/加载会话时克隆并设置 `Root=session cwd`、`ExtraRoots=additionalDirectories + skills`。
- 共享状态工具（todo、bash jobs、file snapshot、memory store）需要单独评估：Q10 只要求根边界按会话隔离，但并发会话共享 TodoStore/BashJobStore 是否安全必须由测试确认。
- slash registry 缓存键为 `(cwd, trust fingerprint)`；trust fingerprint 可由 `trust.Entries()` 或 trust.json 文件指纹派生。
- ash-workbench 侧以单一 `PigoGateway` 为唯一 ACP 连接；`ConversationManager` 按 `acpSessionId` 路由，配置/模型/trust 域直接复用该 gateway。
- 复用现有 `AcpPermissionActions`、`PermissionBroker`、`AcpEventMapper` 与 `ToolEventModule`，不重写权限链路。

## 7. Technical Considerations

- 当前 pigo `internal/acp/dispatch.go` 的进程级 `sysPrompt/mapper/cwd/applyAdditionalDirectories/SetSlashRegistry` 需要迁移到会话级。
- 当前 `internal/cli/acpcmd/acpcmd.go` 使用启动 cwd 构建 `env.SysPrompt` 与 slash registry；需要改为提供按 `cwd` 构建会话上下文的工厂，并保持共享进程启动 cwd 为 `os.homedir()`。
- `internal/cli/run.SetupEnv` 以 `os.Getwd()` 构建 tools/sysPrompt；需要暴露按目录构建的入口，避免共享进程启动目录影响会话。
- `internal/acp/runner.go` 的 `RuntimeRunner.Run` 目前使用单一 `r.Tools`；需要让每个会话携带自己的工具集，或将工具模板 + 会话根传入 Run。
- `internal/acp/permission.go` 已按会话 cwd 判断 trust，但需要增加信任变更后的 slash registry 失效通知。
- `internal/trust.Manager` 目前没有 fingerprint API，可新增 `Fingerprint()` 或由 dispatcher 基于 `Entries()` 派生。
- ash-workbench 需删除 `apps/desktop-electron/src/main/pigo/pool.ts` 及其接线，`PigoGateway` 恢复单例并支持懒加载失败重试。
- `BackendKind` 相关字段与迁移逻辑需要从 `'project'` 回迁到共享语义，并保证 `conversations.json` 迁移幂等。
- 验证命令：pigo `go test ./internal/acp/...`、ash-workbench `pnpm --dir apps/desktop-electron vitest`、type-check、`permission-flow` e2e。
- Windows 全量 `go test ./...` 存在既有环境性失败，改动相关包时优先跑对应包。

## 8. Success Metrics

- 在 `E:\project\ams` 新建会话后，持久化 header 的 Working directory 为 `E:\project\ams`，不再出现 `ash-workbench` 路径
- 任意数量 pigo 对话只对应一个 `pigo.exe --acp` 进程
- ash-workbench 启动后、创建任何对话前，共享 pigo 进程已经存在
- 两个项目会话交替/并发时，各会话的 system prompt、工具事件 cwd、slash registry 均不串
- `allow_always` 重启后不再弹审批，`reject_always` 正确拦截，被拦截 bash 在 UI 显示工具卡
- 多目录项目恢复会话后 read/write/edit 仍可访问全部 `sourceFolders`
- pigo `internal/acp` 测试、ash-workbench vitest、type-check 与 `permission-flow` e2e 全部通过

## 9. Open Questions

- TodoStore、BashJobStore、FileSnapshotRecorder、memory store 在共享进程中是否必须按会话隔离，还是保持进程级共享即可满足并发安全
- slash registry trust fingerprint 采用 `trust.Entries()` 哈希还是 trust.json 文件指纹
- `os.homedir()` 作为共享进程 cwd 后，`ReadableExtraRoots()` 与全局模板加载是否仍符合预期
- 共享 pigo 启动失败时，UI 应采用自动重试还是等待用户手动重试
- 历史 `conversations.json` 中已存在 `backend='project'` 记录的迁移策略是否需要保留旧 id 关联
