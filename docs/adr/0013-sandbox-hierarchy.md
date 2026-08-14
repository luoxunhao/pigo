# 引入 sandbox 体系（模式 + 执行强制 + escalation）

pigo 从零引入 sandbox 体系，对齐 deepseek-harness 的四层架构：三档 sandbox mode（read-only / workspace-write / danger-full-access）作为文件边界策略，执行级强制（进程内 file fence + 进程级进程沙箱）在工具执行时兜底，模式经 PolicyReminder 每轮注入模型上下文，被拒操作可经 escalation（sandbox_permissions + justification + 用户 allowed-once 审批）单次放宽。之所以一次性引入完整体系而非只加"拒绝"层，是因为模式化边界（模型可见）、执行强制（不可绕过）、升级通道（拒绝可行动）三者互为前提——只有强制没有告知，模型无法决策；只有告知没有强制，边界形同虚设；拒绝无升级通道，工作流频繁中断。实现上模式状态存 lane.config 寄存器（复用 pigo 既有配置权威架构，与 DSH 的事件折叠语义等效），进程沙箱后端 Go 原生实现（Windows CreateRestrictedToken + Job Object、Linux landlock 降级 bwrap、macOS sandbox-exec），后端不可用严格 fail-closed（SANDBOX_UNAVAILABLE）而非静默降级。

Status: accepted

## Considered Options

- **只做决策层（trust + hooks 扩展）**：被否。无执行级强制，边界可被绕过；且模型不知情。
- **只做执行层（进程沙箱）**：被否。无模式告知与升级通道，体验割裂。
- **分级降级（后端不可用时退化为仅 fence）**：被否。静默降级制造"看似受保护实则裸奔"的安全空洞；严格 fail-closed 更一致。

## Consequences

- 默认模式 workspace-write：现有工作流（工作区内写文件）行为不变，越界行为首次被强制。
- 信任链变为 hooks → trust → sandbox 三层：hooks/trust 管"该不该调用"，sandbox 管"能否执行"。
- 模式状态进入 lane.config：会话级覆盖持久化，resume 直读；/sandbox 命令为切换入口。
- 新增后端依赖面：Windows 受限令牌 runner（x/sys/windows）、Linux landlock 绑定 / bwrap、macOS sandbox-exec；各平台后端需探针与 SANDBOX_UNAVAILABLE 错误路径。
- escalation 引入新的审批语义（allowed-once 升级），复用 trust 询问通道，不与 allow_always 混淆。
