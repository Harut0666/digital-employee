# Go 迁移（MyGo）· 工程约定

目标：用 Go 重写后端，外壳换成 MyGo；**HTTP + SSE 契约不变，React 前端零改动**，
CLI 与自检用例保留。TypeScript 版本在 `feat/electrobun-shell` 上原样保留，作为比赛交付兜底。

本工作树：`digital-employee/.worktrees/go`，分支 `feat/go-migration`。

## 目录与归属

```
go.mod                        # module digital-employee
internal/contract/            # 【Lead】从 packages/core/src/contract 逐字段移植，唯一事实来源
internal/config/              # 【engine】员工配置加载（workflow.yaml/qc.yaml/form.schema.json/prompts）
internal/engine/              # 【engine】状态机、编排、事件日志、断点恢复、任务目录
internal/qc/                  # 【engine】8 类内置质检、自定义规则、槽位渲染
internal/agent/               # 【engine】判断步骤执行器：真实模型 + 剧本模式
internal/tools/               # 【tools-data】validate_submission / load_sales / compute_metrics /
                              #   parse_minutes / detect_contradictions + 工具注册表
internal/render/              # 【doc】render_report_docx：docx 生成 + 图表 PNG
internal/server/              # 【server】net/http REST + SSE + 角色权限 + settings/config/samples
internal/app/                 # 【server】工作区准备、资源释放、端口选择
internal/cli/                 # 【server】run-case / run-cases / check / fixtures / replay
internal/desktop/             # 【Lead】MyGo 窗口（最后做，先让 HTTP 版跑通）
cmd/digital-employee/main.go  # 【server】子命令分派
packages/web/                 # 前端：**不改**（除数据层适配，若需要）
```

写作用域之外的包不要动；跨包契约变更先改 `internal/contract` 并通知 Lead。

## 契约规则（最容易出错的地方）

1. **JSON 字段名必须与 TS 完全一致**（camelCase）。前端、事件日志、settings.json 都按名字读写。
2. 契约里的 `number | null` 用 `*int64 / *float64`；`string | null` 用 `*string`。
3. 布尔/整数用 `omitempty` 会吞掉 `false` / `0` 这两种有含义的值（`reworkRound: 0` 是首轮），
   这类字段在 `TaskEvent` 里用指针。
4. 事件类型字符串、状态码、错误码、工具名一律用 `internal/contract` 里的常量，不要手写字面量。
5. 工作区里已有的数据（`%APPDATA%\digital-employee`）必须能被 Go 版直接读：事件日志、settings.json、
   员工配置目录都不做格式变更。

## 验收线（Parity Gate）

第一条（必须最先跑通，端到端、不联网）：

```bash
go run ./cmd/digital-employee run-case samples/cases/A-01.json \
  --scripted samples/cases/scripts/A-01.json --out out
# 断言：out/result.json 的 state=delivered，out 下有 .docx，事件日志 seq 连续
```

第二条：`go test ./...` 全绿（工具/质检/引擎的单测从 TS 同名测试移植关键用例）。

第三条：HTTP 冒烟——`go run ./cmd/digital-employee --serve-only` 后
`GET /api/employees`、`GET /api/tasks`、`GET /` 与 SSE 均可用（脚本沿用 `scripts/smoke-exe.ps1` 的断言）。

第四条（最后）：`mygo build` 出 Windows 窗口版，加载内嵌前端 dist，工作区与 HTTP 版共用。

## 常用命令

```bash
go build ./...
go test ./...
go vet ./...
go run ./cmd/digital-employee run-case samples/cases/A-01.json --scripted samples/cases/scripts/A-01.json --out out
```

环境注意（本机）：
- Go 1.27.3+（MyGo 要求；`GOTOOLCHAIN=auto` 会自动拉，但需要 `GOSUMDB=sum.golang.org`，
  本机默认 `GOSUMDB=off` 会失败）。
- Scoop 的 GOPATH 可能不可写；必要时 `GOPATH=... GOMODCACHE=...` 指到工作区内。
- 前端构建仍用 Bun：`bun run --filter @de/web build`。

## 不做的事

- 不改前端业务代码与视觉；
- 不删除 TS 包（作为参照与兜底），直到 Go 版通过全部验收；
- 不引入第二个事件模型：事件仍然是 JSONL + SSE，Go 侧只是生产者。
