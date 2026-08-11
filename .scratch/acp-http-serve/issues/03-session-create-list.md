# 03 - 会话创建与列表

**What to build:** HTTP 客户端可以创建新会话并列出已有会话；创建时支持目录、附加目录、默认模型、默认 mode 和标题，列表支持过滤与分页。

**Blocked by:** 02 - 认证、错误模型与请求追踪

**Status:** ready-for-agent

- [ ] 创建会话接受 `directory`、`additionalDirectories`、`model`、`mode`、`title`、`mcpServers`
- [ ] 默认模型未配置时返回 `MODEL_NOT_CONFIGURED`
- [ ] 创建响应包含 `sessionId`、`directory`、`configOptions`、`availableModes`、`availableCommands`
- [ ] 列表支持 `directory` 过滤、不透明游标和 `limit`
- [ ] 集成测试覆盖创建、默认值、错误和列表分页
