# contextbuild 注册表化并放弃 pi parity，改对齐 DSH

pigo 的 contextbuild（spec-015）从固定拼接的 system prompt 组装升级为 deepseek-harness 式注册表模型：contextbuild.Registry 一次扩展为完整分层注册表（sections / contexts / variables / transforms / projectors / tool providers，全部 parent 链 + 按名 shadow），与 subagent 的 scope 分层共用同一套体系；persona 文案改为 DSH 风格（"You are a coding agent powered by the {{model}} model..."），工具引导段（tool:*）与 sandbox 策略段（sandbox:policy）统一迁入 contexts 注册。同时**放弃 pi 逐字节 parity**（spec-015 验收作废，parity corpus 归档），改以注册表行为测试 + 自持 golden 快照 + 与 DSH standard preset 手工语义对照验收。之所以放弃 pi 对齐，是因为 pi 与 DSH 的组装模型本质不同（固定顺序 vs 注册表），继续锁 pi 输出会使注册表机制（shadow/complete/variables/scope）无法发挥；而 DSH 的 composition 由每部署决定、无确定性对标实现，逐字节 parity 不可行，只能行为对照。动态快照投影（contexts 渲染为持久化 user message）属第二步，本轮不做。

Status: accepted

## Considered Options

- **输出不变、机制升级（parity 继续锁 pi）**：被否。注册表能力（shadow/complete/variables/scope）被 pi 固定输出顺序束缚，机制与目标矛盾。
- **两套 scope（subagent 一套、contextbuild 一套）**：被否。shadow/继承语义分裂，后续合并成本高。
- **策略段保持 reminder 注入**：被否。与 contexts 语义重复，DSH 的 sandbox:policy 本就是 context。

## Consequences

- spec-015 与 issue-036~045 标记 superseded（归档保留）；parity corpus 归档。
- system prompt 输出顺序与 persona 文案变化（"You are pigo" 移除），指纹缓存改为静态骨架 + 渲染时变量插值。
- issue-046（scope 分层）与 issue-054（PolicyReminder）并入/修订承接。
- 会话持久化（v4 entries、lane.config）不受本轮影响；动态快照投影（第二步）才会触碰消息持久化。
