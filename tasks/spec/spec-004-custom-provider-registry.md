# SPEC: pigo 自定义 provider 注册表与远程模型发现

> 技术规格来源：2026-08-06 设计对话（grill-me 收敛）。
> 状态：文档先行，实现待确认后启动。

## Problem Statement

ash-workbench 作为前端时，模型设置页无法为 pigo 配置真实可用的模型：

- 模型页“获取模型列表”被 Electron 适配层 stub 成 `pigo/models`，返回 pigo 内置 `PresetCatalog`，不过滤凭据，因此会展示大量未配置的 OpenRouter 等内置模型。
- `model/set` 和 `session/set_config_option` 只修改会话模型字符串，实际请求仍使用启动时固定的 `RuntimeRunner.Provider` 与 `APIKey`。
- `pigo/config` 能写 `~/.config/pigo/config.toml`，但 ash-workbench 模型页没有调用它；前端保存的 `ai.models` 只存在于客户端 localStorage。
- pigo 启动装配 `run.SetupEnv` 只认识内置 provider 目录，无法解析 UI 保存的自定义端点。

## Goals / Non-Goals

Goals:

- 在 pigo 侧新增自定义 provider 注册表，持久化到 `config.toml`。
- 新增 ACP 方法支持远程模型发现与 provider 管理。
- 运行时按 `providerId/modelId` 动态解析 endpoint/key，无需重启。
- 启动时能解析 `custom-<slug>/...` 默认模型。
- `pigo/models` 只返回已配置的内置 provider 与已保存自定义 provider 的缓存模型。
- ash-workbench 负责角色映射，pigo 负责 provider 定义与当前会话模型。

Non-Goals:

- 不实现 terminal auth / `authenticate` / `authMethods` / `AUTH_REQUIRED`（见 ADR 0001）。
- 不实现 ACP `fs/*`、`terminal/*` 与原生 MCP。
- 不在 pigo 中保存“主力/快速/记忆/生成对话”等角色语义。
- 不把 `/dream` 动态绑定到会话 provider。

## Ownership Boundary

| 能力 | 归属 |
|---|---|
| 自定义 provider 定义（baseUrl/apiKey/protocol、模型缓存） | pigo |
| 当前 pigo 会话实际使用的模型 | pigo |
| 主力/快速/记忆/生成对话等角色映射 | ash-workbench |
| 模型设置表单与保存动作 | ash-workbench |

联动：客户端保存 provider 时调用 `pigo/providers/upsert`；角色需要驱动 pigo 会话时，客户端把 `providerId/modelId` 通过 `model/set` 设置到 pigo 会话。

## Config Shape

`~/.config/pigo/config.toml` 扩展：

```toml
model = "custom-deepseek-proxy/deepseek-v4"

[[providers]]
id = "custom-deepseek-proxy"
name = "DeepSeek Proxy"
base_url = "https://api.deepseek.com/v1"
api_key = "sk-..."
protocol = "openai"

[[providers.models]]
model_id = "deepseek-v4"
name = "DeepSeek V4"
```

- 现有顶层 `model/provider/base_url/api_key/protocol` 保留，作为兼容层；顶层 `model` 可指向 `custom-<slug>/<modelId>`。
- `models` 为 discover 结果的缓存，`pigo/models` 据此展示。
- provider id 首次创建时生成，之后编辑 name/baseUrl 不改变 id。

## Provider ID

- 格式：`custom-<slug>`。
- slug 由首次创建时的 provider name 或 base URL host 生成：小写、非字母数字转 `-`、压缩连续 `-`、去首尾 `-`。
- 不允许含 `/`，保证 `providerId/modelId` 可拆分。
- 模型 id 格式：`custom-<slug>/<modelId>`。

## ACP Methods

### pigo/models/discover

请求：

```json
{
  "name": "DeepSeek Proxy",
  "baseUrl": "https://api.deepseek.com/v1",
  "apiKey": "sk-...",
  "protocol": "openai"
}
```

响应：

```json
{
  "providerId": "custom-deepseek-proxy",
  "providerName": "DeepSeek Proxy",
  "models": [
    { "modelId": "deepseek-v4", "name": "DeepSeek V4" }
  ]
}
```

语义：

- 无副作用，不写配置。
- `protocol` 缺省为 `openai`；允许 `openai|responses|anthropic|gemini`。
- 失败返回 JSON-RPC error，错误信息不得包含 apiKey。

### pigo/providers/upsert

请求：

```json
{
  "providerId": "custom-deepseek-proxy",
  "name": "DeepSeek Proxy",
  "baseUrl": "https://api.deepseek.com/v1",
  "apiKey": "sk-...",
  "protocol": "openai",
  "models": [{ "modelId": "deepseek-v4", "name": "DeepSeek V4" }]
}
```

响应：`{ "providerId": "custom-deepseek-proxy" }`

语义：

- `providerId` 缺省时生成 `custom-<slug>`。
- `apiKey` 为空字符串时保留已有 key（仅更新其它字段）。
- `models` 可选，保存 discover 缓存。
- 返回的响应不回显 key。

### pigo/providers/list

响应：

```json
{
  "providers": [
    {
      "providerId": "custom-deepseek-proxy",
      "name": "DeepSeek Proxy",
      "baseUrl": "https://api.deepseek.com/v1",
      "protocol": "openai",
      "apiKeyConfigured": true,
      "models": [{ "modelId": "deepseek-v4", "name": "DeepSeek V4" }]
    }
  ]
}
```

