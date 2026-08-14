# REPL 解耦直连 runtime（Phase 2）

## Description

本地 fork 的 REPL 通过 in-process ACP（`RunACP`）驱动 agent；upstream 的 REPL 是 `replDeps` + `runtime.StartRun`/`DrainStream` 的直接同步循环，SIGINT 直接 cancel run context。本 Issue 将 REPL 改回直连 runtime。

对应 PRD：US-006、FR-11/12。

## Acceptance Criteria

- [ ] `repl.Run` 不再调用 `acp.StartInProcessWithHooks`
- [ ] REPL 通过 `replDeps` + `runtime.StartRun`/`DrainStream` 直连
- [ ] SIGINT 直接 cancel run context 并回到提示符
- [ ] `--acp` 模式无回归
- [ ] 对应 Go 测试通过

## Dependencies

None（Phase 2，独立 PR）

## Type

backend

## Priority

medium
