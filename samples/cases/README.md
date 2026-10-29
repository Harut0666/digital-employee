# 命令行用例文件格式

`de run-case <case.json> [--scripted <script.json>] [--out <dir>]` 按用例里的命令序列驱动任务引擎，每执行一条命令就等任务停在暂停态或终态，最后把结果写到输出目录。

本目录给出场景 A 公开样例 A-01 至 A-05 的用例文件。用例只描述“发起什么、按什么顺序发什么命令、期望停在哪”，不含模型输出；需要模型的判断步骤由 `--scripted` 指定的剧本式假模型（T7 放在 `scripts/`）或真实模型完成。

用例文件里还能用 `expect.checks` 写判定要点（数字、必须出现和不得出现的文本、必须判出的异常、期望的暂停），由 `de check` 与 `de run-cases` 自动核对交付物，见下文“判定要点”。

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
| `checks` | 仅顶层：判定要点，见下节 |

执行规则：

1. 命令被接受后调用 `waitForSettled`，用返回的快照检查步骤期望；命令被拒绝则用当前快照检查。
2. 步骤期望不满足就停止，不再执行后面的步骤，用例判为失败；命令被拒绝而 `ok` 不是 `false` 同样失败。
3. 所有步骤通过后检查顶层 `expect`（`state` / `pauseCode` / `deliverables`）。
4. `checks` 不在 `run-case` 里核对（它只驱动引擎）；`de check` 与 `de run-cases` 才核对交付物。

## 判定要点（`expect.checks`）

公开样例用例的“判定要点”逐条转写成可自动核对的条目，放在顶层 `expect.checks`。核对器输入是**一次运行的结果目录**（`result.json` + `events.jsonl` + 交付的 Word），逐项给出通过或不通过及原因。

```jsonc
"expect": {
  "state": "delivered",
  "checks": {
    "numbers": [                                   // 数字，默认允许 ±0.5% 的计算误差
      { "path": "table:全公司/实收（元）", "expected": 573635 },   // 周报正文表格单元
      { "path": "table:全公司/环比", "expected": -9.3 },
      { "path": "facts:company.deliveryShare", "expected": 31.8 }, // 事实 JSON 里的值
      { "path": "facts:quality.returns.revenueCents", "expected": -260, "tolerance": 0.001 }
    ],
    "mustContain": ["以数据为准"],                  // 周报正文必须出现（忽略空白）
    "mustContainAny": [                            // 同一判定要点的等价说法：每组命中任意一处即可
      ["不是数据缺失", "不计为数据缺失", "不算数据缺失"]
    ],
    "mustNotContain": ["客单价", "同比"],            // 周报正文不得出现（忽略空白）
    "anomalies": ["region:华东", "store:HZ01"],     // 事实里必须判出的异常 id
    "pause": { "code": "missing_sales",            // 本次运行必须出现过该暂停
               "messageContains": "销售明细" }      // 提示里必须含的片段（可省略）
  }
}
```

### `numbers` 的两种 `path`

| 写法 | 指向 | 例 |
|---|---|---|
| `facts:<点路径>` | `compute_metrics` 输出的事实 JSON | `facts:company.revenueCents`、`facts:regions.华东.wow`、`facts:stores.HZ01.wow`、`facts:quality.duplicates.count`、`facts:topProducts.D02.revenueCents` |
| `table:<行>/<列>` | 周报 Word 里的表格单元；**行**按首列文字匹配，**列**按表头文字匹配 | `table:全公司/实收（元）`、`table:华南/目标达成率` |

- 数组按业务键取值：门店用编码（`stores.HZ01`）、单品用商品编码（`topProducts.D02`）、品类用品类名（`categories.饮品`）。
- **`expected` 填“展示口径”的数字**：金额填元，比例填百分数（`-9.3` 表示 `-9.3%`），计数填整数。`facts:` 路径的换算按叶子字段名定，与槽位渲染一致（`revenueCents` 等 → 元，`wow` / `grossMarginRate` / `achievement` / `deliveryShare` → 百分数，`count` → 整数）。
- 容差是**相对**容差，默认 `0.005`（0.5%），可用 `tolerance` 覆盖（比例多为 1 位小数，0.5% 容差足够吸收取整差）。
- 单元格里的千分位、正负号、`%` 会被解析成数字；`—`、`不可比` 之类按“不是数字”判不通过。

