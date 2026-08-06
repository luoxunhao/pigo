# ACP parity behavior: finalized decisions

pigo 的 ACP 层按“行为子集对齐 pi-acp”推进：ADR 0001 的排除项不算缺口，未记录差异默认视为 gap。以下为已敲定的行为决策（2026-08-06）。

## Standard methods

- `session/list`：空 `cwd` 使用 `lastSessionCwd`；pigo 内部客户端显式传 `cwd`。
- `session/new` / `session/load`：`cwd` 必须为绝对路径。
- `session/new`：无可用模型 / `AUTH_REQUIRED` 保持现状，属于 ADR 0001 排除的 auth 范围。
- `model/set` / `session/set_config_option`：设置前校验模型 id，未知 id 返回 `invalidParams`。
- `session/load`：恢复持久化的会话模型，仅显式传入覆盖模型时例外。
- `session/cancel`：队列非空时发送 `agent_message_chunk` “Cleared queued prompts.”。
- `session/new` configOptions：无可用模型时省略 model 项；有模型时 model 在前、thought_level 在后。
- thinking 模式：ACP 只暴露 `off..xhigh`，名称使用 `Thinking: <id>`；`max` 仅保留在 CLI/内部，ACP 方法拒绝。

## Slash commands

- `/compact [instructions...]` 支持自定义说明，输出压缩前后 token 与摘要。
- `/session` 输出会话统计（session id、session file、messages、cost、tokens）；`/status` 保留为 pigo 扩展。
- `available_commands_update` 保留 pigo 命令超集，pigo-only 命令视为扩展。
- `/export` 保留 `<sessionId>.html` 文件名，不逐字对齐 `pi-session-` 前缀。

## Notifications and metadata

- 保留标准 `usage_update` 通知。
- 不补 pi-acp 的 `queueDepth` 私有元数据。
- `_meta.startupInfo` 保留 `pigo` 命名空间，作为 pigo 扩展。

## Recorded as extensions, no change

- `session/close`
- `pigo/*` 方法
- `session/load` 响应里额外的 `messages` 字段
- startup info 文本内容
- `initialize._meta` 里的 pigo 扩展能力声明
