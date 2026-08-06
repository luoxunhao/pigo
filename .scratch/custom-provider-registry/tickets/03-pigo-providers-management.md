# 03 — pigo/providers upsert/list/delete

**Type:** backend

**What to build:** 新增自定义 provider 管理方法：创建/更新、列出、删除；provider 定义写入注册表并持久化 discover 的模型缓存。

**Blocked by:** #1

**Status:** ready-for-agent

- [ ] upsert 支持缺省 providerId 自动生成，空 apiKey 保留旧值
- [ ] list 返回 provider 元数据与 `apiKeyConfigured`，不回显 key
- [ ] delete 幂等，删除后引用会话报清晰错误
- [ ] `initialize` 的 `_meta` 声明 `pigo.providers`
- [ ] ACP wire 测试覆盖 upsert/list/delete

**Spec:** tasks/spec-custom-provider-registry.md（ACP Methods / Security）
