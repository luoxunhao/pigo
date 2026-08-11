# PRD: pigo 标准 ACP + HTTP Serve 重构

## 1. Introduction

pigo 当前通过 ACP 对外提供服务，但 ACP 层混入了大量非标准能力：`model/set`、`pigo/*` RPC、`pigo/event` 通知，以及标准方法上的扩展字段。TUI/REPL/headless 仍然存在直连 core 的旁路，外部 shell 只能依赖 ACP 私有扩展。

本次重构将 serve 作为 pigo 的主 API，ACP 降级为标准协议适配层。架构参考 opencode：`pigo serve` 是权威 HTTP API，`pigo acp` 在同一进程内启动 serve，并通过真实 loopback HTTP client 调用 serve，再把 ACP JSON-RPC 翻译成 HTTP 调用。

## 2. Goals

- `pigo serve` 提供完整 HTTP API：session、prompt、event、permission、config、models、trust、messages、commands、modes、status。
- `pigo acp` 只实现标准 ACP 方法，不提供任何 `pigo/*` 扩展。
- ACP 只发送标准 `session/update` 与 `session/request_permission`，不再发送 `pigo/event`。
- TUI/REPL/headless 全部改为 serve 的客户端，使用进程内 HTTP client。
- OpenAPI 作为 HTTP 契约的唯一事实来源，生成 Go server 类型与 Go 测试 client。
- mode 由插件扩展注册，核心只提供机制和默认 `build`，不内置复杂 agent mode。
- 本次只验证 Zed；ash-workbench 与 pi-web 不迁移、不验收。

## 3. Non-Goals

- 不迁移或验收 ash-workbench / pi-web。
- 不生成 TypeScript SDK。
- 不实现 MCP 连接、`providers/*`、`logout`、NES、document 等 ACP 可选方法。
- 不实现完整 WebSocket/远程控制双向通道。
- 不在核心内置 plan/code/ask 等复杂 agent mode。
- 不保留 `model/set` 和任何 `pigo/*` ACP 兼容 shim。

## 4. User Stories

### US-001: Zed 使用标准 ACP

**Description:** As a Zed user, I want `pigo acp` 暴露标准 ACP 方法，so that 对话、工具、权限、取消、历史恢复全部可用。

**Acceptance Criteria:**
- [ ] Zed 配置改为 `"args": ["acp"]`
- [ ] `initialize`、`session/new/load/list/delete/close/prompt/cancel/set_mode/set_config_option` 全部可用
- [ ] `session/update` 与 `session/request_permission` 正常流转
- [ ] ACP 响应不包含非标准字段

### US-002: Shell 使用 HTTP serve

**Description:** As a shell developer, I want `pigo serve` 暴露完整的 REST + SSE API，so that 对话、管理、事件流都只走 HTTP。

**Acceptance Criteria:**
- [ ] `GET /api/v1/openapi.json` 返回完整规范
- [ ] session/prompt/cancel/config/models/trust/messages/commands/modes/status 均可通过 HTTP 调用
- [ ] `GET /api/v1/events` 提供可重放 SSE 事件流

### US-003: TUI/REPL/headless 全部消费 serve

**Description:** As a pigo developer, I want TUI/REPL/headless 不再直连 core，so that 所有前端共享同一条 HTTP 契约。

**Acceptance Criteria:**
- [ ] TUI/REPL/headless 使用进程内 HTTP client 调用 serve
- [ ] `--stream-json` 输出由 serve 领域事件转换，保持现有事件形状

### US-004: 插件可以注册 mode

**Description:** As a plugin author, I want 插件能够注册 mode，so that ACP `availableModes` 和 HTTP modes 接口都由插件扩展驱动。

**Acceptance Criteria:**
- [ ] 插件 Manifest 可声明 modes
- [ ] 核心提供 `mode/apply` 扩展点
- [ ] 无插件时 `availableModes` 只包含默认 `build`

## 5. Functional Requirements

### 5.1 CLI

- `pigo`：默认启动 TUI/REPL，内部启动 serve
- `pigo serve`：启动 headless HTTP server
- `pigo acp`：内部启动 serve，并作为 ACP stdio 适配器
- 移除 `--acp` flag

### 5.2 ACP 标准方法面

必须支持：

- `initialize`
- `session/new`
- `session/load`
- `session/list`
- `session/delete`
- `session/close`
- `session/prompt`
- `session/cancel`
- `session/set_mode`
- `session/set_config_option`

必须发送/处理：

- `session/update`
- `session/request_permission`

不实现、不声明：

- `model/set`
- `pigo/*`
- `pigo/event`
- `providers/*`
- `logout`
- `nes/*`
- `document/*`

### 5.3 HTTP API

统一前缀 `/api/v1`，JSON 字段 camelCase，路径段使用下划线风格。

核心端点：