语义：绝不返回 `apiKey` 明文。

### pigo/providers/delete

请求：`{ "providerId": "custom-deepseek-proxy" }`

响应：`{}`

语义：幂等删除；仍引用该 provider 的会话在下次模型请求时返回清晰错误。

### Capability 声明

`initialize` 的 `agentCapabilities._meta` 新增：

- `pigo.models.discover: true`
- `pigo.providers: true`

## Endpoint Mapping and Auth

| protocol | 模型列表端点 | 认证 |
|---|---|---|
| openai | `GET {base}/models` | `Authorization: Bearer <key>` |
| responses | `GET {base}/models` | `Authorization: Bearer <key>` |
| anthropic | `GET {base}/v1/models`；base 已含 `/v1` 时 `GET {base}/models` | `x-api-key: <key>` + `anthropic-version: 2023-06-01` |
| gemini | `GET {base}/v1beta/models` | `x-goog-api-key: <key>` |

归一化：

- 去掉末尾 `/`。
- base 以 `/chat/completions`、`/responses`、`/v1/messages`、`/v1beta/models/...` 结尾时先剥离再拼。
- base 已以 `/models` 结尾时原样使用。
- 模型列表响应兼容 `{ data: [{ id, ... }] }` 与 `{ models: [...] }`；每个条目取 id 作为 `modelId`，name 或 id 作为展示名。

## Runtime Resolution

- `RuntimeRunner` 增加 provider resolver。
- 模型 id 前缀为 `custom-` 时，从注册表读取 baseUrl/apiKey/protocol，动态构造 provider；否则使用启动时解析的 provider。
- 覆盖路径：`session/prompt` 主循环、标题生成、`/btw`、`/compact`、`/rebuild`。
- `/dream` 保持全局 `dreamCfg`。

## Startup Resolution

- `run.SetupEnv` / `acpcmd` 在默认 `model` 为 `custom-<slug>/...` 时读取 `[[providers]]`。
- provider 不存在或 `apiKey` 缺失（本地无 key 的端点除外）时启动失败，错误信息给出 providerId 与缺失字段。
- 不改变非 custom 模型的现有解析路径。

## pigo/models Filtering

- 内置 provider：仅返回凭据已配置（或无需凭据）的 provider 模型。
- 自定义 provider：返回注册表中已保存的缓存 `models`。
- 不再无条件返回整个 `PresetCatalog`。
- `session/new` 的 `models/configOptions` 同样包含自定义 provider 缓存模型。

## Save / Apply Flow

1. 用户填写 name/baseUrl/apiKey/protocol，点击获取列表：ash-workbench 调用 `pigo/models/discover`。
2. 用户选择模型并保存：ash-workbench 调用 `pigo/providers/upsert`（含 models 缓存）。
3. ash-workbench 调用 `pigo/config` 把顶层 `model` 写为 `providerId/modelId`（默认模型）。
4. ash-workbench 调用 `model/set`（或 `session/set_config_option`）应用到当前会话。

## Security

- `apiKey` 只在 `pigo/models/discover`、`pigo/providers/upsert` 请求中出现；所有响应不回显明文。
- `pigo/providers/list` 只返回 `apiKeyConfigured`。
- 网络请求由 pigo 进程发起，不把 key 暴露给 renderer 以外的网络路径。
- HTTP 客户端设置超时，遵循标准代理环境变量；错误信息清洗 key。

## Testing

单元测试：

- slug 生成与稳定性。
- 模型列表端点归一化。
- 认证头映射。
- config.toml `[[providers]]` 读写与旧配置兼容。
- `pigo/models` 过滤逻辑。
- `RuntimeRunner` 对 `custom-<slug>/...` 的动态解析。

集成测试：

- ACP wire：discover / upsert / list / delete 全流程。
- `model/set` 设置 custom 模型后，下一次 `session/prompt` 使用注册表端点。
- 默认模型为 custom 时启动装配成功/失败场景。
- ash-workbench 适配层把 `list_ai_models_by_config` 转发到 discover 并映射 id。

## Scope Decisions

| 决策 | 选择 |
|---|---|
| 对齐基准 | 行为子集对齐 pi-acp，ADR 排除项不算缺口 |
| 未记录差异 | 默认视为 gap |
| session/list 空 cwd | 使用 lastSessionCwd |
| session/new/load | 校验绝对路径 |
| 远程发现位置 | pigo ACP 服务器 |
| 端点语义 | base URL + 协议自动拼模型列表路径 |
| 格式映射 | openai/responses/anthropic/gemini |
| 注册表落盘 | config.toml `[[providers]]` |
| provider id | `custom-<slug>` |
| discover 返回 | providerId/providerName/models |
| 方法面 | discover + providers upsert/list/delete + 复用 model/set |
| 运行时范围 | 会话相关路径动态解析，/dream 全局 |
| 认证 | 按协议标准头 |
| 模型缓存 | 持久化到注册表，pigo/models 过滤 |
| 保存语义 | 当前会话 + 默认配置 |
| 启动解析 | 支持 custom 默认模型 |
| 配置归属 | provider 定义归 pigo，角色映射归 ash-workbench |
