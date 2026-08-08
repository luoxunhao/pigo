# PRD: ACP 按项目进程模型的目录与权限链路修复

> 已废弃：本 PRD 的“按项目进程池 + 独立配置网关”方案已被 `prd-global-shared-acp-session.md` 取代。权限链路部分仍有效，并纳入新 PRD 的回归范围。

## 1. Introduction

ash-workbench 之前用一个全局共享 pigo ACP 进程服务所有项目。pigo 的 `--acp` 进程使用启动时的 cwd 构建 system prompt，因此共享进程会让 `E:\project\ams` 会话被模型当成 `E:\project\ash-workbench\apps\desktop-electron`。

Zed 的实际模式是每个窗口/项目启动一个 `pigo.exe --acp` 进程，进程 cwd 就是项目目录，所以 Zed 不会遇到这个问题。本 PRD 让 ash-workbench 对齐这一模式：客户端按项目启动、复用并清理 pigo 进程，同时保留一个独立配置网关处理全局配置、模型与 trust。

ash-workbench 项目允许包含多个源目录，但存在一个主目录。项目进程以主目录 `project.path` 作为 cwd，附加目录通过 `additionalDirectories` 暴露给 pigo 文件工具，保证多目录项目在恢复会话后仍然具备完整目录边界。

本 PRD 还覆盖同一链路上的两个断点：未信任目录触发 bash 权限审批时，ash-workbench 的权限事件形状与 option 字段不匹配，审批弹窗无法显示；被拦截的工具调用发生在 pigo 发出 pending 事件之前，UI 没有工具卡可展示。

## 2. Goals

- ash-workbench 为每个项目启动一个独立的 `pigo.exe --acp` 进程，进程 cwd 等于项目路径。
- 多目录项目以 `project.path` 为进程 cwd，`sourceFolders` 通过 `additionalDirectories` 传入 `session/new` 与 `session/load`。
- 同一项目内的对话复用同一进程，不同项目不共享进程。
- 新增独立配置网关进程，负责 `pigo/config`、`pigo/models`、`pigo/trust/*` 等全局能力。
- 移除旧全局共享网关兼容路径，`backend='shared'` 不再表示全局共享。
- pigo 工具事件按 `pending -> in_progress -> completed/failed` 发出，结束事件携带 `rawInput`。
- ash-workbench 权限审批弹窗和工具卡完整可用，四种审批结果正确回包。
- “始终允许”写入 `~/.pigo/trust.json`，跨 pigo 重启生效。
- 设置页提供已信任目录列表与撤销信任入口。
- ACP 协议保持与 Zed/pi-web 兼容。

## 3. User Stories

### US-001: 按项目懒启动 pigo 进程池
**Description:** As an ash-workbench desktop 开发者, I want 每个项目首次使用时启动一个独立 pigo 进程, so that 项目上下文由进程 cwd 提供且互不干扰。

**Acceptance Criteria:**
- [ ] 首次在项目 A 创建对话或发消息时，启动一个 `pigo.exe --acp` 进程，cwd 为项目 A 路径
- [ ] 项目 B 使用独立进程，不复用项目 A 的进程
- [ ] 同一项目后续对话复用已启动进程，不重复拉起
- [ ] 应用退出时统一清理全部 pigo 进程
- [ ] 对应 vitest 单元测试通过

### US-002: 项目内会话复用与懒恢复
**Description:** As a user, I want 切换历史对话时才恢复对应项目进程和会话, so that 应用启动不需要为所有项目拉起进程。

**Acceptance Criteria:**
- [ ] 应用启动时不自动恢复所有项目进程
- [ ] 切换/激活某项目对话时，确保该项目进程已启动并执行 `session/load`
- [ ] 同一项目多个历史会话复用同一进程
- [ ] 对应 vitest 单元测试通过

### US-003: 独立配置网关
**Description:** As an ash-workbench desktop 开发者, I want 全局配置、模型与 trust 操作走独立配置网关进程, so that 不依赖任何项目会话是否存在。

