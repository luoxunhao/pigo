# 09 - 命令列表与执行

**What to build:** HTTP 客户端可以查看可用 slash 命令并执行命令；hybrid 命令会等待 follow-up prompt 完成后返回。

**Blocked by:** 08 - Prompt 同步/异步、队列与取消

**Status:** ready-for-agent

- [x] `GET /api/v1/commands` 返回 `name`、`description`、`input`
- [x] `POST /api/v1/session/{id}/command` 返回 `messageId`、`stopReason`、`text`、`usage`
- [x] hybrid 命令等待 follow-up prompt 完成后返回
- [x] 未知 slash 命令在 ACP 适配层按普通文本处理
- [x] 集成测试覆盖命令列表、action 命令和 hybrid 命令
