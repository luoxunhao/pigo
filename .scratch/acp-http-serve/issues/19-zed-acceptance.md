# 19 - Zed 手动验收

**What to build:** 以 Zed 为唯一外部客户端完成最终验收，确认标准 ACP 可用且无任何非标准残留。

**Blocked by:** 15 - ACP 适配层重写, 16 - ACP 事件映射与 load 回放, 17 - TUI/REPL 迁移, 18 - Headless 迁移与 stream-json 兼容

**Status:** ready-for-agent

- [x] Zed 配置改为 `["acp"]`
- [ ] 手动验证对话、工具调用、权限确认、取消和历史恢复
- [ ] 确认 ACP 方法面只包含标准方法
- [ ] 确认 ACP 通知只包含标准 `session/update` 和 `session/request_permission`
- [ ] 记录 Zed 验收结果
