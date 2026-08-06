# 08 — 集成验收与文档闭环

**Type:** backend

**What to build:** 端到端验证 配置→菜单→discover→model/set→prompt 全链路，并确认扩展行为不变；更新 spec/ADR/tickets 为 done。

**Blocked by:** #1, #2, #3, #4, #5, #6, #7

**Status:** done

- [ ] ACP wire 测试覆盖配置读取、菜单、discover、model/set、prompt
- [ ] 扩展行为（close、pigo/*、messages 字段、startup 元数据、命令超集）保持现状
- [ ] `go build ./...` 与相关包测试全绿
- [ ] spec/ADR/本地 tickets 更新为 done 并提交

**Spec:** 2026-08-06 模型配置重构设计（grill 确认）

## Resolution

已解决（2026-08-06）。ACP wire/单元/集成测试覆盖 配置→菜单→discover→model/set→prompt；ADR/Spec 更新，tickets 全部 done；相关包测试与 build 全绿。
