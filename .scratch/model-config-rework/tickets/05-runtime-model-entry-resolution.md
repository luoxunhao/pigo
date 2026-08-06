# 05 — 运行时按模型条目解析

**Type:** backend

**What to build:** 运行时按 `provider/model_id` 找到配置条目，用条目自身 `base_url/protocol/api_key` 构造 provider，真正发请求的 wire model 使用 `model_id`。

**Blocked by:** #1, #2

**Status:** done

- [ ] 模型不在配置时 prompt 返回清晰错误
- [ ] gemini 条目可配置展示，但运行时返回 not implemented
- [ ] `session/load` 恢复模型与 thinking，并校验/重置到模型支持级别
- [ ] 已存在会话在配置更新后按最新条目执行
- [ ] 覆盖 custom 与内置 provider 形态的运行时测试

**Spec:** 2026-08-06 模型配置重构设计（grill 确认）

## Resolution

已解决（2026-08-06）。运行时按配置条目构造 provider，wire model 用 `model_id`；缺模型 prompt 报错；gemini 返回 not implemented；`session/load` 恢复模型与 thinking。
