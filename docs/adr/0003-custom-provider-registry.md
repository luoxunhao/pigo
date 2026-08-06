# Custom provider registry: UI-configured endpoints live in pigo

pigo 增加自定义 provider 注册表（`[[providers]]` in config.toml），并通过 `pigo/models/discover`、`pigo/providers/upsert|list|delete` 暴露远程模型发现与 provider 管理。模型 id 使用 `custom-<slug>/<modelId>`，运行时与启动装配都按注册表动态解析，`pigo/models` 只返回已配置的内置 provider 与缓存的自定义模型。ash-workbench 只负责角色映射与表单，provider 定义与当前会话模型归 pigo。详细规格见 `tasks/spec-custom-provider-registry.md`。

> Superseded by `0005-model-config-model-and-models.md`（2026-08-06）。
