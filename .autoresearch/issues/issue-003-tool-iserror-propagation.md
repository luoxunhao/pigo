# 统一工具失败语义透传 IsError

## Description

工具返回“错误文本 + nil error”时，executor 仍要正确标记失败，避免 `edit`、`bash_output` 等失败被模型当成成功。

## Acceptance Criteria

- [ ] `AgentToolResult` 携带 `IsError`
- [ ] `errorResult` 设置 `IsError=true`
- [ ] executor 在 Go error 为 nil 时也透传 `res.IsError`
- [ ] `edit` / `bash_output` 等错误文本结果最终 `isError=true`
- [ ] 对应单元测试通过

## Dependencies

None

## Type

backend

## Priority

high
