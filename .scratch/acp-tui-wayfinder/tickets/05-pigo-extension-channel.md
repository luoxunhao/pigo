# 05 — pigo/* 扩展通道

**Type:** task

**What to build:** 客户端可以订阅 `pigo/event` 获得完整 agentcore 事件流（compaction、subagent progress、telemetry、rewind、goal、btw），可以通过 `pigo/command` 执行 slash 命令；rewind/fork/tree/status/goal/btw/dream/remotecontrol 的扩展方法面可用并复用现有逻辑。

**Blocked by:** 03 ACP 会话生命周期与 prompt 流、04 权限 broker 与 model/set、D-01 pigo/* 扩展协议形态

**Status:** ready-for-agent

- [ ] `pigo/event` 原样转发 agentcore 事件，外部客户端不订阅时只收标准通道
- [ ] `pigo/command` 执行 slash 命令并返回结果与通知
- [ ] rewind/fork/tree/status/goal/btw/dream/remotecontrol 扩展方法面可用

## Resolution

已解决（2026-08-05）。`pigo/event` 原样转发 agentcore 事件；`pigo/command` 实现 model/think/trust/status；rewind/fork/tree/goal/btw/dream/remotecontrol 方法面已路由并返回结构化未实现错误（完整逻辑随 07/08 迁移）；单测全绿。
