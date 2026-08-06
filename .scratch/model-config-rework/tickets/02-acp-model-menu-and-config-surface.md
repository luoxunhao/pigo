# 02 — ACP 模型菜单与会话配置面

**Type:** backend

**What to build:** `pigo/models` 与 `session/new` 的 models/configOptions 只来自配置文件 `models`，菜单严格等于已配置模型，无 PresetCatalog 也无当前模型兜底。

**Blocked by:** #1

**Status:** done

- [ ] `modelId` 使用 `provider/model_id`，携带 `contextWindow/maxTokens/thinkingLevels/supportsImages`
- [ ] modes 按当前模型 `thinking_levels` 过滤，缺失时回退全局 `off..xhigh`
- [ ] `model/set` 与 `session/set_config_option` 严格白名单校验
- [ ] 切换模型时自动重置 thinking 到新模型支持级别并发送 `current_mode_update`
- [ ] 覆盖菜单、过滤、白名单与切换重置的测试

**Spec:** 2026-08-06 模型配置重构设计（grill 确认）

## Resolution

已解决（2026-08-06）。`pigo/models` 与 `session/new` 只返回已配置模型并携带元数据；modes 按模型 thinking_levels 过滤；`model/set` 严格白名单并自动重置 thinking。
