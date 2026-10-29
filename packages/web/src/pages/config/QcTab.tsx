// 质检规则：内置规则可以开关、调阈值，不能删除；从 6 种模板新增自定义规则，也可以删除自定义规则。
// 写回 qc.yaml。
import { Button, Dropdown, Input, InputNumber, Popconfirm, Select, Space, Switch, Tag, Typography } from "antd";
import { DeleteOutlined, PlusOutlined } from "@ant-design/icons";
import { QC_TEMPLATES, QC_TEMPLATE_LABELS, type QcTemplate } from "@de/core/contract";
import { asArray, asRecord, percentOf, ratioOf, type Json, type TabProps } from "./helpers";

const CUSTOM_TAG = <Tag color="green">自定义</Tag>;
const BUILTIN_TAG = <Tag>内置</Tag>;

function RuleRow({
  label,
  tag,
  enabled,
  onToggle,
  children,
}: {
  label: string;
  tag?: React.ReactNode;
  enabled?: boolean;
  onToggle?: (checked: boolean) => void;
  children?: React.ReactNode;
}) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 12,
        flexWrap: "wrap",
        padding: "10px 14px",
        border: "1px solid var(--line)",
        borderRadius: 10,
        background: "#fff",
      }}
    >
      {onToggle ? <Switch size="small" checked={enabled} onChange={onToggle} /> : <span style={{ width: 28 }} />}
      <span style={{ minWidth: 150, fontWeight: 500 }}>{label}</span>
      {tag}
      <div style={{ flex: 1, minWidth: 240 }}>{children}</div>
    </div>
  );
}

const PERCENT = <span>%</span>;

