# SPEC: pigo 统一 ACP 接口与 TUI 迁移试点

> Technical specification derived from the 2026-08-04/05 design conversation.
> Target tracker: GitHub `smallnest/pigo` (publishing pending: `gh` CLI is not installed on this machine).
> Triage label to apply on publication: `ready-for-agent`.

## Problem Statement

pigo 目前是一个只有终端前端的 Go 编码 agent：交互式 TUI/REPL 直接构造 agent 上下文并调用 runtime loop，headless 模式直接消费 stream-json 事件。它没有标准化的 agent 协议接口，因此 IDE、第三方客户端和自建桌面端都无法以统一方式驱动它。任何新前端都必须重新适配 pigo 的内部事件流。

更重要的是，pigo 的 TUI 与 agent 核心是直接集成：渲染层、slash 命令、会话树、权限确认、模型切换等全部通过进程内直连完成。如果只新增一个 stdio ACP 入口，pigo 会同时存在"TUI 直连"和"ACP 协议"两条前端集成路径，这与"一个 agent 核心、一个协议、多个前端"的目标相悖。要建立真正统一的通信架构，必须让 TUI 也成为 ACP 客户端，通过进程内 transport 连接同一个 ACP server。

pigo 的会话持久化是扁平的：所有会话文件放在同一目录，只在会话头记录工作目录，没有 project 维度，无法按工作区隔离会话列表，也无法表达 workspace、hostname、custom metadata、父子会话关系等数据结构。这个数据模型需要按更完整的模型重构。

## Solution

在 pigo 内新增与 peri 同构的 ACP（Agent Client Protocol）服务层：transport 抽象、dispatch、session manager、event mapper、permission broker 分层实现。transport 第一版提供两种实现：**in-process channel transport** 供 TUI 使用，**stdio transport** 供外部 ACP 客户端（IDE、未来的桌面壳）使用。ACP server 成为 pigo 唯一的前端入口，agent core 不再被任何前端直接调用。

TUI 在试点版本中全量迁移为 ACP 客户端：新建/恢复会话、流式文本/思考/工具卡、权限弹窗、取消、模型切换、slash 命令、rewind/fork/status、goal、btw、dream、remote control 等现有能力全部通过标准 ACP 方法与 `pigo/*` 扩展通道保留。TUI 渲染层基本不动，只替换数据通道；agentcore 事件通过扩展事件通道原样送达 TUI，避免功能退化。

会话持久化升级为 project-scoped store：会话按 workspace slug 分组存放，metadata 带 schemaVersion、workspace/hostname、custom metadata、父子关系等字段，transcript 复用 pigo 已经验证过的 JSONL 树形格式。ACP 的 session id 直接使用 pigo 自己的会话 id，`session/load` 即会话恢复，跨进程续聊可用。

桌面端（Electron 壳 + 复刻的 ash 前端）推迟到 TUI 试点验证完成之后，作为同一 ACP 后端的第二个前端实现。

## User Stories

1. 作为 CLI 用户，我希望运行一个 ACP 模式入口，让任意 ACP 客户端通过 stdio 启动并驱动 pigo，这样 IDE 和自建前端不必关心 pigo 内部实现。
2. 作为 TUI 用户，我希望 TUI 启动时通过进程内 ACP 连接后端，这样我无感获得统一协议架构，不需要学习新命令。
3. 作为 TUI 用户，我希望新建会话、发 prompt、看流式文本/思考/工具卡、取消、切模型等主路径行为与今天完全一致，这样试点不会降低日常体验。
4. 作为 TUI 用户，我希望 `/model`、`/think`、`/compact`、`/trust` 等 slash 命令仍然可用，这样试点期间没有功能退化。
5. 作为 TUI 用户，我希望 `/rewind`、`/fork`、`/tree`、`/status` 等会话树能力仍然可用，这样我可以继续管理长会话。
6. 作为 TUI 用户，我希望 `/goal`、`/btw`、dream 等能力仍然可用，这样高级工作流不被试点阻断。
7. 作为 TUI 用户，我希望 `/remote-control` 仍然可用，这样手机端镜像与远程审批不被试点阻断。
8. 作为 TUI 用户，我希望副作用工具在未信任目录触发权限弹窗，四个选项（允许一次/拒绝一次/总是允许/总是拒绝）行为与今天一致。
9. 作为 TUI 用户，我希望会话按项目隔离，重启后能恢复历史，这样多项目工作不互相污染。
10. 作为 IDE 用户，我希望先完成 initialize 握手并获得能力声明，这样 IDE 知道 pigo 支持 session load、close 等生命周期能力。
11. 作为 IDE 用户，我希望 session/new 返回 pigo 自己的会话 id 和当前模型，这样后续 prompt、load、close 都指向同一个稳定会话。
12. 作为维护者，我希望 ACP 协议层通过一条 wire 协议 seam 做集成测试，这样协议、会话、事件映射、权限、存储一次验证。
13. 作为维护者，我希望 transport 是接口而非具体实现，这样 in-process 与 stdio 共用同一套 dispatch。
14. 作为维护者，我希望 event mapper 输出标准 ACP session/update，同时通过 `pigo/*` 扩展通道原样承载 agentcore 事件，这样外部客户端只消费标准通道，TUI 消费完整通道。
15. 作为维护者，我希望新会话存储的布局和 metadata 结构对齐 ash 的既有事实模型，这样未来桌面端可以直接读取，而不是永久双写。
16. 作为维护者，我希望旧版扁平会话文件保持可读，现有 CLI/REPL/headless 用户不受影响，这样升级不破坏已有数据。
17. 作为产品负责人，我希望 TUI 试点先于桌面端完成，这样协议架构先在已有产品上验证，桌面端复用同一后端。
18. 作为产品负责人，我希望桌面端复用同一 ACP 后端与复刻的 ash 前端包，这样"一个后端、多个前端"的路线可以继续扩展。
19. 作为 MIT 合规负责人，我希望未来复制的 ash 前端保留原版权声明，这样复用不违反许可证。

