# 10 - Trust 管理

**What to build:** HTTP 客户端可以查看、写入和删除目录 trust 决策；`allow_always` 权限选择可以持久化为 Trusted。

**Blocked by:** 02 - 认证、错误模型与请求追踪

**Status:** ready-for-agent

- [ ] `GET /api/v1/permission/trust` 返回已保存决策
- [ ] `POST /api/v1/permission/trust` 支持 `trusted`、`untrusted`、`undecided`
- [ ] `DELETE /api/v1/permission/trust` 支持按路径删除
- [ ] `allow_always` 权限选择写入 Trusted
- [ ] Untrusted 只能通过 trust API 管理
- [ ] 集成测试覆盖列表、写入、删除和持久化
