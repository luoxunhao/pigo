# before_request / stream options 接缝（未实现）

> 状态：**superseded**——由 `tasks/spec/spec-018-contextbuild-registry-dsh-alignment.md` 取代（对齐目标改为 deepseek-harness），归档保留。

Type: grilling
Status: open
Blocked by: issue-041

## Question

是否引入 pi 风格 `before_request`：每次 provider 请求前 patch stream options（model、step、attempt、retry 等）；与现有 `PrepareNextTurn` / `GetAPIKey` / thinking 投影的职责边界；hooks/plugins 是否可注册；错误处理。issue-041 已明确本轮不实现，本 ticket 仅承接后续决策。

## Comments

- 2026-08-13 Codex: created from issue-041 grilling Q6=A；本轮不实现。
