# 07 — ACP 生命周期集成测试与安全收口

**Type:** backend

**What to build:** 端到端验证 discover → upsert → 默认配置 → model/set → prompt 全链路，并确认所有响应与错误路径都不泄漏 API Key。

**Blocked by:** #2, #3, #5, #6

**Status:** ready-for-agent

- [ ] ACP wire 测试覆盖 discover/upsert/list/delete
- [ ] custom 模型设置后下一次 prompt 使用注册表端点
- [ ] 默认 custom 模型启动成功/失败场景有测试
- [ ] 所有响应与错误日志无明文 key

**Spec:** tasks/spec-custom-provider-registry.md（Testing / Security）
