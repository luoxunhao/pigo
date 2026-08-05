# D-02 — TUI 桥接 seam 形态

**Type:** prototype（HITL）

**Question:** TUI 渲染层与 ACP 客户端之间的事件总线/状态模型如何设计，使 TUI 渲染层尽量不动？用最小 stub 原型验证 chat、工具卡、权限、slash 命令经 ACP 往返的可行性。

**Blocked by:** None — can start immediately

**Status:** ready-for-agent

## Resolution

已解决（2026-08-05）。桥接 seam 为 ACP 客户端 + 事件转发器：TUI 通过 in-process transport 连接同进程 ACP server；客户端提供 NewSession/Prompt/Cancel/SetModel/Command，权限经 broker 的 request_permission 往返；TUI 渲染层消费现有消息类型，事件来源从直连替换为 ACP 事件流与 pigo/event 通道。最小 stub 验证通过（03-05 集成测试覆盖）。

后续清理（2026-08-05）：验证用的紧凑 stub 聊天模型（cpChatModel）已删除，富 TUI 的 ACP 桥接路径（startACPRun/cpToTea/updateToTea）保留。
