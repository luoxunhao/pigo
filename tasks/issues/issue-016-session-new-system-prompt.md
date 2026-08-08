# pigo session/new 按请求 cwd 构建 system prompt

## Description

将 pigo ACP 从“进程启动 cwd 决定 system prompt”改为 `session/new` 按请求 `cwd` 构建会话 system prompt。共享进程服务多个项目时，每个新会话都使用自己的项目目录生成环境块与 AGENTS.md 链。对应 PRD US-001 / FR-1、FR-3、FR-4。

## Acceptance Criteria

- [ ] `session/new` 使用请求 `cwd` 作为 `BuildSystemPrompt` 的 `WorkingDir` 与 `Root`
- [ ] 新会话 header 的 `SystemPrompt` 包含该 `cwd` 的 Environment 工作目录
- [ ] 项目 AGENTS.md 按 `cwd` 路径链注入，不读取进程启动目录
- [ ] 同一共享进程创建 `E:\project\ams` 与 `E:\project\ash-workbench` 两个会话时，两个 header 的 Working directory 各自正确
- [ ] 对应 Go 单元测试通过

## Dependencies

None

## Type

backend

## Priority

high
