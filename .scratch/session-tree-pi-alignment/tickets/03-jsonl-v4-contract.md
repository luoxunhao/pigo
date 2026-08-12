# 03 - v4 JSONL typed entry 契约

**Type:** grilling
**Status:** resolved
**Blocked by:** 01, 02

## Question

v4 JSONL 的 header、entry 类型集合、字段名、排序、export/import 无损往返规则，以及 labels/name 等 facts 在 JSONL 里如何表达？

## Answer

已确认（2026-08-12）：

- **Header**：单行 `{"type":"session","version":4,...}`；pigo 扩展字段直接放同一行：`createdAt/updatedAt/model/provider/systemPrompt/additionalDirectories/leafId`，可选字段用 `omitempty`。
- **Entry 类型**：完整对齐 pi 的 9 种 typed entry：`message/model_change/thinking_level_change/compaction/branch_summary/custom/custom_message/label/session_info`；pigo 现有 `user/assistant/toolResult` message 全部落在 `message`，compaction 落在 `compaction`。
- **字段名**：外层 entry 照 pi（`type/id/parentId/timestamp`，`timestamp` 为 ISO 字符串）；内层 `message` 保留 pigo agentcore JSON 形状，usage 继续用 pigo 现有字段，不伪造 cache/cost。
- **排序**：header 第一行；entry 按 SQLite `seq`/append 物理序输出，父 entry 先于子 entry；import 按文件行序分配新 `seq`，并校验 `parentId` 存在。
- **无损边界**：会话语义无损，导出 header、全部 typed entries、`leafId`、facts 与可派生统计；不导出 `lanes/records/lane_moves/writer_leases`，import 后按新会话重建默认 lane。
- **facts**：labels/name 用 pi 的 `label` 与 `session_info` entry 表达；export 把 facts 当前值落成 entry，import 按 `seq` 重放，最新值写入 `facts` 表。
