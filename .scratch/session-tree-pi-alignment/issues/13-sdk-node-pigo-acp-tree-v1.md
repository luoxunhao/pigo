# 13 - sdk/node/pigo-acp 默认声明 tree v1

**What to build:** 让 `sdk/node/pigo-acp` 在 `initialize` 默认声明 `_meta.pigo.sessionTree.version=1`，接收并透出结构化 tree/leaf 元数据；未知 `_meta.pigo.*` 版本忽略并回退文本。

**Blocked by:** 07 - ACP/HTTP 树 surface v1

**Status:** resolved

## Acceptance Criteria

- [x] SDK `initialize` 请求带 `clientCapabilities._meta.pigo.sessionTree.version=1`
- [x] SDK 解析 server `agentCapabilities._meta.pigo.sessionTree`
- [x] SDK 消费 `session_info_update` 的 currentLeafId/currentLane/lanes
- [x] SDK 对未知版本忽略并保持文本 fallback
- [x] 相关 SDK 测试通过

**Type:** frontend

**Priority:** medium

**Spec Reference:** `tasks/spec-session-tree-pi-alignment.md` §9.1, §9.3

