# SPEC: pigo contextbuild 与 pi harness 对齐实施

> 状态：**superseded**——被 `tasks/spec/spec-018-contextbuild-registry-dsh-alignment.md` 取代（对齐目标从 pi 改为 deepseek-harness，注册表化；见 `docs/adr/0014-contextbuild-registry-dsh-alignment.md`）。归档保留，不再实施。
> 原状态：待实施。决策源为 `docs/research/contextbuild-pi-alignment-map.md` 与 `tasks/issues/issue-037-045`（均 resolved）；`issue-044 before_request` 本轮明确不实现。

## 目标

新建 `internal/contextbuild`，统一组装一次 provider 请求的 system prompt、会话消息、工具与每轮动态上下文；支持 `EntryProjector` / `TransformContext` 扩展接缝；REPL / TUI / headless / serve / ACP 统一切换；按 07 验收标准建立 golden parity corpus。

## 范围

涉及：`internal/contextbuild`（新建）、`internal/runtime`、`internal/provider`、`internal/session`、`internal/sessionstore`、`internal/acp`、`internal/cli/config`、`cmd/pigo`。

排除：`before_request` / stream options 接缝（08，本轮不实现）；每轮实时重建 system prompt 的性能与缓存策略（map Not yet specified）；compaction 自动恢复、branch summary、memory/dream 等上下文管理；与 pi JSONL/SQLite 字节级互通。

## 公共 API（01）

- `BuildSessionContext(proj *session.ProjectLeaf, opts SessionBuildOptions) (*SessionContext, error)`：纯投影；输出 `Messages / Model / Provider / ThinkingLevel / ActiveToolNames`；`ActiveToolNames == nil` 表示未记录、使用全部工具；未注册 customType 跳过。
- `BuildProviderContext(ctx, sess *SessionContext, deps BuildDeps, req RequestOptions) (*ProviderRequest, error)`：编排入口，I/O 全部经 `BuildDeps` 注入；顺序为 model/thinking/tools 解析 -> system prompt 指纹缓存 -> `TransformContext` -> `ConvertToLlm` -> `LlmContext`；`ProviderRequest` 含 `Model / Provider / ThinkingLevel / LlmContext`，不含 API key。
- `TransformContext(ctx, msgs) msgs`、`ConvertToLlm(msgs) msgs`、`EntryProjector(entry, index, entries) []Message` 均为非 error 接缝，异常安全回退；外层构建 error 由 loop 转 terminal assistant message。
- reminders 永远经 `TransformContext` 注入请求副本，不进持久历史。

## 投影语义（02）

- `message / compaction / branch_summary` 直接生成消息；`custom` 仅经 `EntryProjector` 按 `customType` 注册生成，未注册跳过；移除 `custom_message` entry 类型。
- assistant `stopReason=error/aborted`（未来 `deferred` 同规则）不进 `Messages`，`length` 保留；raw history / ACP replay 不受影响。
- compaction 只保留最新一条，顺序为 summary -> retainedTail -> 后续 entry，不去重。
- model / thinking / activeTools 以 `lane.config` 为权威，不从 entry 推导；`ProjectLeaf` 增加 `Config *LaneConfig`；`Store.Projection` 加载 lane.config；新建会话创建时初始化，缺失配置视为 error。
- 新增 `agentcore.CustomMessage`（`role="custom"`）；projector 可返回，`ConvertToLlm` 转 user；TUI/ACP 可区分渲染。

## 转换分层（03）

- `contextbuild.ConvertToLlm` 做 provider 无关 role 转换。
- provider `transformMessages` 做 model-aware 塑形（tool id、孤儿 result、图片降级），各 encoder 保留 wire 专属行为。
- parity 只对照通用 `ConvertToLlm` 输出，provider wire 塑形由 pigo 单测覆盖。

## system prompt（04）

- 每目录只取一个 context file：`AGENTS.override.md` > `AGENTS.md` > `AGENTS.MD` > `CLAUDE.md` > `CLAUDE.MD`；override 替换同目录其余候选，跨目录按序叠加。
- 全局 agent 目录为 `$PIGO_HOME`（默认 `~/.pigo`）；祖先链从 cwd 到文件系统根，canonical path 去重；嵌套 linked worktree 跳过自身同名 context file。
- 注入分层：base（含 active tools 的 Available tools / Guidelines 段）-> append-system-prompt -> context files -> skills -> environment/cwd。
- context files 用 `<project_instructions path="...">...</project_instructions>` XML 边界包裹。
- 每次 provider 请求前按输入指纹实时组装；指纹含 base instruction / cwd / context files / skills / active tools / append instructions，不变则复用字符串。
- header `SystemPrompt` 不再作为构建输入；每轮结束后写回实际发送的 prompt，仅用于导出/展示兼容。

