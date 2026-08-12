# 02 - 认证、错误模型与请求追踪

**What to build:** serve 具备可用的安全与错误契约：loopback 默认免密、非 loopback 强制密码、Basic Auth、统一错误 envelope、请求 ID 与 CORS 白名单。

**Blocked by:** 01 - OpenAPI 契约与 serve 骨架

**Status:** ready-for-agent

- [x] loopback 且未配置密码时可以访问
- [x] 非 loopback 且未配置密码时拒绝启动
- [x] 配置密码后所有请求要求 Basic Auth
- [x] 错误响应统一为 `{ error: { code, message, details, requestId } }`
- [x] 响应头返回 `X-Request-Id`
- [x] CORS 白名单可通过配置控制
- [x] 集成测试覆盖认证、错误和 CORS
