# 场景 A · 变异数据（夹具）

边界与异常用例需要“真实数据的变异版本”。这些文件**由脚本生成、生成物入库**，不要手改：

```bash
bun packages/cli/src/main.ts fixtures          # 默认输出到 samples/fixtures/
bun packages/cli/src/main.ts fixtures --out /tmp/fx --source <另一个销售明细.xlsx>
```

- 源数据：`samples/scenario-a/销售明细.xlsx`（sha256 记在 `manifest.json` 里）。
- **确定性**：生成器把 xlsx 里每个 zip 条目的时间戳和文档属性都固定下来，同一份源数据连续跑两次、隔几分钟再跑，字节完全一样。所以重跑不会给仓库带来噪音；只有生成器本身改了，`manifest.json` 与文件才会变。
- `manifest.json` 记下每份夹具的大小、sha256、工作表、销售明细行数、表头与日期覆盖范围；`bun test` 会核对入库文件与 manifest 一致，不一致就说明忘了重跑 `de fixtures`。
- 生成器实现见 `packages/cli/src/fixtures.ts`，测试见 `packages/cli/test/fixtures.test.ts`。

## 夹具清单

| 文件 | 变异 | 预期行为（第 44 周） |
|---|---|---|
| `销售明细-第44周周中截止.xlsx` | 删掉 2026-10-28 之后的销售记录（41994 行，覆盖 08-31 至 10-28） | 准入暂停 `period_incomplete`：数据在周中截止，可补数据或选“按已有数据继续” |
| `销售明细-缺销售目标表.xlsx` | 删掉“销售目标”工作表 | 准入暂停 `missing_dimensions`，提示缺少“销售目标” |
| `销售明细-缺实收金额列.xlsx` | 销售明细少“实收金额”一列 | 文件能读，但实收全部读成 0（缺列的后果），用于边界用例 |
| `销售明细-损坏.xlsx` | 只保留源文件前 64KB（截掉 zip 中央目录） | 准入暂停 `invalid_input`：文件无法读取，提示重新上传 |
| `销售明细-含未知门店编码.xlsx` | 追加 3 行门店编码 `XX99`（门店信息表里没有）的记录 | 能跑通；`quality.unknownCodes.count = 3`，这些行不计入任何合计，实收与公开样例一致 |
| `销售明细-记录ID冲突.xlsx` | 追加 3 行与 10/27 已有记录同 ID、实收金额不同的记录 | 能跑通；`quality.idConflicts.count = 3`，重复项被剔除且不计入合计 |

上表的“预期行为”是用工具服务器实测的（`validate_submission` → `load_sales` → `compute_metrics`），不是猜的。

## 用法示例

用例文件里通过 `attachments[].path` 引用（相对用例文件所在目录）：

```jsonc
{
  "name": "销售明细.xlsx",
  "path": "../fixtures/销售明细-缺销售目标表.xlsx"
}
```

夹具只做“数据变异”，不改剧本、不改员工配置；对应的用例（周中截止、缺表、缺列、损坏、未知编码、ID 冲突）属于自测用例集，见 `samples/cases/README.md`。
