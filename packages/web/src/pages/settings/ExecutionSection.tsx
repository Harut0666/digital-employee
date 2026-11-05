// 执行与预算子页（T22）：步骤超时、模型重试与退避、质检自动修正上限。
//
// 写 `PATCH /api/settings/app` 的 `execution` 分区：表单只是 settings.json 的策展子集，
// 非法输入在本地就拦下来（不发出请求、不写文件），服务端仍会再校验一次。
// 这些值是“默认值”：员工配置里显式配置的步骤超时 / 重试 / 自动修正优先。
import { useEffect, useState } from "react";
import { App as AntApp, Alert, Button, Input, InputNumber, Typography } from "antd";
import { ClockCircleOutlined, ReloadOutlined, SaveOutlined } from "@ant-design/icons";
import { DEFAULT_EXECUTION_SETTINGS, MODEL_RETRIES_MAX, QC_AUTOFIX_MAX, STEP_TIMEOUT_MAX_MS, STEP_TIMEOUT_MIN_MS } from "@de/core/contract";
import type { SettingsApi } from "../../api/settings";
import { formatBackoffInput, validateExecutionForm, type ExecutionFormValues } from "../../view/execution-form";

const { Text } = Typography;

const card: React.CSSProperties = { border: "1px solid var(--line)", borderRadius: 12, background: "var(--card)", padding: "16px 20px", marginBottom: 16 };
const row: React.CSSProperties = { display: "flex", gap: 16, alignItems: "flex-start", padding: "12px 0", borderTop: "1px solid var(--line)" };
const labelStyle: React.CSSProperties = { width: 156, flex: "none", color: "var(--muted)", paddingTop: 6 };
const hintStyle: React.CSSProperties = { color: "var(--muted)", fontSize: 12, marginTop: 6 };

function draftOf(execution: { stepTimeoutMs: number; modelMaxRetries: number; retryBackoffMs: number[]; qcAutofix: number }): ExecutionFormValues {
  return {
    stepTimeoutMs: execution.stepTimeoutMs,
    modelMaxRetries: execution.modelMaxRetries,
    backoffText: formatBackoffInput(execution.retryBackoffMs),
    qcAutofix: execution.qcAutofix,
  };
}