## Implementation Decisions

### 1. ACP 服务层分层

新增 Go ACP 服务包，照 peri 的分层结构组织：

- transport 抽象：`send_request`、`send_notification`、`recv`、`send_response` 四个能力；第一版实现 in-process channel transport 与 stdio transport，共用 request router。
- dispatch 层：纯协议逻辑，包括 initialize 能力构建、session/prompt 参数提取、session/load/close 等生命周期处理，与具体 transport 解耦。
- session manager：进程内会话表，每个会话持有 cwd、历史消息、系统提示、会话级模型配置、取消令牌、frozen 数据；同一会话的 prompt 串行执行。
- event mapper：把 agentcore 事件映射为标准 ACP session/update，同时保留原始事件供 `pigo/*` 扩展通道转发。
- permission broker：把 trust 确认流程映射为 ACP `session/request_permission` 请求并阻塞等待客户端响应。

server 主循环按方法字符串分派请求；`session/prompt` 在后台任务执行，主循环保持响应 `session/cancel` 通知。session/close 时取消会话内所有运行中的 agent。

### 2. 第一版 wire 方法面

标准 ACP 方法：

- `initialize`：声明 `load_session=true`、prompt capabilities、session capabilities（close），协议版本 v1；在 agent capabilities 的 `_meta` 中声明 `pigo.*` 扩展能力。
- `session/new`：创建 pigo 会话并落盘，返回 session id、当前模型、空 modes/config options。
- `session/load`：按 session id 恢复历史、系统提示和模型，返回与 new 相同结构的响应。
- `session/close`：取消运行中任务并释放会话状态。
- `session/prompt`：接受文本 prompt，后台执行 agent 循环，通过 `session/update` 通知流式输出。
- `session/cancel`：通知，取消指定 session 的当前 turn，prompt 响应以 cancelled stop reason 结束。
- `session/request_permission`：服务端向客户端发起的请求，携带工具卡与四个选项，客户端返回选中 outcome。
- `model/set`：更新指定 session 的会话级模型配置，下一轮 prompt 生效。

第一版返回空列表或未实现：`command/list`、`command/execute`、`config/options`、`session/resume`、elicitation 相关方法。

### 3. pigo/* 扩展协议

TUI 全量迁移要求标准 ACP 之外的能力，参照 peri 的 `peri/*` 通道设计 `pigo/*` 扩展：

- `pigo/event`：通知通道，原样承载 agentcore 事件（message、tool、compaction、subagent progress、telemetry、rewind、goal、btw 等），TUI 渲染层继续消费；外部客户端不声明该能力则不接收。
- `pigo/command`：请求方法，执行 slash 命令（`/model`、`/think`、`/compact`、`/trust`、`/rewind`、`/fork`、`/status`、`/goal`、`/btw`、`/remote-control` 等），返回命令结果与产生的通知。
- `pigo/rewind`、`pigo/fork`、`pigo/tree`、`pigo/export`、`pigo/import`：会话树与数据操作扩展，语义与现有 slash 命令一致。
- `pigo/dream`：内存整合运行扩展，复用现有 dream 管线。
- `pigo/remotecontrol`：remote control 生命周期扩展；remote 的输入注入与审批决策通过会话级扩展方法回到 ACP 会话。

