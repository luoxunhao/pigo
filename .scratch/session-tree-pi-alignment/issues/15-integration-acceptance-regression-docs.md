# 15 - 集成验收 + 回归 + 文档

**What to build:** 端到端验收：serve + ACP + TUI/REPL/headless 共享同一 leaf；v4 round-trip；v1/v2/v3 拒绝；旧 id not found；隔离脚本；Zed 手动验收；`go build ./...` 与相关包测试；文档与 AGENTS.md 一致性。

**Blocked by:** 01-14

**Status:** resolved

## Acceptance Criteria

- [x] 跨前端 resume 同一分支（REPL/TUI/serve/ACP）
- [x] `/tree` 切换后 `session_info_update` 与下一次 prompt 投影一致
- [x] compaction 在 serve/headless 落盘并可在新进程 resume
- [x] v4 export→import round-trip 无会话语义损失
- [x] v1/v2/v3 import 明确拒绝；旧 id not found
- [x] `scripts/quarantine-legacy-sessions.*` 执行后旧目录隔离
- [x] ACP 方法面/通知面自动测试仍为标准面
- [x] Zed 手动验收通过并记录到 `docs/zed-acceptance.md` 或等价文档
- [x] `go build ./...` 与相关包测试通过；完整测试遵循 AGENTS.md Windows 说明

**Type:** fullstack

**Priority:** high

**Spec Reference:** `tasks/spec-session-tree-pi-alignment.md` §15, §16


> 验收：2026-08-12 Zed 手动验收全部通过（thinking/工具调用/bash 参数/curLeaf/新旧会话继续）。