### 其余字段语义

- **文本比较忽略空白**（含换行与单元格边界）：`mustContain: ["实收 573,635 元"]` 与正文中跨行的同一句话视为相同。
- **`numbers` 与 `mustContain` 需要交付的 Word**：没有 docx 时这两类记不通过（缺交付物）。
- **`mustContainAny`**：同一判定要点的等价表述，`[["A", "B"], ["C"]]` 表示“A 或 B 至少一处、且 C”都必须成立；每组至少两个候选才有意义，只有一个候选请直接用 `mustContain`。核对器在原因里列出该组尝试过的全部说法，方便改成实际写法。它不降低判定要求，只是不按字面卡死措辞，见下文“等价表述”。
- **`mustNotContain` 在没交付 Word 时通过**：没有正文就没有违规文本；但一旦交付就会逐字核对，所以它仍能抓住“本该在准入停下却生成了周报”的回归。
- **`anomalies`**：用事实里稳定的异常 id（`region:<区域>` / `store:<门店编码>`，见 `WeeklyFacts.anomalies`），列出的每一个都必须判出。
- **`pause`**：在本次运行的**所有暂停**里找 code 等于期望值的最后一条（事件日志里 `state_changed` 的 `pause`），再检查 message 是否含片段。所以 A-03 这种“先暂停、补材料后继续”的用例，仍能核对到中间那次 `missing_sales`。
- 格式写错（未知字段、`expected` 不是数字、`path` 前缀不认识）时**用例文件直接报错**，不会静默跳过。

### 覆盖说明（已知未覆盖的判定要点）

| 用例 | 判定要点 | 现状 |
|---|---|---|
| A-02 | 第 5 条“华南饮品毛利率由 70.4% 降至 60.5%” | 事实里只有品类实收与环比，没有品类毛利率，暂时无法核对；需要先在 `WeeklyFacts` 里加字段 |
| A-04 | 第 4 条中的“北京朝阳店 15 份、-330 元” | 事实里退货只聚合到笔数与金额（`quality.returns`），单品级退货明细没有进事实，只核对了门店、商品、退货笔数与金额。周报正文的数字只能来自事实槽位（ADR-0002），纪要里的份数、金额不写成裸数字，所以“15 份、-330 元”要等事实里有单品级退货明细后才能核对 |
| A-04 | 第 3 条中的“手工台账约 5,100 元” | 公开样例写“**可引用**”，是可选内容，且该金额来自纪要而非事实，不能写进周报正文；因此不作为判定要点，`mustContain` 里已移除 |
| A-05 | 第 1 条“不得输出第 46 周的任何销售数字” | 该用例在准入就停下，没有 Word；用 `mustNotContain` 兜住“本不该生成的周报里出现第 46 周”的回归，没有生成周报时按“没有正文”通过 |
| A-03 | 第 1 条“未上传销售数据时不得生成周报，也不得仅凭纪要编写数字” | 由步骤期望保证（第一次 submit 后停在 `awaiting_initiator`、快照里没有交付物）；`checks` 补的是中间那次 `missing_sales` 暂停与补传后周报的数字 |

### 等价表述（`mustContainAny` 的理由）

真实模型（DeepSeek）会用同义句式表达同一判定要点，逐字 `mustContain` 会把“说对了但换了词”判成不通过。下列条目改成等价说法集合，判定要求不变：

| 用例 | 判定要点 | 原来的写法 | 现在接受的说法 |
|---|---|---|---|
| A-01 | 停业不是数据缺失 | `不是数据缺失` | 不是 / 不计为 / 不算 / 不属于 / 不视为 数据缺失 |
| A-01 | 正文提到外卖占比 | `外卖占比为 31.8%` | `外卖占比 31.8%` / `外卖占比为 31.8%`（数字本身仍由 `facts:company.deliveryShare` 核对） |
| A-02 | 识别重复上传 | `29 条记录` | `29 条记录` / `重复上传 29 条` / `29 条` |
| A-02 | 重复记录的处理 | `去重` | `去重` / `只保留第一条` / `剔除重复记录` / `重复记录不计入` |
| A-04 | 指出缺数日期 | `10 月 14 日` | `10 月 14 日` / `10/14` |

`北京朝阳店`、`草莓奶油切块`、`退货` 等必须出现的判定要点保持不变；数字仍由 `numbers` 逐项核对。

## 输出目录

