# Model config: model + models flat list

pigo 的模型配置改为最简扁平结构：配置文件只包含 `model`（当前默认 `provider/model_id`）和 `models`（完整模型条目列表）。每条模型自带 `provider/model_id/base_url/api_key/protocol/context_window/max_tokens/thinking_levels/supports_images`，运行时按条目自身 endpoint 构造 provider，不再使用内置 PresetCatalog 或 `[[providers]]` 注册表。

菜单与 ACP 模型列表严格来自 `models`；`model/set` 只接受已配置 id。`/models` 拉取是配置阶段操作，由 ash-workbench 调用 `pigo/models/discover` 后写入配置。CLI/REPL/headless/ACP 行为一致：模型缺失时能启动，真正请求时返回 `model "..." is not configured`。

本 ADR 取代 0003 的 `[[providers]]` 设计；详细规格见 `tasks/spec/spec-007-model-config-rework.md`。
