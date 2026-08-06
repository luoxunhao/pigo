# 06 — 启动解析自定义默认模型

**Type:** backend

**What to build:** pigo 启动时若默认模型为 `custom-<slug>/...`，从注册表读取 baseUrl/apiKey/protocol 并构造 provider，保证 UI 保存的默认配置重启后生效。

**Blocked by:** #5

**Status:** done

- [ ] `run.SetupEnv` / `acpcmd` 支持 custom 默认模型
- [ ] provider 缺失或凭据不全时启动失败并给出 providerId/缺失字段
- [ ] 非 custom 模型启动路径不变
- [ ] 测试覆盖启动成功与失败场景

**Spec:** tasks/spec-custom-provider-registry.md（Startup Resolution）

## Resolution

已解决（2026-08-06）。启动装配支持 custom 默认模型解析，provider 缺失或凭据不全时清晰报错，非 custom 路径不变；单测覆盖成功与失败场景。
