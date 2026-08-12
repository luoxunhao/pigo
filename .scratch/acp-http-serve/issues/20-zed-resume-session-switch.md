# 20 - Zed 中 /resume 无法切换会话

**What to build:** 明确 `/resume` 在 Zed 中的能力边界；Zed 模式下它不能改变当前绑定的 ACP sessionId，会话切换必须由客户端调用 `session/load`。

**Blocked by:** 15 - ACP 适配层重写, 19 - Zed 手动验收

**Status:** parked

## 问题

- 在 Zed 中执行 `/resume <n>`，serve 只返回 `selected session: <id> ...` 文本。
- Zed 不会因为这个文本自动调用 `session/load`，所以 title 不变、历史消息不加载。
- 当前 ACP 协议没有让 agent 端主动切换客户端会话的标准机制。

## 当前状态

- TUI：`/resume [n]` 可列出并切换本地会话，真实加载历史。
- REPL：`/resume [n]` 可列出并切换本地会话，真实加载历史。
- Zed/serve：`/resume [n]` 只返回选择结果，不能切换。

## 建议处理

- 从 serve 的 `available_commands_update` 移除 `/resume`，Zed 使用自己的会话切换器（内部调 `session/load`）。
- TUI/REPL 保留 `/resume`。
- 若未来需要 Zed 内 `/resume` 切换，需在客户端侧把 selected session id 接到 `session/load` 流程。

## 验收

- [ ] 决定 Zed 是否保留 `/resume`
- [ ] 若移除，serve 命令列表不再包含 `/resume`
- [ ] TUI/REPL `/resume` 继续可用
- [ ] 记录最终结论
