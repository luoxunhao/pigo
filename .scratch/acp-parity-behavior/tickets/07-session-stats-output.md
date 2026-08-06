# 07 — /session 统计输出

**Type:** task

**What to build:** 用户执行 `/session` 时看到会话 id、会话文件、消息数、可用 token 统计；cost 有值时也显示。`/status` 继续提供原有运行时状态。

**Blocked by:** None — can start immediately

**Status:** done

- [ ] `/session` 输出 session id、session file、messages
- [ ] token 统计聚合已有 Usage 数据，无数据时不输出错误
- [ ] cost 有值时显示，无值时省略
- [ ] `/status` 行为不变
- [ ] 覆盖统计输出与缺失数据场景的测试

## Resolution

已解决（2026-08-06）。`/session` 输出 session id、session file、messages 与聚合 Usage 的 token 统计；无 cost 数据时省略；`/status` 保持原样。
