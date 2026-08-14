# delegationDepth 与 descriptor 持久化

## Description

按 `tasks/spec/spec-016-subagent-scope-alignment.md` 切片 5 实施：持久化 `delegationDepth`（父 depth+1，resume 不归零，`maxDepth` 默认 3 超限拒绝派发）；策略快照（信任目录 + hooks 指纹）入 descriptor；`session.LaneConfig` 扩展（Persona/ToolFilter/MaxDepth/DelegationDepth/Inherit）+ 新增 v4 metadata entry 类型 `descriptor`（model-hidden、投影跳过、export/import 保留），冷恢复从 lane.config + descriptor 重建组成。

## Acceptance Criteria

- [ ] delegationDepth 随子代理会话持久化，resume 不归零；maxDepth（默认 3）超限派发在工具层拒绝并返回错误结果
- [ ] 派发时信任目录快照与 hooks 集指纹写入 descriptor
- [ ] `session.LaneConfig` 扩展字段完成；sessionstore lane_config 读写兼容
- [ ] v4 `descriptor` entry 类型：投影跳过（不生成消息）、export/import 保留、TUI/ACP 不渲染
- [ ] 冷恢复：子代理会话从 store 加载重建组成（persona/toolFilter/depth/继承事实）
- [ ] 单测：深度超限、resume 不归零、descriptor 重建、投影跳过

## Dependencies

issue-047、issue-048（depth 随 fork/spawn 派发）。

## Type

backend

## Priority

high
