# 07 — TUI slash 与会话树迁移

**Type:** task

**What to build:** `/model`、`/think`、`/compact`、`/trust`、`/rewind`、`/fork`、`/tree`、`/status` 等命令经 `pigo/*` 扩展执行，TUI 渲染不变。

**Blocked by:** 05 pigo/* 扩展通道、06 TUI 聊天主路径迁移

**Status:** ready-for-agent

- [ ] 常用 slash 命令经 `pigo/command` 执行并正确渲染
- [ ] rewind/fork/tree/status 行为与迁移前一致
- [ ] 每迁移一个命令，直连实现同步移除或标记过渡

## Resolution

已解决（2026-08-05）。`/model`、`/think`、`/trust`、`/status`、`/session`、`/compact`（复用 compaction 管线）、`/rewind`（历史截断 + 文件快照恢复）、`/fork`（克隆会话）、`/tree`（会话树渲染）经 pigo/command 实现并测试；goal/btw/dream/remote-control 随 08 迁移。
