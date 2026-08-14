# sandbox 策略层（mode + 配置）

## Description

按 `tasks/spec/spec-017-sandbox.md` 切片 1 实施：`internal/sandbox` 包（新建）policy 类型（read-only / workspace-write / danger-full-access）、`writableRoots`（workspaceRoot + 平台临时区，canonical 化）、`session.LaneConfig.SandboxMode` 扩展与 sessionstore 读写、`[sandbox]` 配置表与 `--sandbox-mode` flag。

## Acceptance Criteria

- [ ] `internal/sandbox` 定义 SandboxMode 三档、writableRoots（工作区 + /tmp + os.TempDir() canonical 去重）、解析/校验
- [ ] `session.LaneConfig` 新增 `SandboxMode` 字段；lane_config 读写兼容；缺失时用部署默认（默认 workspace-write）
- [ ] `[sandbox]` 配置表：`default_mode` / `workspace_root` / backend 开关 / `escalation_enabled`；CLI `--sandbox-mode` 覆盖；优先级 CLI > config > 默认
- [ ] 模式解析服务：per-session resolve（lane.config 优先，fallback 部署默认 + workspaceRoot fallback）
- [ ] 单测：三档校验、writableRoots 语义、配置优先级、lane.config 持久化与 resume 直读

## Dependencies

无（独立于 subagent 系列）。

## Type

backend

## Priority

high
