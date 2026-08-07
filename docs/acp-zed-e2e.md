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

- [x] 工具策略下，Zed 中 bash 不在可用工具集。
- [x] 请求执行 `rm -rf` 被 hook 拦截，reason 可见。
- [x] 请求执行 `ls` 可正常执行。
- [x] 记录实际 settings.json 片段：见上方 Zed 配置。
- [x] 记录验证结果：见下方验证记录。

## 验证记录

- 日期：2026-08-07
- 结果：用户确认三项全部验收通过。
- `bash` 工具调用返回 `unknown tool 'bash'`，确认工具级策略生效。
- `rm -rf test` 被 `block-dangerous-commands.sh` 项目 hook 拦截；改用
  `rm test/helloword.txt && rmdir test` 后成功删除 `test` 目录。
- `ls` 正常执行并列出项目文件。
- Zed 版本：验收时未记录（如需补记，填写在此处）。

状态：已验证通过。
