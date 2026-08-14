# SPEC: contextbuild 注册表化（对齐 deepseek-harness）

> 状态：待实施。**替代 `tasks/spec/spec-015-contextbuild-pi-alignment.md`（superseded）**。决策源为 grilling 共识（10 项）与 `docs/adr/0014-contextbuild-registry-dsh-alignment.md`；动态快照投影（DSH runtime-context 持久化 user message）明确为本轮排除、第二步单独排期。

## 目标

把 pigo 的 contextbuild 从固定拼接组装升级为 DSH 式注册表模型：完整分层注册表（sections / contexts / variables / transforms / projectors / tool providers，parent 链 + shadow，与 subagent scope 同一套体系）；persona 换 DSH 文案；工具引导段与 sandbox 策略段迁入 contexts；放弃 pi 逐字节 parity，改以行为测试 + 自持 golden + 手工语义对照验收。

## 范围

涉及：`internal/contextbuild`（Registry 完整分层、sections/contexts/variables 渲染、指纹静态骨架）、`internal/runtime`（内置 sections 迁移、工具引导段、sandbox 策略段迁 contexts）、`internal/agenttool`（tool:* 引导文案）、`internal/cli/run` 与 `cmd/pigo`（组装接线）、`internal/sessionstore`（不受影响，仅验证）。

排除：动态快照投影（第二步：contexts 渲染为持久化 user message、增量替换/清除）；pi parity 续用；服务注入体系。

## 公共 API（注册表）

- `contextbuild.Registry` 一次扩展为完整分层（承接 issue-046 的 parent 链）：
  - `RegisterSection(name, order, text)`：命名静态段；同 scope 重复注册拒绝；子 scope 按名 shadow 父；`complete=true` 整段替换全部 sections（至多一个）。
  - `RegisterContext(name, order, text)`：命名动态段，每请求渲染进请求副本（不进历史）。
  - `RegisterVariable(name, provider)`：`{{name}}` 插值；内置 `model` / `cwd`；开放注册与 shadow。
  - `RegisterTools(provider)`：tool schema provider（保留现有工具注册路径）。
  - 现有 `RegisterTransform` / `RegisterEntryProjector` 保持。
- `BuildSystemPrompt` 兼容层：由注册表渲染（默认注册 = 内置 sections），签名保持。
- 指纹缓存：只含静态骨架（sections 结构 + 静态文本），变量在渲染时插值、不参与指纹。

## 内置 sections（对齐 DSH order 语义）

| name | order | 内容 |
|---|---|---|
| `pigo:identity` | -100 | "You are pigo, an AI agent powered by DeepSeek Harness."（品牌身份） |
| `deployment:persona` | 0 | **DSH 文案**："You are a coding agent powered by the {{model}} model. Your working directory is {{cwd}}."（可被 shadow/complete） |
| `tools` | 100 | # Available tools（active tool 列表） |
| `guidelines` | 110 | # Guidelines（现有默认指南） |
| `append` | 120 | --append-system-prompt 内容 |
| `project_instructions` | 130 | `<project_instructions>` context files（AGENTS/CLAUDE 链） |
| `skills` | 140 | `<available_skills>` 渐进披露 |
| `environment` | 150 | Working directory / OS / arch（静态文本） |

- todo 指南并入 persona 段文本（或独立 `todo` section，order 5，实施时定）。
- `header.SystemPrompt` 写回保留（展示兼容）。

## 工具引导段（tool:* contexts，本轮建）

- 内置 contexts：`tool:read` / `tool:write` / `tool:edit` / `tool:glob` / `tool:grep` / `tool:bash` / `tool:pwsh` 等（每工具一段，order 100~120 区间）：用法、observed-state 规则（先读后写、FS_NOT_OBSERVED）、沙箱边界提示。
- 文案来源：现有工具 description 提炼 + 执行语义（fence/沙箱/超时）说明；与工具 schema description 互补不重复。

## 策略段（contexts 迁入）

- `sandbox:policy` context（承接 issue-054 修订）：动态文本 = 当前模式 + workspace 根 + escalation 通道；每轮经 contexts 渲染注入请求副本。
- ReminderRegistry 保留给 todo/goal/one-shot（非策略注入）。

## Variables

- 内置 `{{model}}`（当前模型 id）、`{{cwd}}`（会话工作目录）；开放注册、同名 shadow。
- 变量渲染期插值；指纹只含静态骨架（模型切换不失效指纹缓存——注意：persona 含 {{model}}，指纹不变但输出变，属预期）。

## Scope 统一

- 与 subagent scope（issue-046~052）同一套 parent 链：子代理 scope 继承父的 sections/contexts/variables/tools/transforms，可 shadow；sandbox 策略段在子代理 scope 自动继承。

## 验收

- 三层验收：
  1. **注册表行为矩阵**：order 排序、shadow 同名覆盖、complete 整段替换（多 complete 拒绝）、suppress 抑制、变量插值与未知变量报错、scope 链合并（父→子 shadow）、工具引导段注入、sandbox 策略段迁入后注入内容。
  2. **兼容回归**：contextbuild 公共 API 签名（BuildSessionContext/BuildProviderContext/RequestOptions）保持；subagent scope 共用不破；sandbox 行为（fence/escalation）不变；既有测试按新输出更新。
  3. **全量构建**：`go build ./...` 与相关包 `go test`。
- 自持 golden：`internal/contextbuild/testdata/golden/` 新快照（pigo 默认注册表配置下输出，刷新脚本 `-update`）；pi parity corpus 归档标记 superseded。
- 与 DSH standard preset 手工语义对照表（评审项，非自动化）。

## 文档处理

- `tasks/spec/spec-015-contextbuild-pi-alignment.md` 头部标记 superseded；`tasks/issues/issue-036~045` 同样标记；parity corpus README 标记 superseded。
- `tasks/issues/issue-046`（scope 分层）与 `issue-054`（PolicyReminder）由本 spec 承接修订。

## 建议实施切片

1. Registry 完整分层：sections/contexts/variables/tools provider 加入现有 transforms/projectors（parent 链 + shadow + complete/suppress），含单测。
2. 内置 sections 迁移：8 个内置 section 注册化，persona 换 DSH 文案 + {{model}}/{{cwd}}；指纹静态骨架改造。
3. variables 插值：渲染器 + 内置变量 + 开放注册。
4. 工具引导段：tool:* contexts（文案提炼 + 注入）。
5. 策略段迁入：sandbox:policy context（修订 issue-054 实现）。
6. 旧 parity 归档 + golden 快照 + 行为矩阵 + 手工对照表 + 兼容回归。

## 验证

- `go build ./...`。
- 注册表行为矩阵、golden 快照、兼容回归（subagent/sandbox 集成）、全量相关包测试。
- REPL / TUI / headless / serve / ACP 回归（system prompt 输出、模型切换后 persona 更新、sandbox 模式注入）。
