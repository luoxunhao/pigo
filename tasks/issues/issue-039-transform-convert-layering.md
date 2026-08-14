# 转换分层契约

> 状态：**superseded**——由 `tasks/spec/spec-018-contextbuild-registry-dsh-alignment.md` 取代（对齐目标改为 deepseek-harness），归档保留。

Type: grilling
Status: resolved
Blocked by: issue-037

## Question

`contextbuild.ConvertToLlm` 与 provider `transformMessages` 的职责边界：compaction / branch_summary / custom_message 如何转成标准消息；tool call / tool result 关联与 tool id 归一化留在哪层；图片过滤由谁做；各 provider encoder 需要保留哪些行为。

## Comments

- 2026-08-13 Codex: claimed via wayfinder workflow; grilling started.
- Q1: A - ConvertToLlm 只做 provider 无关 role 转换（compaction/branch_summary/custom -> user），provider encoder 负责 model-aware 塑形（thinking、tool id、孤儿 tool result、图片能力）。
- Q2: A - 对齐 pi：新增 BranchSummaryMessage/CustomMessage；ConvertToLlm 将 compaction/branch_summary 转为带 summary 包装的 user 文本，custom 保留 text/image 转 user；provider encoder 删除 Compaction 特判。
- Q3: A - provider encoder 内按目标 API 规则归一化 tool id（仅 wire 副本，跨 provider/api/model 时触发）并回写 toolResult，合成孤儿 tool result；persisted messages 不改。
- Q4: A - 模型不支持图片时 provider encoder 降级为占位文本（去掉硬报错）；blockImages 策略过滤放 ConvertToLlm 层。

## Answer

- ConvertToLlm 只做 provider 无关 role 转换：compaction/branch_summary 转成带 summary 包装的 user 文本，custom 保留 text/image 转 user；provider encoder 不再处理 Compaction 特判。
- provider 层新增共享 transformMessages：按目标 API 规则归一化 tool id（仅 wire 副本，跨 provider/api/model 触发）并回写 toolResult，合成孤儿 tool result；图片按模型能力降级为占位文本。
- blockImages 策略过滤放 ConvertToLlm 层；各 encoder 保留既有 wire 专属行为（OpenAI content:null、Anthropic thinking 顺序/signature、Responses item 关联）。
