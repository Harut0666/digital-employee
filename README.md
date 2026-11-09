# 迷你数字员工

> 能接收岗位任务、调用工具产出交付物、交付前接受人工审核的数字员工系统。
> 工作流程由程序编排，模型只在判断步骤里工作；一个员工一套配置，换配置就换岗位。

首个落地的岗位是**经营周报员工（场景 A）**：发起人在对话里说明要做什么，前台 Agent 判断由哪个员工接手、引导备齐材料，系统依次完成准入校验 → 读取数据 → 计算指标 → 解析纪要 → 匹配异常原因 → 撰写报告 → 生成 Word → 质检（不通过自动修正）→ 等待审核；审核人可通过交付，或退回重做。全流程有事件日志，进程被杀后可从断点恢复。

工作区就是电脑上的一个文件夹：会话和任务属于某个工作区，所有工作区同时可用、不用切换；设置、员工和知识库放在应用目录，所有工作区共用（见 [ADR-0006](docs/adr/0006-workspace-is-a-folder.md)）。

<img src="docs/ui/final/01-welcome.png" alt="欢迎页" width="760">

<img src="docs/ui/final/03-review.png" alt="审核页" width="760">
## 亮点

- **程序编排步骤，模型只做判断**：计算步骤直接调工具，判断步骤（原因匹配、写报告、解读退回意见）才交给模型，模型在轮次与时长上限内运行 —— 见 [ADR-0001](docs/adr/0001-program-orchestrated-steps.md)。
- **数字和引用只走槽位**：正文里的数字与原文引用由程序从事实和纪要填入，模型写错会被质检抓出 —— 见 [ADR-0002](docs/adr/0002-facts-via-slots.md)。
- **质检不通过自动修正**：结构、溯源、禁用指标、篇幅、异常覆盖、引用真实、意见响应 7 类检查，按 `fixStep` 重跑对应步骤，次数用尽才转人工。
- **JSONL 事件日志 + 断点恢复**：每个任务 `events.jsonl` 追加写；重启后 `resumeAll()` 重放事件、只重跑未完成步骤；支持命令行回放。
- **轨迹与步骤行**：对话框顶部固定显示当前任务的执行过程（步骤、工具调用、模型轮次、质检、暂停与重做），折叠成一行摘要，运行中自动展开并跟随最新事件；对话里每个步骤也显示成一行（业务说法、状态、耗时、所用模型），点开定位到轨迹详情。两者都只读，不提供推进操作。
- **工作区就是目录**：工作区只是名字加一个本机目录，侧栏同时列出所有工作区和它们下面的会话；取消登记只删登记，目录里的文件、会话和任务都保留；设置、员工和知识库存在应用目录，所有工作区共用 —— 见 [ADR-0006](docs/adr/0006-workspace-is-a-folder.md)。
- **会话里选模型**：设置里能从 `{baseUrl}/models` 拉取模型再勾选；输入框右下角按会话选模型，新会话沿用上次选择，任务运行中切换从下一个判断步骤生效；轨迹和步骤行标出每步用的模型。
- **工具在进程内调用**：Go 主进程直接调用工具，不另起 stdio 子进程；内置故障注入（超时 / 报错 / 非法输出），可现场演示工具故障与重试。
- **权限在服务端校验**：发起人 / 审核人 / 操作员三种角色，越权操作被拒绝并留痕，状态机之外的流转一律拒绝。
- **单文件 exe**：把服务端、前端构建产物、员工配置和公开样例打进一个 exe；双击即用，首次启动建应用目录 `%APPDATA%\digital-employee-app`（`DE_APP_DIR` 可改：设置、员工、知识库、会话和任务都在这里），以及默认工作区「我的工作区」（`文档\数字员工`）；端口冲突自动换端口 —— 见[设计文档 §14](docs/design.md)、[ADR-0006](docs/adr/0006-workspace-is-a-folder.md)。
- **原生桌面窗口**：同一个 Go 主进程内嵌服务端，原生窗口（MyGo + 系统 WebView2）只加载本机地址；用 `digital-employee.exe --window` 打开，与浏览器方式共用应用目录 —— 见下文“方式二”与 [ADR-0004](docs/adr/0004-go-mainline.md)。
- **Go 后端 + React 前端**：后端 Go（`net/http` + SSE），前端 React/Vite + antd；契约类型集中在 `internal/contract`，前后端共用。

