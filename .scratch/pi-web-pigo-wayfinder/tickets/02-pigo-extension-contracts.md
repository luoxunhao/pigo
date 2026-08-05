# 02 pigo 扩展契约总表

**Type:** grilling

## Question

新增协议方法的面和响应结构：`session/list`、`session/delete`、`additionalDirectories`、`pigo/models`、`pigo/config`、`pigo/messages`、resource_link/resource 附件支持。每个方法的参数/响应/能力声明/错误码是什么；哪些进 initialize 的 `_meta` 或标准 capabilities。

## Status

resolved

## Resolution

2.1: `session/list`、`session/delete`、`additionalDirectories` 进标准 capabilities；`pigo/models`、`pigo/config`、`pigo/messages` 进 `_meta` 扩展声明。
2.2: `pigo/config` 对 API key 只返回 `configured: true/false`，不回明文；字段白名单 `model/base_url/api_key/protocol/provider/thinking_level`。
2.3: `pigo/models` 返回 `{currentModelId, models: [{provider, modelId}]}`，由 pigo provider registry 生成。
2.4: resource_link 附件允许任意路径（本机单用户假设）；仍保留 64KB 读取上限防上下文爆炸。
