# 迷你数字员工

> 能接收岗位任务、调用工具产出交付物、交付前接受人工审核的数字员工系统。
> 工作流程由程序编排，模型只在判断步骤里工作；一个员工一套配置，换配置就换岗位。

首个落地的岗位是**经营周报员工（场景 A）**：发起人上传销售明细与周会纪要，系统依次完成准入校验 → 读取数据 → 计算指标 → 解析纪要 → 匹配异常原因 → 撰写报告 → 生成 Word → 质检（不通过自动修正）→ 等待审核；审核人可通过交付，或退回重做。全流程有事件日志，进程被杀后可从断点恢复。

<img src="docs/ui/final/01-welcome.png" alt="欢迎页" width="760">

<img src="docs/ui/final/03-review.png" alt="审核页" width="760">
## 亮点

- **程序编排步骤，模型只做判断**：计算步骤直接调工具，判断步骤（原因匹配、写报告、解读退回意见）才交给模型，模型在轮次与时长上限内运行 —— 见 [ADR-0001](docs/adr/0001-program-orchestrated-steps.md)。
- **数字和引用只走槽位**：正文里的数字与原文引用由程序从事实和纪要填入，模型写错会被质检抓出 —— 见 [ADR-0002](docs/adr/0002-facts-via-slots.md)。
- **质检不通过自动修正**：结构、溯源、禁用指标、篇幅、异常覆盖、引用真实、意见响应 7 类检查，按 `fixStep` 重跑对应步骤，次数用尽才转人工。
- **JSONL 事件日志 + 断点恢复**：每个任务 `events.jsonl` 追加写；重启后 `resumeAll()` 重放事件、只重跑未完成步骤；支持命令行回放。
- **轨迹**：对话框顶部固定显示当前任务的执行过程（步骤、工具调用、模型轮次、质检、暂停与重做），折叠成一行摘要，运行中自动展开并跟随最新事件；只读，不提供推进操作。
- **工具走 MCP**：工具服务器独立进程、stdio 协议，内置故障注入（超时 / 报错 / 非法输出），可现场演示工具故障与重试。
- **权限在服务端校验**：发起人 / 审核人 / 操作员三种角色，越权操作被拒绝并留痕，状态机之外的流转一律拒绝。
- **全栈 TypeScript**：Bun + Elysia + React/Vite + antd；前后端共享契约类型。

## 目录结构

```
digital-employee/
├── packages/
│   ├── core/      状态机、编排器、事件日志、质检引擎、槽位渲染（不依赖外壳）
│   ├── tools/     MCP 工具服务器（场景 A 的工具、故障注入）
│   ├── server/    Elysia：REST 接口、SSE 事件流、角色权限
│   ├── web/       React 前端（任务对话、轨迹、发起表单、审核、设置）
│   └── cli/       命令行：跑用例、回放事件
├── employees/
│   └── weekly-report/   场景 A 员工配置（提示词、输出 Schema、流程与质检配置）
├── samples/
│   ├── scenario-a/      样例数据（销售明细、周会纪要、格式要求）
│   └── cases/           A-01…A-05 命令行用例及其剧本式假模型输出
├── docs/          设计文档、术语表、ADR、ticket、界面稿
└── README.md
```

## 快速开始

### 环境

