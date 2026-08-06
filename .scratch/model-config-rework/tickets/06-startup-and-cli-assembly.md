# 06 — 启动与 CLI 装配

**Type:** backend

**What to build:** CLI/REPL/headless/ACP 都读新配置；`model` 缺失或不在 `models` 时能启动，真正请求时再报错。

**Blocked by:** #1, #5

**Status:** done

- [ ] 启动装配从 `models` 找默认模型条目构造 provider
- [ ] `model` 不在 `models` 或 `models` 为空时不阻断启动
- [ ] prompt 时返回 `model "..." is not configured`
- [ ] CLI flags 保留为临时覆盖，`--model` 必须是已配置 id
- [ ] 所有前端行为一致
- [ ] 覆盖启动成功与请求失败的测试

**Spec:** 2026-08-06 模型配置重构设计（grill 确认）

## Resolution

已解决（2026-08-06）。CLI/REPL/headless/ACP 都读新配置；模型缺失时启动成功、prompt 报错；CLI flags 保留为临时覆盖且 `--model` 必须是已配置 id。
