// 前台 Agent：人设文本框 + 只读工具勾选，写回 frontdesk.md。
import { Alert, Card, Checkbox, Space, Tag, Typography } from "antd";
import { MarkdownEditor } from "../../components/MarkdownEditor";
import { FRONTDESK_FIXED_TOOLS, FRONTDESK_READONLY_TOOLS, type FrontdeskDoc } from "@de/core/contract";
import { asArray, type TabProps } from "./helpers";

export function FrontdeskTab({ config, drafts, setData }: TabProps) {
  const draft = drafts["frontdesk.md"]?.data as Partial<FrontdeskDoc> | undefined;
  const doc: FrontdeskDoc = {
    body: typeof draft?.body === "string" ? draft.body : config.views.frontdesk.body,
    tools: draft?.tools ? asArray<string>(draft.tools) : config.views.frontdesk.tools,
  };
  const update = (patch: Partial<FrontdeskDoc>) => setData("frontdesk.md", { ...doc, ...patch });

  return (
    <Space direction="vertical" size={16} style={{ width: "100%" }}>
      <Alert
        type="info"
        showIcon
        message="前台 Agent 只能在对话里读写工作区文件、通过 run_employee 把工作交给员工（由发起人在员工表单卡上确认）、用只读工具回答问题；任何状态推进都必须由对应角色点击卡片按钮。这里的人设是写给前台 Agent 的、关于这个员工的说明。"
      />
      <Card size="small" title="人设" extra={<Typography.Text type="secondary">frontdesk.md 正文</Typography.Text>}>
        <MarkdownEditor rows={18} value={doc.body} onChange={(text) => update({ body: text })} placeholder="前台 Agent 的人设、能做什么、不能做什么" />
      </Card>
      <Card
        size="small"
        title="开放给你的只读工具"
        extra={
          <Space size={6} wrap>
            {FRONTDESK_FIXED_TOOLS.map((tool) => (
              <Tag key={tool} color="blue">
                {tool}（固定）
              </Tag>
            ))}
          </Space>
        }
      >
        <Checkbox.Group
          value={doc.tools}
          options={FRONTDESK_READONLY_TOOLS.map((tool) => ({ value: tool.name, label: `${tool.label}（${tool.name}）` }))}
          onChange={(values) => update({ tools: values as string[] })}
        />
        <div style={{ marginTop: 8 }}>
          <Typography.Text type="secondary">只读工具只能查询，不能修改任务状态；运行员工（run_employee）是固定能力，不能关闭。</Typography.Text>
        </div>
      </Card>
    </Space>
  );
}
