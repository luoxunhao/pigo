# system prompt 实时构建与 context files

> 状态：**superseded**——由 `tasks/spec/spec-018-contextbuild-registry-dsh-alignment.md` 取代（对齐目标改为 deepseek-harness），归档保留。

Type: grilling
Status: resolved
Blocked by: issue-037

## Question

`BuildSystemPrompt` 升级后的精确规则：`AGENTS.override.md` / `AGENTS.md` / `CLAUDE.md` 的候选顺序与优先级；全局 agent 目录、祖先链、git worktree 遮蔽语义；skills / tools / append-system-prompt 的注入位置；何时重建；header `SystemPrompt` 降级后的读取与写入策略。

## Comments

- 2026-08-13 Codex: claimed via wayfinder workflow; grilling started.
- Q1: A - 每目录只取一个 context file，优先级 `AGENTS.override.md` > `AGENTS.md` > `AGENTS.MD` > `CLAUDE.md` > `CLAUDE.MD`；override 替换同目录其余候选，跨目录文件仍按序叠加。
- Q2: A - 全局 agent 目录采用 `$PIGO_HOME`（默认 `~/.pigo`），对应 pi 的 `~/.pi/agent`。
- Q3: A - 祖先链走到文件系统根：global → cwd→root 全部祖先，按 canonical path 去重。
- Q4: A - 对齐 pi：嵌套 linked worktree 跳过自身同名 context file，让主仓库根那份生效。
- Q5: A - 注入分层：base（含 active tools 段）→ append-system-prompt → context files → skills → environment/cwd。
- Q6: A - 每次 provider 请求前按输入指纹实时组装；指纹不变复用同一字符串，turn 内工具集不变则 prompt 稳定。
- Q7: A - header `SystemPrompt` 不再作为构建输入；每轮结束后更新为实际发送的 prompt，仅用于导出/展示兼容。
- Q8: A - environment 去掉 Date，保留 cwd 与 OS/arch 等静态信息，prompt 指纹跨天稳定。
- Q9: A - context files 用 `<project_instructions path="...">...</project_instructions>` XML 边界包裹注入。

## Answer

2026-08-13 确认（wayfinder grilling）：

- context file 选取：每目录只取一个文件，候选优先级 `AGENTS.override.md` > `AGENTS.md` > `AGENTS.MD` > `CLAUDE.md` > `CLAUDE.MD`；override 替换同目录其余候选，跨目录文件仍按序叠加。
- 来源：全局 agent 目录为 `$PIGO_HOME`（默认 `~/.pigo`），先注入全局；随后从 cwd 向上走到文件系统根注入祖先链，canonical path 去重。
- worktree：嵌套 linked worktree 跳过自身同名 context file，让主仓库根的同名文件生效。
- 注入分层：base（含 active tools 的 Available tools / Guidelines 段）→ append-system-prompt → context files → skills → environment/cwd；environment 去掉 Date，保留 cwd 与 OS/arch 等静态信息。
- 重建：每次 provider 请求前按输入指纹实时组装；指纹含 base instruction / cwd / context files / skills / active tools / append instructions，不变复用同一字符串，turn 内工具集不变则 prompt 稳定。
- header `SystemPrompt`：不再作为构建输入；每轮结束后写回实际发送的 prompt，仅用于导出/展示兼容。