export function ExecutionSection({ api }: { api: SettingsApi }) {
  const { message } = AntApp.useApp();
  const [draft, setDraft] = useState<ExecutionFormValues>();
  const [revision, setRevision] = useState(0);
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState<string>();

  const load = () => {
    api.getAppSettings().then(
      (view) => {
        setRevision(view.revision);
        setDraft(draftOf(view.execution));
        setProblem(undefined);
      },
      (e) => message.error(e instanceof Error ? e.message : "读取设置失败"),
    );
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api]);

  if (!draft) return <div style={{ color: "var(--muted)" }}>读取中…</div>;

  const set = <K extends keyof ExecutionFormValues>(key: K, value: ExecutionFormValues[K]) =>
    setDraft((d) => (d ? { ...d, [key]: value } : d));

  const save = async () => {
    const validation = validateExecutionForm(draft);
    if (!validation.ok) {
      setProblem(validation.message);
      message.warning(validation.message);
      return;
    }
    setSaving(true);
    try {
      const view = await api.patchAppSettings({ revision, execution: validation.patch });
      setRevision(view.revision);
      setDraft(draftOf(view.execution));
      setProblem(undefined);
      message.success("已保存，对之后开始的步骤生效");
    } catch (e) {
      const text = e instanceof Error ? e.message : "保存失败";
      setProblem(text);
      message.error(text);
      // revision 冲突时重新读取，界面拿到服务端最新值再重试
      load();
    } finally {
      setSaving(false);
    }
  };

  const restoreDefaults = () => {
    setDraft(draftOf(DEFAULT_EXECUTION_SETTINGS));
    setProblem(undefined);
    message.info("已填入默认值，点“保存”后写入 settings.json");
  };

  return (
    <div>
      <h2 style={{ margin: 0, fontSize: 24 }}>执行与预算</h2>
      <div style={{ color: "var(--muted)", margin: "4px 0 16px" }}>
        步骤超时、模型重试、工具退避与质检自动修正的默认值。保存后对<strong>之后开始的步骤</strong>生效；正在执行的步骤沿用旧值。
      </div>

      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message="员工配置与连接配置优先"
        description="员工配置里显式写了步骤超时、工具重试退避或质检自动修正上限时，以员工配置为准；连接上写了模型重试次数时以连接为准。这里的值只在这些地方没写时兜底。"
      />

      <div style={card}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <ClockCircleOutlined style={{ color: "var(--primary)" }} />
          <span style={{ fontWeight: 600, fontSize: 16 }}>执行默认值</span>
          <div style={{ flex: 1 }} />
          <Button type="text" size="small" icon={<ReloadOutlined />} onClick={restoreDefaults}>
            恢复默认值
          </Button>
        </div>

        <div style={row}>
          <div style={labelStyle}>步骤默认超时</div>
          <div style={{ flex: 1 }}>
            <InputNumber
              value={draft.stepTimeoutMs}
              min={STEP_TIMEOUT_MIN_MS}
              max={STEP_TIMEOUT_MAX_MS}
              step={1000}
              addonAfter="毫秒"
              style={{ width: 240 }}
              onChange={(value) => set("stepTimeoutMs", value)}
            />
            <div style={hintStyle}>
              单个工作步骤的最大执行时间（{STEP_TIMEOUT_MIN_MS}–{STEP_TIMEOUT_MAX_MS} 毫秒，默认 120000）。
              员工配置里给步骤写了“步骤超时”的以员工配置为准；步骤没写时用这里的值，对之后开始的步骤生效。
            </div>
          </div>
        </div>

        <div style={row}>
          <div style={labelStyle}>模型重试次数</div>
          <div style={{ flex: 1 }}>
            <InputNumber
              value={draft.modelMaxRetries}
              min={0}
              max={MODEL_RETRIES_MAX}
              step={1}
              addonAfter="次"
              style={{ width: 240 }}
              onChange={(value) => set("modelMaxRetries", value)}
            />
            <div style={hintStyle}>
              模型调用失败后的重试次数（0 表示不重试，默认 2）。连接上配置了重试次数时以连接为准，否则用这里的值；对之后开始的步骤生效。
            </div>
          </div>
        </div>

        <div style={row}>
          <div style={labelStyle}>重试退避间隔</div>
          <div style={{ flex: 1 }}>
            <Input
              value={draft.backoffText}
              placeholder="500, 1500, 4000"
              style={{ width: 320 }}
              onChange={(e) => set("backoffText", e.target.value)}
            />
            <div style={hintStyle}>
              逗号分隔的毫秒数，第 N 次重试取第 N 项；最多 10 项、单项不超过 600000 毫秒。默认 500, 1500, 4000。
              员工配置的“工具重试退避”不为空时以员工配置为准；留空（或为空数组）时用这里的值。
            </div>
          </div>
        </div>

        <div style={row}>
          <div style={labelStyle}>质检自动修正上限</div>
          <div style={{ flex: 1 }}>
            <InputNumber
              value={draft.qcAutofix}
              min={0}
              max={QC_AUTOFIX_MAX}
              step={1}
              addonAfter="次"
              style={{ width: 240 }}
              onChange={(value) => set("qcAutofix", value)}
            />
            <div style={hintStyle}>
              质检不通过时自动重跑写作步骤的上限默认值（默认 3）。员工配置里写了自动修正上限时以员工配置为准；留空时用这里的值。
            </div>
          </div>
        </div>

        {problem && <Alert type="error" showIcon style={{ marginTop: 12 }} message={problem} />}

        <div style={{ display: "flex", justifyContent: "flex-end", gap: 8, marginTop: 12 }}>
          <Text type="secondary" style={{ fontSize: 12, alignSelf: "center" }}>
            写入 settings.json 的 execution 分区
          </Text>
          <Button type="primary" icon={<SaveOutlined />} loading={saving} onClick={save}>
            保存
          </Button>
        </div>
      </div>
    </div>
  );
}
