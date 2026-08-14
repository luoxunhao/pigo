# fork/spawn 上下文语义

## Description

按 `tasks/spec/spec-016-subagent-scope-alignment.md` 切片 3 实施：spawn（默认，零父消息，兼容 spec-013）与 fork（显式启用，子代理会话以父会话投影历史为种子，截断尾部未闭合 tool-call 回合，种子与子代理新消息一起持久化——对齐 DSH `completedTurnPrefix` + `seedDescriptorTurn`）。接线：`task` 工具 `inherit` 参数、skill frontmatter `fork` 字段、plugin spec `Inherit`。

## Acceptance Criteria

- [ ] fork 种子 = 父会话当前 leaf 投影历史，截断到最后一个完整回合（尾部未闭合 tool call 排除）
- [ ] fork 种子持久化到子代理会话（对齐 seed 语义），子代理重启后继承部分可恢复
- [ ] spawn 为默认（task/skill/plugin 未声明时行为与现状一致）
- [ ] `task` 工具、skill frontmatter、plugin manifest 三处接线完成
- [ ] 单测：种子截断（未闭合回合）、spawn 零消息、fork 持久化与恢复

## Dependencies

issue-047（子代理接入 contextbuild）。

## Type

backend

## Priority

high