**Acceptance Criteria:**
- [ ] 配置网关使用 `os.homedir()` 作为 cwd
- [ ] `pigo/config`、`pigo/models`、`pigo/trust/list`、`pigo/trust/set` 通过配置网关调用
- [ ] 无项目会话时设置页仍可读取模型列表与信任目录
- [ ] 配置网关崩溃可重建，不影响已启动的项目进程
- [ ] 对应 vitest 单元测试通过

### US-004: backend 字段迁移
**Description:** As an ash-workbench desktop 开发者, I want 旧 `backend='shared'` 记录迁移为 `backend='project'`, so that 代码中不再保留全局共享语义。

**Acceptance Criteria:**
- [ ] 启动时读取 `conversations.json`，将 pigo 记录的 `backend` 迁移为 `project`
- [ ] 迁移幂等，重复启动不重复改写
- [ ] `clientFor` 与 `capabilitiesFor` 不再存在 `backend==='shared'` 的全局单例分支
- [ ] 对应 vitest 单元测试通过

### US-005: 所有工具先发 pending tool_call
**Description:** As a pigo ACP 服务开发者, I want 工具执行前发出 `tool_call status=pending`, so that 客户端可以在权限审批前创建工具卡。

**Acceptance Criteria:**
- [ ] 所有工具调用先收到 `sessionUpdate=tool_call, status=pending`
- [ ] 权限通过并开始执行后收到 `status=in_progress`
- [ ] 执行结束收到 `status=completed` 或 `status=failed`
- [ ] 被 `beforeToolCall` 拦截的工具仍然先有 pending 事件
- [ ] 事件状态保持单调：pending -> in_progress -> completed/failed
- [ ] 对应 Go 与事件映射测试通过

### US-006: tool_call_update 结束事件包含 rawInput
**Description:** As a pigo ACP 服务开发者, I want 失败的工具结束事件携带原始参数, so that UI fallback 可以展示被拦的 bash 命令。

**Acceptance Criteria:**
- [ ] `tool_call_update` completed/failed 事件包含 `rawInput`
- [ ] bash 被拦时 `rawInput.command` 保留原命令
- [ ] 对应 Go 单元测试通过

### US-007: 权限事件标准化并支持 optionId 回包
**Description:** As an ash-workbench 开发者, I want 主进程向 renderer 发送标准化的权限事件, so that 审批弹窗可以正确显示 pigo 的四个选项。

**Acceptance Criteria:**
- [ ] 主进程发出的权限事件包含 `permissionId`、`sessionId`、`toolCall`、`options(optionId/name/kind)`
- [ ] `permissionId` 与 JSON-RPC request id 一一对应
- [ ] `PermissionBroker` 按用户选择的 `optionId` 精确回包
- [ ] 60 秒无响应时自动 cancelled
- [ ] 对应 vitest 单元测试通过

### US-008: submit_acp_permission_response 落地为真实 IPC
**Description:** As an ash-workbench 开发者, I want `ACPClientAPI.submitPermissionResponse` 调用真实桌面 IPC, so that 工具卡按钮可以完成审批回包。

**Acceptance Criteria:**
- [ ] `submit_acp_permission_response` 不再是 unsupported
- [ ] allow once / allow always / reject once / reject always 分别返回 pigo 可识别的 outcome
- [ ] 审批按钮点击后工具卡状态更新为 confirmed 或 rejected
- [ ] 对应 vitest 单元测试通过

### US-009: Failed 无 Started 时兜底创建工具卡
**Description:** As an ash-workbench 开发者, I want 收到失败工具事件但不存在工具卡时自动创建卡片, so that 第三方 ACP agent 的拦截也不会无声消失。

**Acceptance Criteria:**
- [ ] Failed 事件到达且工具卡不存在时，先创建工具卡再标记 error
- [ ] 卡片展示 `rawInput`（存在时）与错误文本
- [ ] 不产生未处理的异常或空 UI
- [ ] 对应 vitest 单元测试通过

