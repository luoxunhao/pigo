# Wayfinder Map — pigo 统一 ACP 接口与 TUI 迁移试点

> Local-markdown tracker（GitHub tracker 未配置：无 `gh`、无 token）。
> Status: charting complete，等待选择票证。实施按用户要求带入 map（"实施计划"），任务票同时是 tracer-bullet 切片。

## Destination

`tasks/spec-acp.md` 落地：pigo 的 ACP server 成为唯一前端契约；TUI 全量迁移试点完成（chat、slash、会话树、remote control、goal/btw/dream 全部走 ACP 且行为不退化）；桌面端在试点验证通过后作为新 effort 启动。

## Notes

- Domain：ACP（Agent Client Protocol）、pigo agent core、TUI/REPL、trust、project-scoped session store、provider/liveconfig。
- 每个 session 应查阅：`grilling`、`domain-modeling`、`to-spec`（spec 在 `tasks/spec-acp.md`）、`to-tickets`（切片）、`wayfinder`（本 map）。
- 既定偏好：单一 ACP wire seam 测试；不引入 Rust/Tauri；桌面端推迟；`pigo/*` 扩展仿 `peri/*` 通道；transcript 复用既有 JSONL 树形格式；session store 对齐 ash 事实模型；legacy 扁平会话只读保留。
- 决策记录：此前 grilling 的 13 个决策已固化在 `tasks/spec-acp.md`，本 map 只做索引，不再逐条 restate。
- 当前 frontier（开放且无阻塞）：`01 project-scoped 会话存储`、`02 ACP transport 与 initialize 握手`、`D-01 pigo/* 扩展协议形态`、`D-02 TUI 桥接 seam 形态`。

## Decisions so far

- 交付顺序：先 TUI 全量迁移试点，桌面端推迟（spec §Solution、§User Stories）。
- 技术栈：Go ACP server；transport 抽象 + in-process/stdio 两种实现；不引入 Tauri/Rust（spec §Implementation Decisions 1/5）。
- 会话模型：project-scoped store，workspace slug，schemaVersion，legacy 保留（spec §6/7）。
- 权限：四个选项映射 trust（spec §8）。
- 事件：标准 `session/update` + `pigo/event` 扩展通道（spec §3/4）。
- 测试：单一 ACP wire seam（spec §Testing Decisions）。

## Not yet specified

- TUI 渲染层与 ACP 事件流之间的中间表示细节：随 D-02 原型后清晰。
- legacy 扁平会话迁入新 store 的时机与格式映射：暂不自动迁移，迁移入口留待后续。
- remote control 多设备/多客户端策略：D-04 解决归属后，其余细节随实现清晰。

## Out of scope

- 桌面端（Electron 壳、`@pigo/*` 前端包）——试点后新 effort。
- 纯 Web UI、IDE（Zed/JetBrains）适配验收。
- `peri/*` 私有协议移植——用 `pigo/*` 替代。
- 打包/自动更新/签名、Playwright E2E、性能压测。
- legacy 扁平会话自动迁移（仅预留入口）。
