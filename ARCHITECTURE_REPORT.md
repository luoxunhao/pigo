# Pigo 项目架构分析报告

> 分析范围: `internal/ai/config`, `internal/ai/provider`, `internal/harness/core/tools`, `internal/harness/core/session`, `internal/harness/core/session/store`

---

## 1. 模型配置系统 (`internal/ai/config`)

### 1.1 配置加载流程

```
配置文件加载优先级: CLI Flag > config.toml > 内置默认值
```

**文件路径**: `~/.config/pigo/config.toml`（尊重 `$XDG_CONFIG_HOME`）

**加载入口**:
- `FileConfigPath()` — 解析配置文件路径
- `LoadFileConfig(path)` — 读取并解码 TOML，缺失文件返回零值（非错误）
- `SaveFileConfig(path, cfg)` — 原子写入（owner-only 0o600 权限，保护 API Key）

**配置结构** (`FileConfig`):
| 字段 | 类型 | 说明 |
|------|------|------|
| `Model` | string | 默认模型 ID |
| `Models` | []ModelConfig | 模型列表（核心） |
| `AllowedTools/DisallowedTools` | []string | 工具白/黑名单 |
| `Memory/Checkpoint/Compaction` | 嵌套结构 | 持久化记忆/压缩配置 |
| `Dream` | DreamConfig | 梦境记忆聚合 |

### 1.2 ConfiguredModels 存储机制

**核心结构** (`models_store.go`):
```go
type ConfiguredModels struct {
    mu   sync.RWMutex  // 读写锁，支持并发
    path string       // 配置文件路径
    cfg  FileConfig    // 内存中的配置
}
```

**关键操作**:
- `Load()` — 从磁盘重新加载配置
- `List()` — 返回所有配置的模型（副本）
- `CurrentModel()` — 返回默认模型 ID
- `SetModel(model)` — 设置默认模型并持久化
- `Replace(models)` — 替换整个模型列表（按 provider/model_id 去重，保留旧 API Key）
- `Upsert(model)` — 插入或更新单个模型（保留旧 API Key）
- `Delete(key)` — 删除模型

**设计特点**:
- 线程安全（RWMutex）
- API Key 在 Upsert/Replace 时自动继承（若新配置未提供）
- JSON 序列化时自动屏蔽 API Key（`MarshalJSON`）

### 1.3 ModelConfig 结构

```go
type ModelConfig struct {
    Provider       string    // 供应商名称
    ModelID        string    // 模型 ID
    Name           string    // 显示名称
    BaseURL        string    // API 端点
    APIKey         string    // API Key（秘密）
    Protocol       string    // 协议: openai/anthropic/openai/resp_api
    ContextWindow  int       // 上下文窗口大小
    MaxTokens      int       // 最大输出 Token
    ThinkingLevels []string  // 推理级别
    SupportsImages bool      // 是否支持图像
    Enabled        *bool     // 启用状态（nil = 启用）
}
```

**标识符**: `provider/model_id`（如 `openai/gpt-4o`）

**验证**: `ValidateModelConfig()` 要求 provider、model_id、base_url、protocol 均非空

### 1.4 模型连接测试

**入口**: `config_probe.go` → `TestConfiguredModel(ctx, entry)`

**流程**:
```
1. NormalizeProtocol(entry.Protocol) — 规范化协议
2. ResolveConfiguredProvider(...) — 解析 Provider
3. 发送最小请求: "ping" → "pong"
4. 流式读取响应:
   - StreamTextEvent → 收集文本
   - StreamDoneEvent → 成功，返回响应文本
   - StreamErrorEvent → 失败，返回错误详情
5. 返回 ConfigTestResult { Success, ResponseText, Details, ResponseTimeMs }
```

**特点**:
- 测试请求: `SystemPrompt: "Reply with exactly: pong"`, `UserMessage: "ping"`
- API Key 不泄露到结果中
- 超时控制: 无显式超时，依赖 ctx
- 错误编码: 所有运行时错误通过流事件返回（FR-13 双重失败模型）

