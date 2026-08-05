# 10 — 收口：移除直连路径

**Type:** task

**What to build:** TUI/REPL 不再直接调用 agent core；ACP 成为唯一前端入口；删除过渡代码，跑全量回归并更新兼容测试。

**Blocked by:** 06 TUI 聊天主路径迁移、07 TUI slash 与会话树迁移、08 TUI remote control 与 dream 迁移、09 行 REPL 迁移、D-03 headless/REPL 收口范围

**Status:** ready-for-agent

- [ ] 直连路径全部移除，无遗留调用
- [ ] 全量回归绿（chat/slash/会话树/remote/dream/REPL）
- [ ] 文档与 spec 同步更新

## Partial progress（2026-08-05）

ACP 事件→TUI msg 桥（startACPRun/acpToTea/updateToTea）已实现并测试，完整 Model 已通过 withACPSession 绑定到 ACP 桥（startRunFn/interruptFn 经 ACP 客户端）。

剩余收口清单：
- [x] /model、/think 等 slash 命令在完整 TUI 下经 pigo/command 路由（installACPSlashCommands，/model 同步 live）
- [x] /rewind、/fork、/tree、/status、/goal、/btw、/compact、/dream、/remote-control 注册到 ACP 路由
- [x] 默认入口切换为 ACP（--no-acp 为过渡逃生门）
- [x] 移除 --no-acp 与入口直连分支（tui.Run/repl.Run 无条件 ACP）
- [x] 删除 runLegacyDirect（死代码）
- [ ] 先把 goal/btw/export/import/copy/rebuild/memory/help 等命令迁到 pigo/command（当前直连原语仍承载这些功能）
- [x] 命令面全量迁到 pigo/command（含 goal/btw/export/import/copy/rebuild/memory/help）
- [x] 删除 tui 直连运行原语（bridge pump/session startRun/buildConfig/rebuild 等）并迁移 tui 回归测试
- [ ] 删除 repl 直连运行原语（runREPL/streamRun/replDeps）并迁移 repl 回归测试

## Resolution

已解决（2026-08-05）。入口（tui.Run/repl.Run）无条件走 ACP；`--no-acp` 已移除；命令面全量经 pigo/command；tui 直连运行原语（bridge pump/session startRun/buildConfig/rebuild 等）与 repl 直连子系统（repl.go/rewind.go/remotecontrol.go/dream_repl.go/host.go/line_editor.go 及对应测试）已删除；回归测试迁移到 ACP 单 seam（acp_bridge_test/acp_repl_test/extensions_test）。受影响包测试全绿。全仓库在 Windows 上的既有失败（hooks 缺 sh、trust/pkgmgr/runtime/dream/agenttool 的 POSIX 断言）与本 effort 无关，已记录于 Evidence 段。
- [x] spec/文档同步“唯一前端入口（交互）”与遗留原语说明

## Evidence / environment note（2026-08-05）

ACP/sessionstore/tui/repl/cmd/plugin 相关包测试全绿。`go test ./...` 在 Windows 上存在一批与本 effort 无关的既有失败：hooks（`sh` 不在 PATH）、trust/pkgmgr/runtime/dream/agenttool（POSIX 路径断言或换行差异）。这些包未被本 effort 改动。
