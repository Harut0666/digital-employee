# A-01 golden（TS 版对拍基准）

这份基准由 TypeScript 版（`feat/electrobun-shell`）跑 A-01 生成，Go 版必须与它对齐，
验收脚本是 `scripts/parity-a01.sh`：事件流先经 `scripts/normalize-events.py` 归一化，再逐字段 diff。

## configHash 变更记录

| 时间 | configHash | 变化原因 |
|---|---|---|
| 基准生成时 | `c9c99c68…` | TS 版当时的员工配置 |
| T44 | `d42bfc59…` | T25 给 `workflow.yaml` 加 `deliverableName`（交付物下载名模板）；T29 给 `form.ui.json` 的统计周期配周选择器（`ui:field`）；T31 给每个步骤加业务文案（`activity`）；T30 加开工清单 `checklist.yaml` 并改 `frontdesk.md` |
| T44 | `4b955c8d…` | T44 按 T35 第 6 项把 `prompts/interpret_feedback.md` 与 `schemas/interpret_feedback.json` 里的“进入待人工处理”“写给操作员看”改成“超范围意见回到待审核”。这是有意不再与冻结的 TS 版逐字节相同，只影响哈希 |

configHash 是员工配置目录**全部文件内容**的 sha256（见 `internal/config/config.go` 的包注释），
上面任何一处改动都会让哈希变化。T44 只把 golden 里记录的哈希换成新值（`events.jsonl`、
`events.normalized.jsonl`、`snapshot.json`、`qc_record.json`），其余字段未变：
归一化后事件序列仍逐字段一致，`finalState=delivered`、`passed=true`、docx 仍含 2 张图片 + 1 张表格。

## 重新生成

golden 由 TS 版生成，Go 版不重新生成；配置再变时按上表方式更新哈希并重跑
`bash scripts/parity-a01.sh`，确认差异只有 `configHash` 一处。
