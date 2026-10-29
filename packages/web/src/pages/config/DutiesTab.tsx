// 岗位说明：三个文本框（职责 / 写作要求 / 注意事项）写回 SKILL.md。
import { Alert, Card, Input, Space, Typography } from "antd";
import type { SkillSections } from "@de/core/contract";
import { asRecord, type TabProps } from "./helpers";

export function DutiesTab({ config, drafts, setData }: TabProps) {
  const draft = drafts["SKILL.md"]?.data;
  const sections: SkillSections = draft
    ? { duties: String(asRecord(draft).duties ?? ""), writing: String(asRecord(draft).writing ?? ""), cautions: String(asRecord(draft).cautions ?? "") }
    : config.views.skill;
  const update = (key: keyof SkillSections) => (event: React.ChangeEvent<HTMLTextAreaElement>) =>
    setData("SKILL.md", { ...sections, [key]: event.target.value });

  return (
    <Space direction="vertical" size={16} style={{ width: "100%" }}>
      <Alert
        type="info"
        showIcon
        message="这里的内容会作为判断步骤的系统提示词；保存时只替换对应章节，其余章节原样保留。"
      />
      <Card size="small" title="岗位职责" extra={<Typography.Text type="secondary">员工是谁、负责什么</Typography.Text>}>
        <Input.TextArea rows={7} value={sections.duties} onChange={update("duties")} placeholder="写出岗位职责与边界" />
      </Card>
      <Card
        size="small"
        title="写作要求"
        extra={<Typography.Text type="secondary">包含「最重要的规则」「周报怎么写」等其余章节（Markdown）</Typography.Text>}
      >
        <Input.TextArea rows={20} value={sections.writing} onChange={update("writing")} placeholder="写作规则、格式要求、示例" />
      </Card>
      <Card size="small" title="注意事项" extra={<Typography.Text type="secondary">禁止事项与常见错误</Typography.Text>}>
        <Input.TextArea rows={8} value={sections.cautions} onChange={update("cautions")} placeholder="不允许做什么" />
      </Card>
    </Space>
  );
}
