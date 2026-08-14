# PRD: pigo 官方网站（对标 MiMo Code）

## Introduction / 概述

为 pigo（`github.com/smallnest/pigo`，一个用 Go 复刻 pi 的命令行编码 Agent）重建一套**纯静态官方网站**，视觉与信息结构对标 [MiMo Code](https://mimo.xiaomi.com/coder)：一个营销风格的落地页（Hero + 交替展示的特性区块），配套多篇文档页。

网站为零构建、零依赖的静态资源，可直接双击打开或托管到 GitHub Pages。首页位于 `docs/index.html`，文档页位于新建目录 `docs/web/`。全站**中英双语**，支持一键切换。

面向读者：首次了解 pigo 的开发者、以及需要查阅安装/配置/命令的使用者。

## Goals / 目标

- 复刻 MiMo Code 的落地页观感：Hero 主视觉 + 若干"图文交替"特性展示区块 + 页脚。
- 首页 `docs/index.html` 一屏内传达 pigo 是什么、核心卖点、如何开始。
- 提供至少 6 篇文档页：快速开始、安装、特性、配置、Slash 命令、架构。
- 全站中英双语，语言切换后所有可见文案（含导航、文档正文）同步切换，且刷新后保持选择。
- 纯静态：无需 npm/构建步骤，任意浏览器直接打开可用。
- 首页与文档页共享统一的导航、配色、排版与暗色主题。

## User Stories

### US-001: 建立站点骨架与共享样式
**Description:** As a developer, I want 一套共享的 CSS/JS 与目录结构 so that 所有页面风格一致且零构建可运行。

**Acceptance Criteria:**
- [ ] 新建目录 `docs/web/`，用于存放文档页与共享资源（`assets/` 下放 css/js/图片）
- [ ] `docs/web/assets/site.css` 定义全站配色、排版、响应式栅格、暗/亮色变量
- [ ] `docs/web/assets/i18n.js` 提供语言切换与 `localStorage` 持久化，默认跟随浏览器语言
- [ ] 所有页面通过 `<html lang>` 与 `data-i18n` 机制支持中英切换
- [ ] 页面在无网络、无构建工具时直接用浏览器打开可正常渲染
- [ ] Verify in a browser（经 `run` 技能或本地打开）

### US-002: 首页 Hero 主视觉
**Description:** As a 访客, I want 首页顶部一眼看懂 pigo 是什么 so that 我能快速判断是否要用。

**Acceptance Criteria:**
- [ ] `docs/index.html` 顶部含产品名 "pigo"、一句话标语、简短副标题
- [ ] 含主 CTA 按钮：`快速开始 / Quickstart`（跳文档）与 `GitHub`（跳仓库）
- [ ] 展示一段可复制的安装/启动命令（如 `curl ... | sh` 或 `go install`）
- [ ] 顶部导航含 Logo、文档入口、语言切换按钮、GitHub 链接
- [ ] 中英切换后 Hero 全部文案同步变化
- [ ] Verify in a browser

### US-003: 首页特性展示区块（图文交替）
**Description:** As a 访客, I want 像 MiMo Code 那样的图文交替特性区 so that 我能直观了解核心能力。

**Acceptance Criteria:**
- [ ] 至少 5 个"图文交替"区块，左右布局逐块交替
- [ ] 覆盖卖点：多 Provider、内置工具集、无头/REPL 双模式、会话续跑、技能与插件、持久记忆与无限上下文
- [ ] 每块含标题、说明文字、配图或代码/终端示意
- [ ] 移动端下自动堆叠为单列
- [ ] 中英切换后区块文案同步变化
- [ ] Verify in a browser

### US-004: 快速开始文档页
**Description:** As a 新用户, I want 一页读完就能跑起来 so that 我能在 5 分钟内完成首次运行。

**Acceptance Criteria:**
- [ ] 新建 `docs/web/quickstart.html`
- [ ] 含最小可用流程：安装 → 配置 API Key/模型 → 无头 `-p` 一次性执行 → 进入交互式 REPL
- [ ] 每步含可复制代码块
- [ ] 与安装页/配置页交叉链接
- [ ] Verify in a browser

### US-005: 安装文档页
**Description:** As a 用户, I want 多种安装方式说明 so that 我能选择适合自己环境的方式。

**Acceptance Criteria:**
- [ ] 新建 `docs/web/install.html`
- [ ] 覆盖：`install.sh` 脚本安装、`go install`（Go 1.27+）、从源码构建、`pigo update` 自更新
- [ ] 说明二进制安装位置与 PATH 配置
- [ ] Verify in a browser

### US-006: 特性文档页
**Description:** As a 用户, I want 完整特性清单与说明 so that 我了解 pigo 能做什么。

**Acceptance Criteria:**
- [ ] 新建 `docs/web/features.html`
- [ ] 覆盖：双运行模式、多 Provider、内置工具（read/write/edit/grep/find/bash/todo/webfetch）、stream-json 输出、系统提示词分层组装、项目信任、会话续跑、上下文自动压缩、持久记忆、包管理、Hooks、插件
- [ ] 每个特性含简述与（如适用）示例命令
- [ ] Verify in a browser

### US-007: 配置文档页
**Description:** As a 用户, I want 配置项说明 so that 我能正确配置 pigo。

**Acceptance Criteria:**
- [ ] 新建 `docs/web/configuration.html`
- [ ] 说明配置文件位置（`~/.config/pigo/config.toml`）与常用键：`model`、`base_url`、`api_key`、`output_format`、`approve`、`no_tools`、`no_skills`、`protocol` 等
- [ ] 说明 `[memory]` 与 `[checkpoint]` 嵌套表
- [ ] 说明常用 `<PROVIDER>_API_KEY` 环境变量与命令行参数优先级
- [ ] 提供一份可复制的示例 `config.toml`
- [ ] Verify in a browser

### US-008: Slash 命令文档页
**Description:** As a 用户, I want REPL 斜杠命令速查 so that 我能高效使用交互模式。

**Acceptance Criteria:**
- [ ] 新建 `docs/web/slash-commands.html`
- [ ] 以表格列出内置命令及说明：`/help`、`/model`、`/models`、`/think`、`/compact`、`/fork`、`/clone`、`/tree`、`/export`、`/import`、`/copy`、`/session`、`/status`、`/memory`、`/exit` 等
- [ ] 说明技能命令 `/skill-name` 与提示词模板 `/name`（含 `$1`/`$@`/`$ARGUMENTS` 参数语法）来源
- [ ] Verify in a browser

### US-009: 架构文档页
**Description:** As a 开发者, I want 了解 pigo 内部架构 so that 我能理解或参与开发。

**Acceptance Criteria:**
- [ ] 新建 `docs/web/architecture.html`
- [ ] 描述 Agent Loop、AgentCore、Provider 抽象、工具系统、运行时组件之间关系
- [ ] 复用/链接 `docs/` 下已有架构图（如 `pigo-agent-loop.html`、`pigo-runtime.html`、`class_*.html`）
- [ ] Verify in a browser

### US-010: 文档导航与首页联通
**Description:** As a 访客, I want 文档页之间与首页有一致导航 so that 我能顺畅跳转。

**Acceptance Criteria:**
- [ ] 每个文档页含左侧或顶部文档导航，列出全部文档页并高亮当前页
- [ ] 首页 CTA 与导航能进入文档，文档页能返回首页
- [ ] 所有内部链接为相对路径，可离线跳转
- [ ] Verify in a browser

## Functional Requirements

- FR-1: 系统须在 `docs/index.html` 提供营销风格首页，含 Hero、特性区块、页脚。
- FR-2: 系统须在 `docs/web/` 目录提供文档页：`quickstart.html`、`install.html`、`features.html`、`configuration.html`、`slash-commands.html`、`architecture.html`。
- FR-3: 系统须提供共享样式表与脚本（置于 `docs/web/assets/`），供首页与所有文档页引用。
- FR-4: 系统须支持中/英双语切换，切换即时生效并通过 `localStorage` 持久化，首次访问默认跟随浏览器语言。
- FR-5: 首页特性区块须采用图文左右交替布局，且在窄屏下堆叠为单列。
- FR-6: 站点须为纯静态资源，不依赖任何构建工具或运行时服务器即可在浏览器打开。
- FR-7: 站点须提供暗色主题（可含亮色），配色与排版全站统一。
- FR-8: 所有页面内部跳转须使用相对路径，保证离线可用。
- FR-9: 文档内容须与项目实际能力一致（Provider、工具、命令、配置项以 README 与 `config.toml.example` 为准）。
- FR-10: 架构文档页须链接或嵌入 `docs/` 下已有的架构图 HTML。

## Non-Goals（不做的事）

- 不引入 VitePress/Docusaurus 等需要构建的静态站点框架。
- 不做后端服务、搜索服务、评论或统计接入。
- 不做账号登录、在线试用 Playground。
- 不改动 `docs/` 下现有 `issue#*.html` 归档文件与既有架构图内容（仅链接引用）。
- 不新增中文/英文之外的第三种语言。
- 不做 Markdown 动态渲染方案（内容直接写入 HTML）。

## Design Considerations

- 参考 MiMo Code：暗色背景、居中 Hero、大标题 + 柔和副标题、圆角卡片、图文交替区块。
- 排版用系统无衬线字体栈；代码块等宽字体 + 深色底 + 可复制。
- 配色建议以 pigo/Go 主题色（如青绿/靛蓝）为强调色，保持克制。
- 响应式：桌面多列、移动端单列。
- 复用现有资源：`docs/pigo-tui.png`、`docs/pigo.png` 可作首页配图。

## Technical Considerations

- 目录：首页 `docs/index.html`；文档与资源 `docs/web/`（`docs/web/assets/`）。
- 注意 `docs/index.html` 已存在，本任务将覆盖重写，需先确认其现有内容非关键归档。
- i18n 采用轻量方案：`data-i18n` 属性 + JS 字典，避免维护两套 HTML。
- 内容真实来源：`README.md`、`config.toml.example`、`docs/` 下架构图。

## Success Metrics

- 首页在浏览器打开后 5 秒内完成渲染，无 JS 报错（console 无 error）。
- 新用户能仅凭 quickstart 页在 5 分钟内完成首次运行。
- 中英切换覆盖 100% 可见文案，无残留未翻译文本。
- 全部内部链接可点击且无 404（离线可跳转）。

## Open Questions

- 现有 `docs/index.html` 是否有需要保留的内容？（默认覆盖）
- 强调色与是否需要亮色主题的最终取舍？
- 首页配图除现有截图外，是否需要额外绘制的特性示意图？
