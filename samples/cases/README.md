# 命令行用例文件格式

`de run-case <case.json> [--scripted <script.json>] [--out <dir>]` 按用例里的命令序列驱动任务引擎，每执行一条命令就等任务停在暂停态或终态，最后把结果写到输出目录。

本目录给出场景 A 公开样例 A-01 至 A-05 的用例文件。用例只描述“发起什么、按什么顺序发什么命令、期望停在哪”，不含模型输出；需要模型的判断步骤由 `--scripted` 指定的剧本式假模型（T7 放在 `scripts/`）或真实模型完成。

## 文件结构

```jsonc
{
  "id": "A-03",                         // 必填，也是默认输出目录名 out/<id>
  "title": "缺材料：先缺销售明细，补传后继续", // 可选，默认等于 id
  "employee": "weekly-report",          // 必填，员工目录名
  "form": { "period": { "year": 2026, "week": 44 } }, // 可选，默认 {}；字段以员工的 form.schema.json 为准
  "attachments": [                      // 可选，发起时上传的附件
    { "name": "周会纪要_2026W44.docx", "path": "../scenario-a/周会纪要_2026W44.docx" }
  ],
  "steps": [                            // 必填，非空，按顺序执行
    {
      "actor": "initiator",             // initiator | reviewer | operator
      "command": { "type": "submit" },  // 即 TaskCommand
      "expect": { "state": "awaiting_initiator", "pauseCode": "missing_sales" } // 可选
    }
  ],
  "expect": { "state": "delivered" }    // 可选，全部步骤跑完后的期望
}
```

- **路径**：`attachments[].path` 与 supplement 命令里的 `attachments[].path` 都相对**用例文件所在目录**解析，`name` 是上传后的文件名。
- **创建任务**：固定以 `initiator` 角色建草稿任务；之后才是 `steps`。
- **命令**：`command` 就是契约里的 `TaskCommand`：
  - `submit`、`continue_without_minutes`、`continue_with_partial`、`retry`、`terminate`、`approve`；
  - `{ "type": "supplement", "form": { ... }, "attachments": [{ "name", "path" }] }`，`form` 和 `attachments` 可选；
  - `{ "type": "return", "comment": "必填", "sections": ["可选章节 id"] }`。

## 期望（`expect`）

步骤级和顶层 `expect` 都可省略任何字段，写了才检查。

| 字段 | 含义 |
|---|---|
| `state` | 任务状态，如 `awaiting_review`、`delivered`（仅步骤级与顶层） |
| `pauseCode` | 暂停原因编码，如 `missing_sales`、`period_out_of_range`、`awaiting_review` |
| `deliverables` | 快照里必须存在的交付物种类数组，如 `["docx", "qc_record"]` |
| `ok` | 仅步骤级：命令是否应被接受，缺省 `true`。设为 `false` 可用来验证越权、非法流转被拒绝 |
| `error` | 仅步骤级且 `ok: false`：期望的错误码，`forbidden_role` / `illegal_transition` / `invalid_input` / `not_found` |

执行规则：

1. 命令被接受后调用 `waitForSettled`，用返回的快照检查步骤期望；命令被拒绝则用当前快照检查。
2. 步骤期望不满足就停止，不再执行后面的步骤，用例判为失败；命令被拒绝而 `ok` 不是 `false` 同样失败。
3. 所有步骤通过后检查顶层 `expect`。

## 输出目录

默认 `out/<id>/`（相对运行命令时的当前目录），可用 `--out` 指定：

| 文件 | 内容 |
|---|---|
| `result.json` | `caseId`、`passed`、`taskId`、`finalState`、`pauseCode`、每步结果 `steps`、`deliverables`（种类、绝对路径、sha256）、`failures` |
| `snapshot.json` | 最后一次的任务快照 |
| `events.jsonl` | 任务的完整事件日志，每行一个事件 |
| `workspace/` | 本次运行用的工作区（含 `tasks/<taskId>/`，交付物也在其中）；设置了环境变量 `DE_WORKSPACE` 时改用该目录 |

用 `de replay <out>/workspace <taskId>` 可回放事件。退出码：用例通过为 0，失败为 1，参数或文件错误为 1 或 2。

## 本目录的用例

| 文件 | 类型 | 要点 |
|---|---|---|
| `A-01.json` | 正常（第 44 周，纪要与数据不一致） | 发起 → 待审核 → 通过 → 已交付 |
| `A-02.json` | 正常（第 43 周，重复记录） | 同上 |
| `A-03.json` | 缺材料 | 只传纪要 → 停在 `missing_sales` → supplement 补传销售明细 → 待审核 → 通过 |
| `A-04.json` | 数据缺失与退货（第 42 周） | 同 A-01 |
| `A-05.json` | 超出数据范围（第 46 周） | 停在 `period_out_of_range`，不继续 |

数字类的判定要点（实收、环比、达成率等）由工具与质检的测试覆盖，不放在用例文件里。

## 运行示例

```powershell
# 用剧本式假模型（T7 提供 samples/cases/scripts/A-01.json）
bun packages/cli/src/main.ts run-case samples/cases/A-01.json --scripted samples/cases/scripts/A-01.json --out out/A-01

# 回放刚才那个任务
bun packages/cli/src/main.ts replay out/A-01/workspace <taskId>
```
