# 子代理接入 contextbuild 管线

## Description

按 `tasks/spec/spec-016-subagent-scope-alignment.md` 切片 2 实施：`runtime.SubAgentSpec` 扩展（Inherit/Persona/ToolFilter/MaxDepth/OutputSchema），`subagent_child.go` 的 ChildSession 从手工 `AgentContext` 组装切换到 `BuildSessionContext + BuildProviderContext`（经 scope 合并的 registry + 继承的 prompt 构建输入），删除旧手工路径（`stream_response.go` fallback 在子代理路径的依赖）。

## Acceptance Criteria

- [ ] `SubAgentSpec` 新增 Inherit / Persona / ToolFilter / MaxDepth / OutputSchema 字段
- [ ] ChildSession 的每轮请求经 `BuildProviderContext` 组装（scope 感知），不再手工构造 `AgentContext{SystemPrompt, Messages, Tools}`
- [ ] 子代理 system prompt 经 scope 继承父构建输入（base + AGENTS.md + skills + env），persona 默认继承可覆盖
- [ ] `stream_response.go` 旧手工 fallback 在子代理路径不再可达（保留 goal/btw 现状直至其独立迁移）
- [ ] skill / plugin / task 三类子代理运行回归（现有行为不变）

## Dependencies

issue-046（scope 分层）。

## Type

backend

## Priority

high
