# 08 — ACP parity 集成验收

**Type:** task

**What to build:** 全部 parity 行为改动通过 ACP wire 集成测试验证，同时确认 ADR 中标记为“扩展、不改”的行为没有被误改。

**Blocked by:** #1, #2, #3, #4, #5, #6, #7

**Status:** ready-for-agent

- [ ] wire 测试覆盖会话作用域、路径校验、配置面、模型校验
- [ ] wire 测试覆盖加载恢复、取消反馈、compact、session 统计
- [ ] 扩展行为（close、pigo/*、messages 字段、startup 元数据、命令超集）保持现状
- [ ] `go test ./internal/acp/...` 全绿
