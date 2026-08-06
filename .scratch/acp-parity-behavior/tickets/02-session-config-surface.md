# 02 — 会话配置面：thinking 模式与 configOptions

**Type:** task

**What to build:** `session/new` 返回的 modes 与 configOptions 和 pi-acp 一致：无可用模型时不出现空的 model 选项；ACP 只暴露 `off..xhigh`，名称统一为 `Thinking: <id>`，`max` 不再被 ACP 方法接受。

**Blocked by:** None — can start immediately

**Status:** done

- [ ] 无可用模型时 configOptions 省略 model 项，只保留 thought_level
- [ ] 有模型时 model 在前、thought_level 在后
- [ ] ACP modes 仅包含 `off/minimal/low/medium/high/xhigh`
- [ ] ACP 方法拒绝 `max`，CLI/内部仍可使用
- [ ] 覆盖 configOptions 与 set_mode 的测试

## Resolution

已解决（2026-08-06）。configOptions 在无可用模型时省略 model 项，有模型时 model 在前；ACP modes 仅暴露 `off..xhigh` 且名称为 `Thinking: <id>`；`max` 被 ACP 方法拒绝。
