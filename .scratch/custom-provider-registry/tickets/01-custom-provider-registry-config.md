# 01 — 自定义 provider 注册表与配置读写

**Type:** backend

**What to build:** pigo 的 `config.toml` 可以持久化多个自定义 provider（endpoint/key/protocol 与模型缓存），同时保持现有顶层 flat 配置兼容；provider 拥有首次生成后稳定的 `custom-<slug>` id。

**Blocked by:** None — can start immediately

**Status:** done

- [ ] `[[providers]]` 支持 id/name/base_url/api_key/protocol/models 读写
- [ ] 旧顶层 `model/provider/base_url/api_key/protocol` 仍可加载，legacy 写入不破坏 providers
- [ ] provider id 首次生成后改名/改端点不变，且不含 `/`
- [ ] 读取路径绝不返回明文 apiKey
- [ ] 单测覆盖配置兼容、slug 稳定性与 key 清洗

**Spec:** tasks/spec-custom-provider-registry.md（Config Shape / Provider ID / Security）

## Resolution

已解决（2026-08-06）。config.toml 支持 `[[providers]]` 读写、legacy flat 兼容、`custom-<slug>` 稳定 id 与 key 清洗；`go test ./internal/cli/config` 全绿。