---

## 2. 模型 Provider 体系 (`internal/ai/provider`)

### 2.1 Provider 接口

```go
type Provider interface {
    Name() string
    Models() []Model
    StreamCompletion(ctx, req) (*AssistantMessageEventStream, error)
}
```

### 2.2 支持的供应商

| 供应商 | 协议 | 认证方式 | 默认端点 |
|--------|------|----------|----------|
| **openai** | openai/chat | Bearer | 无默认（需指定） |
| **openrouter** | openai/chat | Bearer | https://openrouter.ai/api/v1 |
| **ollama** | openai/chat | 无 | http://localhost:11434/v1 |
| **nvidia** | openai/chat | Bearer | https://integrate.api.nvidia.com/v1 |
| **anthropic** | anthropic | x-api-key | https://api.anthropic.com/v1 |
| **bedrock** | anthropic | Bearer | https://bedrock-runtime.us-east-1.amazonaws.com |
| **deepseek** | openai/chat | Bearer | - |
| **groq** | openai/chat | Bearer | - |
| **xai** | openai/chat | Bearer | - |
| **cerebras** | openai/chat | Bearer | - |
| **mistral** | openai/chat | Bearer | - |
| **moonshotai** | openai/chat | Bearer | - |
| **zai** | openai/chat | Bearer | - |
| **fireworks** | openai/chat | Bearer | - |
| **together** | openai/chat | Bearer | - |
| **minimax** | anthropic | x-api-key | - |
| **xiaomi** | anthropic | x-api-key | - |
| **qianfan** | openai/chat | Bearer | - |
| **volcengine** | openai/chat | Bearer | - |
| **dashscope** | openai/chat | Bearer | - |
| **hunyuan** | openai/chat | Bearer | - |
| **google** | gemini | x-goog-api-key | - |

### 2.3 协议支持

**三种协议变体** (`protocol.go`):
```
openai           → Chat Completions (POST {base_url}/chat/completions)
openai/chat      → Chat Completions (openai 的别名)
openai/resp_api  → Responses API    (POST {base_url}/responses)
anthropic        → Anthropic Messages
""               → 未设置（下游根据模型 ID 推断）
```

**协议规范化**: `NormalizeProtocol(raw) → (canonical, error)`

### 2.4 流式协议

**核心抽象** (`protocol.go`):
```go
type StreamFn func(ctx, model, llm LlmContext, cfg StreamConfig) (*AssistantMessageEventStream, error)

type LlmContext struct {
    SystemPrompt string
    Messages     agentcore.MessageList
    Tools        []agentcore.AgentTool
}

type StreamConfig struct {
    APIKey        string
    ThinkingLevel agentcore.ThinkingLevel
    Extra         map[string]any  // 供应商特定选项
}
```

**事件类型** (`AssistantMessageEvent`):
| 事件 | 说明 |
|------|------|
| `StreamStartEvent` | 流开始，携带初始 partial message |
| `StreamTextEvent` | 文本 delta |
| `StreamThinkingEvent` | 推理/思考 delta |
| `StreamToolCallEvent` | 工具调用 delta |
| `StreamDoneEvent` | 流结束（成功），携带最终 message |
| `StreamErrorEvent` | 流结束（错误），携带错误 message |

**双重失败模型** (FR-13):
- 仅"无法建立流"的早期失败返回 Go error
- 所有运行时失败通过 `StreamErrorEvent` 编码在流中

### 2.5 认证体系 (`auth.go`)

**三层认证解析** (`CredentialStore.GetAPIKey`):
```
OAuth Token (自动刷新) → CLI 覆盖 → 环境变量 → 配置文件
```

**OAuth TokenSource**:
- 支持短生命周期 access token
- 自动在过期前 30s 刷新（leeway）
- 线程安全（Mutex）

**环境变量派生**: `GenericBaseURLEnvVar(provider)` → `{PROVIDER}_BASE_URL`

