# 04 — pigo/models/discover 候选形状

**Type:** backend

**What to build:** `pigo/models/discover` 作为配置阶段候选来源，无副作用；ash-workbench 用返回值写入 `models`。

**Blocked by:** #1

**Status:** done

- [ ] 请求 `{ provider?, baseUrl, apiKey?, protocol }`
- [ ] 响应 `{ provider, baseUrl, protocol, models }`
- [ ] 候选模型含 `modelId/name/contextWindow/maxTokens/thinkingLevels/supportsImages`，缺失为 null
- [ ] 未传 provider 时生成 `custom-<host-slug>`
- [ ] 不回显 apiKey，错误不泄漏 key
- [ ] 覆盖四协议映射与缺失元数据的测试

**Spec:** 2026-08-06 模型配置重构设计（grill 确认）

## Resolution

已解决（2026-08-06）。`pigo/models/discover` 返回 `provider/baseUrl/protocol/models`，候选模型带元数据且缺失为 null；未传 provider 时生成 `custom-<host-slug>`。
