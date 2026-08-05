# 04 SessionRouteService 降级矩阵

**Type:** grilling

## Question

pi-web `SessionRouteService` 的每个方法（list/start/messages/status/prompt/abort/stop/archive/restore/cleanup/tree/notifications/shell/commands/setModel/setThinkingLevel/attachments 等）在 pigo backend 下是“实现 / 隐藏 / 返回不支持”中的哪一种？逐项列矩阵，避免 UI 按钮点了没反应。

## Status

resolved

## Resolution

4.1: pigo backend 下隐藏归档/清理/通知/跨机器 UI；删除保留（`session/delete`）；其余不支持的方法返回结构化“不支持”。
4.2: pi-web 自己的终端照常工作；session `shell()` 走 `pigo/command`，无法映射时禁用。
