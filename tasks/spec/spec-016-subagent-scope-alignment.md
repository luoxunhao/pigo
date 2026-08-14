# SPEC: subagent scope 体系与 DSH 对齐实施

> 状态：待实施。决策源为 grilling 共识（15 项）与 `docs/adr/0012-scope-hierarchy-for-subagents.md`；continuable 后台子代理（DSH `backgroundMode: continuable`）明确本轮不实现，单独排期。

## 目标

把 pigo 的 subagent 上下文构建对齐 deepseek-harness：引入 scope 分层体系（contextbuild.Registry + 工具注册表 parent 链继承与 shadow），子代理统一接入 contextbuild 管线，支持 fork/spawn 两种上下文语义、persona 继承与覆盖、toolFilter allow/deny、持久化 delegationDepth、descriptor 组成快照、outputSchema 结构化输出；现有 task/skill/plugin 子代理默认行为（spawn 全新自包含）保持不变。

## 范围

涉及：`internal/contextbuild`（scope 分层）、`internal/agenttool`（tools 注册表分层、structured_output 工具）、`internal/runtime`（subagent.go / subagent_child.go / subagent_process.go 接入管线）、`internal/session` 与 `internal/sessionstore`（descriptor entry、lane.config 扩展）、`internal/cli/run`（SubAgentTool 组装）、`cmd/pigo`（subagent RPC 握手）、`internal/cli/config`（toolFilter/depth 配置）。

排除：continuable 后台子代理（异步结果通道，单独 ticket）；Cordis 式服务注入（不引入）；父会话在子代理运行期间动态变化的 scope 同步（scope 快照于派发时冻结）。

## 公共 API（scope 体系）

- `contextbuild.Registry` 增加分层：`NewRegistry(parent *Registry)`；`RegisterTransform/RegisterEntryProjector` 注册到本层；合并视图按 parent 链展开（父层先、本层 shadow 同名）；`ApplyTransforms` 消费合并后的顺序链。
- 工具注册表（`agenttool.ToolRegistry` 或等价物）支持 parent 链：子代理视图 = 父继承集 → `toolFilter` allow（白名单交集）→ deny（剔除）。
- `runtime.SubAgentSpec` 扩展：`Inherit string`（`"fork"|"spawn"`，默认 `spawn`）、`Persona string`（覆盖）、`ToolFilter {Allow, Deny []string}`、`MaxDepth int`（默认 3）、`OutputSchema json.RawMessage`。
- `task` 工具参数扩展：`inherit`、`persona`、`tools`（allow/deny）、`max_depth`、`output_schema`；skill frontmatter 新增 `fork`/`tool-deny`/`max-depth`/`output-schema` 可选字段（`allowed-tools` 保留为 allow 语义）。
- `BuildProviderContext` 增加 scope 感知：子代理请求经其 scope 合并的 registry + 继承的 prompt 构建输入组装。

## 语义（fork/spawn）

- **spawn（默认）**：子代理零父上下文消息；system prompt 仍经 scope 继承父构建输入（base + AGENTS.md + skills + env），persona 默认继承父，可被声明覆盖。spec-013「全新自包含」契约仅指消息，不指 prompt。
- **fork**：子代理会话以父会话当前 leaf 的投影历史为种子，截断尾部未闭合 tool-call 回合（对齐 DSH `completedTurnPrefix`：到最后一个完成的 turn）；种子与子代理新消息一起持久化到子代理会话（对齐 `seedDescriptorTurn`）。
- persona：父 base instruction 为默认 persona；skill body / plugin spec.SystemPrompt / task `persona` 参数作为 shadow 覆盖。

## 深度与策略（delegation / policy）

- `delegationDepth`：父 depth+1 传入子代理，随 lane.config/descriptor 持久化，resume 不归零；超过 `MaxDepth`（默认 3）的派发在工具层拒绝并返回错误结果。
- 策略继承：维持 hooks 接缝传播（`installSubagentConfigHooks`）与共享 trust store；派发时把信任目录快照与 hooks 集指纹写入 descriptor（对齐 DSH delegation 事件精神，子代理策略可从自身记录重建）。

## 结构化输出（outputSchema）

- 子代理 scope 内挂载 `structured_output` capture 工具（对齐 DSH）：工具 schema = 请求的 `outputSchema`；提示段（order 190）说明"完成时调用一次"；终态 guard 在 capture 成功后拦截其余工具调用。
- 结果校验 JSON Schema 后作为结构化结果返回父（失败以错误结果呈现，父可重试）。

## 持久化（descriptor）

- `session.LaneConfig` 扩展：`Persona`、`ToolFilter`、`MaxDepth`、`DelegationDepth`、`Inherit` 字段（运行时权威）。
- 新增 v4 metadata entry 类型 `descriptor`（model-hidden）：记录子代理组成快照（provider/model/persona/toolFilter/depth/继承事实/策略快照），投影跳过（不生成消息），export/import 保留。
- 冷恢复：子代理会话从 store 加载时以 lane.config + descriptor entry 重建组成。

## 进程隔离（subagent RPC）

- `--subagent-rpc` 握手扩展：可序列化配方跨进程传递——fork 种子消息、persona、toolFilter、delegationDepth、maxDepth、策略快照、lane.config；进程内注册（transforms/projectors/自定义工具）不跨进程（维持 AGENTS.md 约定）。

## 验收与兼容

- 三层验收：
  1. **行为测试矩阵**：fork 种子截断与持久化、spawn 零消息、persona 继承/shadow、toolFilter 交集/剔除、depth 超限拒绝与 resume 不归零、outputSchema 校验与 guard、descriptor 重建。
  2. **兼容回归**：现有 task/skill/plugin 子代理默认行为不变（spawn 语义、allowed-tools 语义、hooks 传播），旧测试全绿。
  3. **全量构建**：`go build ./...` 与 `go test ./...`（相关包：contextbuild / runtime / agenttool / sessionstore / acp / cli）。
- 子代理路径删除旧手工 `AgentContext` 组装（`subagent_child.go`），统一走 `BuildSessionContext + BuildProviderContext`。

## 建议实施切片

1. scope 分层基础设施：`contextbuild.Registry` parent 链 + 工具注册表分层 + shadow 合并（含单测）。
2. 子代理接入 contextbuild：`SubAgentSpec` 扩展 + `subagent_child.go` 换管线 + 旧路径删除。
3. fork/spawn 语义：种子截断、持久化、`task`/skill/plugin 参数接线。
4. persona + toolFilter：继承、shadow、allow/deny 双层。
5. delegationDepth + 策略快照 + descriptor：lane.config 扩展、v4 descriptor entry、冷恢复。
6. outputSchema：capture 工具 + 提示段 + guard + 校验。
7. 进程隔离握手 + 兼容回归 + 三层验收。

## 验证

- `go build ./...`。
- 行为测试矩阵、兼容回归、全量相关包测试。
- REPL / TUI / headless / serve / ACP 子代理路径回归（skill 子代理、plugin 子代理、task 并行 fan-out）。
