# SPEC: contextbuild 与 pi harness 对齐实施 Code Review 修复

> 状态：待实施。决策源为 `616368a`（`实施 contextbuild 与 pi harness 对齐`）相对 `fc09a0c` 的双轴 code review 报告；修复验收沿用 `tasks/spec-contextbuild-pi-alignment.md` 与 `.scratch/contextbuild-pi-alignment/issues/01-09`。

## 目标

把 code review 发现的 Standards 与 Spec 偏差转化为可实施、可验收的修复项，重点是：

- 让 parity corpus 真正执行 07 验收规则（deviations 注册表、tools 语义字段、pi commit 复核）。
- 让 lane.config 成为运行期真正权威（`SetLaneConfig` 接通、缺失配置语义一致）。
- 让 registry/reminders 接缝按 05 落地（内置 `reminders` transform、顺序链消费、lazy rebuild）。
- 消除旧会话加载回归风险与四处代码重复/类型偏差。

## 范围

涉及：`internal/contextbuild`、`internal/cli/run`、`internal/sessionstore`、`internal/session`、`internal/acp`、`internal/httpapi`、`internal/provider`、`internal/cli/{repl,tui,headless}`、`cmd/pigo`。

排除：`08 before_request` / stream options（原 spec 明确本轮不实现）；每轮实时重建 system prompt 的性能策略本身（本轮只修复缓存失效问题，不做新缓存架构）。

## Standards Findings

### S1. 旧 SQLite 会话升级后不可加载（高）

- 位置：`internal/sessionstore/migrations/002_lane_config.sql:1`、`internal/sessionstore/store.go:414`、`internal/session/v4.go:330`。
- 问题：Migration 002 只建表，行仅在 `upsertSessionTx` 回填；`BuildProjection` 在有条目但 main lane config 缺失时硬失败，导致本提交前创建的会话 `Store.Projection` 全部报错。
- 说明：原 spec 02-Q10 明确选择“不兼容旧会话、缺失配置视为 error”，因此这不是 spec 违约；但仍是数据兼容风险。
- 修复方向（二选一，需确认）：迁移脚本对已有会话回填 `{}` 或 header 推导配置；或在文档/迁移日志中显式声明旧会话需先升级，且提供一次性迁移命令。
- 验收：升级后旧会话可 `Load`/`Projection` 或给出明确可执行的迁移指引，两者择一并测试。

### S2. ACP payload 扩展未登记（中，判断项）

- 位置：`internal/acp/http_adapter.go:242`、`internal/acp/http_adapter.go:996`。
- 问题：`session/load` 增加 `_meta.pigo.laneConfig/systemPrompt`，session/update 增加顶层 `laneConfig/systemPrompt`，拓宽标准 ACP payload。
- 说明：spec 06 明确要求 `_meta.pigo` 扩展，属有意为之；需在 `internal/acp` 文档或 `spec-acp.md` 登记 vendor extension 名称与版本。
- 验收：协议文档记录 `_meta.pigo.laneConfig` / `_meta.pigo.systemPrompt` 扩展。

### S3. RequestBuilder 包装重复（低，smell）

- 位置：`internal/cli/headless/headless.go:114`、`internal/cli/repl/repl.go:638`、`internal/cli/tui/session.go:371`、`cmd/pigo/prompt_runner.go:196`。
- 问题：四个前端各自重复“调用 `contextbuild.RequestBuilder` + 捕获 `llm.SystemPrompt` 写回 header”的包装。
- 修复方向：在 `internal/cli/run` 提取 `InstallContextBuild(cfg *runtime.RunConfig, build *ContextBuild, header *session.SessionHeader)` helper。
- 验收：四个前端调用同一 helper，行为不变。

### S4. 图片占位替换重复（低，smell）

- 位置：`internal/contextbuild/convert.go:71`、`internal/provider/transform.go:143`、`internal/provider/transform.go:164`。
- 问题：`replaceImagesWithPlaceholder` 逐字节重复；`blockImages` / `downgradeUnsupportedImages` 同形状；`stringsTrim` 重造 `strings.TrimSpace`。
- 修复方向：共享一个导出的图片替换 helper；`stringsTrim` 替换为 `strings.TrimSpace`。
- 验收：`internal/contextbuild` 与 `internal/provider` 单测覆盖同一 placeholder 逻辑。

### S5. main lane config 复制循环重复（低，smell）

- 位置：`internal/sessionstore/store.go:1520`、`internal/sessionstore/store.go:1611`。
- 修复方向：提取 `mainLaneConfig(lanes []session.LaneState) *session.LaneConfig`。
- 验收：ForkV4 与 ImportV4 共用同一 helper。

### S6. LaneConfig.ThinkingLevel 类型偏差（低，smell）

- 位置：`internal/session/lane.go:10`。
- 问题：`ThinkingLevel` 使用 `string`，而 `agentcore.ThinkingLevel` 已存在。
- 修复方向：字段改为 `agentcore.ThinkingLevel`，JSON 序列化保持字符串。
- 验收：`go build ./...` 与 `internal/session`、`internal/contextbuild` 测试通过。

## Spec Findings

### P1. Parity 验收未真正执行（高）

- Spec：`fixture 内嵌 deviations 注册表（code/scope/pi commit/原因）；刷新脚本对未注册偏差 fail，已注册偏差在报告列出；pi commit 变化时强制复核`。
- 位置：`internal/contextbuild/parity_test.go:75`。
- 问题：`Deviations`/`PiCommit` 只定义不读取；tools 只按 name 对比，未按 `name/description/schema`；corpus 缺 registry/reminders/plugin 注入场景；`piCommit` 记录的是 pigo commit 而非 pi commit。
- 修复方向：测试读取 deviations 并校验未注册偏差；tools 对比扩展 description/schema；新增 registry/reminders/plugin fixture；`piCommit` 改记 pi 参考 commit（可放在 README 的 `piCommit` 字段并在刷新时校验变更需复核）。

