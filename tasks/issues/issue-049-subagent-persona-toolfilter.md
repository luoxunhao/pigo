# persona 继承与 toolFilter

## Description

按 `tasks/spec/spec-016-subagent-scope-alignment.md` 切片 4 实施：persona 段（默认继承父 base instruction，skill body / plugin spec.SystemPrompt / task `persona` 参数作为 shadow 覆盖）与 toolFilter（scope 继承工具集 → allow 白名单交集 → deny 剔除；skill `allowed-tools` 与 plugin `Tools` 保留为 allow 语义，新增 deny 支持）。

## Acceptance Criteria

- [ ] 子代理默认继承父 persona（base instruction），声明覆盖时 shadow（对齐 DSH persona section shadow）
- [ ] 工具集视图 = 父 scope 继承集 → allow（白名单交集）→ deny（剔除）
- [ ] skill frontmatter 新增 `tool-deny`；plugin spec 新增 `ToolFilter{Allow,Deny}`；task 工具 `tools` 参数支持 allow/deny
- [ ] 现有 `allowed-tools` / `Tools` 语义不变（allow）
- [ ] 单测：persona 继承与 shadow、allow 交集、deny 剔除、空 allow=全继承

## Dependencies

issue-046（scope 分层）、issue-047。

## Type

backend

## Priority

high
