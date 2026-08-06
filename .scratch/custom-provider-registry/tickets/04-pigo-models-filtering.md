# 04 — pigo/models 与会话模型选项过滤

**Type:** backend

**What to build:** 模型列表只展示“已配置凭据的内置 provider + 已保存自定义 provider 的缓存模型”，不再无条件返回整个内置目录。

**Blocked by:** #1, #3

**Status:** done

- [ ] `pigo/models` 不再返回完整 `PresetCatalog`
- [ ] 已配置内置 provider 的模型仍可见
- [ ] 自定义 provider 缓存模型出现在 `pigo/models`
- [ ] `session/new` 的 models/configOptions 包含自定义模型
- [ ] 单测覆盖过滤与空配置场景

**Spec:** tasks/spec-custom-provider-registry.md（pigo/models Filtering）

## Resolution

已解决（2026-08-06）。`pigo/models` 与会话模型选项只展示已配置内置 provider 和自定义缓存模型；`session/new` 包含自定义模型；测试覆盖过滤与空配置场景。
