// 开工清单：员工开工前必须备齐的事项，写回 checklist.yaml。
// 每项只判定“文件是否已上传”或“表单字段是否已填写”；内容对不对由准入校验检查。
import { useState } from "react";
import { Alert, App, Button, Card, Empty, Input, Popconfirm, Select, Space, Switch, Typography } from "antd";
import { DeleteOutlined, PlusOutlined } from "@ant-design/icons";
import { asArray, asRecord, type Json, type TabProps } from "./helpers";

const SOURCES = [
  { value: "upload", label: "上传了文件" },
  { value: "field", label: "表单字段已填写" },
];

export function ChecklistTab({ config, drafts, setData }: TabProps) {
  const { message } = App.useApp();
  const file = config.files.find((f) => f.path === "checklist.yaml");
  const items = asArray(asRecord(drafts["checklist.yaml"]?.data ?? file?.parsed).items);
  const schema = (drafts["form.schema.json"]?.data ?? config.files.find((f) => f.path === "form.schema.json")?.parsed ?? {}) as Json;
  const properties = asRecord(schema.properties);
  const fieldOptions = Object.entries(properties).map(([name, p]) => ({ value: name, label: `${asRecord(p).title ?? name}（${name}）` }));
  const booleanOptions = fieldOptions.filter((o) => asRecord(properties[o.value]).type === "boolean");
  const [newId, setNewId] = useState("");

  const save = (next: Json[]) => setData("checklist.yaml", { items: next });
  const update = (index: number, patch: Json) => {
    const next = items.map((item, i) => (i === index ? { ...item, ...patch } : item));
    // 必需项不能跳过：切成必需时去掉跳过设置
    if (patch.required === true) {
      delete next[index]!.skipField;
      delete next[index]!.skipLabel;
    }
    save(next);
  };
  const add = () => {
    const id = newId.trim();
    if (!/^[a-zA-Z][a-zA-Z0-9_-]*$/.test(id) || items.some((item) => item.id === id)) {
      message.error("id 只能用字母、数字、下划线和连字符，且不能与已有项重名");
      return;
    }
    save([...items, { id, label: id, required: true, source: "upload", accept: [".xlsx"] }]);
    setNewId("");
  };

  return (
    <Space direction="vertical" size={16} style={{ width: "100%" }}>
      <Alert
        type="info"
        showIcon
        message="开工清单只由程序勾选：只看文件传了没有、必填项填了没有。周次不符、工作表缺失这类内容问题由准入校验暂停并提示补充。必需项不齐不能开工；可跳过项要发起人确认，开工时把指定的表单字段写成“是”。"
      />
      {items.length === 0 ? <Empty description="没有开工清单：开工前不用备齐任何东西" /> : null}
      {items.map((item, index) => (
        <Card
          key={String(item.id)}
          size="small"
          title={<Typography.Text code>{String(item.id)}</Typography.Text>}
          extra={
            <Popconfirm title="删除这一项？" onConfirm={() => save(items.filter((_, i) => i !== index))}>
              <Button size="small" danger icon={<DeleteOutlined />}>
                删除
              </Button>
            </Popconfirm>
          }
        >
          <Space wrap size={[16, 10]} align="center">
            <Space size={6}>
              名称
              <Input style={{ width: 160 }} value={String(item.label ?? "")} onChange={(e) => update(index, { label: e.target.value })} />
            </Space>
            <Space size={6}>
              必需
              <Switch checked={item.required === true} onChange={(checked) => update(index, { required: checked })} />
            </Space>
            <Space size={6}>
              判定来源
              <Select style={{ width: 160 }} value={item.source} options={SOURCES} onChange={(source) => update(index, { source })} />
            </Space>
            {item.source === "field" ? (
              <Space size={6}>
                字段
                <Select style={{ width: 220 }} value={item.field} options={fieldOptions} onChange={(field) => update(index, { field })} />
              </Space>
            ) : (
              <Space size={6}>
                扩展名
                <Select
                  mode="tags"
                  style={{ minWidth: 200 }}
                  value={asArray<string>(item.accept)}
                  onChange={(accept: string[]) => update(index, { accept: accept.map((a) => (a.startsWith(".") ? a : `.${a}`).toLowerCase()) })}
                  placeholder="如 .xlsx"
                />
              </Space>
            )}
            {item.required !== true ? (
              <>
                <Space size={6}>
                  跳过时写入
                  <Select
                    allowClear
                    style={{ width: 220 }}
                    value={item.skipField}
                    options={booleanOptions}
                    onChange={(skipField) => update(index, { skipField: skipField ?? undefined })}
                    placeholder="不写字段，只留痕"
                  />
                </Space>
                <Space size={6}>
                  跳过按钮
                  <Input style={{ width: 140 }} value={String(item.skipLabel ?? "")} onChange={(e) => update(index, { skipLabel: e.target.value })} placeholder="如 无纪要继续" />
                </Space>
              </>
            ) : null}
          </Space>
        </Card>
      ))}
      <Space>
        <Input style={{ width: 200 }} value={newId} onChange={(e) => setNewId(e.target.value)} placeholder="新项的 id，如 contract" onPressEnter={add} />
        <Button icon={<PlusOutlined />} onClick={add}>
          添加一项
        </Button>
      </Space>
    </Space>
  );
}
