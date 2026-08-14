# scope 分层基础设施

## Description

按 `tasks/spec/spec-016-subagent-scope-alignment.md` 切片 1 实施：`contextbuild.Registry` 增加 parent 链分层（子代理 scope 继承父层注册并可 shadow），工具注册表支持 parent 链视图；不引入服务注入。决策源：grilling 共识第 2/3 项与 `docs/adr/0012-scope-hierarchy-for-subagents.md`。

## Acceptance Criteria

- [ ] `contextbuild.Registry` 支持 `NewRegistry(parent)`；`RegisterTransform/RegisterEntryProjector` 注册到本层；合并视图按 parent 链展开（父层先、本层按名 shadow）
- [ ] `ApplyTransforms` 消费合并后的顺序链（父 transforms 先于子）
- [ ] 工具注册表支持 parent 链：子代理视图 = 父继承集 + 本层注册
- [ ] scope 销毁语义：子 scope 释放不影响父 scope 注册
- [ ] 单测覆盖：继承、shadow、同层重复注册拒绝、父层 shadow 后子层可见性

## Dependencies

spec-016 切片 1；无阻塞（contextbuild 既有 Registry 之上扩展）。

## Type

backend

## Priority

high
