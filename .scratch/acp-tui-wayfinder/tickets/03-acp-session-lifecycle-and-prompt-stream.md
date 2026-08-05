# 03 — ACP 会话生命周期与 prompt 流

**Type:** task

**What to build:** ACP 客户端可以完成 session/new、session/load、session/close、session/prompt、session/cancel；fake provider 下收到流式 session/update（文本、思考、工具卡、usage），运行中可取消，会话可持久化恢复。

**Blocked by:** 01 project-scoped 会话存储、02 ACP transport 与 initialize 握手

**Status:** ready-for-agent

- [ ] session/new/load/close/prompt/cancel 全部走通
- [ ] 标准 session/update 事件流形状正确（sessionUpdate 判别、content type、tool call/update、usage）
- [ ] prompt 后台执行，主循环响应 cancel，取消以 cancelled stop reason 结束
- [ ] 同 session prompt 串行化、close 后会话不可用
- [ ] session/load 恢复历史与系统提示，后续 prompt 基于恢复上下文

## Resolution

已解决（2026-08-05）。实现 session manager、RuntimeRunner（复用 runtime.RunHeadless）、event mapper（文本/思考增量、工具卡、usage）、session/new/load/close/prompt/cancel 与延迟响应；集成测试覆盖生命周期、事件流、取消、恢复、持久化。
