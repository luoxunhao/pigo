# 06 - ACP 树 surface 契约

**Type:** grilling
**Status:** resolved
**Blocked by:** 03, 04

## Question

`session/load` 消息的 `entryId/parentId` 扩展字段、结构化 `/tree` 响应 JSON（nodes、currentLeaf、labels）、`session/status` 是否暴露 leaf、扩展字段的版本协商方式，以及命令列表如何保持“只走 /command、不新增 session/* 方法”。

## Answer

已确认（2026-08-12，经 grilling 共识）。

### 结论

- 树扩展全部走 ACP 官方 `_meta`，不新增 `session/*` 或 `pigo/*` 方法。
- 命名空间为 `_meta.pigo.sessionTree`；旧 `pigo/*` 方法/事件不恢复，`tasks/spec-acp-http-serve.md` 的 strict 说明需在后续迁移设计里澄清。
- 版本协商：`initialize` 双向声明 v1；pigo 总是声明，但只对声明 v1 的客户端发送树元数据；版本 >1 时 pigo 响应 v1，客户端可降级；非法声明视为未声明；启用状态绑定连接级。
- TUI 与 `sdk/node/pigo-acp` 默认声明 v1；06 只写设计，不改代码/SDK。

### 版本协商

客户端在 `initialize` 声明：

```json
{
  "jsonrpc": "2.0",
  "id": 0,
  "method": "initialize",
  "params": {
    "protocolVersion": 1,
    "clientCapabilities": {
      "_meta": {
        "pigo": {
          "sessionTree": { "version": 1 }
        }
      }
    }
  }
}
```

服务端响应：

```json
{
  "jsonrpc": "2.0",
  "id": 0,
  "result": {
    "protocolVersion": 1,
    "agentCapabilities": {
      "loadSession": true,
      "_meta": {
        "pigo": {
          "sessionTree": { "version": 1 }
        }
      }
    }
  }
}
```

规则：

- 客户端未声明或声明的版本大于 1：pigo 不发送 `_meta.pigo.sessionTree` / `_meta.pigo.structured`，只保留标准字段与文本。
- 客户端声明 `version: 1`：pigo 按 v1 契约发送。
- 客户端遇到未知 `_meta.pigo.*` 版本必须忽略并回退文本；服务端对未知客户端版本视为不支持，不报错。
- 启用状态在 `initialize` 时确定，作用于该连接的所有会话。

### 消息元数据

标准 `messageId` 始终发送，等于 entry id。树专属字段只在声明客户端发送：

```json
{
  "jsonrpc": "2.0",
  "method": "session/update",
  "params": {
    "sessionId": "sess_01",
    "update": {
      "sessionUpdate": "agent_message_chunk",
      "messageId": "00000002",
      "content": { "type": "text", "text": "..." },
      "_meta": {
        "pigo": {
          "sessionTree": {
            "version": 1,
            "entryId": "00000002",
            "parentId": "00000001",
            "entryType": "message",
            "seq": 2,
            "lane": "main"
          }
        }
      }
    }
  }
}
```

- `_meta.pigo.sessionTree` 字段：`version/entryId/parentId/entryType/seq/lane`；root 的 `parentId` 为 `null`。
- `seq` 为 1-based 物理 append 序；live 阶段省略，落库后与 `session/load` 回放提供。
- 每个消息 chunk 都带完整 `_meta`；`tool_call` / `tool_call_update` 也带 `_meta.pigo.sessionTree`，`entryId` 指向所属 assistant entry，不带 `messageId`。
- 消息 chunk 的 `lane` 表示 entry 所属 lane，与 `currentLane` 区分。
- `entryId` 在流式开始前分配，turn 结束时用同一 id 落库；用户 entry 在 prompt 开始时 append；assistant entry 在 turn 结束时 append；失败/取消也 append 最终态。
- live 用户消息只对声明树能力的客户端回显 `user_message_chunk`，只含文本；slash 命令不创建 entry；hybrid prompt 命令用展开后文本创建用户 entry。

### leaf 通知

每次 entry append 推进 leaf 和每次 lane move 后发送 `session_info_update`：

```json
{
  "sessionUpdate": "session_info_update",
  "_meta": {
    "pigo": {
      "sessionTree": {
        "version": 1,
        "entryId": "abc123",
        "entryType": "message",
        "currentLeafId": "abc123",
        "currentLane": "main",
        "lanes": [
          { "lane": "main", "leafId": "abc123" },
          { "lane": "side", "leafId": "def456" }
        ]
      }
    }
  }
}
```

### session/load

- 只回放当前 main lane leaf 的 root→leaf 路径。
- 回放 user/assistant/thought 文本与历史工具最终态；compaction 等非消息 entry 不流式。
- 历史工具只回放最终 `tool_call_update`（`completed` / `failed`），带 `rawInput/rawOutput/entryId`。
- parent 链断裂时回放可解析前缀；`lanes.main.leaf_id` 无效时报错。
- 响应 `_meta` 带 `currentLeafId/currentLane/lanes`；`session/new` 同样带空 main lane。

### /tree

- `/tree` 仍是斜杠命令，无新方法。
- HTTP `POST /api/v1/session/{id}/command` 的 `PromptResponse` 增加可选 `structured = {version, kind, data}`；ACP 映射为同构的 `_meta.pigo.structured`。
- `data`：

```json
{
  "nodes": [
    {
      "id": "00000001",
      "parentId": null,
      "kind": "user",
      "summary": "...",
      "timestamp": "2026-08-12T10:30:00+08:00",
      "label": "任务"
    }
  ],
  "currentLeafId": "00000002",
  "currentLane": "main",
  "activePathIds": ["00000001", "00000002"],
  "labels": { "00000001": "任务" },
  "lanes": [
    { "lane": "main", "leafId": "00000002" }
  ]
}
```

- `nodes` 只含逻辑树 entry（`message/model_change/thinking_level_change/compaction/branch_summary/custom/custom_message`），不含 `label/session_info` 节点；`kind` 枚举保留 pi-web `SessionTreeNodeKind` 全套。
- `nodes` 不带 `seq`；pre-order 输出，children 按 `seq` 升序；`summary` 按 pi-web 投影规则生成。
- 孤儿 entry 作为根节点输出，不加标记。
- `labels` 为 string map，只含有 label 的 entry；`nodes[].label` 可选，只有有 label 时带。
- `currentLeafId` 为 `null` 时 `activePathIds` 为 `[]`；`lanes` 中 `main` 第一，其余按名称字母序。
- `/tree N` 返回更新后的完整快照 + 文本确认，并触发 `session_info_update`；空树返回空快照；非法参数/越界走命令错误，不返回 structured。
- `available_commands_update` 的 `tree` 命令对象带 `_meta.pigo.structuredKinds: ["sessionTree"]`，只发给声明客户端。
- 文本 fallback 继续使用现有 `RenderTreeLines` 编号文本。

### session/status

- `GET /api/v1/session/{id}/status` 返回 `currentLeafId/currentLane/lanes`；`null` 表示空会话。
- ACP 不新增 `session/status` 方法；`/status` 文本显示 `leaf: <id>（lane: main）`。
- 本次不引入 `_meta.pigo.structured.kind = "sessionStatus"`；结构化状态暂不提供。

### 命令面

- `tree` 通过 `available_commands_update` 暴露；已知命令仍由 `session/prompt` 文本前缀 `/` 触发，ACP 内部走 `POST /command`；未知命令仍按普通文本交给模型。
- 不新增 `session/tree`、`session/status` 或 `pigo/tree`；所有能力维持“标准 ACP 方法 + 斜杠命令 + `_meta` 扩展数据”。

### 文件范围

- 本次只更新 scratch ticket 与 map；spec、SDK、代码改动留给后续实现 ticket。
