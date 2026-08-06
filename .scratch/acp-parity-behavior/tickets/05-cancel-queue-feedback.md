# 05 — 取消队列反馈

**Type:** task

**What to build:** 用户取消时，如果存在尚未投递的排队消息，客户端会收到 “Cleared queued prompts.” 文本；空队列取消保持静默。

**Blocked by:** None — can start immediately

**Status:** ready-for-agent

- [ ] 非空队列被取消时发送 `agent_message_chunk` “Cleared queued prompts.”
- [ ] 空队列取消不发送额外文本
- [ ] 排队 prompt 的 stopReason 仍为 cancelled
- [ ] 覆盖排队与空队列取消的测试
