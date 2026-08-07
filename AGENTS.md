# AGENTS.md

pigo 是一个用 Go 编写的 coding agent。核心通过 ACP（Agent Client Protocol，JSON-RPC 2.0 over stdio）对外提供服务，TUI/REPL 也统一走 ACP；外部客户端包括 Zed 和 pi-web。

## 快速开始

```powershell
go build -o pigo.exe ./cmd/pigo   # 构建 CLI / ACP 二进制
go test ./internal/acp/...        # ACP 层测试
pigo.exe --acp                    # 以 ACP stdio server 模式启动
pigo.exe --no-tui                 # 行式 REPL
pigo.exe -v                       # 打印版本信息
```

常用配置通过 `~/.config/pigo/config.toml` 覆盖，命令行参数优先级最高（CLI > 配置文件 > 默认值）。

## 目录结构

| 路径 | 职责 |
|---|---|
| `cmd/pigo` | 入口：CLI 参数解析、TUI/REPL/headless 分发、`--acp` stdio server |
| `internal/acp` | ACP 协议层：`dispatch.go` 核心分发、`pigo_rpc.go` 已实现的 `pigo/*` RPC、`extensions.go` 斜杠命令层、`sessioninfo.go` / `events.go` 负载与事件助手、`server_*.go` transport |
| `internal/cli` | TUI、REPL、headless、acpcmd 等前端 |
| `internal/agenttool` | 工具实现：bash、search、websearch、文件工具等 |
| `internal/provider` | 模型 provider 与流式协议适配 |
| `internal/sessionstore` | 会话与转录持久化 |
| `internal/session` | 会话模型 |
| `internal/trust` | 文件操作信任决策 |
| `internal/dream` | 记忆整合 / distill |
| `sdk/node/pigo-acp` | Node ACP 客户端（pi-web 使用） |
| `scripts` | pi-web 启动 / 停止脚本 |

## 关键约定

- ACP 方法按层放置，不按客户端命名：
  - 核心 `session/*` 与 `model/*` 的分发在 `internal/acp/dispatch.go` 的 `HandleRequest`。
- 已实现的 `pigo/*` RPC（`pigo/command`、`pigo/status`、`pigo/models`、`pigo/models/discover`、`pigo/config`、`pigo/config/test`、`pigo/messages`）集中在 `internal/acp/pigo_rpc.go` / `internal/acp/discover.go`。
  - 斜杠命令实现集中在 `internal/acp/extensions.go`。
  - 新增 ACP 客户端只做客户端侧映射，不要新建 `xx_extensions.go`；协议方法只加一次，所有客户端共享。
- 会话删除必须走 `internal/sessionstore` 的 store API，保持磁盘持久化一致。
- 新增能力默认通过 ACP 暴露，TUI/REPL 复用同一通道，避免旁路实现。
- 提交信息使用中文，subject 概括改动，必要时 body 说明动机；未经明确要求不 push。

## 责任边界

- pigo 负责模型供应商配置、模型目录与模型测试：
  - `pigo/config`：读写模型供应商配置（`~/.config/pigo/config.toml`）；API key 永不出 pigo，任何客户端都只拿到 `apiKeyConfigured`。
  - `pigo/models`：返回已配置模型列表。
  - `pigo/models/discover`：向供应商拉取可用的模型列表。
  - `pigo/config/test`：按 `modelId` 测试已配置模型，pigo 自己解析连接信息与 API key，不接收也不返回 key。
- pigo 不负责第三方 ACP agent 的模型目录；第三方 agent 模型列表由客户端（ash-workbench desktop）通过 ACP `session/new` 读取。
- 新增后端逻辑前，先确认该能力属于 pigo（配置/模型目录/模型测试）还是客户端（第三方 agent 模型目录、UI 编排），不要把客户端职责下沉到 pigo。

## 验证

| 场景 | 命令 |
|---|---|
| 编译 | `go build ./...` |
| ACP 层测试 | `go test ./internal/acp/...` |
| 会话存储测试 | `go test ./internal/sessionstore/...` |
| 完整测试 | `go test ./...` |

完整 `go test ./...` 在 Windows 上存在既有环境性失败（缺少 `sh`、路径分隔符假设），改动相关包时优先跑对应包。

## Zed 集成

Zed 通过 `agent_servers.pigo` 使用 pigo：

```json
"pigo": {
  "type": "custom",
  "command": "E:/project/pigo/pigo.exe",
  "args": ["--acp"],
  "env": {}
}
```

修改 Zed 配置后重启 Zed（或重新加载配置），即可在 agent 列表中选择 pigo。
