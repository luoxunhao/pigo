# Prompt variables 插值

## Description

按 `tasks/spec/spec-018-contextbuild-registry-dsh-alignment.md` 切片 3 实施：variables 渲染器（`{{name}}` 插值，strict 语义：未知/未定义变量报错）与内置变量 `{{model}}`（当前模型 id）、`{{cwd}}`（会话工作目录）；开放第三方注册与同名 shadow（scope 感知）。

## Acceptance Criteria

- [ ] 渲染器：sections/contexts 文本中的 `{{name}}` 插值；未知变量、残缺分组报错（对齐 DSH 的 interpolate 语义）
- [ ] 内置 `model` / `cwd`（随会话状态解析：model 切换后下一轮输出即更新）
- [ ] `RegisterVariable` 开放注册 + 同名 shadow（scope 链）
- [ ] 指纹只含静态骨架；变量化 section 的指纹不随变量值变化
- [ ] 单测：插值、未知变量报错、shadow、模型切换时效、指纹稳定性

## Dependencies

issue-061、issue-062。

## Type

backend

## Priority

medium
