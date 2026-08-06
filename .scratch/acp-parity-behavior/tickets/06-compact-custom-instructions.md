# 06 — /compact 自定义 instructions

**Type:** task

**What to build:** 用户执行 `/compact [instructions...]` 时，pigo 把自定义说明传入压缩流程，并返回压缩前后 token 变化与摘要；无参数时行为保持默认。

**Blocked by:** None — can start immediately

**Status:** ready-for-agent

- [ ] `/compact` 无参数时使用默认压缩设置
- [ ] `/compact <instructions>` 将自定义说明传给压缩管线
- [ ] 输出包含压缩前后 token 与摘要
- [ ] 覆盖有/无自定义说明的测试
