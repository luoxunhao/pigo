# D-04 — remote control 的 ACP 归属

**Type:** grilling（HITL）

**Question:** `/remote-control` 走 ACP 时，HTTP/WS 服务器归属 client 侧还是 server 侧？远程输入注入与审批如何路由回 ACP 会话？

**Blocked by:** None — can start immediately

**Status:** ready-for-agent

## Resolution

已解决（2026-08-05）。HTTP/WS 服务器归属 ACP server 侧（后端）；远程输入注入与远程审批经会话级扩展方法与 permission broker 路由回 ACP 会话，客户端（TUI）只作为 ACP 客户端参与。