## 目录结构

```
digital-employee/
├── cmd/digital-employee/   Go 单 exe 入口：子命令分派与真实接线
├── internal/               Go 后端
│   ├── contract/           契约唯一事实来源（事件、状态、设置、配置）
│   ├── config/  engine/  qc/  agent/  tools/  render/   配置、引擎、质检、模型、工具、Word 渲染
│   ├── server/  session/    REST + SSE、角色权限、会话与前台 Agent
│   ├── app/  desktop/  cli/ 应用目录与工作区准备、原生窗口、命令行
├── packages/web/           React 前端（会话、步骤行与轨迹、发起、审核、配置、设置）
├── packages/{core,tools,server,cli,app,desktop}/  TypeScript 旧版（冻结在 feat/electrobun-shell，仅作参考与兜底）
├── employees/
│   ├── weekly-report/      场景 A 员工配置（提示词、输出 Schema、流程与质检配置）
│   └── knowledge-qa/       知识库问答员工（只有一个判断步骤）
├── samples/
│   ├── scenario-a/         样例数据（销售明细、周会纪要、格式要求）
│   └── cases/              A-01…A-05 与 S 系列命令行用例及其剧本式假模型输出
├── scripts/                构建与冒烟：build-go.ps1、smoke-go.ps1、gen-embedded.ts 等
├── docs/                   设计文档、术语表、ADR、工单、界面稿
└── README.md
```

## 快速开始

### 方式一：双击 exe（评审演示用，不需要安装开发环境）

1. 构建一次 exe（需要 Go 1.27.3+、Bun 1.4+）：`powershell -ExecutionPolicy Bypass -File scripts/build-go.ps1`（前端构建 → `go build`（`go:embed` 打进前端与资源）→ init/A-01 冒烟），产物是 `dist/digital-employee.exe`（约 27MB）。
2. 双击运行。首次启动在应用目录 `%APPDATA%\digital-employee-app\`（`DE_APP_DIR` 可覆盖）准备全局内容：释放默认员工配置到 `employees\`、公开样例到 `samples\`（**已存在的文件不覆盖**），写一份不含 Key 的 `settings.json`；工作区登记在 `workspaces.json`，所有工作区的会话与任务在 `sessions\`、`tasks\`。首次启动没有工作区时建“我的工作区”，目录是 `文档\数字员工`（只放「交付」和上传的「材料」）。然后自动打开浏览器到 `http://localhost:8787`。
3. 端口 8787 被占用时自动往后找空闲端口，控制台打印实际地址；不想自动开浏览器就设 `DE_NO_OPEN=1`。
4. 欢迎页点「载入公开样例」：用第 44 周样例（销售明细 + 周会纪要）一键建一份**草稿任务**，点「提交」即可看到发起 → 执行 → 质检 → 审核 → 交付的完整流程。没有 API Key 时用剧本式模型启动：

```powershell
$env:DE_SCRIPTED="$env:APPDATA\digital-employee-app\samples\cases\scripts\A-01.json"; .\dist\digital-employee.exe
```

5. 配真实模型：设置 → 模型连接，填 Base URL 与 API Key，点“测试连接”“拉取模型”，勾选模型并保存；然后在新会话输入框右下角选模型。

实测（Windows 11 x64）：exe 约 **27MB**；冷启动到接口可用约 **0.57 秒**（Go 版实测）；剧本模式跑完 A-01 到 delivered 约数秒。体积与冷启动的逐项对比见 [Go 迁移验收](docs/go-migration/architecture-and-parity.md)。

exe 也能当命令行用：`digital-employee.exe init`（只准备应用目录）、`run-case` / `run-cases` / `check` / `replay` / `fixtures`；`mcp-server` 是为兼容旧用法保留的占位（工具已在进程内直连）。