### 2.6 模型发现 (`discover.go`)

**入口**: `DiscoverModels(baseURL, apiKey, protocol)`

**流程**:
```
1. 规范化 discover endpoint
2. GET {endpoint}/models（或 /v1/models）
3. 解析响应（支持 data[] 和 models[] 两种格式）
4. 返回 DiscoveredModel 列表
```

**返回字段**:
- `ModelID`, `Name`
- `ContextWindow`, `MaxTokens`（可选）
- `ThinkingLevels`（若模型支持推理）
- `SupportsImages`（若支持多模态）

### 2.7 模型推断 (`infer.go`)

**前缀匹配规则**:
```
claude-*      → anthropic
gpt-*, o1-*, o3-*, o4-* → openai
gemini-*      → google
deepseek-*    → deepseek
glm-*         → zai
kimi-*, moonshot-* → moonshotai
qwen-*        → dashscope
ernie-*       → qianfan
doubao-*      → volcengine
grok-*        → xai
mistral-*, codestral-*, devstral-* → mistral
hunyuan-*     → hunyuan
minimax-*     → minimax
mimo-*        → xiaomi
```

**保守策略**: 歧义前缀（llama-*, qwq-*, gemma-*, mixtral-*）不推断，回退到 OpenRouter

---

## 3. 工具系统 (`internal/harness/core/tools`)

### 3.1 工具注册与执行

**注册表** (`registry.go`):
```go
type ToolRegistry struct {
    mu       sync.RWMutex
    tools    map[string]AgentTool
    compiled map[string]*jsonschema.Schema
}
```

**特性**:
- 按名称注册，唯一性检查
- 启动时编译 JSON Schema，失败时立即报错
- 执行前验证参数，返回字段级错误

**执行流程** (`tool_executor.go`):
```
1. Prepare: 查找工具 → 准备参数 → Schema 验证 → beforeToolCall 钩子
2. Execute: 运行工具（支持重试）
3. Finalize: afterToolCall 钩子 → 裁剪结果 → 构建 ToolResultMessage
```

**重试策略**:
- 仅重试瞬态错误（`isRetryableToolError`）
- 上下文取消立即停止
- 退避间隔

### 3.2 工具清单

#### 文件系统工具 (`fs/`)

| 工具 | 执行模式 | 功能 | 边界 |
|------|----------|------|------|
| **read** | 并行 | 读取文件，支持 offset/limit | 2000 行/次，2000 字符/行 |
| **write** | 串行 | 创建/覆盖文件 | 路径逃逸检测 |
| **edit** | 串行 | 精确字符串替换，返回 diff | old_string 必须唯一（除非 replace_all） |
| **grep** | 并行 | 正则搜索文件内容 | 1000 结果上限，尊重 .gitignore |
| **find** | 并行 | 按 glob 查找文件 | 1000 结果上限，尊重 .gitignore |
| **ls** | 并行 | 列出目录 | 区分文件/目录 |

**路径安全**: 所有文件工具共享 `resolveWithin(root, path)` 进行路径逃逸检测

**ExtraRoots**: 信任目录（如 skills 目录），允许读写 workspace 外的文件

#### Bash 工具 (`bash/`)

| 工具 | 执行模式 | 功能 | 边界 |
|------|----------|------|------|
| **bash** | 串行 | 执行 shell 命令 | 默认 2min，最大 10min；输出 30KB 上限 |
| **bash_output** | - | 读取后台任务输出 | - |
| **bash_kill** | - | 终止后台任务 | - |

**背景任务**:
- `run_in_background=true` 时返回 `bash_id`
- 任务脱离 turn context，独立运行
- 通过 `bash_output` 读取，`bash_kill` 终止

**Shell 解析** (Windows):
```
优先: Git Bash (C:\Program Files\Git\bin\bash.exe)
备选: WSL bash (探测是否有效)
备选: PowerShell
最后: cmd
```

#### 状态工具 (`state/`)

