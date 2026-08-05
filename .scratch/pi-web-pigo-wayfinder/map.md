# WAYFINDER MAP: pi-web 接入 pigo ACP

**Status:** complete

## Destination

pi-web（luoxunhao/pi-web fork）通过 pigo 的 stdio ACP 后端提供多 workspace 持久会话 Web UI；pigo 仓库新增 `sdk/node/pigo-acp` npm 包与 ACP 扩展；两个仓库都在 master 上推进，按 M1/M2/M3 交付。

## Notes

域：Go ACP server（pigo）+ TypeScript/Node Web UI（pi-web）。每 session 先读本 map 与相关 ticket；实施前必须先解决 open ticket 对应的决策。Windows 为主运行环境；pigo 已修复 WSL relay 误选。

## Decisions so far

- 改造位置：pi-web fork 内新增 PigoSessionService，保留 Pi backend 可切换
- v1 范围：最小闭环但可用；归档/清理/跨机器降级
- 会话列表：pigo 补标准 `session/list`
- 切换配置：`agentBackend: pi|pigo` + `pigo.command/args`，默认 pi
- ACP client：独立 npm 包 `pigo-acp`，放 pigo `sdk/node/pigo-acp`，先本地 file: 依赖
- 进程模型：按 workspace 一个常驻 `pigo --acp` 进程；workspace 可附加目录
- 权限审批：复用 pi-web askUser 弹窗（单选可承载 allow/reject 四选项）
- 模型/认证：`pigo/models` + `model/set` 完整目录同步；pi-web 复刻 pigo 配置页
- 配置读写：`pigo/config` CRUD，密钥不回显；保存后自动重启 pigo 进程
- 协议形态：沿用 `pigo/*` 扩展；模型切换走标准 `model/set`
- 子会话：pi-web 子会话按钮映射 pigo `btw`，父/子树对齐
- 会话标题：pigo 隐藏 agent 在首条消息后生成一次并写入 metadata
- 删除/归档：pigo 补标准 `session/delete`；pi-web 保留删除，隐藏归档
- slash：`/` 开头输入直接走 `pigo/command`
- 分页：pigo 补 `pigo/messages`
- 附件：pigo 增加 resource_link/resource 内容支持
- 附加目录：pigo 实现标准 `additionalDirectories`
- 默认命令：`pigo --acp`（PATH），config 可覆盖成本地 exe
- 发布：`pigo-acp` 先本地依赖，稳定后发 npm
- 分支：两仓库均直接在 master 提交
- 里程碑：M1 主链路 + 配置页；M2 新协议 + UI；M3 子会话树 + 附件 + npm
- [01 session/load 历史消息契约](tickets/01-session-load-messages-contract.md) — `session/load` 携带首屏消息，形状对齐 pi-web MessagePage
- [02 pigo 扩展契约总表](tickets/02-pigo-extension-contracts.md) — 标准 capabilities 与 `_meta` 扩展分工；密钥不回显；models 目录；resource 任意路径限 64KB
- [03 sessiond 进程生命周期](tickets/03-sessiond-process-lifecycle.md) — 进程退出标记中断可恢复；空闲 10 分钟回收；配置保存确认后重启
- [04 SessionRouteService 降级矩阵](tickets/04-sessionroute-fallback-matrix.md) — 隐藏归档/清理/通知/跨机器；删除保留；session shell 走 pigo/command
- [05 thinking level 与命令目录映射](tickets/05-thinking-commands-mapping.md) — `/think` 切换；静态命令清单；事件驱动 status
- [06 Windows 运行前提](tickets/06-windows-runtime.md) — Node/npm 满足；node-pty 安装时验证；WSL relay 已修复

## Not yet specified

无。所有可明确的问题已转 tickets 01-06 并解决；新 fog 在实施过程中若出现，另行补票。

## Out of scope

- Pi SDK OAuth 登录流程搬进 pi-web（密钥仍由 pigo config.toml 管理）
- pi-web fleet 远程机器代理 pigo 会话（后续 effort）
- 归档/清理/通知中心/跨机器会话同步
- ACP elicitation 完整实现
- 超大上下文性能优化（先分页，后续再做流式裁剪）