嵌进 exe 的资源：前端构建产物、`employees/`、`samples/scenario-a/` 与 `samples/cases/`。**不含 `samples/fixtures/`**（约 12MB 变异数据，只有 `fixtures` 命令和异常用例用得到，打进去不划算）。

### 方式二：原生窗口（`--window`）

同一个 exe 加 `--window` 打开原生窗口（MyGo + 系统 WebView2），后端仍是本机 HTTP 服务，窗口加载 `http://127.0.0.1:<port>`；数据与方式一完全一样（同一个应用目录、同一批工作区）。

```powershell
$env:DE_APP_DIR='D:\program4claude\competition\de-test\app'; .\dist\digital-employee.exe --window
```

- **平台要求**：Windows 11 x64 + 系统 WebView2（Windows 11 自带；缺失或 Windows 10 时先装 Microsoft Edge WebView2 Runtime），否则窗口起不来，可改用方式一。
- 关窗即退出：先停 HTTP 服务与工具客户端再退出，不留端口与后台进程。
- 桌面壳的取舍（为什么放弃 Electrobun）见 [ADR-0004](docs/adr/0004-go-mainline.md)；旧版桌面壳的构建方式保留在下一节，仅供比赛兜底。

### 旧版桌面壳（TypeScript，已冻结，不作为本轮验收依据）

这是 Spec 0003 的 **Electrobun + Hutch** 方案：Electrobun 主进程就是 Bun，工作区准备、Elysia 服务、任务引擎和 MCP 工具服务器都跑在同一个进程里，Electrobun 只套一个系统 WebView2 窗口（不额外带 Chromium）。

- **状态**：冻结在 `feat/electrobun-shell` 分支，只保留作比赛交付兜底；Go 主线不再同步新功能。它**不包含**本轮 Spec 0005/0006 的功能（会话与侧栏“工作区 | 记录”、会话模型、目录材料、交付物写目录、对话里的步骤行等），不要用它验收本轮改动。取舍理由见 [ADR-0004](docs/adr/0004-go-mainline.md)（下载体积更小、但装完更大，且要多维护一套运行时与构建工具链；主线选 Go 单 exe 加 `--window`）。
- **构建**（仅构建机，使用者不需要）：需要 Hutch，首次构建需要联网拉取 Electrobun / Bun 运行时：

```powershell
# 一次性安装 Hutch（PowerShell）
powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm https://hutch.blackboard.sh/hutch/install.ps1)))"
$env:HUTCH_HOME = "D:\hutch-home"   # 中文用户名/非 ASCII 用户目录必须设置，否则报 AccessDenied
$env:PATH = "$env:USERPROFILE\.hutch\bin;$env:PATH"
bun run build:desktop
powershell -ExecutionPolicy Bypass -File scripts/smoke-desktop.ps1
```

- **产物**：`dist/desktop/` 下的安装包 `win-x64-DigitalEmployee-Setup.zip`、便携包 `win-x64-DigitalEmployee-portable.zip` 与绿色目录 `DigitalEmployee/`。
- **实测**（Windows 11 x64，2026-11-05）：安装包 **51.1MB**、便携包 62.3MB、安装后 **142.5MB**；冷启动到 `/api/employees` 可用 **0.61–1.08 秒**；剧本模式 A-01 在桌面版后端跑到 delivered 约 **3.3 秒**。详见[桌面版结果文档](docs/results/electrobun-desktop.md)。

### 环境

> 以下小节是**源码运行**方式；评审演示直接用上面的 exe 即可。需要 Go 1.27.3+（`go version` 确认）与 Bun ≥ 1.4（构建/热更新前端用）。
- Windows 10/11。默认应用目录在 `%APPDATA%\digital-employee-app`（`DE_APP_DIR` 可改）；默认工作区「我的工作区」在 `文档\数字员工`，侧栏可添加其他本机目录。
- `DE_WORKSPACE` 是**单目录模式**：这个目录同时充当应用目录和唯一的工作区（命令行跑用例、评测用），布局与旧版一致；日常使用不要设它。
- 真实模型需要一个 DeepSeek 或 OpenAI 兼容接口的 API Key；只想看流程可以用下面的**离线剧本模式**，不需要 Key。

