# 移除内置模型菜单，模型解析只认配置

pigo 不再内置 `PresetCatalog` 作为 `/models` 菜单或 model id 解析来源。`/models` 只列出 `config.toml` `[[models]]` 中启用的条目，`/model` 只接受已配置的 `provider/model_id`，切换时使用该条目自己的 `base_url/protocol/api_key` 并按 `thinking_levels` 重置 thinking，且不写全局 `model` 默认值。

模型解析统一为配置优先：显式 `--provider` / `--protocol` / `--base-url` 仍按原规则工作，除此之外模型 id 必须是已配置条目；找不到配置返回 `model "..." is not configured`，不再有前缀推断、模型名推断或 OpenRouter 兜底。`dream`、`/btw`、subagent RPC 与启动装配共用同一规则。

`--model` 默认值从 `openrouter/free` 改为空；没有配置时 pigo 可以启动，首次请求才报 `no configured model`。这是 ADR 0005“菜单与模型列表严格来自 models”的最终落地，也收尾了早期 PRD 中“`/models` 展示内置精选目录”的设计。模型配置 UI 属于后续独立功能，不在本决策范围内。
