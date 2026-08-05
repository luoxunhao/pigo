# 04 — 权限 broker 与 model/set

**Type:** task

**What to build:** 客户端可以在副作用工具调用时收到 request_permission（allow_once/reject_once/allow_always/reject_always），决策正确落到 trust；可以调用 model/set 切换会话模型，下一轮生效。

**Blocked by:** 03 ACP 会话生命周期与 prompt 流

**Status:** ready-for-agent

- [ ] 四个权限选项到 trust 决策的映射（含会话级信任与持久化 untrusted）
- [ ] 取消/超时按拒绝处理
- [ ] model/set 更新会话级 LiveConfig，fake provider 断言下一轮 model id

## Resolution

已解决（2026-08-05）。实现 ACPPermissionBroker：四选项 request_permission 映射 trust（allow_once/reject_once/allow_always/reject_always，含取消/超时拒绝）；model/set 与 /model 语义接入会话模型，下一轮生效；单测全绿。