### 安装

```bash
bun install          # 前端依赖（构建前端产物用）
# Go 依赖在首次 go build / go test 时自动下载
```

### 配置模型

推荐在界面里配：设置 → **模型连接** → 新增或展开连接，填 Base URL 和 API Key，点“测试连接”和“拉取模型”，勾选后保存。也可以直接改应用目录里的 `settings.json`（缺省 `%APPDATA%\digital-employee-app`，不是仓库目录，也不是工作区目录）：

```json
{
  "connections": [
    { "name": "deepseek", "baseUrl": "https://api.deepseek.com", "apiKey": "sk-...", "models": ["deepseek-chat"] }
  ]
}
```

- 一个连接可以放多个模型；会话里用的模型在输入框右下角选，最近一次选择记在 `lastModel`，新会话沿用它。
- 旧设置文件里的 `defaultConnection` / `defaultModel` 会在读取时换算成 `lastModel`，不用手改。
- 环境变量 `DEEPSEEK_API_KEY` 优先于文件里的 Key。任何 OpenAI 兼容接口都能接（改 `baseUrl` 与模型名即可）。
- `settings.json` 是**明文存 Key** 的（界面只显示末 4 位）；不要提交到仓库或发给别人。

### 启动

源码形式启动（第一次先构建前端，`go:embed` 需要 `packages/web/dist`）：

```bash
bun run --filter @de/web build
go run ./cmd/digital-employee              # 起服务端并打开浏览器（默认 8787，被占用自动往后找）
go run ./cmd/digital-employee --serve-only # 只起服务端，不打开浏览器
go run ./cmd/digital-employee --window     # 打开原生窗口
```

前端热更新（API 代理到本机服务端）：

```bash
go run ./cmd/digital-employee --serve-only
VITE_API_TARGET=http://localhost:8787 bun run dev:web   # http://localhost:5173
```

端口可用 `DE_PORT` 或 `PORT` 覆盖；`DE_NO_OPEN=1` 不自动打开浏览器。

### 离线剧本模式（不调用真实模型）

```powershell
$env:DE_SCRIPTED="samples/cases/scripts/A-01.json"; go run ./cmd/digital-employee
```

```bash
# macOS / Linux / Git Bash
DE_SCRIPTED=samples/cases/scripts/A-01.json go run ./cmd/digital-employee
```

剧本文件按步骤给出模型回复（`{ "<stepId>": [{ toolCalls: [...] }] }`），样例在 `samples/cases/scripts/`。端到端测试也用同一种剧本。

### 打包 exe 与冒烟

```powershell
powershell -ExecutionPolicy Bypass -File scripts/build-go.ps1   # 前端构建 → go build → init/A-01 冒烟
powershell -ExecutionPolicy Bypass -File scripts/smoke-go.ps1   # 单 exe 冒烟（可加 -SkipBuild）
```

`scripts/smoke-go.ps1` 在**临时目录**（新的 `DE_WORKSPACE`，单目录模式）里真的运行 exe：先 `exe init` 释放资源，再用剧本模式把公开样例 A-01 跑到 delivered 并断言 docx 交付物存在，最后以服务端模式起 exe、从控制台解析实际地址，校验 `/api/employees`、`/api/tasks`、首页、前端脚本和 SSE，然后关掉进程。

## 常用命令

| 命令 | 说明 |
|---|---|
| `go test ./...` | 全部 Go 测试（引擎、工具、质检、渲染、服务端、会话、CLI；剧本模型，不联网、不需要 Key） |
| `bun test packages/web` | 前端纯函数与视图测试 |
| `bun run typecheck` | 各前端包 `tsc --noEmit` |
| `powershell -ExecutionPolicy Bypass -File scripts/build-go.ps1` | 构建单文件 exe：`dist/digital-employee.exe` |
| `powershell -ExecutionPolicy Bypass -File scripts/smoke-go.ps1` | 单 exe 冒烟：临时目录跑 A-01 + 起服务端校验接口、首页、前端脚本与 SSE |
| `go run ./cmd/digital-employee run-case samples/cases/A-01.json --scripted samples/cases/scripts/A-01.json --out out` | 按用例驱动引擎，结果写到 `out/`（`result.json`、`snapshot.json`、`events.jsonl`） |
| `go run ./cmd/digital-employee run-cases "samples/cases/*.json" --scripted --out out/cases` | 批量跑用例，30/30 为通过 |
| `go run ./cmd/digital-employee replay <应用目录> <taskId>` | 按时间回放某任务的事件 |
| `go run ./cmd/digital-employee fixtures` | 从样例销售明细生成变异数据到应用目录的 `samples/fixtures/`（`--out` 可改） |