## 扩展接缝（05）

- `contextbuild.Registry` 为会话级类型化注册表：`RegisterTransform(name, fn)` 按注册顺序追加；`RegisterEntryProjector(customType, fn)` 按 customType 唯一键注册，重复返回 error。
- 跨来源固定顺序：built-in -> 会话 hooks/进程内注册 -> plugin 声明；trust/config 变化时重建 registry。
- `ReminderRegistry` 内置为一条 transform（`name=reminders`），内部复用 provider API；`BuildProviderContext` 只从 registry 顺序链消费；hooks `additionalContext` 继续走 one-shot reminder。
- 不新增 shell `transform_context` 事件；plugin 通过声明式 manifest 贡献 `entryProjectors` 与 `contextTransforms`，pigo 进程内渲染，不新增每请求 RPC。
- 错误处理：built-in/hooks 注册错误 fail-closed；plugin 声明无效按 `Discover` 容错跳过并 warn；运行期 transform/projector panic recover + warn + 回退。

## 前端迁移（06）

- REPL / TUI / headless / serve / ACP 统一切换到 `BuildSessionContext + BuildProviderContext`；删除旧 `AgentContext` 手工组装路径，不设运行时兼容 shim。
- `store.Projection` 保留为运行时上下文构建唯一存储入口；`HistoryWindow` / `store.Load` 仅用于 ACP replay / UI 展示。
- serve compaction 一并迁移：用 `compacted` 标记替代 `len(msgs) < len(history)` 启发式。
- ACP 不新增标准字段；`session/load` 与 `session_info_update` 在 `_meta.pigo` 增补 `laneConfig`（model / thinking / activeTools）与 `systemPrompt`；replay 维持 compaction/branch_summary 跳过，custom 按 user 回放。

## 配置（09）

```toml
[contextbuild]
context_files = true          # --no-context-files 置 false
append_system_prompt = []     # 可重复；文本或路径
plugin_dirs = []              # 可重复；绝对或 cwd 相对
```

- 落点：现有 `~/.config/pigo/config.toml` 新增 `[contextbuild]`，`FileConfig` 增加 `ContextBuild ContextBuildConfig`；优先级 CLI > config.toml > default。
- 现有顶层 `system_prompt` / `no_skills` / `allowed_tools` / `prompts` 不复制到新表。
- 新增 CLI：`--no-context-files`、`--append-system-prompt <text|path>`（可重复）、`--plugin-dir <path>`（可重复）。
- `plugin_dirs` 绝对路径原样使用，相对路径按 session cwd 解析；显式目录视为用户授权，不按项目 trust 门控。
- skills 不新增 `skill_dirs`，继续使用 `SkillsDir()` + `no_skills`。
- 配置或 trust 变化时，下一次 provider 请求前惰性重建 session registry 并让 system prompt 指纹失效。

## 验收与 parity（07）

- 双层锚点：`BuildSessionContext` 输出对照 pi `buildSessionContext`；`BuildProviderContext` 最终 provider 可见请求对照 pi 最终请求；Go 内部 API 不要求一一对应。
- fixture：pigo 自持 golden corpus，位于 `internal/contextbuild/testdata/parity/`，期望输出从 pi 当前行为生成一次并冻结，记录 pi commit，提供刷新脚本。
- 判定：规范化语义相等；system prompt 逐字节精确；消息按 `role/content/toolCallId/stopReason/isError` 等语义字段精确且保序；tools 按 `name/description/schema` 精确；存储元数据、usage/cost、id/timestamp 不进对照。
- 允许差异：Go provider 抽象与 wire 塑形、扩展注册/事件机制、存储元数据与 usage/cost、性能（无验收阈值）。
- 偏差：fixture 内嵌 `deviations` 注册表（`code/scope/pi commit/原因`）；刷新脚本对未注册偏差 fail，已注册偏差在报告列出；pi commit 变化时强制复核。

## 建议实施切片

1. `internal/contextbuild` 核心包、公共类型、config 接线与单元测试。
2. `sessionstore` lane.config 持久化与 `ProjectLeaf.Config` 投影加载。
3. system prompt builder：context files / skills / active tools / append / environment。
4. registry、reminders、plugin manifest 接缝。
5. REPL / TUI / headless / serve / ACP 迁移与旧路径删除。
6. parity corpus、刷新脚本与验收回归。

## 验证

- `go build ./...`。
- `go test ./internal/contextbuild/... ./internal/runtime/... ./internal/sessionstore/... ./internal/acp/... ./internal/cli/config/...`。
- parity corpus 全绿；刷新脚本可重新生成并 diff。
- REPL / TUI / headless / serve / ACP 回归。
