# 内置 sections 迁移与 persona 文案

## Description

按 `tasks/spec/spec-018-contextbuild-registry-dsh-alignment.md` 切片 2 实施：8 个内置 section 注册化——`pigo:identity`(-100) / `deployment:persona`(0) / `tools`(100) / `guidelines`(110) / `append`(120) / `project_instructions`(130) / `skills`(140) / `environment`(150)；persona 换 DSH 文案（"You are a coding agent powered by the {{model}} model. Your working directory is {{cwd}}."），移除 "You are pigo" 身份；指纹缓存改静态骨架。

## Acceptance Criteria

- [ ] 8 个内置 section 注册化，输出顺序 = 上表 order（persona 文案变化为 DSH 风格，identity 保留 pigo 品牌）
- [ ] todo 指南并入 persona 或独立 section（实施时定，order 5）
- [ ] 指纹缓存只含静态骨架（sections 结构 + 静态文本）；`{{model}}`/`{{cwd}}` 渲染期插值、不参与指纹
- [ ] `header.SystemPrompt` 写回保留（展示兼容）
- [ ] `BuildSystemPrompt` 签名兼容（默认注册 = 内置 sections）
- [ ] 单测：输出顺序、persona 文案、模型切换后 persona 更新（指纹不变）、golden 快照更新

## Dependencies

issue-061（Registry 分层）。

## Type

backend

## Priority

high