`de run-case` 默认 `out/<id>/`，`de run-cases` 默认 `out/cases/<id>/`（都相对运行命令时的当前目录），可用 `--out` 指定。每条用例一个目录：

| 文件 | 内容 |
|---|---|
| `result.json` | `caseId`、`passed`、`taskId`、`finalState`、`pauseCode`、每步结果 `steps`、`deliverables`（种类、绝对路径、sha256）、`failures` |
| `snapshot.json` | 最后一次的任务快照 |
| `events.jsonl` | 任务的完整事件日志，每行一个事件 |
| `workspace/` | 本次运行用的工作区（含 `tasks/<taskId>/`，交付物也在其中）；设置了环境变量 `DE_WORKSPACE` 时改用该目录 |

`de run-cases` 另外在 `<out>/summary.md` 写汇总：每条用例的结果、核对通过数、模型调用次数、用时，以及未通过项的具体原因；**退出码等于未通过的条数**。

用 `de replay <out>/workspace <taskId>` 可回放事件。`de run-case` 的退出码：用例通过为 0，失败为 1，参数或文件错误为 1 或 2。`de check` 全部通过为 0，有不通过项为 1。

## 自测用例集（`S-xx`）

`S-01`～`S-24` 按 [design §15](../../docs/design.md) 的场景 A 清单补齐，覆盖正常、边界、缺材料、异常、流程五类；标题以类别开头，方便在汇总里分类看。每条都有配套剧本与 `expect.checks`。

| 用例 | 类别 | 覆盖点 | 终止状态 |
|---|---|---|---|
| `S-01` / `S-02` | 正常 | 第 40、41 周没有纪要，选“无纪要继续”后交付 | `delivered` |
| `S-03` / `S-04` | 正常 | 第 44、43 周纪要与数据一致，没有矛盾要引用（`contradictionRefs` 为空也能过质检） | `delivered` |
| `S-05` | 边界 | 第 36 周是数据首周，无上周、环比“不可比” | `delivered` |
| `S-06` | 边界 | 只传 CSV → 缺维表 → 补传 xlsx 后继续 | `delivered` |
| `S-07` | 边界 | 第 44 周配第 43 周纪要 → 周次不符 → 补传本周纪要 | `delivered` |
| `S-08` | 边界 | 同时上传第 43、44 两份纪要 | `delivered` |
| `S-09` | 边界 | 数据在周中截止 → 选“按已有数据继续”，报告标注数据不完整 | `delivered` |
| `S-10` | 边界 | 数据在周中截止 → 补传完整销售明细后继续 | `delivered` |
| `S-11` | 边界 | 周中截止且无纪要 → 两次继续（按已有数据 + 无纪要） | `delivered` |
| `S-12` | 缺材料 | 缺“销售目标”工作表 → `missing_dimensions` | `awaiting_initiator` |
| `S-13` | 缺材料 | 缺本周纪要 → 选“无纪要继续”后交付 | `delivered` |
| `S-14` | 缺材料 | 缺销售明细 → 发起人终止任务 | `terminated` |
| `S-15` | 异常 | 第 35 周早于数据覆盖范围 → `period_out_of_range` | `awaiting_initiator` |
| `S-16` | 异常 | 损坏的 xlsx（截断字节）→ `invalid_input` | `awaiting_initiator` |
| `S-17` | 异常 | 销售明细缺“实收金额”列：能通过准入，但实收全为 0（不编造） | `delivered` |
| `S-18` | 异常 | 含未知门店编码的记录：不计入合计，数据质量说明里报数量 | `delivered` |
| `S-19` | 异常 | 记录 ID 冲突：剔除后计算，数据质量说明里报数量 | `delivered` |
| `S-20` | 流程 | 审核人退回摘要意见 → 重做（`revisionNotes`）→ 通过 | `delivered` |
| `S-21` | 流程 | 连续两次退回（摘要、核心指标各一次）→ 两轮重做 → 通过 | `delivered` |
| `S-22` | 流程 | 发起人越权点“通过”被拒（`forbidden_role`），审核人才能通过 | `delivered` |
| `S-23` | 流程 | 漏引用纪要矛盾 → 质检 `contradiction_coverage` 不通过 → 自动修正后通过 | `delivered` |
| `S-24` | 流程 | 质检自动修正用尽 → 转人工（`qc_exhausted`）→ 操作员终止 | `terminated` |