### US-010: allow_always 持久化 trust
**Description:** As a user, I want 选择“始终允许”后目录信任跨 pigo 重启生效, so that 下次打开 ash-workbench 不用重复审批同一个项目。

**Acceptance Criteria:**
- [ ] `allow_always` 写入 `~/.pigo/trust.json` 的 Trusted 决策
- [ ] 重新加载 trust manager 后该目录仍为 Trusted
- [ ] `allow_once` 不写入持久化 trust
- [ ] `reject_always` 写入 Untrusted 决策
- [ ] remote confirm 的 allow_always 与工具卡路径行为一致
- [ ] 对应 Go 单元测试通过

### US-011: 设置页信任管理
**Description:** As a user, I want 在 ash-workbench 设置页查看已信任目录并撤销信任, so that 持久化信任是可管理的。

**Acceptance Criteria:**
- [ ] 设置页新增“信任管理”区域，列出 pigo 已信任目录
- [ ] 每个目录提供“撤销信任”操作
- [ ] 撤销后 pigo trust.json 中对应目录不再为 Trusted
- [ ] 无信任目录时显示空状态
- [ ] 在 ash-workbench 桌面端手动验证 UI 与操作

### US-012: 多目录项目目录边界
**Description:** As a user with a multi-directory project, I want pigo 进程以主目录为 cwd 并访问全部 sourceFolders, so that agent 可以跨项目目录读写文件。

**Acceptance Criteria:**
- [ ] 项目 `path=E:\project\main`、`sourceFolders=[E:\project\lib]` 时，项目 pigo 进程 cwd 为 `E:\project\main`
- [ ] `session/new` 参数包含 `additionalDirectories: ["E:\\project\\lib"]`
- [ ] `session/load` 参数包含 `additionalDirectories: ["E:\\project\\lib"]`
- [ ] read/write/edit 工具可以访问 sourceFolders
- [ ] 主目录受信任时，sourceFolders 不需要单独审批
- [ ] 对应 vitest 与 Go 单元测试通过

## 4. Functional Requirements

- FR-1: desktop 主进程必须为每个项目维护独立 pigo 进程，并以项目路径作为进程 cwd。
- FR-2: 项目进程必须按需懒启动，并在应用退出时统一清理。
- FR-3: 同一项目内多个对话必须复用同一 pigo 进程。
- FR-4: 系统必须提供独立配置网关进程，负责 `pigo/config`、`pigo/models`、`pigo/trust/list`、`pigo/trust/set`。
- FR-5: 配置网关必须使用 `os.homedir()` 作为 cwd。
- FR-6: 系统必须把旧 `backend='shared'` 记录迁移为 `backend='project'`。
- FR-7: pigo 必须对所有工具先发出 `tool_call status=pending`。
- FR-8: pigo 必须在执行开始时发出 `tool_call_update status=in_progress`。
- FR-9: pigo 必须在结束时发出 `tool_call_update status=completed|failed`，并携带 `rawInput`。
- FR-10: ash-workbench 主进程必须发送包含 `permissionId/sessionId/toolCall/options` 的标准化权限事件。
- FR-11: `PermissionBroker` 必须按用户选择的 `optionId` 回包。
- FR-12: `submit_acp_permission_response` 必须通过桌面 IPC 完成真实审批回包。
- FR-13: ash-workbench 必须在 Failed 且无 Started 时创建兜底工具卡。
- FR-14: pigo 所有 `allow_always` 路径必须将目录写为持久化 Trusted。
- FR-15: pigo 必须提供 `pigo/trust/list` 与 `pigo/trust/set` RPC。
- FR-16: ash-workbench 设置页必须提供已信任目录列表与撤销信任操作。
- FR-17: desktop 必须使用 `project.path` 作为项目 pigo 进程的 cwd。
- FR-18: desktop 必须把 `sourceFolders` 作为 `additionalDirectories` 传给 `session/new` 与 `session/load`。
- FR-19: pigo `session/load` 必须接收 `additionalDirectories` 并重新合并到 read/write/edit 工具。
- FR-20: 主目录受信任时，项目内 `sourceFolders` 视为同一项目信任边界。

