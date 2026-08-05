# 05 thinking level 与命令目录映射

**Type:** grilling

## Question

pi-web 的 availableThinkingLevels/setThinkingLevel 映射到 pigo 的哪个方法（pigo/command `/think`、pigo/config 还是新增扩展）；pi-web 的 commands() 命令目录来源（解析 `/help` 还是静态清单）；会话 status 用轮询还是事件驱动。

## Status

resolved

## Resolution

5.1: `setThinkingLevel` 走 `pigo/command /think <level>`；可选枚举 `off|minimal|low|medium|high|xhigh|max`。
5.2: commands() 返回静态清单，对应 pigo/command 已知命令。
5.3: 会话状态事件驱动更新，`status()` 读内存状态，不轮询 pigo。