扩展能力在 initialize 的 `_meta` 中声明，客户端可按需订阅；stdio 外部客户端不订阅 `pigo/event` 时只收到标准 `session/update`。

### 4. 事件映射契约

agentcore 事件到 ACP session/update 的映射：

- assistant 文本增量 → `agent_message_chunk`，content block 为 text 类型。
- 思考/推理增量 → `agent_thought_chunk`。
- 工具开始 → `tool_call`（InProgress，携带工具名、kind、rawInput）。
- 工具结束 → `tool_call_update`（Completed/Failed，携带 rawOutput）。
- todo 更新 → plan 更新。
- 模型用量 → usage update（used/size）。

session/update 通知的 JSON 形状以 ACP 客户端实际解析为准：update 对象使用 `sessionUpdate` 判别字段，content block 使用 `type` 判别字段。工具 kind 按 pigo 工具名推断（read/edit/execute/search/fetch/other）。标准通道覆盖不了的事件（compaction、subagent progress、telemetry、rewind、goal、btw）不丢给标准通道，只走 `pigo/event`。

### 5. in-process transport

参照 peri 的 `mpsc_transport_pair()`：一对 Go channel 构造 client/server 两个 transport，共享 request router（请求 id 到响应的关联）。TUI 进程内启动 ACP server goroutine，TUI 侧通过 client transport 发请求、收通知；server 侧通过 server transport 收请求、发响应/通知。server 与 TUI 在同一进程，不引入 IPC 序列化开销。

### 6. 会话数据模型

会话持久化升级为 project-scoped store，布局对齐 ash 的既有事实模型：

- 根路径：`$PIGO_HOME/projects/<workspace-slug>/sessions/`；未设置 `PIGO_HOME` 时使用 `~/.pigo`。
- workspace slug：由 canonical 工作区路径生成，小写字母数字保留、其余字符转 `-`、首尾去 `-`、超长时保留前缀并追加 sha256 前 12 位；空路径回退为 `workspace`。
- 每个 project 下有一个会话索引文件（schemaVersion + 可见会话快照 + 更新时间）和每个会话一组文件：metadata 文件（schemaVersion + metadata 字段）、transcript JSONL（复用 pigo 已验证的树形消息格式）、state 文件（后续扩展）。
- metadata 字段至少包含：session id、name、agent type、model name、created/last active、turn/message/tool 计数、状态、tags、workspace path、workspace hostname、custom metadata、parent session id。
- ACP 会话的 custom metadata 记录 client id、remote session id、resume strategy、last resume error，与 ash 的 ACP 记录约定一致。

旧版扁平会话目录保留为 legacy 只读来源，现有 CLI/REPL/headless 行为不变；预留迁移入口，不在第一版执行自动迁移。

### 7. 会话身份与恢复

ACP session id 直接使用 pigo 的会话 id。客户端把 pigo 返回的 session id 记为 remote session id；重启后优先调用 `session/load` 恢复。session/load 语义等同于 CLI 的 resume：读取历史、系统提示、模型配置，使后续 prompt 在完整上下文中继续。

未来重构方向：桌面 Node 后端直接读取 pigo 的 project-scoped store，消除双写；ACP 协议层不暴露任何前端的 UI DTO。

### 8. 权限映射

`session/request_permission` 的选项固定为四个：

- `allow_once`：放行本次工具调用。
- `reject_once`：阻止本次工具调用。
- `allow_always`：映射为会话级信任（当前 pigo 进程内有效，不落盘），后续副作用工具不再逐个确认。
- `reject_always`：映射为持久化 untrusted 决策（等同 CLI 的 `/trust off`），后续仍需确认。

未信任目录之外的普通工具调用不触发权限请求。客户端取消或超时按拒绝处理。remote control 的远程审批通过同一 permission broker 接入，远程决策与本地决策等价。

### 9. TUI 客户端化

TUI 启动流程改为：构造 in-process transport 对 → 启动 ACP server → 初始化 ACP client → 声明 `pigo.*` 扩展能力 → 新建或恢复会话。TUI 的渲染层（markdown、工具卡、spinner、subagent 面板、状态栏）保留，数据源从"直接回调"换成"ACP 客户端事件流 + `pigo/event` 扩展通道"。slash 命令由 TUI 通过 `pigo/command` 发出，不再直接持有 provider/tools/session。

试点验收标准：TUI 现有功能逐项走 ACP 通道后行为不变。收口阶段后，交互入口（TUI 与行 REPL）无条件走 ACP，`--no-acp` 逃生门已移除；遗留直连装配仅保留为回归测试使用的底层原语，待回归测试迁移完成后删除，不作为长期架构。

