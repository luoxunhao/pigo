# 05 - 会话配置与 mode 切换

**What to build:** HTTP 客户端可以通过统一配置接口更新模型、思考强度和 mode，并通过语义化 mode 接口快速切换。

**Blocked by:** 04 - 会话加载、关闭、删除与状态

**Status:** ready-for-agent

- [ ] `PATCH /api/v1/session/{id}` 支持 `model`、`thinkingLevel`、`mode`
- [ ] `POST /api/v1/session/{id}/mode` 作为 mode 语义化别名
- [ ] 两个接口的响应都返回完整 `configOptions`
- [ ] 未知 model、thinkingLevel、mode 返回明确错误
- [ ] 集成测试覆盖配置更新和 mode 切换
