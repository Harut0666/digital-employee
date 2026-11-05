// 工作步骤：步骤卡片（只能改参数，不能增删和排序；可选步骤可以开关）写回 workflow.yaml。
// 判断步骤的提示词是独立文件（prompts/*.md），在这里直接编辑并作为单独文件保存。
// T20：判断步骤增加“思考强度”（写回 model.thinking），任务级增加工具重试退避编辑（limits.toolRetry.backoffMs）。
import { Alert, Card, Collapse, Input, InputNumber, Select, Space, Switch, Tag, Typography } from "antd";
import { THINKING_LEVELS, TOOL_NAMES, type ThinkingLevel } from "@de/core/contract";
import { asArray, asRecord, textOf, type Json, type TabProps } from "./helpers";

const EDITABLE_TOOLS = TOOL_NAMES.filter((tool) => tool !== "web_search");

/** 推理档位的中文标签；不设置时跟随连接上的模型默认档位。 */
const THINKING_LABELS: Record<ThinkingLevel, string> = {
  off: "关闭",
  minimal: "最低",
  low: "低",
  medium: "中",
  high: "高",
  xhigh: "最高",
};

/** 逗号 / 空格分隔的毫秒数：只保留合法数字（非法输入由保存时的配置校验兜底拒绝）。 */
function parseBackoff(text: string): number[] {
  return text
    .split(/[,，\s]+/)
    .map((part) => part.trim())
    .filter((part) => part !== "")
    .map(Number)
    .filter((value) => Number.isFinite(value) && value >= 0);
}

const KIND_LABEL: Record<string, { text: string; color: string }> = {
  compute: { text: "计算步骤", color: "blue" },
  judge: { text: "判断步骤", color: "purple" },
  qc: { text: "质检步骤", color: "green" },
};