| 工具 | 执行模式 | 功能 |
|------|----------|------|
| **todo** | 串行 | 任务列表管理（pending/in_progress/completed） |
| **goal_complete** | 串行 | 声明目标完成，终止运行 |
| **goal_blocked** | 串行 | 声明目标受阻，终止运行 |
| **memory_search** | 并行 | BM25 搜索持久化记忆 |

**TodoTool**:
- 每次调用提交完整列表（替换而非增量）
- 严格校验: 每个 item 必须非空内容 + 有效状态
- 渲染格式: `[ ] pending`, `[~] in_progress`, `[x] completed`

**GoalState**:
- 状态机: `idle → active → complete/blocked/paused/budget_limited`
- 跟踪: iterations, no_progress, token_budget, tokens_used
- `goal_complete` 校验摘要不含否定词（防止误判）

#### Web 工具 (`web/`)

| 工具 | 功能 |
|------|------|
| **web_fetch** | 获取 URL 内容 |
| **web_search** | 网络搜索 |

### 3.3 工具执行模式

```go
type ToolExecutionMode int
const (
    ToolExecutionParallel   // 无副作用，可并行
    ToolExecutionSequential // 有副作用，必须串行
)
```

**模式分配**:
- 并行: read, grep, find, ls, memory_search
- 串行: bash, write, edit, todo, goal_complete, goal_blocked

### 3.4 结果预算

**双重预算机制**:
1. **工具内层预算**: 各工具自定义（read: 2000 行, bash: 30KB, search: 1000 结果）
2. **执行器外层预算**: `toolResultMaxBytes = 100KB`（统一裁剪）

**裁剪策略**: head + `[truncated N bytes]` + tail，保持在 UTF-8 边界

---

## 4. 会话系统 (`internal/harness/core/session`)

### 4.1 会话数据结构

**SessionHeader** (`session.go`):
```go
type SessionHeader struct {
    Version         int       // .schema 版本 (当前: 3)
    ID              string    // 会话 ID（也是文件名）
    CreatedAt       time.Time
    UpdatedAt       time.Time
    Model           string    // 模型 ID
    Provider        string    // 供应商
    SystemPrompt    string    // 系统提示词
    ParentSession   string    // 父会话 ID（fork/clone 溯源）
    Cwd             string    // 工作目录
    ContextFrom     string    // 继承的压缩上下文来源
    ContextWatermark int      // 压缩断点
}
```

**Entry** (v3 树结构):
```go
type Entry struct {
    ID       string        // 条目 ID（4 字节随机）
    ParentID string        // 父条目 ID（形成树）
    Timestamp time.Time
    Message  agentcore.Message
}
```

### 4.2 Schema 版本演进

| 版本 | 特性 |
|------|------|
| v1 | 原始 JSONL，每行一个消息 |
| v2 | 添加 compaction 消息角色 |
| v3 | 添加 id/parentId 树结构，支持 fork/clone |

**向后兼容**: v1/v2 文件加载时自动迁移（合成 id 和 parentId 链）

### 4.3 会话生命周期

```
┌─────────────────────────────────────────────────────────────────┐
│                     会话生命周期                                 │
└─────────────────────────────────────────────────────────────────┘

1. 创建 (Create)
   ├─ SessionManager.New(cwd, model, ctx, store)
   ├─ 生成 ID: {YYYYMMDD}-{HHMMSS}-{微秒}
   ├─ 写入 metadata.json
   └─ 写入空 transcript.jsonl

2. 加载 (Load)
   ├─ SessionManager.Load(cwd, sessionID, model, ctx, store)
   ├─ 读取 metadata.json + transcript.jsonl
   ├─ 重建 system prompt（基于 cwd）
   ├─ 设置 CurLeaf = 最后一条 entry 的 ID
   └─ 设置 Persisted = 消息总数

3. 运行 (Run)
   ├─ SessionManager.Run(ctx, sess, prompt, images, ...)
   ├─ 执行 agent loop
   ├─ 收集新消息 (tail)
   └─ 持久化: AppendBranch(header, curLeaf, tail)
       └─ 新增 entry 以 curLeaf 为 ParentID
       └─ 更新 CurLeaf = 新 leaf ID

4. 持久化 (Persist)
   ├─ Append: 追加消息，更新 UpdatedAt
   ├─ AppendBranch: 追加分支消息，返回新 leaf ID
   ├─ Fork: 复制路径到新手册（保留 id/parentId）
   └─ PersistRewind: 重写整个 transcript（rewind/compaction 后）

5. 加载恢复 (Resume)
   ├─ LoadEntries: 读取 header + entries
   ├─ PathToLeaf: 从 leaf 回溯到 root
   └─ 重放: 将线性 conversation 作为初始 history

6. 删除 (Delete)
   ├─ DeleteEverywhere: 删除所有 project store 中的会话
   └─ 清理 metadata.json + transcript.jsonl
```

