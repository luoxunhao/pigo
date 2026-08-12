# 07 - SSE 事件流与事件存储

**What to build:** serve 提供可重放的标准 SSE 事件流，支持事件序号、过滤、保留窗口和游标过期错误。

**Blocked by:** 04 - 会话加载、关闭、删除与状态

**Status:** ready-for-agent

- [x] `GET /api/v1/events` 使用标准 `text/event-stream`
- [x] 事件 envelope 为 `{ id, type, data }`
- [x] 支持 `after`、`directory`、`sessionId`、`types` 过滤
- [x] 事件保留窗口默认 10000 条或 24 小时
- [x] 游标过期返回 `EVENT_CURSOR_GONE`
- [x] 集成测试覆盖重放、过滤和保留窗口
