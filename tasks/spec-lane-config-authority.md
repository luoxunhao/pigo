# SPEC: lane.config 单一权威与旧配置字段清理

> 状态：待实施。决策源为 2026-08-14 的 grilling/to-spec 会话；实施采用 TDD，先文档后代码。

## Problem Statement

当前 session 的 model/thinking 配置存在多头状态：`lane.config` 表、`metadata.header.laneConfig` 与 `metadata.customMetadata.thinkingLevel` 各自保存一份；`session/update` 只写 `customMetadata`，运行期 prompt 又接受客户端请求里的 model/thinking 并覆盖 contextbuild 上下文。结果是一个 session 可能持久化 `thinkingLevel=medium`、客户端配置 `high`、实际请求又取第三方值，界面与真实行为无法对齐。

另外新增 `lane_config` 表后，同一配置在 SQLite 表和 metadata JSON 里重复存储；旧 session 还可能只有空 `{}` 配置，加载时静默带空 model 继续运行。

## Solution

让 `lane.config` 成为唯一持久化与运行期权威：

- 新 session 创建时把 model/provider/thinking 写满 lane.config。
- `session/update` 是唯一配置变更入口，写 lane.config 后所有读取接口立即返回新值。
- `session/prompt` 不再接受 model/thinking 配置字段，携带即报错。
- 删除 `SessionHeader.LaneConfig`，新增显式携带 lane.config 的创建入口。
- migration 003 物理清除 `customMetadata.thinkingLevel` 与 `header.laneConfig`。
- 缺失或空 lane.config 的 session 加载时报错，不再静默运行。

## User Stories

1. As an ACP client, I want to change model/thinking through `session/update`, so that the change is persisted to the single lane.config source.
2. As an ACP client, I want `session/prompt` to accept only prompt text, so that configuration cannot be overridden per request.
3. As an ACP client, I want `session/load`, `session/status`, and `session_info_update` to read lane.config, so that every read API agrees on the current model and thinking.
4. As a user, I want model/thinking changes to be reflected immediately after `session/update`, so that the UI and the next request do not diverge.
5. As a developer, I want `customMetadata.thinkingLevel` removed from code and storage, so that there is no second thinking source.
6. As a developer, I want `header.laneConfig` removed from persisted metadata, so that the lane_config table is the only stored copy.
7. As a developer, I want a new session-creation API that explicitly carries lane.config, so that Fork/Import and frontends do not smuggle config through the session header.
8. As a user resuming a new session, I want model/provider/thinking restored from lane.config, so that the conversation continues with the same configuration.
9. As a developer, I want missing or empty lane.config to fail loading loudly, so that broken sessions are never silently run with an empty model.
10. As a maintainer, I want migration 003 to clean legacy JSON fields in one pass, so that existing databases converge without per-session lazy cleanup.
11. As a tester, I want session deletion to remove lane.config rows, so that deleted sessions leave no orphaned configuration.
12. As a developer, I want `activeToolNames` to remain `nil` meaning all tools and not gain a second update path yet, so that the backlog item is explicit instead of speculative code.
13. As a user, I want the model shown in session lists to stay synchronized with lane.config, so that display metadata does not become misleading.
14. As a client migrating to the new contract, I want an explicit error when I still send model/thinking in a prompt, so that I know to switch to `session/update`.
15. As a developer, I want old sessions with stale or empty configuration to be intentionally unsupported, so that no compatibility shim is introduced during development.

## Implementation Decisions

- **范围**：只修新 session；现有 session 的旧字段与空配置不做兼容、不回填。
- **持久化权威**：`lane_config` 表是唯一持久化的 lane config；`SessionHeader.LaneConfig` 字段整体移除，metadata JSON 不再携带 laneConfig。
- **创建入口**：保留原有无配置创建 API；新增显式携带 lane.config 的创建 API；Fork 与 Import 通过新入口写配置。
- **运行期权威**：每次 prompt 从 store 投影读取 lane.config，作为 model/provider/thinking 的唯一来源；`PromptRequest` 携带 model 或 thinkingLevel 时返回参数错误，不做忽略或覆盖。
- **配置变更**：`session/update` 写 lane.config（model/provider/thinking），并同步展示用 modelName；其响应以及 `session/load`、`session/status`、`session_info_update` 全部从 lane.config 读取。
- **元数据清理**：`customMetadata` 只保留 mode；`thinkingLevel` 的读写路径全部删除；`modelName` 保留为展示缓存，Create/UpdateConfig 时同步。
- **迁移**：migration 003 用 SQL `json_remove` 清理 `$.customMetadata.thinkingLevel` 与 `$.header.laneConfig`，走现有迁移链并由 `schema_migrations` 记录版本。
- **校验**：lane.config 缺失或空配置均视为加载/投影错误；新 session 创建时写满 model/provider/thinking。
- **activeToolNames**：保持 `nil`（全部工具），`session/update` 暂不接入该字段，记入 backlog。
- **关联修复**：session 删除必须清理 `lane_config` 行，已按此方向补齐。

## Testing Decisions

- **Seam 1：sessionstore 持久化边界**。通过 store 公开 API 验证创建、读取、投影、删除与迁移行为；仅在公开 API 无法观察存储不变量时直接断言表行，沿用现有测试先例。
- **Seam 2：httpapi 请求边界**。通过 `SessionService` 与 prompt 管理验证 `session/update` 写 lane.config、读取接口一致、prompt 携带 model/thinking 报错。
- **测试模块**：`internal/sessionstore`、`internal/session`、`internal/httpapi`。
- **先例**：`internal/sessionstore` 的迁移/lane 测试与 `internal/httpapi` 的 sessions/prompt 测试；已存在的 `DeleteRemovesLaneConfig` 测试作为删除行为的先例。
- **方式**：TDD 红绿循环，一次一个垂直切片；先写失败测试，再实现最小改动。

## Out of Scope

- 现有 session 的兼容、迁移或自动修复；旧 session 可能继续无法加载。
- `activeToolNames` 更新路径与工具白名单管理（K1 backlog）。
- 客户端侧改造：pi-web、ash-workbench 等停止在 prompt 中发送 model/thinking，属协议变更后的客户端跟进项。
- 除本 spec 覆盖项外的其他 code review findings（registry/reminders、lazy rebuild、parity、plugin_dirs 等）。
- mode 从 `customMetadata` 迁出；mode 继续留在原位置。
- system prompt 缓存/性能架构调整。

## Further Notes

- K1 backlog 已按 L1 决定追加到现有 code-review spec，并在本 spec 中记录完整决策链。
- 实施完成后跑目标包测试与全量编译，并重新编译 `pigo.exe` 供手工验证。
- 本仓库未配置外部 issue tracker，文档按本地惯例发布到 `tasks/` 目录。
