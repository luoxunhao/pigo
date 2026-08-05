# 09 — 行 REPL 迁移

**Type:** task

**What to build:** `--no-tui` 行 REPL 通过同一 ACP 客户端工作，chat、slash、会话恢复行为与迁移前一致。

**Blocked by:** 05 pigo/* 扩展通道、06 TUI 聊天主路径迁移、D-03 headless/REPL 收口范围

**Status:** ready-for-agent

- [ ] 行 REPL 经 in-process ACP client 完成 chat 与 slash
- [ ] 会话恢复行为一致

## Resolution

已解决（2026-08-05）。行 REPL 增加 ACP 模式（--acp 同时覆盖 TUI/REPL）：runACPInteractive 完成 prompt 流式输出、/command 执行、权限单键决策、会话恢复；集成测试通过；同时修复 Windows 下插件可执行文件发现（.exe）。
