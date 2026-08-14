# golden 快照与验收回归

## Description

按 `tasks/spec/spec-018-contextbuild-registry-dsh-alignment.md` 切片 6 实施：pi parity corpus 归档（标记 superseded）；自持 golden 快照（`internal/contextbuild/testdata/golden/`，pigo 默认注册表配置下输出，`-update` 刷新脚本）；与 DSH standard preset 手工语义对照表；三层验收收尾（注册表行为矩阵 / 兼容回归 / 全量构建）。

## Acceptance Criteria

- [ ] `internal/contextbuild/testdata/parity/` 标记 superseded 归档（README 注明替代关系）
- [ ] `internal/contextbuild/testdata/golden/` 新快照 + 刷新脚本（-update）；默认注册表配置下 system prompt 输出冻结
- [ ] 注册表行为矩阵全绿：order/shadow/complete/suppress/变量/scope 链合并/工具引导段/sandbox:policy 注入
- [ ] 兼容回归全绿：contextbuild 公共 API 签名保持；subagent scope 共用不破；sandbox 行为（fence/escalation）不变；既有测试按新输出更新
- [ ] 与 DSH standard preset 手工语义对照表完成（评审项）
- [ ] `go build ./...` 与相关包 `go test` 通过
- [ ] REPL / TUI / headless / serve / ACP 回归（system prompt 输出、模型切换 persona 更新、sandbox 模式注入）

## Dependencies

issue-061 ~ issue-065 全部完成。

## Type

backend

## Priority

high
