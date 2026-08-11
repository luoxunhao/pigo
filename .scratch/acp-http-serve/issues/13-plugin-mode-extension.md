# 13 - 插件 mode 扩展点

**What to build:** 插件可以声明 mode，并通过 `mode/apply` 执行切换逻辑；核心应用工具、模型和系统提示后调用插件。

**Blocked by:** 04 - 会话加载、关闭、删除与状态

**Status:** ready-for-agent

- [ ] 插件 Manifest 支持声明 `modes`
- [ ] 插件协议新增 `mode/apply`
- [ ] 核心按 manifest 应用 tools、model、systemPrompt
- [ ] 核心应用后再调用插件 `mode/apply`
- [ ] 无插件注册时默认只有 `build`
- [ ] 集成测试覆盖插件 mode 注册与切换
