# EntryProjector 与 TransformContext 扩展接缝

> 状态：**superseded**——由 `tasks/spec/spec-018-contextbuild-registry-dsh-alignment.md` 取代（对齐目标改为 deepseek-harness），归档保留。

Type: grilling
Status: resolved
Blocked by: issue-037, issue-039

## Question

`contextbuild` 扩展注册表 API 与 hooks/plugins 接线：`EntryProjector` / `TransformContext` 的注册顺序、覆盖/冲突、生命周期；与现有 `ReminderRegistry`、hooks dispatcher、plugin manager 的关系；是否需要 pi 风格 `before_request` / `transform_context` 事件；错误处理。

## Comments

- 2026-08-13 Codex: claimed via wayfinder workflow; grilling started.
- Q1: A - 新增类型化 contextbuild.Registry：RegisterTransform 按注册顺序追加，RegisterEntryProjector 按 customType 唯一键注册。
- Q2: A - TransformContext 全部执行按注册顺序串联；EntryProjector 同一 customType 重复注册返回 error（顺序链 + 唯一键拒绝）。
- Q3: A - 注册表按会话组装：built-in 先注册，再按 session cwd/trust 接入 hooks 扩展，plugin 贡献以进程级只读源参与；trust/config 变化时重建。
- Q4: A - ReminderRegistry 内置为 registry 的一条 transform（name=reminders），复用其 provider API；BuildProviderContext 只从 registry 顺序链消费，hooks additionalContext 继续走 one-shot reminder。
- Q5: A - 不新增 shell transform_context 事件；shell hooks 继续走 additionalContext -> one-shot reminder -> registry 内置 reminders transform。
- Q6: A - 本轮不引入 before_request（未实现，留待后续单独 ticket；不要当作已落地）。
- Q7: A - plugin 通过声明式 manifest 接缝：Manifest 新增 entryProjectors 与 contextTransforms，pigo 进程内渲染，不新增每请求 RPC。
- Q8: A - 固定跨来源注册顺序：built-in -> 会话 hooks/进程内注册 -> plugin 声明；EntryProjector 冲突按 Q2 拒绝。
- Q9: A - 分来源错误处理：built-in/hooks 注册错误 fail-closed，plugin 声明错误按 Discover 容错跳过并 warn；运行期 transform/projector panic recover + warn + 回退。

## Answer

2026-08-13 确认（wayfinder grilling）：

- `contextbuild.Registry` 是会话级类型化注册表：`RegisterTransform(name, fn)` 按注册顺序追加；`RegisterEntryProjector(customType, fn)` 按 customType 唯一键注册，重复注册返回 error。
- `TransformContext` 全部执行、按注册顺序串联，每个看到前一个的输出；`EntryProjector` 同一 customType 拒绝重复（顺序链 + 唯一键拒绝）。
- 生命周期：每个 session 组装一次 registry，built-in 先注册，再按 session cwd/trust 接入 hooks/进程内注册；plugin 贡献以进程级只读源参与；trust/config 变化时重建。
- `ReminderRegistry` 内置为 registry 的一条 transform（`name=reminders`），内部复用其 provider API；`BuildProviderContext` 只从 registry 顺序链消费；hooks `additionalContext` 继续走 one-shot reminder。
- 不新增 shell `transform_context` 事件；shell hooks 维持现有 `UserPromptSubmit` / `SessionStart` -> additionalContext -> one-shot reminder 路径。
- 本轮不引入 `before_request`（未实现，后续单独 issue-044 承接，不当作已落地）。
- plugin 接缝为声明式 manifest：新增 `entryProjectors`（customType -> 固定/模板 user 消息）与 `contextTransforms`（固定注入文本），pigo 进程内渲染，不新增每请求 RPC。
- 跨来源注册顺序固定为 built-in -> 会话 hooks/进程内注册 -> plugin 声明。
- 错误处理：built-in/hooks 注册错误 fail-closed（session 构建失败）；plugin 声明无效/冲突按现有 `Discover` 容错，跳过该插件并 warn；运行期 transform/projector panic 时 recover、warn 并回退原始消息列表或跳过该 projector 输出。
