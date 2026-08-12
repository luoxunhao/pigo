# 10 - 旧目录隔离脚本 + 入口语义 + 文档/ADR

**What to build:** 新增 `scripts/quarantine-legacy-sessions.ps1` 与 `.sh`；`/export` 输出 v4 JSONL/HTML，`/import` 只接受 v4 JSONL；更新 AGENTS.md、CONTEXT.md、README、docs/architecture 与 ADR；旧 id 统一 not found。

**Blocked by:** 09 - sessionstore SQLite 重写 + 旧 JSONL 运行时删除

**Status:** partial

## Acceptance Criteria

- [ ] 两个隔离脚本把旧目录移到 `$PIGO_HOME/legacy-sessions/`，只移动不删除不转换
- [ ] 脚本幂等；重复执行不覆盖已有 legacy 内容
- [ ] `/export` 默认 v4 JSONL，HTML 只读分享
- [ ] `/import` 拒绝 v1/v2/v3，明确报错
- [ ] 旧 id 返回标准 not found，不探测旧目录
- [ ] AGENTS.md / CONTEXT.md / README / docs/architecture 全面改为 SQLite + v4 JSONL 表述
- [ ] ADR 0006-0010 与实现一致

**Type:** docs

**Priority:** high

**Spec Reference:** `tasks/spec-session-tree-pi-alignment.md` §11

