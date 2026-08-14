# contextbuild 配置表形态与默认值

> 状态：**superseded**——由 `tasks/spec/spec-018-contextbuild-registry-dsh-alignment.md` 取代（对齐目标改为 deepseek-harness），归档保留。
> 注：本卡原始内容因编码事故损坏且未入 git，以下按 `tasks/spec/spec-015-contextbuild-pi-alignment.md` 09 配置章节重建。

Type: grilling
Status: resolved
Blocked by: issue-037

## Question

contextbuild 的配置表形态与默认值：`[contextbuild]` TOML 表字段（context_files / append_system_prompt / plugin_dirs）的语义与落点；三个 CLI flag（--no-context-files / --append-system-prompt / --plugin-dir）与配置的优先级；现有顶层配置项（system_prompt / no_skills / allowed_tools / prompts）是否复制到新表；skills 目录是否新增 skill_dirs；plugin_dirs 的路径解析与信任门控；配置/trust 变化时的 registry 重建与指纹失效。

## Comments

- 2026-08-13 Codex: claimed via wayfinder workflow; grilling started.
- Q1: A - `[contextbuild]` 表：context_files（默认 true，--no-context-files 置 false）、append_system_prompt（可重复，文本或路径）、plugin_dirs（可重复，绝对或 cwd 相对）。
- Q2: A - 落点：`~/.config/pigo/config.toml` 新增 `[contextbuild]`，FileConfig 增加 ContextBuild ContextBuildConfig；优先级 CLI > config.toml > default。
- Q3: A - 现有顶层 system_prompt / no_skills / allowed_tools / prompts 不复制到新表。
- Q4: A - 新增 CLI：--no-context-files、--append-system-prompt（可重复）、--plugin-dir（可重复）。
- Q5: A - plugin_dirs 绝对路径原样使用，相对路径按 session cwd 解析；显式目录视为用户授权，不按项目 trust 门控。
- Q6: A - skills 不新增 skill_dirs，继续使用 SkillsDir() + no_skills。
- Q7: A - 配置或 trust 变化时，下一次 provider 请求前惰性重建 session registry 并让 system prompt 指纹失效。

## Answer

- `[contextbuild]` 配置表落地：`context_files`（默认 true）、`append_system_prompt`（可重复；文本或路径）、`plugin_dirs`（可重复；绝对或 cwd 相对）。
- 落点：现有 `~/.config/pigo/config.toml` 新增 `[contextbuild]`，`FileConfig` 增加 `ContextBuild ContextBuildConfig`；优先级 CLI > config.toml > default。
- 现有顶层 `system_prompt` / `no_skills` / `allowed_tools` / `prompts` 不复制到新表。
- 新增 CLI：`--no-context-files`、`--append-system-prompt <text|path>`（可重复）、`--plugin-dir <path>`（可重复）。
- `plugin_dirs` 绝对路径原样使用，相对路径按 session cwd 解析；显式目录视为用户授权，不按项目 trust 门控。
- skills 不新增 `skill_dirs`，继续使用 `SkillsDir()` + `no_skills`。
- 配置或 trust 变化时，下一次 provider 请求前惰性重建 session registry 并让 system prompt 指纹失效。