用例文件格式见 [samples/cases/README.md](samples/cases/README.md)。

> 新工作区第一次跑 `go build` / `go test` 前，先 `bun install && bun run --filter @de/web build`：`go:embed` 需要 `packages/web/dist` 存在。

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
- 提交任务前先检查会话模型（所在连接有 Key、模型在连接的模型列表里），不通过就拦下并给设置入口，不会跑到一半才失败。
- 任务的执行过程在对话里以步骤行显示，顶部保留轨迹状态机；两者只读，只由流程卡片上的按钮推进。
- 完整流转表见[设计文档 §5](docs/design.md)。

## HTTP 接口

| 方法与路径 | 说明 |
|---|---|
| `GET /api/employees` | 员工列表（含发起表单 Schema、可退回章节） |
| `GET /api/preflight` | 提交前检查（会话模型是否可用） |
| `GET /api/tasks` | 任务摘要列表（损坏的日志会带 `error` 字段，不影响其他任务） |
| `POST /api/tasks` | 创建任务（multipart：`employee`、`form`、`attachments`） |
| `GET /api/tasks/:id` | 任务快照（步骤、预算、质检结果、交付物、会话模型） |
| `POST /api/tasks/:id/commands` | 命令：提交 / 补充 / 继续 / 重试 / 终止 / 通过 / 退回 |
| `GET /api/tasks/:id/events?after=<seq>` | 事件日志 |
| `GET /api/tasks/:id/stream?after=<seq>` | SSE 事件流（含心跳，重连用 `Last-Event-ID`） |
| `GET /api/tasks/:id/deliverables/:kind` | 下载交付物（`docx` / `md` / `qc_record`，可带版本） |
| `GET /api/workspaces` · `POST` · `PATCH /:id` · `DELETE /:id` | 工作区：列表（含会话摘要与“未归属”）、新建、重命名、取消登记 |
| `GET /api/records` | 记录：所有已开工的任务按状态分组，带工作区名 |
| `GET /api/sessions?workspaceId=<id>` · `POST /api/sessions` | 会话列表（可按工作区过滤）、新建（带 `workspaceId`） |
| `PATCH /api/sessions/:id` | 重命名 / 归档会话 |
| `PUT /api/sessions/:id/model` | 选会话模型（写会话日志并更新 `lastModel`） |
| `POST /api/sessions/:id/messages` | 发消息，立即返回 202；回复走会话事件流 |
| `GET /api/sessions/:id/stream` | 会话事件流（文字增量、工具调用、流程卡片） |
| `POST /api/sessions/:id/stop` | 停止当前一轮 |
| `POST /api/sessions/:id/attachments` | 上传材料 |
| `GET /api/sessions/:id/folder-files` · `POST` | 列出 / 确认使用工作区目录里的文件 |
| `POST /api/sessions/:id/submit` | 提交表单，开工 |
| `GET /api/settings/models` | 会话里可选的模型（只列有 Key 的连接） |
| `POST /api/settings/connections/:name/models` | 拉取 `{baseUrl}/models`（结果不写设置） |

附件只能通过 multipart 上传；命令 JSON 里自带 `attachments[].sourcePath` 会被拒绝（防止服务端读取任意本地文件）。完整路由见 `internal/server/server.go`，以代码为准。

## 员工配置

一个员工一个目录，全部行为写在配置里：