- [Bun](https://bun.sh) ≥ 1.4（`bun --version` 确认）
- Windows 10/11（默认工作区在 `%APPDATA%\digital-employee`；其他平台用 `DE_WORKSPACE` 指定工作区）
- 真实模型需要一个 DeepSeek 或 OpenAI 兼容接口的 API Key；只想看流程可以用下面的**离线剧本模式**，不需要 Key

### 安装

```bash
bun install
```

### 配置模型

在**工作区**（默认 `%APPDATA%\digital-employee`，不是仓库目录）放 `settings.json`：

```json
{
  "connections": [
    { "name": "deepseek", "baseUrl": "https://api.deepseek.com", "apiKey": "sk-...", "models": ["deepseek-chat"] }
  ],
  "defaultConnection": "deepseek",
  "defaultModel": "deepseek-chat"
}
```

环境变量 `DEEPSEEK_API_KEY` 优先于文件里的 Key。任何 OpenAI 兼容接口都能接（改 `baseUrl` 与模型名即可）。

### 启动

```bash
bun run dev:server   # API 服务：http://localhost:3000
bun run dev:web      # 前端热更新：http://localhost:5173（/api 代理到 3000）
```

想在 3000 端口直接打开完整界面，先构建一次前端，服务端会自动托管 `packages/web/dist`：

```bash
bun run --filter @de/web build
bun run dev:server
```

端口可用 `DE_PORT` 或 `PORT` 覆盖。

### 离线剧本模式（不调用真实模型）

```powershell
$env:DE_SCRIPTED="samples/cases/scripts/A-01.json"; bun run dev:server
```

```bash
# macOS / Linux / Git Bash
DE_SCRIPTED=samples/cases/scripts/A-01.json bun run dev:server
```

剧本文件按步骤给出模型回复（`{ "<stepId>": [{ toolCalls: [...] }] }`），样例在 `samples/cases/scripts/`。端到端测试也用同一种剧本。

## 常用命令

| 命令 | 说明 |
|---|---|
| `bun test` | 全仓库测试（含端到端；剧本式模型，不联网、不需要 Key） |
| `bun run typecheck` | 5 个包分别 `tsc --noEmit` |
| `bun packages/cli/src/main.ts run-case samples/cases/A-01.json --scripted samples/cases/scripts/A-01.json --out out` | 按用例驱动引擎，结果写到 `out/<id>/result.json` |
| `bun packages/cli/src/main.ts replay <workspace> <taskId>` | 按时间回放某任务的事件 |
| `bun packages/cli/src/main.ts mcp-server` | 以 stdio 运行工具服务器 |

用例文件格式见 [samples/cases/README.md](samples/cases/README.md)。

## 任务流程

- **主路径**：草稿 →（提交）准入校验 →（通过）执行中 →（全部步骤完成）质检中 →（质检通过）待审核 →（通过）已交付。
- **回流与暂停**：
  - 准入不通过 → 待发起人补充；补充 / 继续（无纪要、按现有资料）后回到准入校验，也可终止；
  - 执行中超限、工具多次失败、质检自动修正用尽 → 待人工处理；操作员可重试（从失败步骤继续）或终止；
  - 审核人退回 → 执行中（重做），退回意见作为判断步骤的修正上下文；
  - 质检不通过且可自动修正 → 回到执行中，按 `fixStep` 重跑对应步骤。
- **终态**：已交付 / 已终止。
- 角色：`initiator`（发起人）、`reviewer`（审核人）、`operator`（操作员）。HTTP 请求用 `x-role` 头声明角色，服务端校验。
- 表外的流转一律拒绝，并写一条 `transition_rejected` 事件；越权操作同样留痕。
- 完整流转表见[设计文档 §5](docs/design.md)。

## HTTP 接口

| 方法与路径 | 说明 |
|---|---|
| `GET /api/employees` | 员工列表（含发起表单 Schema、可退回章节） |
| `GET /api/tasks` | 任务摘要列表（损坏的日志会带 `error` 字段，不影响其他任务） |
| `POST /api/tasks` | 创建任务（multipart：`employee`、`form`、`attachments`） |
| `GET /api/tasks/:id` | 任务快照（步骤、预算、质检结果、交付物） |
| `POST /api/tasks/:id/commands` | 命令：提交 / 补充 / 继续 / 重试 / 终止 / 通过 / 退回 |
| `GET /api/tasks/:id/events?after=<seq>` | 事件日志 |
| `GET /api/tasks/:id/stream?after=<seq>` | SSE 事件流（含心跳，重连用 `Last-Event-ID`） |
| `GET /api/tasks/:id/deliverables/:kind` | 下载交付物（`docx` / `md` / `qc_record`） |

附件只能通过 multipart 上传；命令 JSON 里自带 `attachments[].sourcePath` 会被拒绝（防止服务端读取任意本地文件）。

## 员工配置

一个员工一个目录，全部行为写在配置里：

```
employees/weekly-report/
├── SKILL.md          岗位职责与写作要求（判断步骤的系统提示词）
├── frontdesk.md      前台 Agent 人设
├── workflow.yaml     步骤列表、输入映射、预算、质检输入/修正步骤
├── qc.yaml           质检规则、禁用词、阈值、自定义规则
├── form.schema.json  发起表单（前端用 JSON Schema 渲染）
├── form.ui.json      表单布局
├── prompts/*.md      各判断步骤的提示词
└── schemas/*.json    各判断步骤的输出 Schema
```

`workflow.yaml` 里两个可选映射值得知道：

- `qcSources`：质检从哪些步骤取草稿、纪要、渲染结果（缺省 `write_report` / `parse_minutes` / `render_document`）。
- `qcFixSteps`：质检不通过时重跑哪一步（`write` 缺省跟随 `qcSources.draft`，`causes` 缺省 `match_causes`）。解析出的 id 必须真实存在，否则加载配置时直接报错。

契约（共享类型与 zod Schema）集中在 [`packages/core/src/contract`](packages/core/src/contract)，是唯一事实来源；其余包只实现契约。

## 架构

| 包 | 职责 |
|---|---|
| `@de/core` | 任务引擎（状态机、编排、事件日志、断点恢复）、质检引擎、槽位渲染、配置加载、模型与 MCP 客户端 |
| `@de/tools` | MCP 工具服务器：`validate_submission`、`load_sales`、`compute_metrics`、`parse_minutes`、`render_report_docx`（`web_search` 仅用于演示越权拦截） |
| `@de/server` | Elysia REST + SSE、角色权限、静态资源托管 |
| `@de/web` | React 前端：任务对话与轨迹 / 发起 / 审核 / 设置 |
| `@de/cli` | `run-case` 批量跑用例、`replay` 事件回放、`mcp-server` |

- 数据流：表单与附件 → 准入工具校验 → 计算步骤（工具）与判断步骤（模型 + 受限工具）→ 质检 → 自动修正或转人工 → 人工审核 → 交付物。
- 每个任务目录下保存事件日志、附件、配置快照、产物与交付物；重启后只重放日志，不重新调用模型。
- 工具调用失败先按配置重试，再暂停转人工，不编造结果；写文件类工具用「任务 + 步骤 + 输入哈希」做幂等。

## 测试与样例

- `bun test` 覆盖 core / tools / server / cli / web，以及不联网的端到端用例（真实工具 + 真实 Word 生成 + 剧本式模型）。
- `samples/cases/` 是场景 A 公开样例 A-01…A-05 的用例描述；`samples/cases/scripts/` 是对应的剧本式假模型输出。
- `samples/scenario-a/` 是原始数据：销售明细、W42–W44 周会纪要、格式要求、数据说明。

## 文档

- [设计文档](docs/design.md)：范围、技术栈、架构、状态机、质检、场景 A/B、界面、打包
- [术语表](GLOSSARY.md)：任务、角色、步骤、事实等术语的准确含义
- [ADR-0001 程序编排步骤](docs/adr/0001-program-orchestrated-steps.md) / [ADR-0002 事实与槽位](docs/adr/0002-facts-via-slots.md)
- [规格 0001](docs/specs/0001-skeleton.md) 与 [tickets T1–T7](docs/specs/tickets)
- [用例文件格式](samples/cases/README.md)

## 现状

- 场景 A（经营周报）已端到端跑通：契约、工具、引擎、服务端/CLI、前端、质检与集成测试均已实现。
- 场景 B（技术文献研判）的设计见[设计文档 §11](docs/design.md)，员工配置尚未落地。
- 单文件 exe 打包（设计 §14）尚未加入仓库脚本，当前以源码方式用 Bun 运行。
