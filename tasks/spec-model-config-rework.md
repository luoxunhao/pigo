# SPEC: pigo 模型配置重构（model + models）

> 状态：已实现（2026-08-06）。

## Config

```toml
model = "opencode-go/deepseek-v4-flash"

[[models]]
provider = "opencode-go"
model_id = "deepseek-v4-flash"
name = "DeepSeek V4 Flash"
base_url = "https://opencode.ai/zen/go/v1"
api_key = "..."            # 可选；空值保留旧 key，缺失时回退环境变量
protocol = "openai"
context_window = 128000
max_tokens = 8192
thinking_levels = ["off", "low", "medium", "high"]
supports_images = false
```

- 身份：`provider/model_id`，菜单与 `model/set` 都使用它。
- 必填：`provider/model_id/base_url/protocol`；其余可选。
- 去重/删除按 `provider/model_id`。
- 删除当前默认时 `model` 置空。

## ACP

- `pigo/models`：只返回 `models`，携带元数据。
- `session/new`：models/configOptions 来自 `models`；modes 按当前模型 `thinking_levels` 过滤。
- `model/set` / `session/set_config_option`：严格白名单；切换模型时自动重置 thinking。
- `pigo/config`：读写 `model + models`，支持整体替换、upsert、delete；每条返回 `apiKeyConfigured`，不回显 key；无 `needsRestart`。
- `pigo/models/discover`：无副作用，返回候选模型与元数据；未传 provider 时生成 `custom-<host-slug>`。
- `initialize._meta`：保留 `pigo.models.discover/pigo.models/pigo.config`，移除 `pigo.providers`。

## Runtime / CLI

- 运行时按条目 `base_url/protocol/api_key` 构造 provider，wire model 用 `model_id`。
- `gemini` 可配置展示，运行时返回 not implemented。
- 模型缺失或 `models` 为空时启动成功，prompt 时报 `model "..." is not configured`。
- CLI flags 保留为临时覆盖，`--model` 必须是已配置 id。
- 旧 flat 字段与 `[[providers]]` 不再参与配置解析。

## Testing

- `go test ./internal/acp/... ./internal/cli/config/... ./internal/provider/...` 全绿。
- 覆盖 discover、config 读写、菜单、白名单、runtime 解析、缺模型报错、gemini 拒绝、thinking 恢复。
