# outputSchema 结构化输出

## Description

按 `tasks/spec/spec-016-subagent-scope-alignment.md` 切片 6 实施：子代理 scope 内挂载 `structured_output` capture 工具（schema = 请求 outputSchema）、提示段（order 190，"完成时调用一次"）、终态 guard（capture 成功后拦截其余工具调用）；结果 JSON Schema 校验后作为结构化结果返回父。接线：task 工具 `output_schema` 参数、plugin spec `OutputSchema`、skill frontmatter `output-schema`。

## Acceptance Criteria

- [ ] 子代理 scope 内挂载 `structured_output` 工具（参数 schema = outputSchema）+ 提示段 + 终态 guard
- [ ] 结果按 JSON Schema 校验；校验失败以错误结果呈现（父可重试）
- [ ] 结构化结果经工具结果通道返回父（与文本结果区分）
- [ ] 三处接线完成（task / plugin / skill）
- [ ] 单测：schema 校验、guard 拦截、无 outputSchema 时行为不变

## Dependencies

issue-046、issue-047。

## Type

backend

## Priority

medium