边界与异常用例优先用 `samples/fixtures/` 的变异数据（周中截止、缺销售目标表、缺实收金额列、损坏文件、未知门店编码、记录 ID 冲突），夹具说明见 [fixtures/README.md](../fixtures/README.md)。

三类**进程级**流程（工具故障后重试、模型调用超限、崩溃后断点恢复）无法用用例文件表达（要注入故障或重启进程），由 `packages/cli/test/e2e/weekly-report.e2e.test.ts` 覆盖；`S-22`/`S-24` 覆盖可在进程内表达的“越权拦截、非法流转、转人工、终止”。

## 本目录的用例

| 文件 | 类型 | 要点 | `checks` |
|---|---|---|---|
| `A-01.json` | 正常（第 44 周，纪要与数据不一致） | 发起 → 待审核 → 通过 → 已交付 | 核心指标数字 + 文本 + 异常 + 不得出现禁用指标 |
| `A-02.json` | 正常（第 43 周，重复记录） | 同上 | 数字 + 去重（不得出现未去重的 208,749）+ 文本 + 异常 |
| `A-03.json` | 缺材料 | 只传纪要 → 停在 `missing_sales` → supplement 补传销售明细 → 待审核 → 通过 | 中间那次 `missing_sales` 暂停 + 补传后周报的数字 |
| `A-04.json` | 数据缺失与退货（第 42 周） | 同 A-01 | 数字 + 退货/缺失说明 + 文本 + 异常 |
| `A-05.json` | 超出数据范围（第 46 周） | 停在 `period_out_of_range`，不继续 | 暂停提示里说明实际覆盖范围 |
| `A-01-rework.json` | A-01 变体 | 待审核 → 发起人点“通过”被拒 → 审核人退回（摘要） → 重做 → 通过 → 已交付 | 与 A-01 相同 |

## 剧本（`scripts/`）

`scripts/<用例 id>.json` 是配套的剧本式假模型：按步骤 id 列出每一轮模型响应，格式见 `@de/core` 的 `ModelScript`。剧本里每一轮都是 `submit_result` 工具调用，叙述只含 `{{path}}` 槽位（ADR-0002），所以质检是真的通过，不是被桩掉。`A-03` 与 `A-01` 同一份内容（补传销售明细后跑的是同一周）；`A-05` 在准入就停下，剧本为空；`A-01-rework` 多出 `interpret_feedback` 和第二轮 `match_causes`、`write_report`，对应重做。

T8 之后剧本要贴合九步流程：`match_causes` 除了 `causes` 还要给 `claims`（可核验说法，没有就提交空数组），

- 有矛盾时 `write_report` 要带 `contradictionRefs`（id 形如 `contradiction:region:华南`），并在“异常与原因”章节提到矛盾对象；
- 没有矛盾时可以不写 `contradictionRefs`（等同于空数组）；
- 重做轮次的 `write_report` 要带 `revisionNotes`，且被退回章节的内容必须真的变了（`S-20`、`S-21`）；
- 质检不通过会自动修正：`S-23` 第一版故意漏引用矛盾，`write_report` 第二次才补上；`S-24` 连续三版都不合格，自动修正用尽后转人工。

数字类的判定要点写进用例文件的 `checks`（见上文），由 `de check` / `de run-cases` 核对交付物；`checks` 之外的计算口径（如品类毛利率）仍由工具与质检的测试覆盖。

## 运行示例

```powershell
# 用剧本式假模型（samples/cases/scripts/A-01.json）
bun packages/cli/src/main.ts run-case samples/cases/A-01.json --scripted samples/cases/scripts/A-01.json --out out/A-01

# 回放刚才那个任务
bun packages/cli/src/main.ts replay out/A-01/workspace <taskId>

# 只核对一次运行的交付物（不重跑）：第二个参数可以是用例文件，也可以是单独的 {"checks": {...}}
bun packages/cli/src/main.ts check out/A-01 samples/cases/A-01.json

# 批量跑 + 核对（glob 由命令行自己解析，Windows 下也能用），汇总写到 out/cases/summary.md
bun packages/cli/src/main.ts run-cases "samples/cases/*.json" --scripted --out out/self-test
```

全量跑（A-0x + S-xx 共 30 条）的结果存档在 [docs/results/self-test-scripted.md](../../docs/results/self-test-scripted.md)。
