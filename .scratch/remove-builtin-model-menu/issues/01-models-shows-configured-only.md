# 01 — /models 只展示已配置且启用的模型

**What to build:** 用户运行 `/models` 时只能看到 `config.toml` 中 `[[models]]` 里已启用、真正可用的模型，不再看到任何内置预设 provider 或无效模型。

**Blocked by:** 无 — 可立即开始

**Status:** ready-for-agent

- [ ] `/models` 只列出 `[[models]]` 中启用的模型，不再输出内置预设目录。
- [ ] `/models <provider>` 仍按 provider 过滤已配置模型。
- [ ] 没有已配置模型时输出清晰的 `no configured models` 提示。
- [ ] `enabled = false` 的模型不会出现在列表里。
- [ ] REPL 与 TUI 共用同一套 `/models` 行为，相关测试已更新并通过。
