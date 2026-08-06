# 02 — pigo/models/discover 远程模型发现

**Type:** backend

**What to build:** 客户端传入 `{ name, baseUrl, apiKey, protocol }` 后，pigo 按协议请求模型列表端点，返回 `{ providerId, providerName, models }`，且调用不写配置。

**Blocked by:** #1

**Status:** done

- [ ] 支持 openai/responses/anthropic/gemini 四种端点映射
- [ ] 认证头按协议正确（Bearer / x-api-key+version / x-goog-api-key）
- [ ] 响应为 `{ providerId, providerName, models }`，错误信息不含 key
- [ ] 无副作用；`initialize` 的 `_meta` 声明 `pigo.models.discover`
- [ ] ACP wire 测试覆盖请求、响应与失败

**Spec:** tasks/spec-custom-provider-registry.md（ACP Methods / Endpoint Mapping and Auth）

## Resolution

已解决（2026-08-06）。`pigo/models/discover` 支持四种协议端点映射与标准认证头，返回 providerId/providerName/models，无副作用，并在 `initialize._meta` 声明能力；ACP wire 测试覆盖。