### 4.4 树结构支持

**PathToLeaf** (`session.go`):
```go
func PathToLeaf(entries []Entry, leafID string) []Entry
```
- 从 leaf 沿 ParentID 链回溯到 root
- 返回 root→leaf 顺序的线性 conversation
- 处理断链和环（安全停止）

**RenderTreeLines**:
- ASCII 树形展示: `├─`, `└─` 连接器
- 当前分支标记: `← current`
- 稳定排序: timestamp → id

### 4.5 分支操作

**Fork** (`store.go`):
```
/fork <user_message_id>
  → 复制 root → parent(user_message) 的路径
  → 新会话 ID（时间戳）
  → ParentSession = 源会话 ID
  → 用户在新分支上重新提示
```

**Clone** (`store.go`):
```
/clone <leaf_id>
  → 复制 root → leaf 的完整路径
  → 新会话 ID
  → 完全独立的副本
```

---

## 5. 会话持久化 (`internal/harness/core/session/store`)

### 5.1 存储布局

```
$PIGO_HOME/projects/<workspace-slug>/sessions/
├── index.json                    # 项目级会话索引
├── {session-id}.metadata.json    # 会话元数据
└── {session-id}.jsonl            # 会话转录本（委托给 session.Store）
```

**Workspace Slug**:
- 规范化路径 → ASCII 小写 → 非字母数字替换为 `-`
- 超长时附加 sha256 前缀（总长 ≤200）

### 5.2 Metadata 结构

```go
type Metadata struct {
    SchemaVersion   int
    SessionID       string
    SessionName     string
    AgentType       string     // "pigo"
    ModelName       string
    CreatedAt       time.Time
    LastActiveAt    time.Time
    LastFinishedAt  *time.Time
    TurnCount       int
    MessageCount    int
    ToolCallCount   int
    Status          Status     // active | completed | archived
    Tags            []string
    WorkspacePath   string
    WorkspaceHost   string     // "localhost"
    ParentSessionID string
}
```

### 5.3 Store 操作

| 操作 | 说明 |
|------|------|
| `Create(meta, header, messages)` | 创建新会话 |
| `ImportEntries(meta, header, entries)` | 导入现有转录本（保留 id/parentId） |
| `Load(sessionID)` | 加载元数据 + 转录本 |
| `Append(sessionID, updatedAt, messages)` | 追加消息，更新计数 |
| `AppendBranch(sessionID, header, parentLeafID, messages)` | 追加分支，返回新 leaf ID |
| `SaveMetadata(meta)` | 更新元数据 |
| `UpdateHeader(sessionID, header)` | 重写转录本 header |
| `List()` | 列出所有会话（按 LastActiveAt 降序） |
| `Delete(sessionID)` | 删除会话 |
| `Touch(sessionID)` | 刷新 LastActiveAt |

### 5.4 索引机制

**IndexFile**:
```go
type IndexFile struct {
    SchemaVersion int
    UpdatedAt     time.Time
    Sessions      []Metadata
}
```

