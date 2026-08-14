# File fence 文件工具强制

## Description

按 `tasks/spec/spec-017-sandbox.md` 切片 3 实施：进程内 file fence——路径包含检查（canonical fast path + filesystem identity fallback，处理 Windows 8.3/大小写别名），read/write/edit/search 等文件工具接入；目标必须是 writableRoots 之一或其下，否则返回 denial marker。

## Acceptance Criteria

- [ ] fence 实现：canonical 词法检查 fast path + stat identity fallback（dev/ino 比较）
- [ ] read/write/edit/search 工具接入 fence；拒绝以 `[sandbox: file access denied under <mode> mode]` 呈现（工具错误结果，非命令失败）
- [ ] workspace-write 下工作区 + 临时区可写、工作区外拒绝；read-only 下全部拒绝；full-access 下不过 fence
- [ ] 与 observed-state（FS_NOT_OBSERVED / FS_STALE_VERSION）不冲突
- [ ] 单测：包含/排除、别名路径（8.3/大小写）、临时区、三档模式矩阵

## Dependencies

issue-053（策略层）。

## Type

backend

## Priority

high
