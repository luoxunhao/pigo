# SPEC: pigo 标准 ACP + HTTP Serve

> Derived from `tasks/prd-acp-http-refactor.md` and the 2026-08-11 design conversation.

## 1. Problem Statement

pigo 的 ACP 层混入了 `model/set`、`pigo/*`、`pigo/event` 和非标准响应字段，外部 shell 只能依赖 ACP 私有扩展。TUI/REPL/headless 仍然直连 core，存在多条旁路。

本 SPEC 将 serve 定义为主 API，ACP 定义为标准协议适配层。所有前端都消费 serve，ACP 只负责 Zed 等标准客户端接入。

## 2. Architecture

```text
core -> httpapi -> HTTP clients
                   ├── pigo acp (loopback HTTP + ACP stdio)
                   ├── TUI/REPL (in-process HTTP client)
                   ├── headless (in-process HTTP client)
                   └── external shell (HTTP + SSE)
```

代码结构：

- `api/v1/openapi.yaml`：权威 OpenAPI 契约
- `internal/httpapi`：路由、handler、事件流、权限、管理 API
- `internal/acp`：stdio JSON-RPC transport 与 ACP->HTTP 翻译
- `internal/httpclient`：生成的 Go client

`pigo acp` 在同一进程内调用 `Server.listen()`，随后使用真实 loopback HTTP client 访问 serve。TUI/REPL/headless 使用进程内 HTTP transport 直接路由到 HTTP handler，不监听端口，但契约仍是 HTTP。

## 3. CLI

```text
pigo            # TUI/REPL，内部启动 serve
pigo serve      # headless HTTP server
pigo acp        # 内部启动 serve + ACP stdio adapter
```

移除 `--acp` flag。

网络参数：

```text
--port
--hostname
--password
--cors
```

优先级：CLI > 环境变量 > `config.toml` > 默认值。

## 4. HTTP API

统一前缀：`/api/v1`

JSON 字段：camelCase

路径段：下划线风格

### 4.1 Endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/v1/health` | 健康检查 |
| GET | `/api/v1/openapi.json` | OpenAPI spec |
| GET | `/api/v1/doc` | 可浏览文档 |
| GET | `/api/v1/events` | SSE 事件流 |
| POST | `/api/v1/session` | 创建会话 |
| GET | `/api/v1/session` | 列表会话 |
| POST | `/api/v1/session/{id}/load` | 加载会话并返回消息窗口 |
| POST | `/api/v1/session/{id}/resume` | 恢复运行态，不回放 |
| POST | `/api/v1/session/{id}/fork` | 分支会话 |
| POST | `/api/v1/session/{id}/close` | 释放运行态，保留历史 |
| DELETE | `/api/v1/session/{id}` | 删除会话 |
| PATCH | `/api/v1/session/{id}` | 更新会话配置 |
| GET | `/api/v1/session/{id}/status` | 会话状态 |
| GET | `/api/v1/session/{id}/messages` | 历史消息 |
| POST | `/api/v1/session/{id}/prompt` | 同步 prompt |
| POST | `/api/v1/session/{id}/prompt_async` | 异步 prompt |
| POST | `/api/v1/session/{id}/cancel` | 取消并清队列 |
| POST | `/api/v1/session/{id}/command` | 执行 slash 命令 |
| POST | `/api/v1/session/{id}/mode` | 切换 mode |
| POST | `/api/v1/session/{id}/permissions/{permissionId}/reply` | 权限回包 |
| GET | `/api/v1/modes` | mode 列表 |
| GET | `/api/v1/commands` | 命令列表 |
| GET | `/api/v1/config` | 全局配置 |
| PATCH | `/api/v1/config` | 更新全局配置 |
| GET | `/api/v1/config/providers` | provider/model 列表 |
| PUT | `/api/v1/config/providers/{providerId}` | upsert provider |
| DELETE | `/api/v1/config/providers/{providerId}` | 删除 provider |
| POST | `/api/v1/config/providers/discover` | 远程模型发现 |
| POST | `/api/v1/config/providers/test` | 模型连通性测试 |
| GET | `/api/v1/permission/trust` | trust 列表 |
| POST | `/api/v1/permission/trust` | 写 trust 决策 |
| DELETE | `/api/v1/permission/trust` | 删除 trust 决策 |

### 4.2 Session Create

`POST /api/v1/session`

```json
{
  "directory": "E:/project/foo",
  "additionalDirectories": ["E:/project/bar"],
  "model": "deepseek/deepseek-v4-pro",
  "mode": "build",
  "title": "New session",
  "mcpServers": []
}
```

