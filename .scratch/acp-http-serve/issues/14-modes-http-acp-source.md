# 14 - Modes HTTP 与 ACP 数据源

**What to build:** serve 提供 mode 列表与切换接口，并成为 ACP `availableModes` 的唯一事实来源。

**Blocked by:** 13 - 插件 mode 扩展点

**Status:** ready-for-agent

- [x] `GET /api/v1/modes` 返回完整 mode 信息，包括 systemPrompt
- [x] `POST /api/v1/session/{id}/mode` 切换 mode
- [x] mode 切换后发出 `mode.updated` 事件
- [x] 未知 mode 返回 `MODE_NOT_FOUND`
- [x] ACP 适配层从 serve 读取 `availableModes`
- [x] 集成测试覆盖列表、切换、事件和错误
