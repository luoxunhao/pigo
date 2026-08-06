# 03 — 模型设置校验

**Type:** task

**What to build:** 客户端通过 `model/set` 或 `session/set_config_option` 设置未知模型 id 时，立即收到 `invalidParams`，而不是把无效值写进会话后再在请求时失败。

**Blocked by:** #2

**Status:** done

- [ ] `model/set` 校验模型 id，未知 id 返回 `invalidParams`
- [ ] `session/set_config_option` 的 model 值走同一校验
- [ ] 内置 provider 与自定义 provider 的模型 id 都按可用列表校验
- [ ] 覆盖合法与非法模型 id 的测试

## Resolution

已解决（2026-08-06）。`model/set` 与 `session/set_config_option` 在写入前校验模型 id：可用列表精确匹配或已配置内置 provider 的 `provider/model` 形态，未知 id 返回 `invalidParams`。
