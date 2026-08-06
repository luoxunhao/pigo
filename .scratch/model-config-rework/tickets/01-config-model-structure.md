# 01 — 配置模型重构：model + models 数据结构

**Type:** backend

**What to build:** 配置文件只认 `model`（当前默认 `provider/model_id`）和 `models`（完整模型条目列表）；旧 flat 字段与 `[[providers]]` 不再作为配置来源。

**Blocked by:** None — can start immediately

**Status:** done

- [ ] `model` 存当前默认 `provider/model_id`，允许为空
- [ ] `models` 条目必填 `provider/model_id/base_url/protocol`，可选 `name/api_key/context_window/max_tokens/thinking_levels/supports_images`
- [ ] 提供 find/upsert/delete/split 辅助逻辑，按 `provider/model_id` 去重
- [ ] `api_key` 不回显，缺失时按 provider 回退环境变量
- [ ] 旧 flat 字段与 `[[providers]]` 不再参与配置解析
- [ ] 单测覆盖解析、写入、去重、删除与 key 清洗

**Spec:** 2026-08-06 模型配置重构设计（grill 确认）

## Resolution

已解决（2026-08-06）。`config.toml` 只保留 `model + models`；新 `ModelConfig` 支持必填/可选字段、去重、删除、key 不回显与 env 回退；旧 flat 字段与 `[[providers]]` 已移除。
