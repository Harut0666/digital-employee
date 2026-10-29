// 演示与调试子页：工具故障（经服务端 admin_set_faults 下发）、数据溯源故障、模拟崩溃。
// 每个开关旁都写清触发后果，方便现场讲解。
import { useEffect, useState } from "react";
import { Alert, App as AntApp, Divider, Select, Space, Switch, Tag, Typography } from "antd";
import { BugOutlined, ExclamationCircleOutlined, ThunderboltOutlined } from "@ant-design/icons";
import type { DemoState, DemoView, FaultMode, SettingsApi } from "../../api/settings";

const { Text, Paragraph } = Typography;

const MODE_LABEL: Record<FaultMode, string> = {
  timeout: "超时",
  error: "报错",
  invalid_output: "返回非法数据",
};

const MODE_OPTIONS = [
  { value: "off", label: "关闭" },
  { value: "timeout", label: "超时（一直不返回）" },
  { value: "error", label: "报错（立即失败）" },
  { value: "invalid_output", label: "返回非法数据（Schema 校验失败）" },
];

/** 写清“打开后会发生什么”，现场演示时照着念即可。 */
function effectOf(tool: string, mode: FaultMode | undefined): string {
  switch (mode) {
    case "timeout":
      return `下一次调用 ${tool} 时超时（工具卡住不返回），触发重试；重试用尽后任务停在「待人工处理」。`;
    case "error":
      return `下一次调用 ${tool} 时立即报错，触发重试；重试用尽后任务停在「待人工处理」。`;
    case "invalid_output":
      return `下一次调用 ${tool} 时返回不符合 Schema 的数据，客户端校验为失败，触发重试；重试用尽后任务停在「待人工处理」。`;
    default:
      return "关闭：工具正常执行。";
  }
}

const card: React.CSSProperties = { border: "1px solid var(--line)", borderRadius: 12, background: "#fff", padding: "16px 20px", marginBottom: 16 };
const toolRow: React.CSSProperties = { display: "flex", alignItems: "flex-start", gap: 16, padding: "12px 0", borderTop: "1px solid var(--line)" };
const switchRow: React.CSSProperties = { display: "flex", alignItems: "flex-start", gap: 16, padding: "12px 0", borderTop: "1px solid var(--line)" };

