# 进程隔离握手与验收回归

## Description

按 `tasks/spec/spec-016-subagent-scope-alignment.md` 切片 7 实施：`--subagent-rpc` 握手扩展（可序列化配方跨进程：fork 种子消息、persona、toolFilter、delegationDepth、maxDepth、策略快照、lane.config；进程内注册不跨进程），以及三层验收（行为测试矩阵 / 现有子代理兼容回归 / 全量构建）收尾。

## Acceptance Criteria

- [ ] subagent RPC 握手携带可序列化配方；fork 种子消息随握手序列化
- [ ] 进程内注册（transforms/projectors/自定义工具）确认不跨进程（维持 AGENTS.md 约定）
- [ ] 行为测试矩阵全绿：fork 种子截断与持久化、spawn 零消息、persona 继承/shadow、toolFilter 交集/剔除、depth 超限与 resume 不归零、outputSchema 校验与 guard、descriptor 重建
- [ ] 兼容回归全绿：现有 task/skill/plugin 子代理默认行为不变（spawn、allowed-tools、hooks 传播），旧测试无破坏
- [ ] `go build ./...` 与相关包 `go test` 通过
- [ ] REPL / TUI / headless / serve / ACP 子代理路径回归（skill 子代理、plugin 子代理、task 并行 fan-out）

## Dependencies

issue-046 ~ issue-051 全部完成。

## Type

backend

## Priority

high
