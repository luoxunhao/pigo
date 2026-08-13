# 08 - 旧会话移除与入口清理

**Type:** grilling
**Status:** resolved
**Blocked by:** 02, 04, 05, 06

## Question

移除旧 v1/v2/v3 JSONL 与旧 sessionstore 运行路径时，`--resume/--continue/--list-sessions`、`/export`、`/import`、AGENTS.md、CONTEXT.md、ADR 的清理清单和验收边界是什么；旧文件在磁盘上如何处理（忽略/隐藏/删除）。

## Answer

已确认（2026-08-12，经 grilling 共识）。

- **旧格式读取**：v1/v2/v3 JSONL 与 `$PIGO_HOME/sessions` 旧平铺目录在运行时完全不可读；不迁移、不向后兼容，一切从新开始。
- **旧文件磁盘处理**：pigo 不做任何移动；由仓库内 `scripts/quarantine-legacy-sessions.ps1` + `.sh` 幂等地把 `$PIGO_HOME/sessions` 与 `$PIGO_HOME/projects/*/sessions` 移到 `$PIGO_HOME/legacy-sessions/`，只移动、不删除、不转换；升级文档说明手动执行一次。
- **入口面**：`--list-sessions`、`--resume`、`--continue`、`/resume` 全部保留，语义不变，数据源换成 SQLite canonical；headless/REPL/TUI/serve/ACP `session/load` 共用同一 leaf 投影入口。
- **导出/导入**：`/export` 从 SQLite 生成 v4 typed JSONL（默认）或自包含 HTML（只读分享，不参与导入）；`/import` 只接受 v4 JSONL，遇到 v1/v2/v3 返回明确错误。
- **导入身份与校验**：导入总是创建全新 SQLite 会话（新 uuidv7 id），原 id 仅作血缘记录；main lane leaf 按 header `leafId` 恢复（null 为空会话）；严格校验 header/entry type/唯一 id/parentId/leafId/facts，任何非法输入整体失败，不产生部分会话。
- **旧 id 错误语义**：统一返回标准 not found；不探测旧目录，不输出 legacy 专属提示。
- **代码清理**：`internal/session` 瘦身为 v4 typed JSONL 编解码，删除 `Store/Load/LoadEntries/Append/AppendBranch/Fork/PathToLeaf` 等 JSONL 运行 API；`internal/sessionstore` 重写为 SQLite canonical 唯一存储入口，删除 `.metadata.json`/`index.json`/`*.jsonl` 运行路径。
- **文档清理**：新增 ADR 记录“SQLite canonical、v4 JSONL 仅导出/导入、旧格式废弃、旧目录脚本隔离、不迁移”；AGENTS.md、CONTEXT.md、README、`docs/architecture` 全面改写为 SQLite + v4 JSONL 表述。
- **验收边界**：硬验收。代码 grep 无旧运行路径引用；入口行为全部来自 SQLite；测试覆盖 v4 round-trip、v1/v2/v3 拒绝、旧 id not found、隔离脚本幂等；`go build ./...` 与相关包测试通过。
