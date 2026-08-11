# 06 - 历史消息分页

**What to build:** HTTP 客户端可以按 `before` 游标和 `limit` 分页读取会话历史，历史来自持久化 transcript，而不是事件日志。

**Blocked by:** 04 - 会话加载、关闭、删除与状态

**Status:** ready-for-agent

- [ ] `GET /api/v1/session/{id}/messages` 返回领域消息结构
- [ ] 支持 `before`、`limit`、`hasMore`、`nextCursor`
- [ ] 历史数据来自 sessionstore，事件日志不承担历史事实来源
- [ ] 集成测试覆盖首屏加载与向前翻页
