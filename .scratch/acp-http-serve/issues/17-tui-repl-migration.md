# 17 - TUI/REPL 迁移

**What to build:** TUI 和 REPL 通过进程内 HTTP client 消费 serve，不再直连 core。

**Blocked by:** 08 - Prompt 同步/异步、队列与取消, 09 - 命令列表与执行, 11 - 权限审批流, 12 - Config / Providers / Models API, 14 - Modes HTTP 与 ACP 数据源

**Status:** ready-for-agent

- [x] TUI/REPL 启动时内部启动 serve
- [x] 对话、事件、命令、权限、配置和 mode 都通过 HTTP client
- [x] TUI/REPL 不再直连 core
- [x] 现有交互行为保持兼容
- [x] 集成测试覆盖 TUI/REPL 的 serve client 路径
