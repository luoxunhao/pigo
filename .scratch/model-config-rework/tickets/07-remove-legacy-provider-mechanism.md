# 07 — 移除旧 provider 机制

**Type:** backend

**What to build:** 移除 `[[providers]]`、`pigo/providers/*`、`CustomProviders` 注册表与 `pigo.providers` 能力声明，清理相关测试和文档。

**Blocked by:** #1, #3, #5

**Status:** done

- [ ] 配置模型不再包含 `[[providers]]`
- [ ] `pigo/providers/upsert|list|delete` 从 ACP 方法面移除
- [ ] `CustomProviders` 相关代码删除
- [ ] `initialize._meta` 移除 `pigo.providers`
- [ ] 旧测试与文档同步清理

**Spec:** 2026-08-06 模型配置重构设计（grill 确认）

## Resolution

已解决（2026-08-06）。移除 `[[providers]]`、`pigo/providers/*`、`CustomProviders` 与 `pigo.providers` 能力声明；相关测试和文档已清理。