响应：

```json
{
  "sessionId": "abc",
  "directory": "E:/project/foo",
  "configOptions": [],
  "availableModes": [],
  "availableCommands": []
}
```

规则：

- `directory` 必填且为绝对路径
- `model` 缺省使用全局默认模型
- `mode` 缺省为 `build`
- 全局默认模型未配置时返回 `MODEL_NOT_CONFIGURED`
- `mcpServers` 接受并存储，不连接

### 4.3 Session List

`GET /api/v1/session`

参数：

- `directory`：可选，缺省返回全部
- `before`：可选不透明游标
- `limit`：默认 50，最大 200

响应：

```json
{
  "sessions": [
    {
      "sessionId": "abc",
      "directory": "E:/project/foo",
      "title": "Add auth",
      "updatedAt": "2026-08-11T10:00:00Z",
      "additionalDirectories": ["E:/project/bar"]
    }
  ],
  "nextCursor": null
}
```

### 4.4 Session Load

`POST /api/v1/session/{id}/load`

```json
{
  "directory": "E:/project/foo",
  "additionalDirectories": ["E:/project/bar"],
  "limit": 50,
  "before": "msg_100"
}
```

响应：

```json
{
  "sessionId": "abc",
  "directory": "E:/project/foo",
  "configOptions": [],
  "messages": [],
  "hasMore": false,
  "nextCursor": null
}
```

load 不覆盖模型，不覆盖 mode。历史事实来源是 sessionstore。

### 4.5 Session Config

`PATCH /api/v1/session/{id}`

```json
{
  "model": "deepseek/deepseek-v4-pro",
  "thinkingLevel": "high",
  "mode": "build"
}
```

响应：

```json
{
  "configOptions": [
    { "id": "model" },
    { "id": "thought_level" },
    { "id": "mode" }
  ]
}
```

configId 集合固定为 `model`、`thought_level`、`mode`。

### 4.6 Prompt

`POST /api/v1/session/{id}/prompt`

```json
{
  "directory": "E:/project/foo",
  "prompt": [
    { "type": "text", "text": "hello" }
  ],
  "model": "deepseek/deepseek-v4-pro",
  "mode": "build",
  "thinkingLevel": "high"
}
```

响应：

```json
{
  "messageId": "msg_1",
  "stopReason": "end_turn",
  "usage": {}
}
```

`POST /api/v1/session/{id}/prompt_async`

```json
{
  "directory": "E:/project/foo",
  "prompt": [
    { "type": "text", "text": "hello" }
  ]
}
```

响应 `202`：

```json
{
  "messageId": "msg_1",
  "accepted": true
}
```

队列规则：

- 忙碌时进入队列
- 默认上限 100
- 超出返回 `QUEUE_FULL`
- 同步 prompt 会等待自己的 turn 完成

### 4.7 Cancel

`POST /api/v1/session/{id}/cancel`

返回 `204`。

行为：

- 取消当前 turn
- 清空 queued prompts
- serve 随后发送 `session.status`，状态为 `cancelled`

### 4.8 Command

`GET /api/v1/commands`

```json
{
  "commands": [
    {
      "name": "compact",
      "description": "Manually compact the session context",
      "input": { "hint": "optional custom instructions" }
    }
  ]
}
```

`POST /api/v1/session/{id}/command`

```json
{
  "command": "compact",
  "arguments": "optional text",
  "directory": "E:/project/foo"
}
```

响应：

```json
{
  "messageId": "msg_2",
  "stopReason": "end_turn",
  "text": "compacted: 12000 -> 5000 tokens",
  "usage": {}
}
```

hybrid 命令会等待 follow-up prompt 完成后返回。

### 4.9 Modes

`GET /api/v1/modes?directory=...`

```json
{
  "modes": [
    {
      "id": "build",
      "name": "Build",
      "description": "Default mode",
      "tools": [],
      "model": null,
      "systemPrompt": ""
    }
  ]
}
```

`POST /api/v1/session/{id}/mode`

```json
{
  "modeId": "plan"
}
```

响应：

```json
{
  "currentModeId": "plan",
  "availableModes": []
}
```

未知 mode 返回 `MODE_NOT_FOUND`。

### 4.10 Permission

SSE 事件 `permission.asked`：

