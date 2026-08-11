# 12 - Config / Providers / Models API

**What to build:** HTTP 客户端可以管理全局配置、provider、模型列表、远程模型发现和模型连通性测试。

**Blocked by:** 02 - 认证、错误模型与请求追踪

**Status:** ready-for-agent

- [ ] `GET` / `PATCH` 全局配置可用
- [ ] `GET /api/v1/config/providers` 返回 provider 分组模型
- [ ] `PUT` / `DELETE` provider 可 upsert 和删除
- [ ] `POST /api/v1/config/providers/discover` 可发现远程模型
- [ ] `POST /api/v1/config/providers/test` 可测试模型
- [ ] 任何响应都不包含明文 API key
- [ ] 集成测试覆盖配置读写、发现、测试和密钥安全
