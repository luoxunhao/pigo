# 01 session/load 历史消息契约

**Type:** grilling

## Question

pigo 的 `session/load` 目前只返回 sessionId/configOptions/models，不返回历史消息；pi-web 恢复会话后必须显示历史。首屏消息走扩展后的 `session/load` 响应，还是统一走 `pigo/messages` 拉取？消息 JSON 形状（角色/内容块/时间戳/父 id）如何对齐 pi-web 的 MessagePage？

**Blocked by:** 02 pigo 扩展契约总表（部分）

## Status

resolved

## Resolution

1.1: `session/load` 响应直接携带消息列表作为首屏，后续分页走 `pigo/messages`。
1.2: 消息 JSON 对齐 pi-web MessagePage：`id / role / content blocks / timestamp / parentId`，block 支持 text/thinking/tool。
