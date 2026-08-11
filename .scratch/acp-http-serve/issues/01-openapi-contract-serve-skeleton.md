# 01 - OpenAPI 契约与 serve 骨架

**What to build:** 建立 serve 的最小可运行骨架：`pigo serve` 可以启动，健康检查、OpenAPI 规范和文档端点可用；OpenAPI 成为唯一契约来源，代码生成与 CI 一致性检查就位。

**Blocked by:** None - can start immediately

**Status:** ready-for-agent

- [ ] `pigo serve` 可启动并响应 `/api/v1/health`
- [ ] OpenAPI 规范是权威契约，生成的 Go 类型可编译
- [ ] `/api/v1/openapi.json` 与 `/api/v1/doc` 可访问
- [ ] CI 能检查生成产物无 diff
- [ ] 集成测试覆盖健康检查、OpenAPI 与文档端点