```json
{
  "id": "evt_1",
  "type": "permission.asked",
  "data": {
    "permissionId": "perm_1",
    "sessionId": "abc",
    "toolCall": {},
    "options": [
      { "optionId": "allow_once", "kind": "allow_once", "name": "Allow once" },
      { "optionId": "allow_always", "kind": "allow_always", "name": "Always allow" },
      { "optionId": "reject_once", "kind": "reject_once", "name": "Reject" }
    ]
  }
}
```

`POST /api/v1/session/{id}/permissions/{permissionId}/reply`

```json
{
  "optionId": "allow_once"
}
```

映射：

- `allow_once`：放行，不落盘
- `allow_always`：写入 Trusted
- `reject_once`：拒绝，不落盘
- Untrusted 只通过 trust API 管理

### 4.11 Trust

`GET /api/v1/permission/trust`

```json
{
  "entries": [
    { "path": "E:/project/foo", "decision": "trusted" }
  ]
}
```

`POST /api/v1/permission/trust`

```json
{
  "path": "E:/project/foo",
  "decision": "trusted"
}
```

`DELETE /api/v1/permission/trust?path=E:/project/foo`

### 4.12 Config Providers

`GET /api/v1/config/providers`

```json
{
  "defaultModel": "deepseek/deepseek-v4-pro",
  "providers": [
    {
      "id": "deepseek",
      "name": "DeepSeek",
      "models": [
        {
          "id": "deepseek-v4-pro",
          "name": "DeepSeek V4 Pro",
          "contextWindow": 128000,
          "maxTokens": 8192,
          "thinkingLevels": ["off", "low", "medium", "high"],
          "supportsImages": false,
          "enabled": true
        }
      ]
    }
  ]
}
```

`PUT /api/v1/config/providers/{providerId}` upsert provider。

`POST /api/v1/config/providers/discover`

```json
{
  "name": "my-gw",
  "baseUrl": "https://gw.example/v1",
  "apiKey": "sk-...",
  "protocol": "openai"
}
```

响应：

```json
{
  "models": []
}
```

`POST /api/v1/config/providers/test`

```json
{
  "modelId": "deepseek/deepseek-v4-pro"
}
```

响应：

```json
{
  "success": true,
  "responseTimeMs": 320,
  "modelResponse": "pong"
}
```

API key 只出现在请求中，任何响应不回显明文。

## 5. SSE Event Stream

`GET /api/v1/events`

参数：

- `after`
- `directory`
- `sessionId`
- `types`

格式：

```text
id: evt_1
event: message.part.delta
data: { "sessionId": "abc", "messageId": "msg_1", "partId": "part_1", "delta": "hello" }

```

事件统一 envelope：

```json
{
  "id": "evt_1",
  "type": "message.part.delta",
  "data": {}
}
```

领域事件：

- `session.status`
- `message.part.delta`
- `message.part.updated`
- `tool.updated`
- `permission.asked`
- `mode.updated`
- `config.updated`
- `session.updated`
- `commands.updated`
- `queue.updated`

事件持久化：

- 默认 10000 条或 24 小时
- 支持 `after` 重放
- 游标过期返回 `EVENT_CURSOR_GONE`

历史消息由 sessionstore 提供，事件日志只负责实时流与短时恢复。

## 6. ACP Adapter

### 6.1 Method Mapping

| ACP | HTTP |
|---|---|
| `initialize` | 本地能力声明，不调用 HTTP |
| `authenticate` | 返回 unknown auth method |
| `session/new` | `POST /api/v1/session` |
| `session/load` | `POST /api/v1/session/{id}/load` + 分页 messages |
| `session/list` | `GET /api/v1/session` |
| `session/delete` | `DELETE /api/v1/session/{id}` |
| `session/close` | `POST /api/v1/session/{id}/close` |
| `session/prompt` | `POST /api/v1/session/{id}/prompt` + idle 等待 |
| `session/cancel` | `POST /api/v1/session/{id}/cancel` |
| `session/set_mode` | `POST /api/v1/session/{id}/mode` |
| `session/set_config_option` | `PATCH /api/v1/session/{id}` 或 mode 接口 |
| `session/fork` | `POST /api/v1/session/{id}/fork` |
| `session/resume` | `POST /api/v1/session/{id}/resume` |

`session/fork` 与 `session/resume` 实现映射，但 `initialize` 不声明对应能力。

### 6.2 Event Mapping

