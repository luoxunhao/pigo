# 多项目并发会话隔离集成测试

## Description

覆盖两个项目会话交替与并发运行时，system prompt、文件工具根、eventMapper cwd、slash registry 互不污染。对应 PRD US-013 / FR-3、FR-4、FR-5、FR-7。

## Acceptance Criteria

- [ ] 项目 A 与项目 B 的 system prompt 互不覆盖
- [ ] 项目 A 的工具根与 eventMapper cwd 不因项目 B 的活动改变
- [ ] 项目 A 的 slash registry 不加载项目 B 的模板
- [ ] 集成测试覆盖“交替会话”与“并发运行”两种场景
- [ ] 对应 Go 集成测试通过

## Dependencies

Issue #016、#017、#018、#019、#020、#021

## Type

backend

## Priority

medium