### P2. ReminderRegistry 未进入 registry 顺序链（高）

- Spec：`ReminderRegistry 内置为 registry 的一条 transform（name=reminders）；BuildProviderContext 只从 registry 顺序链消费`。
- 位置：`internal/cli/run/run.go:243`、`internal/contextbuild/contextbuild.go:199`。
- 问题：`BuildProviderContext` 在 registry 链外单独应用 `ReminderTransform`；built-in/hooks 注册顺序未落地。
- 修复方向：`RunConfig` 装配时把 reminders 注册为 registry 的 `name=reminders` transform；`BuildProviderContext` 只消费 registry 链。

### P3. 配置/trust 变化无 lazy rebuild（高）

- Spec：`配置或 trust 变化时，下一次 provider 请求前惰性重建 session registry 并让 system prompt 指纹失效`。
- 位置：`internal/cli/run/run.go` `NewContextBuild`。
- 问题：session registry 只在会话装配时构建一次。
- 修复方向：在 `run.ContextBuild` 增加 `RebuildIfNeeded(input)`（按 config/trust 指纹惰性重建）；前端在每次 provider 请求前调用。

### P4. 相对 plugin_dirs 按进程 cwd 解析（中）

- Spec：`plugin_dirs` 相对路径按 session cwd 解析。
- 位置：`internal/cli/run/run.go:127`。
- 问题：共享 ACP/serve 多项目进程在启动 cwd 下解析相对路径。
- 修复方向：`ContextBuildInput` 增加 `PluginDirs []string` 与 `SessionCwd`，`NewContextBuild` 内按 session cwd 解析并合并插件发现；SetupEnv 只保留默认 plugins 目录。

### P5. ACP replay 丢弃 custom 消息（中）

- Spec：`replay 维持 compaction/branch_summary 跳过，custom 按 user 回放`。
- 位置：`internal/session/v4.go` `IsMessageEntry`、`internal/acp/http_adapter.go` `replayMessagesInto`。
- 问题：custom 不再是 `IsMessageEntry`，`Store.Load`/ACP replay 完全省略。
- 修复方向：ACP replay 对 `EntryTypeCustom` 按 user 回放；`Store.Load` 可保留 raw path 或显式扩展投影选项。

### P6. 缺失 lane.config 语义不一致（中）

- Spec：`缺失配置视为 error`。
- 位置：`internal/sessionstore/store.go:414`、`internal/session/v4.go:330`。
- 问题：`upsertSessionTx` 兜底写 `{}`，`BuildProjection` 只在有条目时报错。
- 修复方向：与 S1 一并决策：要么迁移时显式 backfill 并视为“空配置”，要么缺失即 error 并在迁移时对旧会话给出明确处理。

### P7. 指纹缓存失效（中）

- Spec：`指纹含 base instruction / cwd / context files / skills / active tools / append instructions，不变则复用字符串`。
- 位置：`internal/contextbuild/contextbuild.go:176`、`internal/contextbuild/contextbuild.go:292`。
- 问题：`RequestBuilder` 走包级 `BuildProviderContext`，每次新建 `Builder`，`promptCache` 从未复用。
- 修复方向：`run.ContextBuild` 持有 `*contextbuild.Builder`，`RequestBuilder` 调用 `Builder.BuildProviderContext`。

### P8. lane.config 变更后不是权威（高）

- Spec：`model / thinking / activeTools 以 lane.config 为权威`。
- 位置：`internal/httpapi/sessions.go:419`。
- 问题：`UpdateConfig` 只写 `CustomMetadata`，从不调用 `SetLaneConfig`；`session/load` 与 `session_info_update` 返回陈旧 `laneConfig`。
- 修复方向：`UpdateConfig` 同步写 `lane_config`（model/thinking/activeTools），并让 `session/load` 从 store 读最新值。

### P9. panic recover 不 warn（低）

- Spec：`运行期 transform/projector panic recover + warn + 回退`。
- 位置：`internal/contextbuild/registry.go` `safeTransform`、`internal/contextbuild/contextbuild.go` `safeProject`。
- 修复方向：recover 后向 `io.Writer`（warn）输出，未注入 writer 时至少带包级日志；回退行为保持不变。

## 建议实施切片

1. lane.config 权威与兼容：S1/S6/P6/P8（迁移回填或显式声明、`UpdateConfig` 写 lane_config、类型统一）。
2. Parity 强制执行：P1（deviations/tools/pi commit/新 fixture）。
3. Registry/reminders：P2/P3/P9（reminders 进 registry、lazy rebuild、panic warn）。
4. ACP 与 replay：S2/P5（vendor extension 登记、custom 按 user 回放）。
5. 共享 helper 与去重：S3/S4/S5。
6. plugin_dirs session cwd：P4。

## 验收

- `go build ./...`。
- `go test ./internal/contextbuild/... ./internal/runtime/... ./internal/sessionstore/... ./internal/acp/... ./internal/cli/config/...`。
- parity corpus 全绿；`refresh.ps1` 可重新生成并 diff；未注册偏差 fail。
- `session/load` 与 `session_info_update` 的 `laneConfig` 在 model/thinking 变更后立即反映。
- REPL / TUI / headless / serve / ACP 回归通过（真实客户端验证按用户既有偏好另行执行）。

## Backlog

- K1: `activeToolNames` 更新路径暂不接入 `session/update`；`lane.config.ActiveToolNames` 保持 `nil`（= 全部工具）。后续出现真实需求时再实现；完整决策链见 `tasks/spec-lane-config-authority.md`。
