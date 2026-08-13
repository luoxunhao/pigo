# 03 — 模型解析统一为配置优先，移除 preset 与隐式推断

**What to build:** 所有模型解析路径都只认显式 provider/base-url/protocol 或 `[[models]]`；内置目录、前缀推断、模型名推断和 OpenRouter 兜底全部移除，未配置模型统一报错。

**Blocked by:** 01 — /models 只展示已配置且启用的模型

**Status:** ready-for-agent

- [ ] 显式 provider/base-url/protocol 的解析行为保持不变。
- [ ] `provider/model_id` 优先按 `[[models]]` 解析，使用条目自己的 endpoint 与凭据。
- [ ] 查不到配置且无显式覆盖时返回 `model "..." is not configured`。
- [ ] `ResolveProvider` 不再引用内置目录、前缀推断、模型名推断或 OpenRouter 兜底。
- [ ] dream、/btw、subagent RPC 使用同一套配置优先规则，相关测试已更新。
