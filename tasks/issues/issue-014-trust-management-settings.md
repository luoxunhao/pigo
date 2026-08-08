# 设置页信任管理

## Description

ash-workbench 设置页新增“信任管理”区域，通过 `pigo/trust/list` 与 `pigo/trust/set` 列出已信任目录并支持撤销。对应 PRD US-011 / FR-16。

## Acceptance Criteria

- [ ] 设置页列出 pigo 已信任目录
- [ ] 每个目录提供“撤销信任”操作
- [ ] 撤销后 trust.json 中对应目录不再为 Trusted
- [ ] 无信任目录时显示空状态
- [ ] 在 ash-workbench 桌面端手动验证 UI 与操作

## Dependencies

Issue #13

## Type

fullstack

## Priority

medium