- `GET /api/v1/health`
- `GET /api/v1/openapi.json`
- `GET /api/v1/doc`
- `GET /api/v1/events`
- `POST /api/v1/session`
- `GET /api/v1/session`
- `POST /api/v1/session/{id}/load`
- `POST /api/v1/session/{id}/resume`
- `POST /api/v1/session/{id}/fork`
- `POST /api/v1/session/{id}/close`
- `DELETE /api/v1/session/{id}`
- `PATCH /api/v1/session/{id}`
- `GET /api/v1/session/{id}/status`
- `GET /api/v1/session/{id}/messages`
- `POST /api/v1/session/{id}/prompt`
- `POST /api/v1/session/{id}/prompt_async`
- `POST /api/v1/session/{id}/cancel`
- `POST /api/v1/session/{id}/command`
- `POST /api/v1/session/{id}/mode`
- `POST /api/v1/session/{id}/permissions/{permissionId}/reply`
- `GET /api/v1/modes`
- `GET /api/v1/commands`
- `GET /api/v1/config`
- `PATCH /api/v1/config`
- `GET /api/v1/config/providers`
- `PUT /api/v1/config/providers/{providerId}`
- `DELETE /api/v1/config/providers/{providerId}`
- `POST /api/v1/config/providers/discover`
- `POST /api/v1/config/providers/test`
- `GET /api/v1/permission/trust`
- `POST /api/v1/permission/trust`
- `DELETE /api/v1/permission/trust`

### 5.4 事件流

- SSE 使用标准 `text/event-stream`
- 每条事件带 `id` 与 `event`，data 统一为 `{ id, type, data }` 领域事件
- 领域事件类型采用点号命名：`session.status`、`message.part.delta`、`message.part.updated`、`tool.updated`、`permission.asked`、`mode.updated`、`config.updated`、`session.updated`、`commands.updated`、`queue.updated`
- 支持 `after`、`directory`、`sessionId`、`types` 过滤
- 事件持久化窗口默认 10000 条或 24 小时

### 5.5 Prompt 与队列

- `POST /prompt` 同步等待，返回 `{ messageId, stopReason, usage? }`
- `POST /prompt_async` 返回 `202 { messageId, accepted }`
- 忙碌时 prompt 进入队列；队列上限默认 100
- `POST /cancel` 返回 204，取消当前 turn 并清空队列

### 5.6 Permission

- SSE 推送 `permission.asked`
- 客户端通过 `POST /session/{id}/permissions/{permissionId}/reply` 回包
- 选项固定为 `allow_once`、`allow_always`、`reject_once`
- `allow_always` 写入 Trusted；`reject_once` 不落盘
- Untrusted 只通过 trust API 管理

### 5.7 Config 与 Trust

- 配置、trust、session store 只能由 serve 读写
- ACP 适配层不直接访问文件
- `GET/PATCH /api/v1/config` 管理全局默认模型
- provider 使用 `PUT/DELETE /api/v1/config/providers/{providerId}`
- trust 使用 `GET/POST/DELETE /api/v1/permission/trust`

### 5.8 Modes

- mode 列表来自插件 mode registry
- 无插件时只返回默认 `build`
- `GET /api/v1/modes` 返回完整 mode 信息，包括 systemPrompt
- `POST /api/v1/session/{id}/mode` 切换 mode，响应返回当前 mode 与 availableModes
- 未知 mode 返回 `invalidParams`

### 5.9 会话配置

- configId 使用 `model`、`thought_level`、`mode`
- `PATCH /api/v1/session/{id}` 是统一配置入口
- `POST /api/v1/session/{id}/mode` 保留为 mode 的语义化别名
- ACP `session/set_config_option` 按 configId 分流，统一返回 configOptions

### 5.10 认证与安全

- 默认绑定 `127.0.0.1`
- loopback 默认无密码
- 非 loopback 必须设置 `PIGO_SERVER_PASSWORD`，否则拒绝启动
- 使用 HTTP Basic Auth，username 固定 `pigo`
- `pigo acp` 内部自连使用随机 Token
- 单一 Token 可访问全部 directory；directory 只是过滤参数

### 5.11 OpenAPI 与代码生成

- 权威契约位于 `api/v1/openapi.yaml`
- 使用 `oapi-codegen` 生成 Go server 类型与 Go client
- 生成代码提交进仓库，CI 检查无 diff

## 6. Architecture

```text
core -> httpapi -> HTTP clients
                   ├── pigo acp (loopback HTTP + ACP stdio)
                   ├── TUI/REPL (in-process HTTP client)
                   ├── headless (in-process HTTP client)
                   └── external shell (HTTP + SSE)
```

代码结构：

- `api/v1/openapi.yaml`
- `internal/httpapi`：路由、handler、事件流、权限、管理 API
- `internal/acp`：stdio JSON-RPC transport 与 ACP->HTTP 翻译
- `internal/httpclient`：生成的 Go client

## 7. Compatibility and Migration

- 立即移除 ACP 上的 `model/set` 与 `pigo/*`
- 不保留兼容 shim
- Zed 配置从 `["--acp"]` 改为 `["acp"]`
- TUI/REPL/headless 在本重构中同步迁移到 serve client
- `--stream-json` 输出形状保持兼容，内部改为消费 serve 领域事件

## 8. Verification

- OpenAPI 契约测试：所有端点符合 `api/v1/openapi.yaml`
- HTTP handler 集成测试：session/prompt/cancel/config/modes/commands/permission/trust/messages
- ACP wire 测试：fake ACP client 覆盖完整标准流程
- 手动 Zed 冒烟：对话、工具、权限、取消、历史恢复
- `go test ./internal/httpapi/...`
- `go test ./internal/acp/...`

## 9. Open Questions

- mode 插件协议的具体 schema 和 `mode/apply` 行为，在 SPEC 阶段细化
- WebSocket 远程控制是否在 v2 加入
