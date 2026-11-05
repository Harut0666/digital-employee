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
- **单文件 exe**：`bun build --compile` 把服务端、前端构建产物、员工配置、公开样例和 resvg wasm 打进一个 exe；双击即用，首次启动自动建 `%APPDATA%\digital-employee` 工作区，端口冲突自动换端口 —— 见[设计文档 §14](docs/design.md)。
- **原生桌面窗口**：同一个 Bun 主进程内嵌 Elysia，Electrobun 只套一个系统 WebView2 窗口（不额外带 Chromium），打出 51.1MB 安装包与免安装便携包；与 exe 共用工作区 —— 见[方式二](#方式二桌面窗口electrobun)与 [ADR-0003](docs/adr/0003-electrobun-desktop-shell.md)。
- **全栈 TypeScript**：Bun + Elysia + React/Vite + antd；前后端共享契约类型。

## 目录结构

```
digital-employee/
├── packages/
│   ├── core/      状态机、编排器、事件日志、质检引擎、槽位渲染（不依赖外壳）
│   ├── tools/     MCP 工具服务器（场景 A 的工具、故障注入）
│   ├── server/    Elysia：REST 接口、SSE 事件流、角色权限
│   ├── web/       React 前端（任务对话、轨迹、发起表单、审核、设置）
│   ├── cli/       命令行：跑用例、回放事件
│   ├── app/       打包入口：exe 子命令分派、工作区释放、端口选择（design §14）
│   └── desktop/   Electrobun 桌面壳：主进程入口、窗口后端装配、资源路径（design §14）
├── employees/
│   └── weekly-report/   场景 A 员工配置（提示词、输出 Schema、流程与质检配置）
├── samples/
│   ├── scenario-a/      样例数据（销售明细、周会纪要、格式要求）
│   └── cases/           A-01…A-05 命令行用例及其剧本式假模型输出
├── scripts/       构建与冒烟：gen-embedded.ts、build-exe.ts、smoke-exe.ps1、build-desktop.ts、smoke-desktop.ps1
├── docs/          设计文档、术语表、ADR、ticket、界面稿
└── README.md
```

## 快速开始

### 方式一：双击 exe（评审演示用，不需要安装开发环境）

1. 构建一次 exe：`bun run build:exe`（先构建前端 → 生成嵌入归档 → `bun build --compile`），产物是 `dist/digital-employee.exe`。
2. 双击运行。首次启动在 `%APPDATA%\digital-employee\` 建立工作区：释放默认员工配置到 `employees\`、公开样例到 `samples\`（**已存在的文件不覆盖**），写一份不含 Key 的 `settings.json`，然后自动打开浏览器到 `http://localhost:8787`。
3. 端口 8787 被占用时自动往后找空闲端口，控制台打印实际地址；不想自动开浏览器就设 `DE_NO_OPEN=1`。
4. 欢迎页点「载入公开样例」：用释放出来的第 44 周样例（销售明细 + 周会纪要）一键建一份**草稿任务**，点「提交任务」即可看到发起 → 执行 → 质检 → 审核 → 交付的完整流程。没有 API Key 时用剧本式模型启动：

```powershell
$env:DE_SCRIPTED="$env:APPDATA\digital-employee\samples\cases\scripts\A-01.json"; .\dist\digital-employee.exe
```

实测（Windows 11、Bun 1.4.2）：exe **105.8MB**（含 24 条 S 系列用例后）；冷启动到接口可用约 **1.0 秒**（3 次测量 0.83–1.10s）；剧本模式跑完 A-01 到 delivered 约 3.4 秒。

exe 也能当命令行用：`digital-employee.exe init`（只准备工作区）、`mcp-server`（工具服务器），以及和 `de` 一样的 `run-case` / `run-cases` / `check` / `replay`。

嵌进 exe 的资源：前端构建产物、`employees/`、`samples/scenario-a/` 与 `samples/cases/`。**不含 `samples/fixtures/`**（约 12MB 变异数据，只有 `de fixtures` 和异常用例用得到，打进去不划算）。

### 方式二：桌面窗口（Electrobun）

同一套后端与 React 界面装进原生窗口（Electrobun + 系统 WebView2），不打开浏览器；适合现场演示“可执行程序自己的窗口”。

**平台要求**：Windows 11 x64 + 系统 WebView2（Windows 11 自带；缺失时装 Microsoft Edge WebView2 Evergreen Runtime）。Windows 10 不在 Electrobun 官方支持列表，请用方式一。

**安装与运行**

1. 构建一次：`bun run build:desktop` → `dist/desktop/` 下得到安装包 `win-x64-DigitalEmployee-Setup.zip`、便携包 `win-x64-DigitalEmployee-portable.zip`，以及解压即可运行的绿色目录 `DigitalEmployee/`。
2. 安装版：解压 `win-x64-DigitalEmployee-Setup.zip`，运行其中的 `Digital Employee-Setup.exe` 按提示安装；免安装：解压便携包，双击 `DigitalEmployee\bin\launcher.exe`。
3. 首次启动不需要命令行：自动建立/沿用 `%APPDATA%\digital-employee` 工作区（与 exe 共用，`DE_WORKSPACE` 可覆盖），释放员工配置与公开样例，写一份不含 Key 的 `settings.json`；窗口标题为“迷你数字员工”。
4. 没有 API Key 时用剧本模式演示：

```powershell
$env:DE_SCRIPTED="$env:APPDATA\digital-employee\samples\cases\scripts\A-01.json"
# 再双击应用（安装版从开始菜单，便携版运行 DigitalEmployee\bin\launcher.exe）
```

**菜单与单实例**（Windows 快捷键用 Control；实测状态见[桌面版结果](docs/results/electrobun-desktop.md)）

- 菜单提供“打开工作区文件夹”“重新加载”“开发者工具（仅开发构建）”“关于（应用版本 + Electrobun / Bun 运行时版本）”“退出”。
- 关闭窗口即退出：先关工具客户端与 HTTP 服务再退出，不留端口与后台进程。
- 同一工作区重复启动会提示“已经有一个窗口在运行”，不会起第二份服务、不会重复执行任务；exe 不检查这个锁，所以**不要和 exe 同时打开**。

**构建要求**：需要 Hutch（仅构建机使用，使用者不需要），首次构建需要联网拉取 Electrobun / Bun 运行时：

```powershell
# 一次性安装 Hutch（PowerShell）
powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm https://hutch.blackboard.sh/hutch/install.ps1)))"
$env:HUTCH_HOME = "D:\hutch-home"   # 中文用户名/非 ASCII 用户目录必须设置，否则报 AccessDenied
$env:PATH = "$env:USERPROFILE\.hutch\bin;$env:PATH"
bun run build:desktop
powershell -ExecutionPolicy Bypass -File scripts/smoke-desktop.ps1
```

`bun run dev:desktop` 用 Hutch 的 dev 模式起窗口并在改主进程后自动重建。

**实测**（Windows 11 x64，2026-11-05）：安装包 **51.1MB**、便携包 62.3MB、安装后 **142.5MB**（exe 为 105.8MB）；冷启动到 `/api/employees` 可用 **0.61–1.08 秒**（多点测量）；剧本模式 A-01 在桌面版后端跑到 delivered 约 **3.3 秒**。详见[桌面版结果文档](docs/results/electrobun-desktop.md)。

**与 exe 的关系**：两套产物共用 `%APPDATA%\digital-employee` 工作区（设置、Key、任务、样例通用），但**不要同时运行**——两个进程会同时写任务目录与设置。桌面版不提供 `init` / `mcp-server` / `run-cases` 等命令行子命令，这些继续用 exe；桌面化出问题随时退回 exe。

### 环境

> 以下小节是**源码运行**方式；评审演示直接用上面的 exe 即可。需要 [Bun](https://bun.sh) ≥ 1.4（`bun --version` 确认）。
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

### 打包 exe 与冒烟

```bash
bun run build:exe    # 前端构建 → 生成 packages/app/assets/*.bundle → dist/digital-employee.exe
powershell -ExecutionPolicy Bypass -File scripts/smoke-exe.ps1
```

`scripts/smoke-exe.ps1` 在**临时工作区**（新的 `DE_WORKSPACE`）里真的运行 exe：先 `exe init` 释放资源，再用剧本模式把公开样例 A-01 跑到 delivered 并断言 docx 交付物存在，最后以服务端模式起 exe、从控制台解析实际地址，校验 `/api/employees`、`/api/samples`、首页与前端脚本都可访问，然后关掉进程。

## 常用命令

| 命令 | 说明 |
|---|---|
| `bun test` | 全仓库测试（含端到端；剧本式模型，不联网、不需要 Key） |
| `bun run typecheck` | 6 个包分别 `tsc --noEmit` |
| `bun run build:exe` | 构建单文件 exe：`dist/digital-employee.exe` |
| `bun run build:desktop` | 构建桌面版：`dist/desktop/` 下的安装包、便携 zip 与绿色目录（需要 Hutch） |
| `bun run dev:desktop` | 用 Hutch dev 模式起桌面窗口，改主进程后自动重建 |
| `powershell -ExecutionPolicy Bypass -File scripts/smoke-exe.ps1` | exe 冒烟：临时工作区跑 A-01 + 起服务端校验接口与界面 |
| `powershell -ExecutionPolicy Bypass -File scripts/smoke-desktop.ps1` | 桌面版冒烟：临时工作区启动应用，校验接口/首页，HTTP 跑 A-01 到 delivered 并断言 docx |
| `bun packages/cli/src/main.ts run-case samples/cases/A-01.json --scripted samples/cases/scripts/A-01.json --out out` | 按用例驱动引擎，结果写到 `out/`（`result.json`、`snapshot.json`、`events.jsonl`） |
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

## 设置中心

侧栏「设置」页分七节：**模型连接、外观与交互、执行与预算、演示与调试、权限与审批、诊断与关于、工作区**。

- **设置文档**：全部设置存在工作区的 `settings.json`（`version: 2`），每次写入 `revision` +1；旧版文件读时自动补默认值，第一次写入时迁移为 v2，未知字段原样保留。两个窗口同时保存时会返回冲突提示，不会静默覆盖。
- **模型连接**：可配多个连接（名称 / Base URL / API Key / 模型列表），每个模型还能单独设上下文窗口、输出上限与推理档位（关闭 / 最低 / 低 / 中 / 高 / 最高），连接级可设超时与重试次数；「测试连接」发一条极短请求验证 Key 与模型是否可用。
- **外观与交互**：主题（浅色 / 深色 / 跟随系统）、字号、轨迹详细度（简洁 / 标准 / 详细）、轨迹折叠时机、回车行为、链接打开方式；保存后立即生效。
- **执行与预算**：`stepTimeoutMs`、`modelMaxRetries`、`retryBackoffMs`、`qcAutofix` 是**默认值**，优先级为「步骤或连接配置 → 设置 → 内置值」（内置分别为 120 秒 / 2 次 / `[500,1500,4000]` 毫秒 / 3 次）。员工配置里显式写了就以员工配置为准。
- **演示与调试**：工具故障注入、数据溯源故障、模拟崩溃（一次性开关，命中即清）。
- **权限与审批**：只读 / 工作区可写 / 完全访问三档预设、审批策略（每次询问 / 从不询问）、默认角色；危险操作会二次确认并写审计。**本机模式：预设只影响审批确认与审计留痕，不提供系统级文件沙箱，也不改变操作系统文件权限。**
- **诊断与关于**：应用版本、运行时（Bun / 平台 / 架构）、工作区路径、日志级别（error / warn / info / debug）、复制诊断信息、导出诊断包（脱敏）、用本机编辑器打开 `settings.json`。
- **导出 / 导入**：默认导出脱敏（只保留 Key 末 4 位），勾选「包含密钥」需二次确认且文件名追加 `-with-keys`；导入会先校验并备份当前设置到 `config-versions/settings/<时间戳>.json`。
- **审计**：恢复默认员工配置、导入设置、导出含密钥设置、切到完全访问 / 从不询问、模拟崩溃等危险操作都会追加写入 `<workspace>/audit.jsonl`（一行一条 JSON，含时间、角色、操作、目标、结果）。

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
- [ADR-0001 程序编排步骤](docs/adr/0001-program-orchestrated-steps.md) / [ADR-0002 事实与槽位](docs/adr/0002-facts-via-slots.md) / [ADR-0003 Electrobun 桌面壳](docs/adr/0003-electrobun-desktop-shell.md)
- [桌面版结果（体积、冷启动、A-01、人工目视清单）](docs/results/electrobun-desktop.md)、[Electrobun spike](docs/results/electrobun-spike.md)
- [规格 0001](docs/specs/0001-skeleton.md)、[规格 0003 桌面壳](docs/specs/0003-electrobun-desktop-shell.md) 与 [tickets T1–T7](docs/specs/tickets)
- [用例文件格式](samples/cases/README.md)

## 现状

- 场景 A（经营周报）已端到端跑通：契约、工具、引擎、服务端/CLI、前端、质检与集成测试均已实现。
- 真实模型验证：A-01 至 A-05 各 3 次、共 444 项判定要点全部通过；自测用例集 30 条（6 条公开样例 + 24 条 S 系列）在剧本模式 30/30 通过，S 系列另做了真实模型抽跑。结果见 [`docs/results/`](docs/results/)。
- 场景 B（技术文献研判）的设计见[设计文档 §11](docs/design.md)，员工配置尚未落地。
- 单文件 exe 已落地（设计 §14）：`bun run build:exe` → `dist/digital-employee.exe`（约 105.8MB），首次启动释放工作区，欢迎页可一键载入公开样例；`scripts/smoke-exe.ps1` 覆盖“exe 跑通 A-01 + 起服务端提供界面”。
- 桌面窗口（Electrobun）已落地（设计 §14、[ADR-0003](docs/adr/0003-electrobun-desktop-shell.md)）：`bun run build:desktop` → `dist/desktop/` 下安装包（51.1MB）、便携 zip（62.3MB）与绿色目录（安装后 142.5MB）；冷启动 0.61–1.08 秒，剧本模式 A-01 约 3.3 秒；`scripts/smoke-desktop.ps1` 覆盖“启动 → 接口/首页 → HTTP 跑 A-01 到 delivered 并断言 docx”。体积与人工目视清单见[桌面版结果](docs/results/electrobun-desktop.md)。
- 已知限制：
  - 字体未嵌入。ticket 里的“思源黑体子集（OFL）”需要仓库里不存在、也没找到可合法分发来源的字体文件，因此**跳过字体嵌入**并保留回退：图表先用 `DE_FONT_PATHS` 指定的字体，否则用系统中文字体（Windows 上是微软雅黑 `msyh.ttc`）；Word 正文字体名固定为 `Microsoft YaHei`（`noto` 等缺失时由阅读端回退），不嵌入字体二进制。实测 Windows 11 上图表与 Word 中文显示正常；在完全没有中文字体的机器上图表中文会缺字，这是已知风险。
  - `samples/fixtures/`（约 12MB）不进 exe；需要变异数据时跑 `digital-employee.exe fixtures`（源数据已嵌入，输出到工作区 `samples/fixtures/`，约 31 秒）或源码 `de fixtures`；8 条依赖夹具的 S 用例要先这样生成夹具才能跑。
  - **工作区里的员工配置与样例不会自动覆盖**（保护用户改动）。升级 exe 后如需最新的默认配置/样例，请先删除或改名 `%APPDATA%\digital-employee`（或改用新的 `DE_WORKSPACE`），否则会继续用旧副本；源码模式同理，跑命令行用例时建议显式设置 `DE_WORKSPACE=<新的空目录>`。
  - 源码模式（`bun run dev:server`）不会释放工作区样例：欢迎页「载入公开样例」只在 exe 首次启动（或工作区里已有 `samples/`）时可用；源码演示请直接用 exe，或把 `samples/` 复制进工作区。
  - exe 与桌面版安装包都未签名，首次运行可能被 SmartScreen 拦一次（点“更多信息 → 仍要运行”）。
  - 桌面版只支持 **Windows 11 x64 + 系统 WebView2**；Windows 10 不在 Electrobun 官方支持列表，评审机不满足时用 exe。
  - 桌面版**不承诺命令行子命令**（`init` / `mcp-server` / `run-cases` / `fixtures` / `replay` 继续用 exe）；桌面版与 exe 共用工作区，但**不要同时运行**。
  - 桌面版构建需要 **Hutch**（仅构建机，使用者不需要）；中文用户名下必须把 `HUTCH_HOME` 指到纯 ASCII 路径。安装后约 **142MB**（本次实测；比 exe 的 105.8MB 大，下载体积则更小）。
