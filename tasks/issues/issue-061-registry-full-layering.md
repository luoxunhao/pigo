# Registry 完整分层（sections/contexts/variables/tools）

## Description

按 `tasks/spec/spec-018-contextbuild-registry-dsh-alignment.md` 切片 1 实施（承接 issue-046 的 scope 分层并扩展）：`contextbuild.Registry` 一次扩展为完整分层注册表——`RegisterSection`（order/complete/suppress）、`RegisterContext`（动态段）、`RegisterVariable`（{{name}}）、`RegisterTools`（schema provider）加入现有 `RegisterTransform`/`RegisterEntryProjector`；全部 parent 链 + 按名 shadow；子 scope 释放不影响父。

## Acceptance Criteria

- [ ] `RegisterSection(name, order, text)`：同 scope 重复拒绝；子 scope 按名 shadow 父；`complete=true` 整段替换（多 complete 拒绝）；suppress 抑制动态 context 块
- [ ] `RegisterContext(name, order, text)`：每请求渲染进请求副本（不进历史）
- [ ] `RegisterVariable(name, provider)`：插值 `{{name}}`，未知变量报错；同名 shadow
- [ ] `RegisterTools(provider)`：工具 schema 提供者（保留现有工具路径）
- [ ] 渲染管线：sections 按 order 排序 → shadow 合并 → complete/suppress 应用 → contexts 渲染 → variables 插值
- [ ] 与 subagent scope（issue-046~052）同一套 parent 链：子代理 scope 继承父的 sections/contexts/variables/tools/transforms
- [ ] 单测：order/shadow/complete/suppress/变量/scope 链合并

## Dependencies

承接 issue-046（scope 分层基础设施，其范围并入本卡）。

## Type

backend

## Priority

high