### 10. 桌面端推迟

Electron 壳 + 复刻 ash 前端（`@pigo/ui-core`、`@pigo/coder-ui`）推迟到 TUI 试点验证完成后实施。仓库结构决策（pnpm workspace、`apps/desktop`、Tauri API shim、Node ACP client）保留为后续阶段的设计，不在第一版实现。

## Testing Decisions

测试只验证外部可观察行为，不绑定实现细节。主 seam 是 ACP wire 协议：用 Go 集成测试直接扮演 ACP 客户端，分别通过 in-process channel transport 与 stdio transport 驱动 ACP server，覆盖完整生命周期。仓库内已有先例：`internal/jsonrpc` 的传输测试、headless stream-json 事件测试，以及 peri 的 transport/dispatch/event mapper 测试。

### 单元测试

- transport：JSON-RPC envelope 编解码、请求/通知/响应区分、请求 id 关联、in-process 与 stdio 行为一致性、stdin 关闭行为。
- dispatch：initialize 能力声明、session/prompt 参数校验、未知方法错误码、`pigo/*` 扩展方法路由。
- event mapper：agentcore 事件到 session/update 的形状与字段，以及 `pigo/event` 扩展事件的完整保留。
- sessionstore：workspace slug 稳定性与超长路径、metadata 读写与 schemaVersion 校验、transcript 读写、索引原子更新、损坏文件容错。
- permission broker：四个选项到 trust 决策的映射、取消/超时按拒绝处理。
- session manager：同 session prompt 串行化、cancel 后新 prompt 可用、close 释放状态。

### 集成测试

单一 seam 的完整场景：

1. initialize 返回 v1 协议、load/close 能力与 `pigo.*` 扩展声明。
2. session/new 创建会话并落盘，返回 session id。
3. session/prompt 注入 fake provider，断言流式 `session/update` 顺序：文本增量、工具卡开始、工具卡完成、usage。
4. 未信任目录的副作用工具触发 `session/request_permission`；分别验证四个选项的决策结果与 trust 状态变化。
5. 运行中收到 `session/cancel`，prompt 以 cancelled stop reason 结束。
6. session/close 后再次 prompt 返回会话不存在。
7. 会话落盘后 session/load 恢复历史，后续 prompt 基于恢复上下文执行。
8. model/set 后下一轮 prompt 使用新模型（fake provider 断言 model id）。
9. `pigo/command` 执行 `/model`、`/think`、`/compact`、`/trust`、`/status` 等命令并返回结果。
10. `pigo/event` 通道原样送达 compaction、subagent progress、telemetry 等 agentcore 事件。
11. TUI 客户端（fake TUI host）通过 in-process transport 完成一次完整交互，断言与 stdio 客户端收到相同的标准事件序列。

### 回归测试

TUI 迁移期间保留直连路径的兼容测试；每条 TUI 功能迁移到 ACP 后，删除对应直连实现并更新回归测试，保证"通过 ACP 的行为"与"原直连行为"一致。

## Out of Scope

- 桌面端（Electron 壳、复刻 ash 前端、Tauri API shim、Node ACP client）——推迟到 TUI 试点之后。
- 纯 Web UI 及其数据适配器。
- `command/list`、`command/execute` 的完整实现。
- `session/resume` 方法（客户端优先走 load，第一版以 load 覆盖）。
- elicitation/ask-user。
- JetBrains 等其它第三方 ACP 客户端的适配验收（Zed 已作为首个 stdio 客户端接入）。
- 打包安装器、自动更新、代码签名。
- Playwright 端到端测试、性能压测。
- 旧版扁平会话的自动迁移（仅预留入口）。
- 完整品牌替换与 ash 前端复制（随桌面端阶段实施）。

## Further Notes

- `pigo --acp` 已实现为 Zed 的首个外部 stdio 入口：Zed 的 `agent_servers` 注册 custom agent（`type=custom`、`args=["--acp"]`），与 TUI 复用同一套 dispatcher/权限/会话 wiring。
- 文档先落 `tasks/`，尚未发布到 GitHub issues；本机没有 `gh` CLI，发布时需要安装 `gh` 或提供带 issue 写权限的 token，并打 `ready-for-agent` 标签。
- TUI 试点是架构验证的第一阶段：先建立 ACP server + in-process transport + `pigo/*` 扩展，再把 TUI 功能逐项迁移；每项迁移独立可合并。
- 桌面端阶段复用同一 ACP 后端与复刻的 ash 前端包；未来重构方向包括桌面端直接读取 pigo 的 project-scoped store，消除双写。
