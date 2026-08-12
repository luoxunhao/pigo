# 15 - ACP 适配层重写

**What to build:** `pigo acp` 作为标准 ACP 适配层，内部启动 serve 并通过 loopback HTTP client 调用；移除所有非标准 ACP 方法、通知和响应字段。

**Blocked by:** 05 - 会话配置与 mode 切换, 08 - Prompt 同步/异步、队列与取消, 09 - 命令列表与执行, 11 - 权限审批流, 12 - Config / Providers / Models API, 14 - Modes HTTP 与 ACP 数据源

**Status:** ready-for-agent

- [x] `pigo acp` 内部启动 serve 并使用 loopback HTTP client
- [x] 标准 ACP 方法面完整可用
- [x] `authenticate` 实现但 `authMethods` 为空，未知方法返回错误
- [x] `model/set`、`pigo/*`、`pigo/event` 全部移除
- [x] ACP 请求/响应不再包含非标准字段
- [x] `initialize` 能力声明与实现一致
- [x] ACP wire 测试覆盖完整标准流程