```
employees/weekly-report/
├── SKILL.md          岗位职责与写作要求（判断步骤的系统提示词）
├── frontdesk.md      前台 Agent 人设
├── checklist.yaml    开工清单（缺什么不能开工、哪些可跳过）
├── workflow.yaml     步骤列表、输入映射、预算、质检输入/修正步骤
├── qc.yaml           质检规则、禁用词、阈值、自定义规则
├── form.schema.json  发起表单（前端用 JSON Schema 渲染）
├── form.ui.json      表单布局
├── prompts/*.md      各判断步骤的提示词
└── schemas/*.json    各判断步骤的输出 Schema
```

首次启动会把默认员工配置复制到应用目录的 `employees/`（已有文件不覆盖）；运行中的任务使用发起时的配置副本。

`workflow.yaml` 里两个可选映射值得知道：

- `qcSources`：质检从哪些步骤取草稿、纪要、渲染结果（缺省 `write_report` / `parse_minutes` / `render_document`）。
- `qcFixSteps`：质检不通过时重跑哪一步（`write` 缺省跟随 `qcSources.draft`，`causes` 缺省 `match_causes`）。解析出的 id 必须真实存在，否则加载配置时直接报错。

契约（共享类型与校验）集中在 [`internal/contract`](internal/contract)，是唯一事实来源；其余包只实现契约。

## 设置中心

侧栏左上角的设置入口里分七节：**模型连接、外观与交互、执行与预算、演示与调试、权限与审批、诊断与关于、工作区**。

- **设置文档**：全部设置存在**应用目录**的 `settings.json`（`version: 2`；应用目录缺省 `%APPDATA%\digital-employee-app`，可用 `DE_APP_DIR` 改），每次写入 `revision` +1；旧版文件读时自动补默认值，第一次写入时迁移为 v2，未知字段原样保留；旧文档里的 `defaultConnection` / `defaultModel` 会换算成 `lastModel`。两个窗口同时保存时会返回冲突提示，不会静默覆盖。
- **模型连接**：可配多个连接（名称 / Base URL / API Key / 模型列表），每个模型还能单独设上下文窗口、输出上限与推理档位（关闭 / 最低 / 低 / 中 / 高 / 最高），连接级可设超时与重试次数；「测试连接」发一条极短请求验证 Key 与模型是否可用；「拉取模型」请求 `{baseUrl}/models`，勾选后再保存，拉取结果本身不写设置。
- **会话模型**：输入框右下角按会话选模型，显示“连接 / 模型”；切换后对话里有一条说明；最近一次选择记进设置 `lastModel`，新会话沿用它。任务运行中切换会写一条“模型已切换”任务事件，从下一个判断步骤生效，正在进行的调用不中断。
- **外观与交互**：主题（浅色 / 深色 / 跟随系统）、字号、轨迹详细度（简洁 / 标准 / 详细）、轨迹折叠时机、回车行为、链接打开方式；保存后立即生效。
- **执行与预算**：`stepTimeoutMs`、`modelMaxRetries`、`retryBackoffMs`、`qcAutofix` 是**默认值**，优先级为「步骤或连接配置 → 设置 → 内置值」（内置分别为 120 秒 / 2 次 / `[500,1500,4000]` 毫秒 / 3 次）。员工配置里显式写了就以员工配置为准。
- **演示与调试**：工具故障注入、数据溯源故障、模拟崩溃（一次性开关，命中即清）。
- **权限与审批**：只读 / 工作区可写 / 完全访问三档预设、审批策略（每次询问 / 从不询问）、默认角色；危险操作会二次确认并写审计。**本机模式：预设只影响审批确认与审计留痕，不提供系统级文件沙箱，也不改变操作系统文件权限。**
- **诊断与关于**：应用版本、运行时（Go / 平台 / 架构）、应用目录（界面显示为“工作区路径”）、日志级别（error / warn / info / debug）、复制诊断信息、导出诊断包（脱敏）、用本机编辑器打开 `settings.json`。
- **工作区**（设置页里的这一节）：查看当前应用目录与工作区目录、在资源管理器中打开工作区、恢复默认员工配置。工作区本身的新增、重命名、取消登记在左侧栏。
- **导出 / 导入**：默认导出脱敏（只保留 Key 末 4 位），勾选「包含密钥」需二次确认且文件名追加 `-with-keys`；导入会先校验并备份当前设置到 `config-versions/settings/<时间戳>.json`。
- **审计**：恢复默认员工配置、导入设置、导出含密钥设置、切到完全访问 / 从不询问、模拟崩溃等危险操作都会追加写入 `<应用目录>/audit.jsonl`（一行一条 JSON，含时间、角色、操作、目标、结果）。

