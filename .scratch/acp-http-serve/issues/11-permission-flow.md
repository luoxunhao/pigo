# 11 - 权限审批流

**What to build:** serve 通过 SSE 推送权限请求，客户端通过 REST 回包；支持三个选项并正确映射到 trust。

**Blocked by:** 07 - SSE 事件流与事件存储, 10 - Trust 管理

**Status:** ready-for-agent

- [x] `permission.asked` 事件携带 `permissionId`、`sessionId`、`toolCall`、`options`
- [x] `POST /api/v1/session/{id}/permissions/{permissionId}/reply` 接收 `optionId`
- [x] 支持 `allow_once`、`allow_always`、`reject_once`
- [x] `allow_always` 持久化为 Trusted
- [x] `reject_once` 不落盘
- [x] 集成测试覆盖三个选项和 trust 映射
