# v4 typed JSONL: export/import only, never runtime storage

pigo 引入 v4 typed JSONL 作为会话的导出/导入格式，不再作为运行时存储。header 单行声明 `type:"session", version:4`，并携带 `createdAt/updatedAt/cwd/model/provider/systemPrompt/additionalDirectories/leafId` 等 pigo 扩展字段；entry 对齐 pi 的 9 种类型（`message/model_change/thinking_level_change/compaction/branch_summary/custom/custom_message/label/session_info`），行序即 SQLite `seq` 物理序。

`/export` 从 SQLite canonical 生成 v4 JSONL（或自包含 HTML，仅分享不导入）；`/import` 只接受 v4 JSONL，总是创建新 uuidv7 会话，严格校验 header/entry/parentId/leafId/facts，任何非法输入整体失败。label/name 在 JSONL 中以 `label` / `session_info` entry 表达，导入后按 seq 重放并写入 `facts` 最新值；不导出 `lanes/records/lane_moves/writer_leases`，side lanes 不保留。

该格式追求会话语义无损，不追求与 pi 文件字节级互通。完整设计见 `tasks/spec/spec-012-session-tree-pi-alignment.md` §6。

