# 09 - sessionstore SQLite 重写 + 旧 JSONL 运行时删除

**What to build:** 把 `internal/sessionstore` 完整重写为 SQLite canonical 唯一入口；删除 `internal/session` 的 JSONL 运行时 API（`Store/Load/LoadEntries/Append/AppendBranch/Fork/PathToLeaf` 等）；更新 `cmd/pigo`、`internal/httpapi`、`internal/runtime`、`internal/cli` 全部调用方到 SQLite + ProjectLeaf。

**Blocked by:** 01-08

**Status:** ready-for-agent

## Acceptance Criteria

- [ ] `sessionstore` 不再读写 `.metadata.json` / `index.json` / `*.jsonl`
- [ ] `internal/session` 保留 codec/projection/rendering，删除运行时 store API
- [ ] `cmd/pigo`、`httpapi`、`runtime`、`cli` 不再有 `LoadEntries`/metadata curLeaf 路径
- [ ] `--resume/--continue/--list-sessions` 数据源为 SQLite
- [ ] `go build ./...` 通过；相关包测试通过
- [ ] grep 无旧运行时路径引用

**Type:** backend

**Priority:** high

**Spec Reference:** `tasks/spec-session-tree-pi-alignment.md` §11.1, §11.3

