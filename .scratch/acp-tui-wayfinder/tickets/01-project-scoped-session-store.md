# 01 — project-scoped 会话存储

**Type:** task

**What to build:** 用户/维护者可以按项目（workspace）创建、列出、恢复、删除会话；会话元数据带 schemaVersion、workspace/hostname、custom metadata、父子关系；transcript 复用既有 JSONL 树形格式；旧扁平会话目录保持只读兼容。

**Blocked by:** None — can start immediately

**Status:** ready-for-agent

- [ ] workspace slug 生成稳定（大小写、分隔符、超长截断、空路径回退）
- [ ] metadata/index/transcript 落盘与原子写入
- [ ] 创建、列表、恢复、删除的集成测试
- [ ] 旧扁平会话目录不受影响（兼容回归测试）

## Resolution

已解决（2026-08-05）。新增 Go 会话存储包：workspace slug 生成、metadata/index/transcript 落盘、原子写入、按项目隔离；`go test ./internal/sessionstore` 全绿。
