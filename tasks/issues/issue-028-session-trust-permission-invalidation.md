# pigo 会话级 trust/权限与缓存失效

## Description

信任与权限判断按会话 cwd 进行；`allow_always` / `reject_always` 持久化到 `~/.pigo/trust.json`；信任变化后对应会话的 slash registry 缓存失效。对应 PRD US-006 / FR-8、FR-9、FR-10。

## Acceptance Criteria

- [ ] `BeforeToolCall` 继续使用会话 cwd 判断 `IsTrusted`
- [ ] `allow_always` 持久化写入 `~/.pigo/trust.json`
- [ ] `reject_always` 写入 Untrusted 并正确拦截
- [ ] 信任变更后对应会话的 slash registry 缓存失效
- [ ] 主目录受信任时，项目 `sourceFolders` 视为同一信任边界
- [ ] 对应 Go 单元测试通过

## Dependencies

Issue #018

## Type

backend

## Priority

high
