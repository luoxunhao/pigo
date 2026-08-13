# 14 - OpenAPI / generated client 更新

**What to build:** 更新 `api/v1/openapi.yaml`：`PromptResponse.structured`、`session/load` leaf 字段、`status` 的 currentLeaf/lanes、session list 子会话字段；重新生成 `internal/httpapi/gen` 与 Go client，确保无 diff 校验。

**Blocked by:** 07 - ACP/HTTP 树 surface v1, 09 - sessionstore SQLite 重写

**Status:** resolved

## Acceptance Criteria

- [x] OpenAPI 是权威契约，新字段全部入 schema
- [x] 生成类型与 client 可编译
- [x] CI/脚本检查生成产物无 diff
- [x] HTTP 集成测试覆盖 structured `/tree`、status、session list
- [x] 不引入破坏旧字段的重命名

**Type:** backend

**Priority:** medium

**Spec Reference:** `tasks/spec-session-tree-pi-alignment.md` §9, §14

