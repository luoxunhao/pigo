# Map: pigo 上下文构建对齐 pi harness

> 归档说明：本文档为 wayfinder 决策图，决策卡已按 AGENTS.md 归档到 `tasks/issues/issue-037-045`；实施规格见 `tasks/spec/spec-015-contextbuild-pi-alignment.md`。

## Destination

让 pigo 的上下文构建管线与 pi 新 harness 对齐：新建 `internal/contextbuild`，统一组装一次 provider 请求的 system prompt、会话消息、工具与每轮动态上下文，支持 `EntryProjector` / `TransformContext` 扩展接缝；投影状态覆盖 model / thinking / active tools；system prompt 由 context files / skills / tools 实时构建。

## Notes

- 领域：`internal/contextbuild`、`internal/runtime`、`internal/provider`、`internal/session`、`internal/sessionstore`、`internal/acp`、`cmd/pigo`。
- 参考：pi `packages/agent/src/harness/session/context.ts`、`agent-harness.ts`、`packages/ai/src/api/transform-messages.ts`。
- 会话约定：wayfinder charting；一次只处理一个 ticket；本 map 只做决策，不做实现。

## Decisions so far

- 目的地范围：全构建管线对齐（A）。
- 参考目标：pi 新 harness 的 `buildSessionContext` / `entryProjectors` / `transform_context` / `toProviderMessages`（A）。
- 落点：新建 `internal/contextbuild`（A）。
- 扩展接缝：内置注册表 + hooks/plugins 接线（A）。
- system prompt：对齐 `AGENTS.override.md` / `AGENTS.md` / `CLAUDE.md`、全局 + 祖先链、worktree 遮蔽（A）。
- 转换分层：`contextbuild.ConvertToLlm` 通用转换 + provider `transformMessages` 专属归一化（A）。
- 工具集状态：`activeToolNames` 以 lane.config 寄存器承载，不落 entry（Q5=B / Q6=B）。
- system prompt 权威源：每次构建实时组装，header `SystemPrompt` 降级为兼容字段（A）。
- 地图形态：决策图，实施另开票（A）。

- [issue-037-contextbuild-api-and-dataflow](../tasks/issues/issue-037-contextbuild-api-and-dataflow.md) — 两段式：BuildSessionContext 纯投影 + BuildProviderContext 注入组装；ProviderRequest 自包含模型状态，内部接缝非 error，EntryProjector 在投影阶段。
- [issue-038-session-projection-semantics](../tasks/issues/issue-038-session-projection-semantics.md) — 消息投影边界、custom projector、stopReason 过滤、compaction retainedTail 顺序、lane.config 权威与缺失语义。

- [issue-039-transform-convert-layering](../tasks/issues/issue-039-transform-convert-layering.md) — ConvertToLlm 做 provider 无关 role 转换，provider transformMessages 做 model-aware 塑形（tool id、孤儿 result、图片降级），各 encoder 保留 wire 专属行为。
- [issue-040-system-prompt-context-files](../tasks/issues/issue-040-system-prompt-context-files.md) — context file 候选/优先级、全局 agent 目录与祖先链、worktree 遮蔽、注入分层、重建指纹、header SystemPrompt 兼容写回。
- [issue-041-extension-seams](../tasks/issues/issue-041-extension-seams.md) — 会话级 contextbuild.Registry；TransformContext 顺序链 + EntryProjector 唯一键拒绝；ReminderRegistry 内置 transform；不新增 shell transform_context / before_request（后者见 issue-044）；plugin 声明式 manifest 接缝；错误分来源处理。
- [issue-042-frontend-acp-migration](../tasks/issues/issue-042-frontend-acp-migration.md) — 统一切换（无 shim）、Projection 仍为运行时唯一入口、SystemPrompt 仅兼容写回、serve compaction 一并处理、ACP 状态经 _meta.pigo 扩展。
- [issue-043-parity-acceptance](../tasks/issues/issue-043-parity-acceptance.md) — 双层锚点 + pigo 自持 golden fixture；规范化语义对照；允许差异清单；fixture 内嵌偏差注册表。
- [issue-045-contextbuild-config-table](../tasks/issues/issue-045-contextbuild-config-table.md) — 现有 config.toml 新增 [contextbuild]：context_files / append_system_prompt / plugin_dirs；CLI 覆盖；下轮惰性重建。

## Not yet specified

- 每轮实时重建 system prompt 的性能与缓存策略。

## Out of scope

- 上下文管理：compaction、overflow 自动恢复、branch summary、memory/dream。
- TUI/REPL/ACP 视觉与协议方法新增（保持标准 ACP 方法）。
- 与 pi JSONL/SQLite 字节级互通。
