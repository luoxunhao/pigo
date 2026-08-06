# 01 — 会话作用域与入参校验

**Type:** task

**What to build:** ACP 客户端在 `session/list` 不传 `cwd` 时看到最近会话所在项目的会话列表；`session/new` 和 `session/load` 传相对路径时立即收到 `invalidParams`，不再产生工作区漂移。

**Blocked by:** None — can start immediately

**Status:** ready-for-agent

- [ ] `session/list` 空 `cwd` 使用最近会话 cwd 过滤，而非全局列表
- [ ] pigo 内部客户端显式传 `cwd`，需要全项目列表时有明确入口
- [ ] `session/new` 与 `session/load` 拒绝相对路径，返回 `invalidParams`
- [ ] 覆盖 scoping、路径校验与内部客户端调用的测试