| serve 领域事件 | ACP |
|---|---|
| `message.part.delta` text | `agent_message_chunk` |
| `message.part.delta` reasoning | `agent_thought_chunk` |
| `tool.updated` | `tool_call` / `tool_call_update` |
| `permission.asked` | `session/request_permission` |
| `mode.updated` | `current_mode_update` |
| `config.updated` | `config_option_update` |
| `session.updated` | `session_info_update` |
| `commands.updated` | `available_commands_update` |
| `session.status` idle | 用于 `session/prompt` 完成等待 |
| `queue.updated` | 可选发送标准文本提示，不新增事件 |

### 6.3 Capabilities

```json
{
  "protocolVersion": 1,
  "agentCapabilities": {
    "loadSession": true,
    "promptCapabilities": {
      "image": true,
      "audio": false,
      "embeddedContext": false
    },
    "sessionCapabilities": {
      "list": {},
      "delete": {},
      "close": {}
    },
    "mcpCapabilities": {
      "http": false,
      "sse": false
    }
  },
  "authMethods": [],
  "agentInfo": {
    "name": "pigo",
    "title": "pigo ACP",
    "version": "..."
  }
}
```

`embeddedContext` 仅当 `PIGO_ACP_ENABLE_EMBEDDED_CONTEXT=true` 时为 true。

### 6.4 Strict ACP

移除：

- `model/set`
- `pigo/*`
- `pigo/event`
- `session/list` 的 `all`
- `session/load` 的 `modelId`
- `session/load` 响应中的 `messages`
- `session/new` / `session/load` 响应中的 `models`
- `session/new` / `session/load` 响应中的 `availableCommands`
- `initialize._meta.pigo.*`

斜杠命令：

- ACP prompt 以 `/` 开头时，先查 serve 命令列表
- 已知命令调用 `POST /command`
- 未知命令按普通文本发给模型

## 7. Auth and Security

- 默认绑定 `127.0.0.1`
- loopback 默认无密码
- 非 loopback 必须设置 password，否则拒绝启动
- 认证方式：HTTP Basic Auth，username 固定 `pigo`
- `pigo acp` 内部自连使用随机 Token
- 单一 Token 可访问全部 directory
- `directory` 必须为绝对路径
- 文件操作信任边界继续由 trust/permission 控制

## 8. Error Model

统一错误 envelope：

```json
{
  "error": {
    "code": "SESSION_NOT_FOUND",
    "message": "session not found",
    "details": {},
    "requestId": "req_1"
  }
}
```

`requestId` 由 serve 生成，并通过 `X-Request-Id` 响应头返回。

错误码：

| HTTP | Code |
|---|---|
| 400 | `DIRECTORY_INVALID` |
| 400 | `INVALID_PARAMS` |
| 400 | `MODEL_NOT_FOUND` |
| 400 | `MODE_NOT_FOUND` |
| 400 | `UNKNOWN_AUTH_METHOD` |
| 401 | `UNAUTHORIZED` |
| 404 | `SESSION_NOT_FOUND` |
| 409 | `MODEL_NOT_CONFIGURED` |
| 410 | `PERMISSION_EXPIRED` |
| 410 | `EVENT_CURSOR_GONE` |
| 429 | `QUEUE_FULL` |
| 500 | `INTERNAL` |

## 9. OpenAPI and Codegen

- 权威契约：`api/v1/openapi.yaml`
- 工具：`oapi-codegen`
- 生成 Go server 类型：`internal/httpapi/gen`
- 生成 Go client：`internal/httpclient`
- 生成代码提交进仓库
- CI 执行 `make generate && git diff --exit-code`

## 10. Plugin Mode Protocol

插件 Manifest 扩展：

```json
{
  "name": "my-plugin",
  "modes": [
    {
      "name": "plan",
      "description": "Read-only planning",
      "tools": ["read", "grep", "find", "ls"],
      "model": "claude-haiku-4-5",
      "systemPrompt": "You are in plan mode."
    }
  ]
}
```

新增 RPC：

```text
mode/apply {mode, args}
```

切换流程：

1. 更新当前 mode id
2. 核心应用 manifest 中的 tools / model / systemPrompt
3. 调用插件 `mode/apply`
4. serve 发送 `mode.updated`

无插件注册 mode 时，`availableModes` 只包含默认 `build`。

## 11. Migration

- 立即移除 ACP 非标准方法，不保留 shim
- Zed 配置改为 `["acp"]`
- TUI/REPL/headless 迁移到 serve client
- `--stream-json` 输出形状保持兼容，内部改为消费 serve 领域事件

## 12. Testing

- OpenAPI 契约测试
- HTTP handler 集成测试
- ACP wire 测试
- 手动 Zed 冒烟

验证命令：

```text
go test ./internal/httpapi/...
go test ./internal/acp/...
```
