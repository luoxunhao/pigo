# Zed 验收清单

对应 `.scratch/acp-http-serve/issues/19-zed-acceptance.md`。

## 前置

- 已使用 `pigo acp` 作为 Zed 的 agent server：

```json
{
  "agent_servers": {
    "pigo": {
      "type": "custom",
      "command": "E:/project/pigo/pigo.exe",
      "args": ["acp"],
      "env": {}
    }
  }
}
```

- 重启 Zed 或重新加载配置。

## 手动验收步骤

1. 对话
   - 新建会话并发送普通文本，确认流式文本实时出现。
2. 工具调用
   - 要求读取文件、执行 bash、写文件，确认工具卡片出现，完成后能看到结果文本或终端输出。
3. 权限确认
   - 在未信任目录执行 `bash` / `write` / `edit`，确认弹出权限确认，选择 allow/always/reject 后行为正确。
4. 取消
   - 长任务运行中点击取消，确认 turn 停止、队列清空、状态回到 idle。
5. 历史恢复
   - 关闭后从会话列表重新打开，确认历史消息、工具调用和分支 `curLeaf` 恢复正确。
6. 方法面
   - 可选：抓取 stdio JSON-RPC，确认没有 `model/set`、`pigo/*`、`pigo/event`。
   - 自动化测试已覆盖：`go test ./internal/acp/...`

## 记录

在此文件末尾追加：验证日期、Zed 版本、通过/失败项、问题复现步骤。
