# Legacy v1-v3 JSONL deprecated: no migration, no runtime reads, quarantine by script

pigo 废弃 v1/v2/v3 JSONL 与旧平铺会话目录（`$PIGO_HOME/sessions`、`$PIGO_HOME/projects/*/sessions`）。运行时完全不可读，不迁移、不向后兼容、不保留兼容 shim；旧 id 统一返回标准 not found。

pigo 自身不移动旧文件。仓库提供 `scripts/quarantine-legacy-sessions.ps1` 与 `.sh`，由升级文档指导手动把旧目录移到 `$PIGO_HOME/legacy-sessions/`；脚本只移动、不删除、不转换。`--list-sessions/--resume/--continue//resume` 入口语义保留，数据源全部换成 SQLite canonical；`/export` 输出 v4 JSONL + HTML，`/import` 拒绝 v1/v2/v3。

完整设计见 `tasks/spec/spec-012-session-tree-pi-alignment.md` §11。

