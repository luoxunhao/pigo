# 05 — 运行时动态 provider 解析

**Type:** backend

**What to build:** `RuntimeRunner` 按 `custom-<slug>/<modelId>` 从注册表动态构造 provider，让主会话和会话相关模型路径真正使用 UI 保存的 endpoint/key。

**Blocked by:** #3

**Status:** ready-for-agent

- [ ] `model/set` 与 `session/set_config_option` 接受 custom 模型 id
- [ ] session/prompt 使用注册表 endpoint/key
- [ ] 标题生成、/btw、/compact、/rebuild 走同一动态解析
- [ ] /dream 保持全局 `dreamCfg`
- [ ] 单测覆盖 custom 与内置 provider 两条路径

**Spec:** tasks/spec-custom-provider-registry.md（Runtime Resolution）
