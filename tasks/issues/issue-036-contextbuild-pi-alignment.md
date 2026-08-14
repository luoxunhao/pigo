# contextbuild 与 pi harness 对齐实施

## Description

按 `tasks/spec/spec-015-contextbuild-pi-alignment.md` 实施 contextbuild：新建 `internal/contextbuild`，统一切换全前端，按 07 验收标准建立 golden parity corpus。决策源为 `.scratch/contextbuild-pi-alignment/map.md` 与 `issues/01-09`。

## Acceptance Criteria

- [ ] `internal/contextbuild` 提供 `BuildSessionContext` / `BuildProviderContext` / `TransformContext` / `ConvertToLlm` / `EntryProjector` / `Registry` 公共 API
- [ ] `ProjectLeaf.Config *LaneConfig` 与 `Store.Projection` lane.config 加载完成
- [ ] system prompt 按 04 规则实时组装，header `SystemPrompt` 仅兼容写回
- [ ] registry / reminders / plugin manifest 接缝按 05 落地
- [ ] REPL / TUI / headless / serve / ACP 统一切换，旧 `AgentContext` 手工组装路径删除
- [ ] `[contextbuild]` 配置表与三个 CLI flag 落地
- [ ] parity corpus 与刷新脚本落地，07 验收规则全绿
- [ ] `go build ./...` 与相关包测试通过

## Dependencies

contextbuild map `issues/01-09`（决策，已 resolved）；实施切片见 spec。

## Type

backend

## Priority

high