## 架构

| 包 | 职责 |
|---|---|
| `internal/contract` | 契约唯一事实来源：事件、任务状态、设置、员工配置等共享类型与校验 |
| `internal/config` | 员工配置加载（`workflow.yaml` / `qc.yaml` / 表单 Schema / 提示词） |
| `internal/engine` | 任务引擎：状态机、编排、事件日志、断点恢复、交付物 |
| `internal/qc` | 质检引擎、槽位渲染 |
| `internal/agent` | 判断步骤执行器：真实 OpenAI 兼容模型 + 剧本式模型 |
| `internal/tools` | 数据类工具注册表与故障注入（进程内调用） |
| `internal/render` | 纯 Go 生成 Word 与图表 PNG |
| `internal/server` | REST + SSE、角色权限、设置、样例、员工配置接口 |
| `internal/session` | 会话、前台 Agent 循环、目录材料 |
| `internal/app` / `internal/desktop` | 应用目录与工作区准备、启动、端口；原生窗口 |
| `internal/cli` / `cmd/digital-employee` | 命令行子命令与程序入口 |
| `packages/web` | React 前端：会话与步骤行轨迹 / 发起 / 审核 / 设置 / 配置 / 知识库 |

- 数据流：表单与材料 → 准入工具校验 → 计算步骤（工具）与判断步骤（模型 + 受限工具）→ 质检 → 自动修正或转人工 → 人工审核 → 交付物。
- 每个任务目录下保存事件日志、附件、配置快照、产物与交付物；重启后只重放日志，不重新调用模型。
- 工具调用失败先按配置重试，再暂停转人工，不编造结果；写文件类工具用「任务 + 步骤 + 输入哈希」做幂等。
- 整个进程只有一个引擎和一个会话模块：数据都按“应用目录 + 工作区 ID”定位，不按工作区目录重建。

## 测试与样例

- `go test ./...` 覆盖契约、配置、引擎、质检、模型、工具、渲染、服务端、会话与 CLI；端到端用例用剧本式模型，不联网、不需要 Key。
- `bun test packages/web` 覆盖前端纯函数与视图（步骤行推导、侧栏分组、模型下拉等）；`bun run typecheck` 检查类型。
- `samples/cases/` 是 30 条自测用例（6 条公开 A 系列 + 24 条 S 系列）的描述；`samples/cases/scripts/` 是对应的剧本式假模型输出。剧本模式 30/30 通过，A 系列另做过真实模型验证，结果见 [`docs/results/`](docs/results/)。
- `samples/scenario-a/` 是原始数据：销售明细、W42–W44 周会纪要、格式要求、数据说明。公开样例会释放到**应用目录**的 `samples/`（不在工作区目录里）。

## 文档

- [设计文档](docs/design.md)：范围、技术栈、架构、状态机、质检、场景 A/B、界面、打包
- [术语表](GLOSSARY.md)：任务、角色、步骤、事实等术语的准确含义
- [ADR-0001 程序编排步骤](docs/adr/0001-program-orchestrated-steps.md) / [ADR-0002 事实与槽位](docs/adr/0002-facts-via-slots.md) / [ADR-0004 Go 主线](docs/adr/0004-go-mainline.md) / [ADR-0005 知识库是背景参考](docs/adr/0005-knowledge-base-is-background.md) / [ADR-0006 工作区即目录](docs/adr/0006-workspace-is-a-folder.md)
- [规格 0006 工作区、会话模型、前台 Agent 循环](docs/specs/0006-workspace-folder-and-agent-loop.md)、[真实模型手动测试指南](docs/manual-test-0006.md)
- [Go 迁移验收（架构、对拍、体积）](docs/go-migration/architecture-and-parity.md)、[Go 版使用说明](docs/go-migration/README.md)
- [规格 0001](docs/specs/0001-skeleton.md)、[规格 0003 桌面壳](docs/specs/0003-electrobun-desktop-shell.md) 与 [tickets](docs/specs/tickets)
- [用例文件格式](samples/cases/README.md)

