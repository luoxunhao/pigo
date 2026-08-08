# pigo 会话级 slash registry 与 trust 指纹缓存

## Description

每个 `AcpSession` 持有自己的 slash registry，按会话 cwd 与 trust 状态解析；项目级 prompt 模板只在目录受信任时加载，并以 `(cwd, trust fingerprint)` 为键缓存，信任变化后失效。对应 PRD US-003 / FR-5、FR-6。

## Acceptance Criteria

- [ ] `AcpSession` 持有自己的 slash registry，不共享进程级单例
- [ ] 项目 `.pigo/prompts` 只在会话 cwd 受信任时加载
- [ ] registry 以 `(cwd, trust fingerprint)` 为键缓存，同一 cwd 复用
- [ ] trust 决策变化后，对应 cwd 的 registry 缓存失效并重建
- [ ] 两个项目同名模板在各自会话中解析到各自内容
- [ ] 对应 Go 单元测试通过

## Dependencies

Issue #016

## Type

backend

## Priority

high