export function DemoDebug({ api }: { api: SettingsApi }) {
  const { message } = AntApp.useApp();
  const [demo, setDemo] = useState<DemoView>();
  const [saving, setSaving] = useState<string>();

  const load = () => {
    api.getDemo().then(setDemo, (e) => message.error(e instanceof Error ? e.message : "读取演示设置失败"));
  };
  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api]);

  /** 保存一次开关改动：立即写 settings.json 并同步下发（工具故障经 admin_set_faults）。 */
  const save = async (patch: Partial<DemoState>, key: string) => {
    setSaving(key);
    try {
      const res = await api.saveDemo(patch);
      setDemo((prev) => (prev ? { ...prev, ...res.demo, tools: res.tools } : prev));
      if (res.toolError) message.warning(`设置已保存，但没有下发到工具服务器：${res.toolError}`);
      else message.success("已生效");
      if (patch.crashOnNextStep) message.warning("已布防：进程会在下一个步骤开始执行后退出。");
    } catch (e) {
      message.error(e instanceof Error ? e.message : "保存失败");
    } finally {
      setSaving(undefined);
    }
  };

  const changeTool = (tool: string, value: string) => {
    const next = { ...(demo?.toolFaults ?? {}) };
    if (value === "off") delete next[tool];
    else next[tool] = value as FaultMode;
    return save({ toolFaults: next }, tool);
  };

  if (!demo) return <div style={{ color: "var(--muted)" }}>读取中…</div>;

  return (
    <div>
      <h2 style={{ margin: 0, fontSize: 24 }}>演示与调试</h2>
      <div style={{ color: "var(--muted)", margin: "4px 0 16px" }}>
        故障注入只影响演示，不会写进交付物；开关保存在工作区 settings.json，重启后仍保留。
      </div>

      <div style={card}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <BugOutlined style={{ color: "var(--warn)" }} />
          <span style={{ fontWeight: 600, fontSize: 16 }}>工具故障</span>
          <Tag color="orange">工具子进程</Tag>
        </div>
        <Paragraph type="secondary" style={{ margin: "6px 0 0", fontSize: 13 }}>
          打开某个工具的故障后，任务下一次调用该工具会先重试（3 次，退避 1 / 2 / 4 秒）；仍失败则暂停在「待人工处理」。
          关闭后由操作员点「重试」即可从失败的步骤继续。
        </Paragraph>
        {demo.tools.map((tool) => {
          const mode = demo.toolFaults[tool.name];
          return (
            <div key={tool.name} style={toolRow}>
              <div style={{ width: 220, flex: "none" }}>
                <div style={{ fontWeight: 500 }}>{tool.label}</div>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  {tool.name}
                </Text>
              </div>
              <div style={{ flex: 1 }}>
                <Space>
                  <Select
                    style={{ width: 250 }}
                    value={mode ?? "off"}
                    options={MODE_OPTIONS}
                    loading={saving === tool.name}
                    onChange={(value) => changeTool(tool.name, value)}
                  />
                  {mode && <Tag color="orange">故障已布防</Tag>}
                </Space>
                <div style={{ color: "var(--muted)", fontSize: 12, marginTop: 6 }}>{effectOf(tool.name, mode)}</div>
              </div>
            </div>
          );
        })}
      </div>

      <div style={card}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <ThunderboltOutlined style={{ color: "var(--info)" }} />
          <span style={{ fontWeight: 600, fontSize: 16 }}>引擎故障</span>
          <Tag color="blue">服务端进程</Tag>
        </div>
        <Paragraph type="secondary" style={{ margin: "6px 0 0", fontSize: 13 }}>
          这两项在服务端进程内生效，不需要工具服务器参与。都是一次性的，触发后自动关闭。
        </Paragraph>

        <div style={switchRow}>
          <div style={{ flex: 1 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 8, fontWeight: 500 }}>
              数据溯源故障
              <Tag color={demo.corruptTraceability ? "orange" : "default"}>{demo.corruptTraceability ? "已布防" : "关闭"}</Tag>
            </div>
            <div style={{ color: "var(--muted)", fontSize: 13, marginTop: 4 }}>
              打开后，下一次撰写周报的正文会被注入一个与表格不符的数字。质检会判「数据溯源」不通过 → 自动修正 → 重新质检通过。
              用于演示赛题示例 2；生效一次后自动关闭。
            </div>
          </div>
          <Switch
            loading={saving === "corrupt"}
            checked={demo.corruptTraceability}
            onChange={(checked) => save({ corruptTraceability: checked }, "corrupt")}
          />
        </div>

        <div style={switchRow}>
          <div style={{ flex: 1 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 8, fontWeight: 500 }}>
              模拟程序崩溃
              <Tag color={demo.crashOnNextStep ? "red" : "default"}>{demo.crashOnNextStep ? "已布防" : "关闭"}</Tag>
            </div>
            <div style={{ color: "var(--muted)", fontSize: 13, marginTop: 4 }}>
              打开后，进程会在下一个步骤开始执行后先写入 fault_injected 事件、清除这个一次性标记，然后真的退出（进程退出码 1）。
              重启服务后会从断点继续，已完成的步骤不重跑。生效一次后不会再次崩溃。
            </div>
            {demo.crashOnNextStep && (
              <Alert
                style={{ marginTop: 8 }}
                type="warning"
                showIcon
                icon={<ExclamationCircleOutlined />}
                message="已布防：下一次运行步骤时服务进程会退出。重启服务即可继续演示断点恢复。"
              />
            )}
          </div>
          <Switch
            loading={saving === "crash"}
            checked={demo.crashOnNextStep}
            onChange={(checked) => save({ crashOnNextStep: checked }, "crash")}
          />
        </div>
      </div>

      <Divider style={{ margin: "8px 0 12px" }} />
      <div style={{ color: "var(--muted)", fontSize: 12 }}>
        提示：工具故障在工具子进程里；数据溯源与崩溃在服务端进程里。演示后记得把开关关掉。
      </div>
    </div>
  );
}
