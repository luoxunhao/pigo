# contextbuild API 与数据流

> 状态：**superseded**——由 `tasks/spec/spec-018-contextbuild-registry-dsh-alignment.md` 取代（对齐目标改为 deepseek-harness），归档保留。

Type: grilling
Status: resolved
Blocked by:

## Question

`internal/contextbuild` 的公共类型和一次 provider 请求的构建顺序应该是什么？需要定：`BuildSessionContext` / `BuildProviderContext` / `TransformContext` / `EntryProjector` / `ConvertToLlm` 的输入输出；`ProjectLeaf`、system prompt、tools、reminders 在哪一步汇入；构建失败的错误语义；纯函数与 I/O 的边界。

## Comments

- 2026-08-13 Codex: claimed via wayfinder workflow; grilling started.
- Q1: A - 两段式：BuildSessionContext 纯投影，BuildProviderContext 请求组装。
- Q2: C - 核心纯逻辑，I/O 通过 BuildDeps 注入，BuildProviderContext 为编排入口。
- Q3: A - ProviderRequest 包含 Model/Provider/ThinkingLevel + LlmContext，不含 API key。
- Q4: A - 组装顺序：model/tools/system prompt -> TransformContext -> ConvertToLlm -> LlmContext。
- Q5: A - 外层返回 error 并转 terminal message；内部接缝保持非 error 安全回退。
- Q6: A - EntryProjector 在 BuildSessionContext 纯投影阶段，按 customType 注册，未注册跳过。
- Q7: A - TransformContext/ConvertToLlm 保持现有非 error 签名，放入 BuildDeps。
- Q8: A - SessionContext 含 Model/Provider/ThinkingLevel/ActiveToolNames；nil 表示全量工具。

## Answer

`internal/contextbuild` 采用两段式公共 API：

- `BuildSessionContext(proj *session.ProjectLeaf, opts SessionBuildOptions) (*SessionContext, error)`：纯函数投影，输入 ProjectLeaf + EntryProjector，输出 Messages / Model / Provider / ThinkingLevel / ActiveToolNames；`ActiveToolNames == nil` 表示未记录、使用全部工具；未注册 customType 跳过；构建失败返回 error。
- `BuildProviderContext(ctx, sess *SessionContext, deps BuildDeps, req RequestOptions) (*ProviderRequest, error)`：编排入口，I/O 全部经 BuildDeps 注入（PromptBuilder / ToolResolver / ReminderRegistry / TransformContext / ConvertToLlm）。顺序为 model/thinking/tools 解析 -> 指纹缓存构建 system prompt -> TransformContext（reminders/hooks 注入请求副本）-> ConvertToLlm -> `ProviderRequest{Model, Provider, ThinkingLevel, LlmContext}`，不含 API key。
- `TransformContext(ctx, msgs) msgs`、`ConvertToLlm(msgs msgs) msgs`、`EntryProjector(entry, index, entries) []Message` 均为非 error 接缝，异常安全回退；外层构建 error 由 loop 转成 terminal assistant message。
- reminders 永远走 TransformContext 注入请求副本，不进持久历史。
- system prompt "实时组装"按输入指纹缓存：base instruction / cwd / context files / skills / active tools / append instructions 不变则复用同一字符串，保护 prompt cache。