## 5. Non-Goals

- 不实现 pigo per-session prompt 重建；项目上下文由进程 cwd 提供。
- 不迁移旧会话已持久化的错误 system prompt；旧会话继续保留，用户新建对话。
- 不保留旧全局共享网关兼容路径，不为 `backend='shared'` 旧语义添加兼容分支。
- 不对 `sourceFolders` 做单独信任决策；项目主目录信任覆盖整个项目边界。
- 不实现主进程原生 Electron 审批弹窗。
- 不自动信任新建项目目录，首次 bash 仍必须走审批。
- 本迭代不把 Zed / pi-web 端到端验证设为强制验收项。
- 不重建每会话的 tools、provider、plugins 环境。
- 不修改第三方 ACP agent 的模型目录职责。

## 6. Design Considerations

- 项目进程池可参考现有 `ConversationProcessHost` 的生命周期管理，但键改为项目路径而非对话 id。
- 多目录项目的附加目录统一走 ACP `additionalDirectories`，不在客户端侧自行放宽文件工具边界。
- 复用现有 `AcpPermissionActions`、`AcpPermissionToolCardModule`、`PermissionBroker`、`AcpEventMapper` 与 `ToolEventModule`。
- trust.json 继续由 pigo 持有；ash-workbench 只通过 `pigo/trust/list` 与 `pigo/trust/set` 读取和修改。
- 工具事件状态机对齐 pi-acp：`pending -> in_progress -> completed/failed`。
- 旧 `conversations.json` 迁移在启动时一次性完成，迁移逻辑幂等。

## 7. Technical Considerations

- ash-workbench 需要新增 `PigoGatewayPool` 或等价组件，按项目路径管理多个 `PigoGateway` 实例。
- `WorkbenchHost`、`ConfigDomainImpl`、`WorkbenchDomainImpl` 需要从单例 `PigoGateway` 改为“项目进程池 + 配置网关”两个入口。
- `ConversationManager.clientFor` 按 `backend='project'` 路由到对应项目进程。
- pigo 需要新增 `pigo/trust/list` 与 `pigo/trust/set`，并修改 `permission.go` 的 `allow_always` 为持久化 `SetDecision`。
- pigo 需要新增 pending 工具事件，并让 `tool_call_update` 结束事件携带 `rawInput`。
- pigo `session/load` 需要扩展 `additionalDirectories` 参数并重新合并到文件工具，保证恢复会话后多目录边界仍生效。
- Windows 上 `go test ./...` 存在既有环境性失败，pigo 侧使用针对性测试命令。
- 两端分别提交，提交信息使用中文，未经要求不 push。

## 8. Success Metrics

- 新建 `E:\project\ams` 会话后，转录 header 的 system prompt 工作目录为 ams，且不出现 ash-workbench。
- bash 工具调用在审批前显示 pending 工具卡。
- allow once 执行成功，allow always 重启后不再弹审批，reject 正确拦截。
- 被拦截工具在 UI 上可见，不再出现“无卡片但报错”的状态。
- 多目录项目恢复会话后，read/write/edit 工具仍可访问全部 `sourceFolders`。
- `conversations.json` 中 pigo 记录全部为 `backend='project'`。
- pigo `internal/acp` 相关测试、ash-workbench 相关 vitest 与 type-check 全部通过。

## 9. Open Questions

- `pigo/trust/list` 与 `pigo/trust/set` 的具体请求/响应字段需要在 SPEC 阶段确定。
- 设置页信任管理是否也需要展示 Untrusted 目录以便用户恢复信任。
- 是否在本迭代后把 Zed / pi-web 纳入正式回归验收。
