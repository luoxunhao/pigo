# 桌面端端到端验收

## Description

手动验证 A1 进程模型与权限/工具链路：新建 ams 会话、bash pending 卡、四种审批、allow always 重启生效、多目录 sourceFolders 可访问。对应 PRD Success Metrics。

## Acceptance Criteria

- [ ] 新建 `E:\project\ams` 会话后 system prompt 工作目录为 ams
- [ ] bash 工具调用在审批前显示 pending 工具卡
- [ ] allow once 执行成功，allow always 重启后不再弹审批，reject 正确拦截
- [ ] 被拦截工具在 UI 上可见
- [ ] 多目录项目恢复会话后 read/write/edit 可访问全部 sourceFolders
- [ ] `conversations.json` 中 pigo 记录全部为 `backend='project'`

## Dependencies

Issue #1 至 Issue #14

## Type

infra

## Priority

medium
