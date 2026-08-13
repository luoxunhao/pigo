# 04 — 启动默认模型改为空

**What to build:** pigo 不再内置 `openrouter/free` 作为默认模型；没有配置时启动只提示 `no configured model`，真正请求时才报错。

**Blocked by:** 无 — 可立即开始

**Status:** ready-for-agent

- [ ] CLI、ACP、serve 与 runtime 的默认模型不再使用 `openrouter/free`。
- [ ] 无配置时启动不报错，首次请求时报 `no configured model`。
- [ ] `--model` 帮助文案不再暗示内置模型推断。
- [ ] 相关启动测试已更新并通过。
