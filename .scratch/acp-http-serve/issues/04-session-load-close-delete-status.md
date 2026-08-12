# 04 - 会话加载、关闭、删除与状态

**What to build:** HTTP 客户端可以加载会话并拿到消息窗口，可以释放运行态、删除会话，并查询会话当前状态。

**Blocked by:** 03 - 会话创建与列表

**Status:** ready-for-agent

- [x] load 返回会话信息、configOptions 和当前消息窗口
- [x] load 响应包含 `hasMore` 与 `nextCursor`
- [x] close 返回 204，释放运行态但保留历史
- [x] delete 对不存在会话也返回 204，保持幂等
- [x] status 返回 `sessionId`、`status`、`model`、`mode`、`thinkingLevel`、`queuedCount`、`contextUsage`
- [x] 集成测试覆盖 load、close、delete 和 status
