# D-01 — pigo/* 扩展协议形态

**Type:** prototype（HITL）

**Question:** 定义 `pigo/*` 扩展协议的具体形态：方法/通知命名（`pigo/event`、`pigo/command`、`pigo/rewind` 等）、envelope、initialize 的 `_meta` 能力键、`pigo/event` 的载荷形状；产出与标准 ACP 不冲突的草案供评审。

**Blocked by:** None — can start immediately

**Status:** ready-for-agent

## Resolution

已解决（2026-08-05）。协议形态定为：通知 `pigo/event`（载荷 `{sessionId, event:{type,...}}`，raw agentcore 事件）；请求 `pigo/command`（载荷 `{sessionId, command:"/name args"}`，响应 `{text, notifications}`）；`pigo/status`；rewind/fork/tree/goal/btw/dream/remotecontrol 作为方法面保留，逻辑随 07/08 迁移。能力键 `pigo.event`/`pigo.command`/`pigo.status` 声明于 initialize `_meta`。
