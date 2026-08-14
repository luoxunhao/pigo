# 引入 scope 分层体系对齐 DSH 子代理机制

为对齐 deepseek-harness 的子代理上下文构建（fork/spawn 语义、persona shadow、toolFilter、descriptor 持久化），pigo 采用机制照搬而非纯语义对齐：在 contextbuild.Registry 与工具注册表上引入分层 scope——每个 agent（会话/子代理）拥有自己的注册层，子代理经 parent 链继承父层并可 shadow，而不是把 DSH 行为翻译成扁平的白名单/参数组合。之所以照搬机制而非仅对齐行为，是因为 DSH 的"继承 + shadow"语义（子代理自动获得父的 persona/工具引导/策略段）在扁平实现里需要逐项手工复制且无法表达"默认继承、按名覆盖"，scope 分层是表达该语义最直接的结构；不引入 Cordis 式服务注入，hooks/plugin 已覆盖该职责。子代理由此接入既有 contextbuild 管线，消除 subagent_child 的手工 AgentContext 旧路径。

Status: accepted

## Considered Options

- **语义对齐（Go 原生实现）**：只对齐行为，用显式配方参数（inherit flag、persona 文本、allow/deny 列表）表达继承。被否：继承的注册项（提示段、transforms、工具引导）会退化为一次性快照，无法随父变化演进，shadow 语义需逐名特判。
- **机制照搬（引入 scope 体系）**：采纳。registry + tools 分层，parent 链 + 按名 shadow，与 DSH 的 ScopedLayers 语义一致；不引入服务注入（pigo 无 ctx 服务体系，hooks/plugin 已覆盖）。

## Consequences

- contextbuild.Registry 与工具注册表需支持 parent 链与 shadow 合并；注册生命周期按 scope 创建/销毁。
- 子代理（fork 与 spawn）统一走 BuildSessionContext/BuildProviderContext，旧手工 AgentContext 路径（subagent_child.go）删除。
- 进程隔离子代理只能传递可序列化配方（种子/persona/toolFilter/深度/策略快照），进程内注册不跨进程。
- 现有 task/skill/plugin 子代理默认保持 spawn 语义（spec-013 契约不变），fork 为显式选项。
