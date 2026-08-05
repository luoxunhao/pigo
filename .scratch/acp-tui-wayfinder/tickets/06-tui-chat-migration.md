# 06 — TUI 聊天主路径迁移

**Type:** task

**What to build:** 用户运行 `pigo` 时，TUI 通过 in-process transport 连接 ACP server，完成建会话、流式文本/思考/工具卡渲染、权限弹窗、取消、模型切换；直连路径暂留（expand），行为无可见回归。

**Blocked by:** 03 ACP 会话生命周期与 prompt 流、04 权限 broker 与 model/set、05 pigo/* 扩展通道、D-02 TUI 桥接 seam 形态

**Status:** ready-for-agent

- [ ] TUI 启动时进程内起 ACP server 并初始化 client
- [ ] chat 主路径（会话、prompt、流式渲染、权限、取消、切模型）经 ACP 工作
- [ ] 直连路径保留期间无行为回归（回归测试）

## Resolution

已解决（2026-08-05）。实现 ACP 客户端（initialize/new/load/prompt/cancel/model/command + 通知泵 + 权限处理）、进程内 server 装配（StartInProcess）、`--tui-acp` 门控的 ACP 聊天 TUI（流式文本/工具卡/权限按键/取消//model）；直连 TUI 保持默认且测试全绿；client 与 chat 模型单测通过。

后续清理（2026-08-05）：迁移期最小前端 cpChatModel 已删除，TUI 入口统一为完整 Model 的 ACP 会话路径（RunACP + withACPSession + ACP bridge）。
