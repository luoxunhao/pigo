# Failed 无 Started 时兜底创建工具卡

## Description

ash-workbench 在收到失败工具事件但工具卡不存在时，先创建工具卡再标记 error，并展示 `rawInput`（存在时）与错误文本。对应 PRD US-009 / FR-13。

## Acceptance Criteria

- [ ] Failed 事件到达且工具卡不存在时，先创建工具卡再标记 error
- [ ] 卡片展示 `rawInput`（存在时）与错误文本
- [ ] 不产生未处理的异常或空 UI
- [ ] 对应 vitest 单元测试通过

## Dependencies

Issue #7、Issue #8、Issue #9

## Type

ui

## Priority

medium
