# 02 — /model 切换只认已配置模型，补全同步收敛

**What to build:** 用户只能用 `/model <provider/model_id>` 切换到 `config.toml` 中已配置的模型；切换会使用该模型自己的 endpoint 和凭据，且补全只建议已配置模型。

**Blocked by:** 01 — /models 只展示已配置且启用的模型

**Status:** ready-for-agent

- [ ] `/model` 无参数时仍显示当前模型。
- [ ] `/model <已配置 id>` 只切换当前会话，不写全局 `model` 默认值。
- [ ] 切换后使用该条目自己的 `base_url/protocol/api_key` 构造 provider。
- [ ] 未配置 id 返回 `model "..." is not configured`，不会回退到内置解析。
- [ ] 切换后按新模型支持的 `thinking_levels` 重置 thinking。
- [ ] REPL/TUI 的 Tab 补全只建议已配置模型 id，不再建议内置模型。