## 现状

- 场景 A（经营周报）已端到端跑通：契约、工具、引擎、服务端/CLI、前端、质检与集成测试均已实现；Go 成为主线（[ADR-0004](docs/adr/0004-go-mainline.md)）。
- 第二个员工「知识库问答员工」已落地（T34）：只有一个判断步骤，查知识库索引、按需读正文；已交付文件经人确认才入库（[ADR-0005](docs/adr/0005-knowledge-base-is-background.md)）。
- Spec 0006 已落地（T36–T43）：工作区即目录（应用目录 + 工作区登记 + 旧数据一次性迁移 + 单目录模式）、设置里拉取模型、侧栏“工作区 | 记录”、会话模型与运行中切换、前台 Agent 流式循环、目录材料、交付物写入工作区、对话里的步骤行。
- 真实模型验证：A-01 至 A-05 各 3 次、共 444 项判定要点全部通过；自测用例集 30 条在剧本模式 30/30 通过，S 系列另做了真实模型抽跑。结果见 [`docs/results/`](docs/results/)。
- 场景 B（技术文献研判）的设计见[设计文档 §11](docs/design.md)，员工配置尚未落地。
- 单文件 exe：`scripts/build-go.ps1` → `dist/digital-employee.exe`（约 27MB），首次启动准备应用目录，欢迎页可一键载入公开样例；`scripts/smoke-go.ps1` 覆盖“exe 跑通 A-01 + 起服务端提供界面与 SSE”。
- 原生窗口：同一个 exe 加 `--window` 用 MyGo 打开（系统 WebView2），与浏览器方式共用应用目录。
- 已知限制：
  - 字体未嵌入。ticket 里的“思源黑体子集（OFL）”需要仓库里不存在、也没找到可合法分发来源的字体文件，因此**跳过字体嵌入**并保留回退：图表先用 `DE_FONT_PATHS` 指定的字体，否则用系统中文字体（Windows 上是微软雅黑 `msyh.ttc`）；Word 正文字体名固定为 `Microsoft YaHei`（`noto` 等缺失时由阅读端回退），不嵌入字体二进制。实测 Windows 11 上图表与 Word 中文显示正常；在完全没有中文字体的机器上图表中文会缺字，这是已知风险。
  - `samples/fixtures/`（约 12MB）不进 exe；需要变异数据时跑 `digital-employee fixtures`（输出到应用目录的 `samples/fixtures/`）或源码 `go run ./cmd/digital-employee fixtures`；8 条依赖夹具的 S 用例要先这样生成夹具才能跑。
  - **应用目录里的员工配置与样例不会自动覆盖**（保护用户改动）。升级 exe 后如需最新的默认配置/样例，请先备份并删除 `%APPDATA%\digital-employee-app` 下的 `employees/`、`samples/`（或改用新的 `DE_APP_DIR`），否则会继续用旧副本。
  - 旧数据迁移只做一次（标记文件 `<应用目录>/migrated.json`，可重复运行、结果不变）：原默认工作区 `%APPDATA%\digital-employee` 的设置、员工、知识库，以及原工作区的任务和会话会复制进应用目录并归到“我的工作区”；**原目录只读不删**。
  - 单目录模式（`DE_WORKSPACE` 或命令行用例）布局和旧版一致：这个目录同时是应用目录和唯一工作区，不读写 `workspaces.json`。
  - 真实供应商的流式增量（文字增量、工具调用参数分片、`[DONE]`）只用本地假模型服务覆盖过，没有对所有 OpenAI 兼容服务逐一验证；接新供应商时先按手动测试指南跑一遍。
  - exe 未签名，首次运行可能被 SmartScreen 拦一次（点“更多信息 → 仍要运行”）。
  - `--window` 只在 Windows 11 x64 + 系统 WebView2 上验证过；Windows 10 需要先装 WebView2 Runtime，否则用浏览器方式。
