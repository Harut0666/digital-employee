// 表单字段：增删字段、选类型、设必填、选用途，写回 form.schema.json（顺序同步写 form.ui.json）。
import { useState } from "react";
import { Alert, App, Button, Empty, Input, List, Popconfirm, Select, Space, Switch, Tag, Typography } from "antd";
import { DeleteOutlined, PlusOutlined } from "@ant-design/icons";
import { asArray, asRecord, isValidFieldName, type Json, type TabProps } from "./helpers";

const FIELD_TYPES = [
  { value: "string", label: "文本" },
  { value: "integer", label: "整数" },
  { value: "number", label: "数字" },
  { value: "boolean", label: "是 / 否" },
  { value: "array", label: "列表" },
  { value: "object", label: "分组" },
];

const NEEDS_DEV = "__needs_dev__";

export function FieldsTab({ config, drafts, setData }: TabProps) {
  const { message } = App.useApp();
  const schema = (drafts["form.schema.json"]?.data ?? config.files.find((f) => f.path === "form.schema.json")?.parsed ?? {}) as Json;
  const properties = asRecord(schema.properties);
  const required = asArray<string>(schema.required);
  const formUi = (drafts["form.ui.json"]?.data ?? config.files.find((f) => f.path === "form.ui.json")?.parsed ?? {}) as Json;
  const order = asArray<string>(formUi["ui:order"]);

  const [newName, setNewName] = useState("");
  const [newTitle, setNewTitle] = useState("");
  const [newType, setNewType] = useState("string");
  const [needsDev, setNeedsDev] = useState<string[]>([]);

  const updateSchema = (mutate: (draft: Json) => void) => {
    const next = structuredClone(schema);
    next.properties = next.properties ?? {};
    mutate(next);
    setData("form.schema.json", next);
  };

  const updateOrder = (names: string[]) => {
    setData("form.ui.json", { ...formUi, "ui:order": names });
  };

  const setRequired = (name: string, checked: boolean) => {
    updateSchema((draft) => {
      const current = asArray<string>(draft.required);
      draft.required = checked ? [...new Set([...current, name])] : current.filter((item) => item !== name);
    });
  };

  const setPurpose = (name: string, value: string) => {
    updateSchema((draft) => {
      const property = asRecord(draft.properties[name]);
      if (value === "" || value === NEEDS_DEV) delete property["x-purpose"];
      else property["x-purpose"] = value;
      draft.properties[name] = property;
    });
    setNeedsDev((prev) => (value === NEEDS_DEV ? [...new Set([...prev, name])] : prev.filter((item) => item !== name)));
  };

  const setProperty = (name: string, patch: Json) => {
    updateSchema((draft) => {
      draft.properties[name] = { ...asRecord(draft.properties[name]), ...patch };
    });
  };

  const removeField = (name: string) => {
    updateSchema((draft) => {
      delete draft.properties[name];
      draft.required = asArray<string>(draft.required).filter((item) => item !== name);
    });
    updateOrder(order.filter((item) => item !== name));
    setNeedsDev((prev) => prev.filter((item) => item !== name));
  };

  const addField = () => {
    const name = newName.trim();
    if (!isValidFieldName(name, Object.keys(properties))) {
      message.error("字段名只能用字母、数字、下划线，且不能与已有字段重名");
      return;
    }
    const title = newTitle.trim() || name;
    updateSchema((draft) => {
      draft.properties[name] = newType === "array" ? { type: "array", title, items: { type: "string" } } : { type: newType, title };
    });
    updateOrder([...order.filter((item) => item !== name), name]);
    setNewName("");
    setNewTitle("");
  };

  const purposeOptions = [
    { value: "", label: "未设置" },
    { value: "writing_requirement", label: "作为写作要求（注入判断步骤上下文）" },
    ...config.views.formParams.map((param) => ({ value: `param:${param.name}`, label: `绑定参数：${param.label}` })),
    { value: NEEDS_DEV, label: "需要新计算逻辑（需要开发支持）" },
  ];

  return (
    <Space direction="vertical" size={16} style={{ width: "100%" }}>
      <Alert
        type="info"
        showIcon
        message="用途决定字段值怎么被使用"
        description={
          <span>
            「作为写作要求」：字段值会作为“发起人的写作要求”注入每个判断步骤，不用改代码。
            「绑定参数」：只能选程序已经支持的参数（{config.views.formParams.map((p) => p.label).join("、")}）。
            需要新计算逻辑的字段，请选择“需要开发支持”。
          </span>
        }
      />
      {needsDev.length > 0 ? (
        <Alert type="warning" showIcon message={`需要开发支持：${needsDev.join("、")}`} description="这些字段需要新的计算逻辑，当前不会被执行，已按未设置用途保存。" />
      ) : null}

      <List
        bordered
        locale={{ emptyText: <Empty description="还没有表单字段" /> }}
        dataSource={Object.keys(properties)}
        renderItem={(name) => {
          const property = asRecord(properties[name]);
          const children = Object.keys(asRecord(property.properties));
          const purpose = typeof property["x-purpose"] === "string" ? (property["x-purpose"] as string) : "";
          return (
            <List.Item key={name}>
              <Space direction="vertical" size={8} style={{ width: "100%" }}>
                <Space size={12} wrap>
                  <Typography.Text code>{name}</Typography.Text>
                  <Input
                    size="small"
                    style={{ width: 180 }}
                    value={String(property.title ?? name)}
                    onChange={(event) => setProperty(name, { title: event.target.value })}
                    placeholder="标题"
                  />
                  <Select
                    size="small"
                    style={{ width: 110 }}
                    value={String(property.type ?? "string")}
                    options={FIELD_TYPES}
                    onChange={(type) => setProperty(name, { type })}
                  />
                  <Space size={4}>
                    <Typography.Text type="secondary">必填</Typography.Text>
                    <Switch size="small" checked={required.includes(name)} onChange={(checked) => setRequired(name, checked)} />
                  </Space>
                  <Popconfirm title={`删除字段 ${name}？`} description="如果工作流还在引用它，任务会出错。" onConfirm={() => removeField(name)}>
                    <Button size="small" danger type="text" icon={<DeleteOutlined />}>
                      删除
                    </Button>
                  </Popconfirm>
                </Space>
                <Space size={12} wrap>
                  <Typography.Text type="secondary">用途</Typography.Text>
                  <Select size="small" style={{ width: 320 }} value={purpose} options={purposeOptions} onChange={(value) => setPurpose(name, value)} />
                  {children.length > 0 ? <Tag>分组：{children.join("、")}</Tag> : null}
                  {property.type === "array" ? <Tag>列表项：{String(asRecord(property.items).type ?? "string")}</Tag> : null}
                  {property.type === "object" || property.type === "array" ? (
                    <Typography.Text type="secondary">嵌套字段请用右上角「查看源文件」编辑</Typography.Text>
                  ) : null}
                </Space>
              </Space>
            </List.Item>
          );
        }}
      />

      <Space size={8} wrap>
        <Input placeholder="字段名，如 focusRegion" style={{ width: 200 }} value={newName} onChange={(event) => setNewName(event.target.value)} />
        <Input placeholder="标题，如 重点关注区域" style={{ width: 220 }} value={newTitle} onChange={(event) => setNewTitle(event.target.value)} />
        <Select style={{ width: 120 }} value={newType} options={FIELD_TYPES} onChange={setNewType} />
        <Button type="dashed" icon={<PlusOutlined />} onClick={addField}>
          新增字段
        </Button>
      </Space>
    </Space>
  );
}
