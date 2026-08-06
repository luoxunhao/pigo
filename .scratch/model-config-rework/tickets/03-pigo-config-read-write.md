# 03 — pigo/config 新读写面

**Type:** backend

**What to build:** `pigo/config` 只读写 `model + models`，支持整体替换、按 `provider/model_id` upsert/delete，写入即时生效。

**Blocked by:** #1

**Status:** done

- [ ] 读取返回 `model` 与 `models`，每条带 `apiKeyConfigured`，不回显 key
- [ ] 支持 `{ model, models }` 整体替换
- [ ] 支持 `{ upsertModel }`，空 `api_key` 保留旧值
- [ ] 支持 `{ deleteModel }`，删除当前默认时 `model` 置空
- [ ] 不再返回 `needsRestart`
- [ ] 覆盖读写、upsert/delete 与 key 清洗的测试

**Spec:** 2026-08-06 模型配置重构设计（grill 确认）

## Resolution

已解决（2026-08-06）。`pigo/config` 读写 `model + models`，支持整体替换/upsert/delete，返回 `apiKeyConfigured`，无 `needsRestart`。
