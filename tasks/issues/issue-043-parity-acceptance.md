# pi harness parity 验收标准

> 状态：**superseded**——由 `tasks/spec/spec-018-contextbuild-registry-dsh-alignment.md` 取代（对齐目标改为 deepseek-harness），归档保留。

Type: grilling
Status: resolved
Blocked by: issue-038, issue-039, issue-040, issue-041

## Question

与 pi harness 的语义对照表和测试矩阵：哪些行为必须一致（投影、转换、system prompt 顺序、扩展注入）；哪些允许差异（Go provider 抽象、无 extension 事件系统、性能）；验收基线用什么 fixture；失败时如何记录偏差。

## Comments

- 2026-08-13 Codex: claimed via wayfinder workflow; grilling started.
- Q1: A - 双层锚点：BuildSessionContext 输出对齐 pi buildSessionContext；BuildProviderContext 最终 provider 可见请求对齐 pi；Go 内部 API 不要求一一对应。
- Q2: A - pigo 自持 golden fixture 包：internal/contextbuild/testdata/parity/ 小型确定性场景，期望输出从 pi 当前行为生成一次并冻结，记录 pi commit，提供刷新脚本。
- Q3: A - 规范化语义相等：system prompt 逐字节精确；消息按语义字段精确且保序；tools 按 name/description/schema 精确；存储元数据、usage/cost、id/timestamp 不进对照。
- Q4: A - fixture 输入 = ProjectLeaf + LaneConfig + entries；期望 state 等于 LaneConfig；pi 侧状态仅作等价逻辑状态元数据，不比较推导机制；消息投影逐条对照。
- Q5: A - 只对齐通用 ConvertToLlm 输出；provider 专属 wire 塑形不进 parity corpus，由 pigo 自己的 provider 单测覆盖。
- Q6: A - 只对照扩展注入最终效果（位置/顺序/内容）；注册机制、hook 名称、plugin 形态不纳入 parity。
- Q7: A - 允许差异清单 = Go provider 抽象与 wire 塑形、扩展注册/事件机制、存储元数据与 usage/cost、性能；性能完全出 parity，无验收阈值。
- Q8: A - fixture 内嵌 deviations 注册表（code/scope/pi commit/原因）；刷新脚本对未注册偏差 fail，已注册偏差在报告列出；pi commit 变化时强制复核。

## Answer

2026-08-13 确认（wayfinder grilling）：

- 验收锚点：双层。第一层 `BuildSessionContext` 输出与 pi `buildSessionContext` 对照：消息投影（顺序、集合、stopReason 过滤、compaction、自定义 projector）与逻辑状态；model/thinking/activeTools 以 `lane.config` 为权威，不比较 pi 的 entry 推导机制。第二层 `BuildProviderContext` 的 provider 可见请求与 pi 最终请求对照：system prompt、消息、tools、reminders。
- fixture：pigo 自持 golden corpus（`internal/contextbuild/testdata/parity/`），小型确定性场景，期望输出从 pi 当前行为生成一次并冻结，fixture 记录 pi commit，提供刷新脚本。
- 对照规则：规范化语义相等；system prompt 逐字节精确；消息按 `role/content/toolCallId/stopReason/isError` 等语义字段精确且保序；tools 按 `name/description/schema` 精确；存储元数据、usage/cost、id/timestamp 不进对照。
- system prompt section 顺序锁定为 pi `buildSystemPrompt` 当前顺序：base -> tools -> guidelines -> pi docs -> append -> project context -> skills -> cwd。
- `ConvertToLlm`：只对照 provider 无关通用转换；provider `transformMessages` / wire 塑形由 pigo 单测覆盖。
- 扩展注入：只对照最终效果（位置、顺序、内容）；注册机制、hook 名称、plugin 形态不纳入。
- 允许差异：Go provider 抽象与 wire 塑形、扩展注册/事件机制、存储元数据与 usage/cost、性能（无验收阈值）。
- 偏差：fixture 内嵌 `deviations` 注册表（`code/scope/pi commit/原因`）；刷新脚本对未注册偏差 fail，已注册偏差在报告列出；pi commit 变化时强制复核。
- fixture 场景：compaction retained tail / stopReason 过滤 / custom projector / entry transform / lane.config state；`ConvertToLlm` role 与内容块；system prompt default/custom/context/skills/append/tools；registry 顺序链、reminders、plugin 声明注入。

测试矩阵：

| 维度 | 对照对象 | 判定 | 允许差异 |
| --- | --- | --- | --- |
| 投影 | `BuildSessionContext` messages + state | 语义字段精确且保序；state = `LaneConfig` | entry 推导机制 |
| 转换 | `ConvertToLlm` 输出 | role/content/toolCallId/isError 精确 | provider wire 塑形 |
| system prompt | 完整字符串与 section 顺序 | 逐字节精确 | provider 放置方式 |
| 扩展注入 | 注入内容的位置与顺序 | 最终效果精确 | 注册机制/hook/plugin 形态 |
| 工具 | activeToolNames + tool 定义 | name/description/schema 精确 | Go tool 实现 |
| 元数据 | id/timestamp/usage/cost | 不参与对照 | 全部允许差异 |
| 性能 | 无 | 不参与对照 | 全部允许差异 |
