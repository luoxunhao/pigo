# ACP Zed 端到端验收记录

目标：验证 pigo `--acp` 下工具级策略和命令级 hooks 在 Zed 中真实生效。

## Zed 配置

```json
"agent_servers": {
  "pigo": {
    "type": "custom",
    "command": "E:/project/pigo/pigo.exe",
    "args": ["--acp", "--allowed-tools", "read,grep", "--disallowed-tools", "bash"],
    "env": {}
  }
}
```

命令级拦截依赖 `PreToolUse` hooks（示例：`scripts/hooks/block-dangerous-commands.sh`），
全局配置放 `$PIGO_HOME/config.json`，项目配置放受信任目录的 `.pigo/config.json`。

## 验收清单

- [ ] 工具策略下，Zed 中 bash 不在可用工具集。
- [ ] 请求执行 `rm -rf` 被 hook 拦截，reason 可见。
- [ ] 请求执行 `ls` 可正常执行。
- [ ] 记录 Zed 版本：___
- [ ] 记录实际 settings.json 片段：___
- [ ] 记录验证结果/截图：___

状态：待真实 Zed 环境验证后填写。