export function StepsTab({ config, drafts, setData, setContent }: TabProps) {
  const workflow = (drafts["workflow.yaml"]?.data ?? config.files.find((f) => f.path === "workflow.yaml")?.parsed ?? {}) as Json;
  const steps = asArray<Json>(workflow.steps);
  const limits = asRecord(workflow.limits);
  const taskLimits = asRecord(limits.task);
  const toolRetry = asRecord(limits.toolRetry);

  const update = (mutate: (draft: Json) => void) => {
    const next = structuredClone(workflow);
    mutate(next);
    setData("workflow.yaml", next);
  };

  const updateStep = (index: number, mutate: (step: Json) => void) => {
    update((draft) => {
      const step = asArray<Json>(draft.steps)[index];
      if (step) mutate(step);
    });
  };

  return (
    <Space direction="vertical" size={16} style={{ width: "100%" }}>
      <Alert
        type="info"
        showIcon
        message="步骤顺序与数量由计算步骤之间的数据依赖决定，这里只能修改参数；标为“可选”的步骤可以关闭。"
        description="判断步骤的“思考强度”不设置时跟随模型连接上的默认档位（都没配置按“关闭”）；步骤超时与质检自动修正次数留空时，用设置页「执行与预算」里的值（内置 120 秒 / 3 次）。"
      />

      <Card size="small" title="任务级上限">
        <Space size={24} wrap>
          <label>
            <Typography.Text type="secondary">模型调用次数</Typography.Text>
            <InputNumber
              min={1}
              value={taskLimits.modelCalls}
              onChange={(value) =>
                update((draft) => {
                  draft.limits = draft.limits ?? {};
                  draft.limits.task = draft.limits.task ?? {};
                  draft.limits.task.modelCalls = value ?? 1;
                })
              }
            />
          </label>
          <label>
            <Typography.Text type="secondary">任务时长上限（秒）</Typography.Text>
            <InputNumber
              min={1}
              value={taskLimits.durationSec}
              onChange={(value) =>
                update((draft) => {
                  draft.limits = draft.limits ?? {};
                  draft.limits.task = draft.limits.task ?? {};
                  draft.limits.task.durationSec = value ?? 1;
                })
              }
            />
          </label>
          <label>
            <Typography.Text type="secondary">工具重试次数</Typography.Text>
            <InputNumber
              min={1}
              value={toolRetry.attempts}
              onChange={(value) =>
                update((draft) => {
                  draft.limits = draft.limits ?? {};
                  draft.limits.toolRetry = draft.limits.toolRetry ?? {};
                  draft.limits.toolRetry.attempts = value ?? 1;
                })
              }
            />
          </label>
          <label>
            <Typography.Text type="secondary">工具重试退避（毫秒，留空用设置值）</Typography.Text>
            <Input
              style={{ width: 180 }}
              value={asArray<number>(toolRetry.backoffMs).join(",")}
              placeholder="500,1500,4000"
              onChange={(event) =>
                update((draft) => {
                  draft.limits = draft.limits ?? {};
                  draft.limits.toolRetry = draft.limits.toolRetry ?? {};
                  draft.limits.toolRetry.backoffMs = parseBackoff(event.target.value);
                })
              }
            />
          </label>
          <label>
            <Typography.Text type="secondary">质检自动修正次数（留空用设置值）</Typography.Text>
            <InputNumber
              min={0}
              placeholder="默认（设置值）"
              value={limits.qcAutofix}
              onChange={(value) =>
                update((draft) => {
                  draft.limits = draft.limits ?? {};
                  if (value === null) delete draft.limits.qcAutofix;
                  else draft.limits.qcAutofix = value;
                })
              }
            />
          </label>
        </Space>
      </Card>

      {steps.map((step, index) => {
        const kind = String(step.kind ?? "compute");
        const label = KIND_LABEL[kind] ?? KIND_LABEL.compute!;
        const stepLimits = asRecord(step.limits);
        const model = asRecord(step.model);
        const promptPath = typeof step.prompt === "string" ? step.prompt : undefined;
        return (
          <Card
            key={String(step.id)}
            size="small"
            title={
              <Space size={8} wrap>
                <Typography.Text strong>
                  {index + 1}. {String(step.label ?? step.id)}
                </Typography.Text>
                <Tag color={label.color}>{label.text}</Tag>
                <Typography.Text type="secondary" code>
                  {String(step.id)}
                </Typography.Text>
                {step.optional ? <Tag>可选</Tag> : null}
              </Space>
            }
            extra={
              kind !== "qc" ? (
                <Space size={6}>
                  <Typography.Text type="secondary">可选步骤</Typography.Text>
                  <Switch
                    size="small"
                    checked={Boolean(step.optional)}
                    onChange={(checked) => updateStep(index, (draft) => void (draft.optional = checked))}
                  />
                </Space>
              ) : null
            }
          >
            {kind === "compute" ? (
              <Typography.Text type="secondary">
                工具：<Typography.Text code>{String(step.tool ?? "")}</Typography.Text>（计算步骤由程序直接调用工具，不经过模型）
              </Typography.Text>
            ) : kind === "qc" ? (
              <Typography.Text type="secondary">质检由程序执行，规则在「质检规则」标签页配置。</Typography.Text>
            ) : (
              <Space direction="vertical" size={12} style={{ width: "100%" }}>
                <Space size={24} wrap>
                  <label>
                    <Typography.Text type="secondary">轮次上限</Typography.Text>
                    <InputNumber
                      min={1}
                      value={stepLimits.rounds}
                      onChange={(value) => updateStep(index, (draft) => void (((draft.limits ??= {}).rounds = value ?? 1)))}
                    />
                  </label>
                  <label>
                    <Typography.Text type="secondary">步骤超时（秒，留空用设置值）</Typography.Text>
                    <InputNumber
                      min={1}
                      placeholder="默认（设置值）"
                      value={stepLimits.durationSec}
                      onChange={(value) =>
                        updateStep(index, (draft) => {
                          draft.limits = draft.limits ?? {};
                          if (value === null) delete draft.limits.durationSec;
                          else draft.limits.durationSec = value;
                        })
                      }
                    />
                  </label>
                  <label>
                    <Typography.Text type="secondary">思考强度</Typography.Text>
                    <Select<ThinkingLevel>
                      allowClear
                      style={{ width: 140 }}
                      placeholder="跟随连接默认"
                      value={typeof model.thinking === "string" ? (model.thinking as ThinkingLevel) : undefined}
                      onChange={(value) =>
                        updateStep(index, (draft) => {
                          draft.model = draft.model ?? {};
                          if (value === undefined) delete draft.model.thinking;
                          else draft.model.thinking = value;
                        })
                      }
                      options={THINKING_LEVELS.map((level) => ({ value: level, label: THINKING_LABELS[level] }))}
                    />
                  </label>
                  <label>
                    <Typography.Text type="secondary">连接</Typography.Text>
                    <Input
                      style={{ width: 140 }}
                      value={String(model.connection ?? "default")}
                      onChange={(event) => updateStep(index, (draft) => void (((draft.model ??= {}).connection = event.target.value)))}
                    />
                  </label>
                  <label>
                    <Typography.Text type="secondary">模型</Typography.Text>
                    <Input
                      style={{ width: 160 }}
                      value={String(model.model ?? "default")}
                      onChange={(event) => updateStep(index, (draft) => void (((draft.model ??= {}).model = event.target.value)))}
                    />
                  </label>
                </Space>
                <div>
                  <Typography.Text type="secondary">开放工具</Typography.Text>
                  <Select
                    mode="multiple"
                    style={{ width: "100%" }}
                    value={asArray<string>(step.tools)}
                    options={EDITABLE_TOOLS.map((tool) => ({ value: tool, label: tool }))}
                    onChange={(tools) => updateStep(index, (draft) => void (draft.tools = tools))}
                    placeholder="默认不开放任何工具"
                  />
                </div>
                {promptPath ? (
                  <Collapse
                    size="small"
                    items={[
                      {
                        key: "prompt",
                        label: (
                          <Space size={8}>
                            <span>提示词</span>
                            <Typography.Text type="secondary" code>
                              {promptPath}
                            </Typography.Text>
                          </Space>
                        ),
                        children: (
                          <Input.TextArea
                            rows={14}
                            value={textOf(config, drafts, promptPath)}
                            onChange={(event) => setContent(promptPath, event.target.value)}
                          />
                        ),
                      },
                    ]}
                  />
                ) : null}
              </Space>
            )}
          </Card>
        );
      })}
    </Space>
  );
}
