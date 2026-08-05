# 08 — TUI remote control 与 dream 迁移

**Type:** task

**What to build:** `/remote-control` 的镜像、远程审批、远程输入注入与 dream 流程全部走扩展通道；手机端体验与迁移前一致。

**Blocked by:** 05 pigo/* 扩展通道、06 TUI 聊天主路径迁移、D-04 remote control 的 ACP 归属

**Status:** ready-for-agent

- [ ] remote control 生命周期经扩展方法启动/停止
- [ ] 远程审批经 permission broker 路由，与本地决策等价
- [ ] 远程输入注入回到 ACP 会话
- [ ] dream 运行经扩展方法复用现有管线

## Resolution

已解决（2026-08-05）。RemoteBridge 接入 ACP 服务端：/remote-control 启停、远程输入泵注入会话 prompt、远程审批经 broker hook 路由（与本地决策等价）；/dream 经现有 dream 管线执行（DreamConfig 注入）；单测覆盖远程确认、dream 未配置错误、bridge 生命周期。
