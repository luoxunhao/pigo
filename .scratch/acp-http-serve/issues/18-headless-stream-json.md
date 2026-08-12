# 18 - Headless 迁移与 stream-json 兼容

**What to build:** headless 模式通过 serve 领域事件驱动，同时保持 `--stream-json` 输出形状兼容。

**Blocked by:** 17 - TUI/REPL 迁移

**Status:** ready-for-agent

- [x] headless 使用 serve 作为后端
- [x] `--stream-json` 输出与现有事件形状兼容
- [x] 非交互运行不绕过 serve
- [x] 集成测试覆盖 headless 与 stream-json 输出
