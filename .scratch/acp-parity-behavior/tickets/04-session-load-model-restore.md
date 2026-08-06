# 04 — 会话加载恢复持久化模型

**Type:** task

**What to build:** 恢复历史会话时，pigo 使用该会话持久化的模型，而不是被启动默认模型覆盖；只有调用方显式传入覆盖模型时才使用新值。

**Blocked by:** None — can start immediately

**Status:** ready-for-agent

- [ ] `session/load` 默认恢复 metadata 中的模型
- [ ] 显式传入覆盖模型时允许覆盖
- [ ] 恢复后的 models/configOptions 反映持久化模型
- [ ] 覆盖无元数据与显式覆盖场景的测试
