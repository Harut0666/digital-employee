# 步骤：撰写周报叙述

本步骤写出周报的标题和六个章节的叙述，并把原因匹配结果带入草稿。表格和图表由程序按事实生成，不由你写。结果通过 `submit_result` 工具提交，不要在对话里直接回答。写作规则以岗位说明为准，尤其是：数字只写槽位、原因只引用纪要原文并注明周次、纪要与数据矛盾以数据为准、不写禁用指标。

## 输入

本步骤收到的消息是一份 JSON，字段如下：

| 字段 | 内容 |
|---|---|
| `period` | 统计周期，`year` 和 `week` |
| `remark` | 发起人的备注，作为补充写作要求 |
| `partialPeriod` | 为真表示数据只到周中，发起人选择按已有数据继续 |
| `facts` | 程序算出的全部事实（`period`、`dataCoverage`、`company`、`regions`、`stores`、`categories`、`regionCategories`、`topProducts`、`anomalies`、`trend`、`quality`）。数字只能通过槽位引用。`regionCategories` 是区域×品类的实收与本周、上周毛利率；`quality.returns.groups` 是具名退货明细（门店、日期、单品、份数、金额） |
| `minutes` | 解析后的纪要，用于判断停业和写下周关注事项 |
| `causes` | 上一步为每条异常匹配的原因 |
| `contradictions` | 程序判出的纪要矛盾；没有则为空数组。每条含 `id`、`subject`（对象，`name` 是写进周报的展示名）、`claim`（纪要说法）、`actualWow`、`rule`、`sourceWeek`、`quote` |
| `feedback` | 解读后的退回意见清单；首次执行时为空 |
| `previousDraft` | 被退回的上一版草稿；首次执行时没有 |
| `structure` | 章节清单（`sections` 的 `id` 与 `heading`）、摘要句数上限、下周关注条数上限、图表下限 |
| `forbiddenTerms` | 禁用词 |
| `anomalyThreshold` | 异常阈值 |

如果消息里还有 `corrections`，那是质检指出的具体问题，必须逐条改正。

## 章节

`sections` 必须按 `structure.sections` 的顺序，包含全部六章，`id` 和 `heading` 与其一字不差：

1. **本周摘要**（`summary`）：不超过 `structure.summaryMaxSentences` 句，按句号、问号、感叹号数。依次交代全公司实收（`{{company.revenue}}`）、环比（`{{company.wow}}`）、毛利率、目标达成率，再用 1 至 2 句讲最重要的异常。`partialPeriod` 为真时，第一句先说明数据只到事实 `dataCoverage` 的截止日（写成“10 月 29 日”这样的日期），周报按已有数据编写。
2. **核心指标**（`metrics`）：1 至 2 句点评，如哪个区域达成最好、哪个区域最需关注，数字用槽位。不要逐项罗列，表格由程序生成。
3. **品类表现**（`categories`）：用要点写各品类实收与环比的主要特点，再用一个要点列出本周实收前 5 名单品（单品名按 `facts.topProducts` 的顺序写，实收用槽位）。纪要提到某区域某品类毛利率变化（或该区域该品类毛利率两周差异明显）时，用槽位写出上周与本周的毛利率：`{{regionCategories.华南.饮品.prevGrossMarginRate}}`、`{{regionCategories.华南.饮品.grossMarginRate}}`（区域、品类换成纪要或事实里的那一个）。
4. **异常与原因**（`anomalies`）：
   - 对 `facts.anomalies` 里的**每一条**异常写一个要点，先区域后门店，同级按环比变化从大到小；一条都不能少。
   - 每个要点写：对象名称、环比和实收（槽位，区域用 `regions.<区域名>`，门店用 `stores.<门店编码>`）、原因，并注明“（第 N 周纪要）”。原因取自 `causes` 中对应的一条，`cause` 是“原因待核实”的就如实写“原因待核实”，不补充推测。
   - 该异常的 `baseWeekHasMissingDays` 为真时，加一句“上周存在数据缺失日，环比基数偏低”。
   - 沿用上级原因的，保留“（区域整体原因）”“（店型整体原因）”标注。
   - 最后写纪要矛盾（见下）。
5. **数据质量说明**（`data_quality`）：按岗位说明里“数据质量说明怎么写”逐项写。判断门店无数据日是停业还是数据缺失时，对照 `facts.quality.storeDays` 和各周 `minutes` 里的说法；上周的无数据日看 `facts.quality.prevWeekStoreDays`。退货有具名明细（`facts.quality.returns.groups`，含门店、日期、单品、份数、金额）时，用槽位写出门店、日期、品名、份数和金额，如“北京朝阳店 10 月 15 日草莓奶油切块退货 {{quality.returns.groups.BJ01.count}} 份，合计 {{quality.returns.groups.BJ01.revenue}}”。
6. **下周关注事项**（`next_week`）：不超过 `structure.nextWeekMaxItems` 条，写成要点。来自本周纪要的行动项，或数据里的风险点（如活动结束后的回落、仍待处理的数据问题）。每条一句话。

标题写成“经营周报（2026 年第 44 周）”的形式，年和周取自 `period`。

## 纪要矛盾

是否构成矛盾已由程序判定，你不再自己判断。`contradictions` 里的**每一条**都要在“异常与原因”章节写一段：

- 写明纪要怎么说，并注明第 N 周纪要（`sourceWeek`）；原文可用引号逐字引用 `quote`；
- 用槽位写数据显示的实际环比（`{{regions.华南.wow}}` 这样的写法，对象按 `subject.level` 与 `subject.key` 取值），并写明“以数据为准”；
- 段落里必须出现矛盾对象名 `subject.name`，否则质检不通过；
- 把这名矛盾的 `id` 原值写进输出的 `contradictionRefs`，如 `"contradiction:region:华南"`，不要改写格式。

不要把没有矛盾的说法也列出，也不要写 `contradictions` 以外的矛盾。例如：“第 44 周纪要称华南本周与上周基本持平，数据显示华南环比 {{regions.华南.wow}}，以数据为准。”

## 原因匹配结果

`causes` 字段原样取自输入的 `causes`，不改动其中任何字，每条异常一条。

## 重做

输入里有 `feedback` 和 `previousDraft` 时，本次是重做：

- 以 `previousDraft` 为基础修改，没有被意见涉及的内容保持不变。
- `feedback` 的每个条目都要有对应的改动；`sections` 里列出的章节必须真的发生变化。
- 提交 `revisionNotes`，每个条目一条：`comment` 写对应的意见，`sections` 写改动的章节 `id`，`change` 写改了什么。
- 首次执行时不提交 `revisionNotes`。

## 输出

调用 `submit_result` 提交一个 JSON 对象，字段为 `title`、`sections`、`causes`、`contradictionRefs`，重做时再加 `revisionNotes`，结构符合本步骤的输出 Schema（与 DraftReport 一致）。`contradictionRefs` 列出写进“异常与原因”的每条矛盾的 `id`，一条都不能漏；没有矛盾时提交空数组。叙述里不得出现裸数字、禁用词，不要加章节以外的内容。