export function QcTab({ config, drafts, setData }: TabProps) {
  const qc = (drafts["qc.yaml"]?.data ?? config.files.find((f) => f.path === "qc.yaml")?.parsed ?? {}) as Json;
  const builtin = asRecord(qc.builtin);
  const sections = asArray<Json>(asRecord(builtin.structure).sections);
  const contradiction = asRecord(qc.contradiction);
  const custom = asArray<Json>(qc.custom);

  const update = (mutate: (draft: Json) => void) => {
    const next = structuredClone(qc);
    next.builtin = next.builtin ?? {};
    mutate(next);
    setData("qc.yaml", next);
  };

  const updateBuiltin = (check: string, mutate: (config: Json) => void) => {
    update((draft) => {
      draft.builtin[check] = draft.builtin[check] ?? {};
      mutate(draft.builtin[check]);
    });
  };

  const updateContradiction = (mutate: (config: Json) => void) => {
    update((draft) => {
      draft.contradiction = draft.contradiction ?? {};
      mutate(draft.contradiction);
    });
  };

  const setCustom = (rules: Json[]) => {
    update((draft) => {
      draft.custom = rules;
    });
  };

  const patchRule = (index: number, patch: Json) => {
    const next = structuredClone(custom);
    next[index] = { ...asRecord(next[index]), ...patch };
    setCustom(next);
  };

  const patchParams = (index: number, patch: Json) => {
    const next = structuredClone(custom);
    next[index] = { ...next[index], params: { ...asRecord(next[index]!.params), ...patch } };
    setCustom(next);
  };

  const addRule = (template: QcTemplate) => {
    const firstSection = String(asRecord(sections[0]).id ?? "summary");
    const defaults: Record<QcTemplate, Json> = {
      forbidden_terms: { terms: ["客单价"] },
      required_terms: { terms: ["下周关注"] },
      section_must_contain: { section: firstSection, terms: ["下周关注"] },
      max_items: { section: firstSection, max: 5 },
      max_pages: { max: 3 },
      number_range: { path: "company.wow", min: -100, max: 100 },
    };
    const rule: Json = {
      id: `custom_${Date.now().toString(36)}`,
      label: QC_TEMPLATE_LABELS[template],
      template,
      enabled: true,
      params: defaults[template],
    };
    setCustom([...custom, rule]);
  };

  const paramsEditor = (rule: Json, index: number) => {
    const params = asRecord(rule.params);
    const sectionOptions = sections.map((section) => ({ value: String(section.id), label: String(section.heading ?? section.id) }));    switch (rule.template as QcTemplate) {
      case "forbidden_terms":
      case "required_terms":
        return (
          <Select
            mode="tags"
            style={{ width: "100%" }}
            value={asArray<string>(params.terms)}
            placeholder="输入词后回车"
            onChange={(terms) => patchParams(index, { terms })}
          />
        );
      case "section_must_contain":
        return (
          <Space wrap>
            <Select
              style={{ width: 160 }}
              value={String(params.section ?? "")}
              options={sectionOptions}
              onChange={(section) => patchParams(index, { section })}
            />
            <Select
              mode="tags"
              style={{ minWidth: 240 }}
              value={asArray<string>(params.terms)}
              placeholder="必须出现的内容"
              onChange={(terms) => patchParams(index, { terms })}
            />
          </Space>
        );
      case "max_items":
        return (
          <Space wrap>
            <Select
              style={{ width: 160 }}
              value={String(params.section ?? "")}
              options={sectionOptions}
              onChange={(section) => patchParams(index, { section })}
            />
            <InputNumber min={0} addonBefore="最多" addonAfter="条" value={typeof params.max === "number" ? params.max : undefined} onChange={(max) => patchParams(index, { max: max ?? 0 })} />
          </Space>
        );
      case "max_pages":
        return (
          <InputNumber min={0} addonBefore="最多" addonAfter="页" value={typeof params.max === "number" ? params.max : undefined} onChange={(max) => patchParams(index, { max: max ?? 0 })} />
        );
      case "number_range":
        return (
          <Space wrap>
            <Input style={{ width: 200 }} value={String(params.path ?? "")} placeholder="槽位路径，如 company.wow" onChange={(event) => patchParams(index, { path: event.target.value })} />
            <InputNumber placeholder="最小" value={typeof params.min === "number" ? params.min : undefined} onChange={(min) => patchParams(index, { min: min ?? undefined })} />
            <InputNumber placeholder="最大" value={typeof params.max === "number" ? params.max : undefined} onChange={(max) => patchParams(index, { max: max ?? undefined })} />
          </Space>
        );
      default:
        return null;
    }
  };

  return (
    <Space direction="vertical" size={12} style={{ width: "100%" }}>
      <Typography.Text type="secondary">
        内置质检可以开关、调阈值，但不能删除；自定义规则从模板新增，可以删除。保存后对新任务生效。
      </Typography.Text>

      <RuleRow
        label="异常阈值"
        tag={BUILTIN_TAG}
        enabled={Boolean(asRecord(builtin.anomaly_coverage).enabled)}
        onToggle={(checked) => updateBuiltin("anomaly_coverage", (config) => void (config.enabled = checked))}
      >
        <InputNumber
          min={0}
          max={100}
          addonAfter={PERCENT}
          value={percentOf(asRecord(builtin.anomaly_coverage).threshold)}
          onChange={(next) => updateBuiltin("anomaly_coverage", (config) => void (config.threshold = ratioOf(next) ?? 0))}
        />
      </RuleRow>

      <RuleRow
        label="禁用指标"
        tag={BUILTIN_TAG}
        enabled={Boolean(asRecord(builtin.forbidden_terms).enabled)}
        onToggle={(checked) => updateBuiltin("forbidden_terms", (config) => void (config.enabled = checked))}
      >
        <Select
          mode="tags"
          style={{ width: "100%" }}
          value={asArray<string>(asRecord(builtin.forbidden_terms).terms)}
          placeholder="输入禁用词后回车"
          onChange={(terms) => updateBuiltin("forbidden_terms", (config) => void (config.terms = terms))}
        />
      </RuleRow>

      <RuleRow
        label="篇幅上限"
        tag={BUILTIN_TAG}
        enabled={Boolean(asRecord(builtin.length).enabled)}
        onToggle={(checked) => updateBuiltin("length", (config) => void (config.enabled = checked))}
      >
        <InputNumber
          min={1}
          addonAfter="页"
          value={asRecord(builtin.length).maxPages}
          onChange={(next) => updateBuiltin("length", (config) => void (config.maxPages = next ?? 1))}
        />
      </RuleRow>

      <RuleRow
        label="结构完整"
        tag={BUILTIN_TAG}
        enabled={Boolean(asRecord(builtin.structure).enabled)}
        onToggle={(checked) => updateBuiltin("structure", (config) => void (config.enabled = checked))}
      >
        <Space wrap>
          <span>
            摘要最多
            <InputNumber
              min={1}
              style={{ width: 90, margin: "0 6px" }}
              value={asRecord(builtin.structure).summaryMaxSentences}
              onChange={(next) => updateBuiltin("structure", (config) => void (config.summaryMaxSentences = next ?? 1))}
            />
            句
          </span>
          <span>
            下周关注最多
            <InputNumber
              min={1}
              style={{ width: 90, margin: "0 6px" }}
              value={asRecord(builtin.structure).nextWeekMaxItems}
              onChange={(next) => updateBuiltin("structure", (config) => void (config.nextWeekMaxItems = next ?? 1))}
            />
            条
          </span>
          <span>
            图表至少
            <InputNumber
              min={0}
              style={{ width: 90, margin: "0 6px" }}
              value={asRecord(builtin.structure).minCharts}
              onChange={(next) => updateBuiltin("structure", (config) => void (config.minCharts = next ?? 0))}
            />
            张
          </span>
        </Space>
      </RuleRow>

      <RuleRow
        label="数据溯源"
        tag={BUILTIN_TAG}
        enabled={Boolean(asRecord(builtin.traceability).enabled)}
        onToggle={(checked) => updateBuiltin("traceability", (config) => void (config.enabled = checked))}
      >
        <Typography.Text type="secondary">叙述里不能有未经槽位引用的数字</Typography.Text>
      </RuleRow>

      <RuleRow
        label="引用真实"
        tag={BUILTIN_TAG}
        enabled={Boolean(asRecord(builtin.quote_integrity).enabled)}
        onToggle={(checked) => updateBuiltin("quote_integrity", (config) => void (config.enabled = checked))}
      >
        <Typography.Text type="secondary">原文摘录必须是纪要原文的连续片段</Typography.Text>
      </RuleRow>

      <RuleRow
        label="意见响应"
        tag={BUILTIN_TAG}
        enabled={Boolean(asRecord(builtin.feedback_response).enabled)}
        onToggle={(checked) => updateBuiltin("feedback_response", (config) => void (config.enabled = checked))}
      >
        <Typography.Text type="secondary">仅重做时检查退回意见是否被采纳</Typography.Text>
      </RuleRow>

      <RuleRow
        label="纪要矛盾覆盖"
        tag={BUILTIN_TAG}
        enabled={Boolean(asRecord(builtin.contradiction_coverage).enabled)}
        onToggle={(checked) => updateBuiltin("contradiction_coverage", (config) => void (config.enabled = checked))}
      >
        <Typography.Text type="secondary">程序判出的每条纪要矛盾都要写进「异常与原因」并引用矛盾 id</Typography.Text>
      </RuleRow>

      <RuleRow label="纪要矛盾阈值">
        <Space wrap>
          <span>
            持平容差
            <InputNumber
              min={0}
              max={100}
              addonAfter={PERCENT}
              style={{ margin: "0 6px" }}
              value={percentOf(contradiction.flatTolerance)}
              onChange={(next) => updateContradiction((config) => void (config.flatTolerance = ratioOf(next) ?? 0))}
            />
          </span>
          <span>
            方向最小幅度
            <InputNumber
              min={0}
              max={100}
              addonAfter={PERCENT}
              style={{ margin: "0 6px" }}
              value={percentOf(contradiction.directionMin)}
              onChange={(next) => updateContradiction((config) => void (config.directionMin = ratioOf(next) ?? 0))}
            />
          </span>
          <span>
            幅度偏差
            <InputNumber
              min={0}
              max={100}
              addonAfter={PERCENT}
              style={{ margin: "0 6px" }}
              value={percentOf(contradiction.magnitudeTolerance)}
              onChange={(next) => updateContradiction((config) => void (config.magnitudeTolerance = ratioOf(next) ?? 0))}
            />
          </span>
        </Space>
      </RuleRow>

      {custom.map((rule, index) => (
        <RuleRow
          key={String(rule.id ?? index)}
          label={String(rule.label ?? rule.template)}
          tag={CUSTOM_TAG}
          enabled={Boolean(rule.enabled)}
          onToggle={(checked) => patchRule(index, { enabled: checked })}
        >
          <Space direction="vertical" size={6} style={{ width: "100%" }}>
            <Space wrap>
              <Input
                size="small"
                style={{ width: 220 }}
                value={String(rule.label ?? "")}
                onChange={(event) => patchRule(index, { label: event.target.value })}
                placeholder="规则名称"
              />
              <Tag>{QC_TEMPLATE_LABELS[rule.template as QcTemplate] ?? String(rule.template)}</Tag>
              <Typography.Text type="secondary" code>
                {String(rule.template)}
              </Typography.Text>
              <Popconfirm title={`删除规则「${String(rule.label)}」？`} onConfirm={() => setCustom(custom.filter((_, i) => i !== index))}>
                <Button size="small" danger type="text" icon={<DeleteOutlined />}>
                  删除
                </Button>
              </Popconfirm>
            </Space>
            {paramsEditor(rule, index)}
          </Space>
        </RuleRow>
      ))}

      <Dropdown
        trigger={["click"]}
        menu={{
          items: QC_TEMPLATES.map((template) => ({ key: template, label: QC_TEMPLATE_LABELS[template] })),
          onClick: ({ key }) => addRule(key as QcTemplate),
        }}
      >
        <Button type="dashed" block icon={<PlusOutlined />}>
          从模板新增规则
        </Button>
      </Dropdown>
    </Space>
  );
}
