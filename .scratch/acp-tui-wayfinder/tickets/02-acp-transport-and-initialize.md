# 02 — ACP transport 与 initialize 握手

**Type:** task

**What to build:** ACP 客户端可以通过 in-process channel 或 stdio 连接 ACP server，完成 JSON-RPC 2.0 握手与 initialize，获得 v1 能力声明和 `pigo.*` 扩展声明；未知方法返回标准错误。

**Blocked by:** None — can start immediately

**Status:** ready-for-agent

- [ ] transport 抽象（send_request/send_notification/recv/send_response）与 request router
- [ ] in-process channel transport 与 stdio transport 行为一致
- [ ] initialize 返回协议版本、load/close 能力与 `pigo.*` 扩展声明
- [ ] 未知方法、非法 JSON、断线行为的标准错误与测试

## Resolution

已解决（2026-08-05）。实现 JSON-RPC 2.0 transport 抽象、in-process channel 对与 stdio transport、request router、initialize 握手（v1 + load/close + pigo 扩展声明）；`go test ./internal/acp` 覆盖。