**一致性检查**:
- `listLocked()` 先尝试读索引
- 若索引缺失或版本号不匹配，触发 `rebuildIndexLocked()`
- `indexConsistent()` 验证索引中的每个 session 文件存在

### 5.5 原子写入

所有写操作使用 `writeJSONAtomic()`:
```
1. 写入临时文件 {path}.{pid}.tmp
2. Flush + Close
3. os.Rename(tmp, path)  // 原子替换
```

---

## 6. 数据流向

### 6.1 整体架构图

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              用户请求                                        │
│                         (prompt + images)                                   │
└─────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                        SessionManager.Run()                                  │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ 1. 构建 AgentContext                                                 │   │
│  │    - 加载历史消息 (sess.Messages)                                    │   │
│  │    - 重建 SystemPrompt (基于 cwd)                                    │   │
│  │    - 绑定工具集 (sess.Tools)                                         │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                      │                                      │
│                                      ▼                                      │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ 2. Agent Loop (runtime.Run)                                          │   │
│  │    ┌─────────────────────────────────────────────────────────────┐  │   │
│  │    │ 调用 StreamFn (Provider)                                    │  │   │
│  │    │   ├─ 认证解析 (CredentialStore.GetAPIKey)                    │  │   │
│  │    │   ├─ 编码请求 (OpenAI/Anthropic wire format)                 │  │   │
│  │    │   └─ 流式解码 (Decoder) → AssistantMessageEventStream        │  │   │
│  │    └─────────────────────────────────────────────────────────────┘  │   │
│  │                                      │                              │   │
│  │                                      ▼                              │   │
│  │  ┌─────────────────────────────────────────────────────────────┐  │   │
│  │  │ 工具调用循环                                                 │  │   │
│  │  │   ├─ 解析 tool_calls                                        │  │   │
│  │  │   ├─ ToolRegistry.Execute()                                 │  │   │
│  │  │   │   ├─ prepare: 验证 Schema                               │  │   │
│  │  │   │   ├─ execute: 运行工具 (bash/read/write/...)             │  │   │
│  │  │   │   └─ finalize: 裁剪结果，构建 ToolResultMessage           │  │   │
│  │  │   └─ 追加到 messages                                        │  │   │
│  │  └─────────────────────────────────────────────────────────────┘  │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                      │                                      │
│                                      ▼                                      │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ 3. 持久化                                                            │   │
│  │    - 计算 tail = messages[Persisted:]                                │   │
│  │    - Store.AppendBranch(id, header, curLeaf, tail)                   │   │
│  │    - 更新 sess.CurLeaf, sess.Persisted                               │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 6.2 配置 → Provider → Session 数据流

```
┌──────────────────────────────────────────────────────────────────────┐
│                     config.ConfiguredModels                           │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │ List() → []ModelConfig                                        │   │
│  │ Find(key) → (ModelConfig, bool)                               │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                              │                                       │
│                              ▼                                       │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │ provider.ResolveConfiguredProvider()                          │   │
│  │  ├─ 根据 provider 名称解析认证 (env/config/OAuth)              │   │
│  │  ├─ 规范化 protocol                                           │   │
│  │  └─ 返回 Provider 实例                                        │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                              │                                       │
│                              ▼                                       │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │ provider.StreamFn()                                           │   │
│  │  ├─ 编码 LlmContext (system prompt + messages + tools)        │   │
│  │  ├─ 发送 HTTP 请求 (SSE stream)                               │   │
│  │  └─ 解码为 AssistantMessageEventStream                        │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                              │                                       │
│                              ▼                                       │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │ session.AcpSession                                            │   │
│  │  ├─ Messages: agentcore.MessageList                           │   │
│  │  ├─ CurLeaf: string (当前分支叶节点)                           │   │
│  │  └─ Persisted: int (已持久化消息数)                           │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                              │                                       │
│                              ▼                                       │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │ session.store.Store                                           │   │
│  │  ├─ AppendBranch() → 新 leaf ID                               │   │
│  │  ├─ Fork() → 新会话 (复制路径)                                 │   │
│  │  └─ Load() → (Metadata, Header, Messages)                     │   │
│  └──────────────────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────────────────┘
```

