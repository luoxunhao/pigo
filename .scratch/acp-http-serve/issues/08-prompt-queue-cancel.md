# 08 - Prompt 同步/异步、队列与取消

**What to build:** HTTP 客户端可以同步或异步发送 prompt，会话忙碌时进入队列，并可取消当前 turn 和清空队列。

**Blocked by:** 07 - SSE 事件流与事件存储

**Status:** ready-for-agent

- [x] 同步 prompt 返回 `messageId`、`stopReason`、`usage`
- [x] 异步 prompt 返回 202 和 `messageId`、`accepted`
- [x] prompt 请求体使用 ContentBlock 数组
- [x] 忙碌时进入队列，默认上限 100，超出返回 `QUEUE_FULL`
- [x] cancel 返回 204，取消当前 turn 并清空队列
- [x] 事件流发出 `queue.updated` 与 `session.status`
- [x] 集成测试覆盖同步、异步、队列和取消
