# 工具引导段（tool:* contexts）

## Description

按 `tasks/spec/spec-018-contextbuild-registry-dsh-alignment.md` 切片 4 实施：内置工具引导 contexts——`tool:read` / `tool:write` / `tool:edit` / `tool:glob` / `tool:grep` / `tool:bash` / `tool:pwsh` 等（每工具一段，order 100~120）：用法、observed-state 规则（先读后写、FS_NOT_OBSERVED/FS_STALE_VERSION）、沙箱边界提示；文案与工具 schema description 互补不重复。

## Acceptance Criteria

- [ ] 每个受覆盖工具注册一个 `tool:<name>` context（order 100~120 区间）
- [ ] 文案提炼自现有工具 description + 执行语义（fence/沙箱/超时/observed-state），与 schema description 不重复
- [ ] 经 contexts 渲染每轮注入请求副本（不进历史）
- [ ] scope 感知：子代理 scope 自动继承工具引导段
- [ ] 单测：注入位置/顺序/内容、与工具集变化联动（工具不可用时对应引导段移除）

## Dependencies

issue-061、issue-062。

## Type

backend

## Priority

medium