### 6.3 工具执行数据流

```
┌─────────────────────────────────────────────────────────────────────┐
│                    Agent Loop (工具调用)                             │
│                                                                     │
│  AssistantMessage (含 tool_calls)                                   │
│        │                                                            │
│        ▼                                                            │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │ ToolRegistry.Execute()                                       │   │
│  │  ├─ 查找工具: registry.Get(name)                             │   │
│  │  ├─ 验证参数: registry.Validate(name, args)                  │   │
│  │  └─ 执行: tool.Execute(ctx, id, args, onUpdate)              │   │
│  └─────────────────────────────────────────────────────────────┘   │
│        │                                                            │
│        ├─────────────────────────────────────────────────────────┐  │
│        ▼                                                         │  │
│  ┌─────────────────────────────────────────────────────────────┐│  │
│  │ BashTool: exec.Command → stdout/stderr → AgentToolResult    ││  │
│  │  - 流式 onUpdate → UI 实时显示                               ││  │
│  │  - 输出截断到 30KB                                          ││  │
│  └─────────────────────────────────────────────────────────────┘│  │
│        │                                                         │  │
│        ├─────────────────────────────────────────────────────────┐│  │
│        ▼                                                         ││  │
│  ┌─────────────────────────────────────────────────────────────┐│  │
│  │ ReadTool: os.Open → 逐行读取 → 编号输出                      ││  │
│  │  - 最多 2000 行，每行 2000 字符                              ││  │
│  └─────────────────────────────────────────────────────────────┘│  │
│        │                                                         │  │
│        ├─────────────────────────────────────────────────────────┐│  │
│        ▼                                                         ││  │
│  ┌─────────────────────────────────────────────────────────────┐│  │
│  │ WriteTool/EditTool: os.WriteFile + FileSnapshotRecorder      ││  │
│  │  - 记录快照支持 /rewind 回滚                                 ││  │
│  └─────────────────────────────────────────────────────────────┘│  │
│        │                                                         │  │
│        └─────────────────────────────────────────────────────────┘│
│                                                                     │
│  ToolResultMessage → 追加到 messages → 下一轮 LLM 调用               │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 7. 关键设计决策

### 7.1 配置优先级

```
CLI Flag > config.toml > 内置默认值
```

### 7.2 认证解析顺序

```
OAuth Token (自动刷新) > CLI Override > Env Var > Config File
```

### 7.3 工具执行容错

- 所有失败编码为 `IsError=true` 的 ToolResultMessage
- 仅"无法建立流"返回 Go error
- JSON Schema 验证失败返回字段级错误，供模型修正

### 7.4 会话树结构

- v3 schema 支持 fork/clone
- 每个 entry 有唯一 ID 和 ParentID
- `PathToLeaf` 重建线性 conversation
- `AppendBranch` 支持分支增长

### 7.5 持久化策略

- 增量追加（仅写入新消息）
- 原子写入（tmp + rename）
- 索引缓存（避免每次扫描磁盘）

---

## 8. 总结

Pigo 项目采用**分层架构**，各层职责清晰:

| 层 | 包 | 职责 |
|----|-----|------|
| **配置层** | `internal/ai/config` | 加载/存储模型配置，管理 API Key |
| **Provider 层** | `internal/ai/provider` | 抽象多供应商，处理认证、协议、流式解码 |
| **工具层** | `internal/harness/core/tools` | 注册/执行工具，参数验证，结果预算 |
| **会话层** | `internal/harness/core/session` | 会话模型定义，树结构支持 |
| **持久化层** | `internal/harness/core/session/store` | 项目级会话管理，元数据 + 索引 |

**核心数据流**:
```
Config → Provider → StreamFn → AgentLoop → Tools → SessionStore
```

**安全设计**:
- API Key 永不记录/暴露
- 路径逃逸检测
- 结果大小预算
- 双重失败模型（错误编码在流中）
