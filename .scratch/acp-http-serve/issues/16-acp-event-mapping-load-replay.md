# 16 - ACP 事件映射与 load 回放

**What to build:** ACP 适配层把 serve 领域事件映射为标准 `session/update`，并在 `session/load` 时回放完整历史、在 prompt 时等待 idle。

**Blocked by:** 15 - ACP 适配层重写

**Status:** ready-for-agent

- [ ] serve 领域事件映射为对应 ACP `session/update`
- [ ] `session/load` 回放完整历史后才返回响应
- [ ] `session/prompt` 等待事件流 idle 后再返回
- [ ] `available_commands_update` 在 `session/new` / `session/load` 后发送
- [ ] `permission.asked` 映射为 `session/request_permission`
- [ ] 集成测试覆盖事件映射、回放和完成时序
