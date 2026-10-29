// 模型连接子页：多连接编辑、测试连接、API Key 打码与环境变量来源。
import { useEffect, useState } from "react";
import { App as AntApp, Button, Input, Popconfirm, Space, Tag, Tooltip, Typography } from "antd";
import {
  ApiOutlined,
  CheckCircleFilled,
  CloseCircleFilled,
  DeleteOutlined,
  DownOutlined,
  InfoCircleOutlined,
  PlusOutlined,
  RightOutlined,
  RobotOutlined,
  SaveOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";
import type { ConnectionTestResult, SettingsApi, SettingsConnectionView } from "../../api/settings";

const { Text } = Typography;

const row: React.CSSProperties = { display: "flex", gap: 16, alignItems: "flex-start", marginBottom: 16 };
const labelStyle: React.CSSProperties = { width: 96, flex: "none", color: "var(--muted)", paddingTop: 6 };
const cardStyle: React.CSSProperties = {
  border: "1px solid var(--line)",
  borderRadius: 12,
  background: "#fff",
  marginBottom: 12,
  overflow: "hidden",
};
const headerStyle: React.CSSProperties = { display: "flex", alignItems: "center", gap: 10, padding: "12px 16px", cursor: "pointer" };
const bodyStyle: React.CSSProperties = { borderTop: "1px solid var(--line)", padding: "16px 20px 8px" };

interface DraftState {
  baseUrl: string;
  apiKey: string;
  models: string[];
}

type TestState = ConnectionTestResult | "loading" | undefined;

function statusOf(test: TestState, connection: SettingsConnectionView) {
  if (test === "loading") return { dot: "loading" as const, color: "var(--muted)", text: "测试中…" };
  if (test) {
    return test.ok
      ? { dot: "ok" as const, color: "var(--primary)", text: `连接正常 · ${test.latencyMs} ms` }
      : { dot: "fail" as const, color: "var(--danger)", text: "连接失败" };
  }
  return connection.hasKey
    ? { dot: "idle" as const, color: "var(--muted)", text: "未测试" }
    : { dot: "idle" as const, color: "var(--muted)", text: "未配置 Key" };
}

/** 可增删的模型标签编辑器。 */
function ModelTags({ value, onChange }: { value: string[]; onChange: (models: string[]) => void }) {
  const [input, setInput] = useState("");
  const add = () => {
    const model = input.trim();
    if (!model) return;
    if (!value.includes(model)) onChange([...value, model]);
    setInput("");
  };
  return (
    <div style={{ flex: 1, display: "flex", flexWrap: "wrap", gap: 6, alignItems: "center" }}>
      {value.map((model) => (
        <Tag key={model} closable onClose={() => onChange(value.filter((m) => m !== model))} style={{ marginInlineEnd: 0 }}>
          {model}
        </Tag>
      ))}
      <Space.Compact size="small">
        <Input
          size="small"
          style={{ width: 140 }}
          placeholder="添加模型"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onPressEnter={add}
        />
        <Button size="small" onClick={add}>
          添加
        </Button>
      </Space.Compact>
    </div>
  );
}

function ConnectionCard({
  api,
  connection,
  onChanged,
}: {
  api: SettingsApi;
  connection: SettingsConnectionView;
  onChanged: () => void;
}) {
  const { message } = AntApp.useApp();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<DraftState>({ baseUrl: connection.baseUrl, apiKey: "", models: connection.models });
  const [test, setTest] = useState<TestState>();
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setDraft({ baseUrl: connection.baseUrl, apiKey: "", models: connection.models });
  }, [connection]);

  const status = statusOf(test, connection);

  const save = async () => {
    if (!draft.baseUrl.trim()) return message.warning("请填写 Base URL");
    if (draft.models.length === 0) return message.warning("请至少添加一个模型");
    setSaving(true);
    try {
      const res = await api.saveConnection(connection.name, {
        baseUrl: draft.baseUrl.trim(),
        apiKey: draft.apiKey.trim() === "" ? undefined : draft.apiKey.trim(),
        models: draft.models,
      });
      if (res.warning) message.warning(`已保存，但重建模型提供者失败：${res.warning}`);
      else message.success("已保存，对新的判断步骤生效");
      setDraft((d) => ({ ...d, apiKey: "" }));
      onChanged();
    } catch (e) {
      message.error(e instanceof Error ? e.message : "保存失败");
    } finally {
      setSaving(false);
    }
  };

  const runTest = async () => {
    setTest("loading");
    try {
      setTest(await api.testConnection(connection.name, draft.models[0]));
    } catch (e) {
      setTest({ ok: false, latencyMs: 0, model: draft.models[0] ?? "", error: e instanceof Error ? e.message : "测试失败" });
    }
  };

  return (
    <div style={cardStyle}>
      <div style={headerStyle} onClick={() => setOpen((v) => !v)}>
        <CloudLogo name={connection.name} />
        <span style={{ fontWeight: 600, fontSize: 16 }}>{connection.name}</span>
        {connection.isDefault && <Tag color="green">默认</Tag>}
        {connection.source === "env" && (
          <Tooltip title="环境变量 DEEPSEEK_API_KEY 优先于此处设置">
            <Tag color="orange">来自环境变量</Tag>
          </Tooltip>
        )}
        <div style={{ flex: 1 }} />
        <span style={{ display: "inline-flex", alignItems: "center", gap: 6, color: status.color }}>
          {status.dot === "ok" ? <CheckCircleFilled /> : status.dot === "fail" ? <CloseCircleFilled /> : <span className="dot default" />}
          {status.text}
        </span>
        {open ? <DownOutlined /> : <RightOutlined />}
      </div>
      {test && test !== "loading" && !test.ok && test.error && (
        <div style={{ padding: "0 20px 10px", color: "var(--danger)", fontSize: 12 }}>错误：{test.error}</div>
      )}
      {open && (
        <div style={bodyStyle}>
          <div style={row}>
            <div style={labelStyle}>名称</div>
            <div style={{ flex: 1 }}>
              <Input value={connection.name} disabled />
            </div>
          </div>
          <div style={row}>
            <div style={labelStyle}>Base URL</div>
            <div style={{ flex: 1 }}>
              <Input value={draft.baseUrl} onChange={(e) => setDraft((d) => ({ ...d, baseUrl: e.target.value }))} placeholder="https://api.deepseek.com" />
            </div>
          </div>
          <div style={row}>
            <div style={labelStyle}>API Key</div>
            <div style={{ flex: 1 }}>
              <Input.Password
                value={draft.apiKey}
                onChange={(e) => setDraft((d) => ({ ...d, apiKey: e.target.value }))}
                placeholder={
                  connection.hasKey
                    ? `已保存（末 4 位 ${connection.apiKeyLast4}），留空保持原值`
                    : "请输入 API Key"
                }
                autoComplete="new-password"
              />
              <div style={{ color: "var(--muted)", fontSize: 12, marginTop: 4 }}>
                明文保存在本机 settings.json；界面与日志只显示末 4 位。
              </div>
            </div>
          </div>
          <div style={row}>
            <div style={labelStyle}>模型</div>
            <ModelTags value={draft.models} onChange={(models) => setDraft((d) => ({ ...d, models }))} />
          </div>
          <div style={{ display: "flex", justifyContent: "flex-end", gap: 8, paddingBottom: 8 }}>
            <Button icon={<ThunderboltOutlined />} loading={test === "loading"} onClick={runTest}>
              测试连接
            </Button>
            <Button type="primary" icon={<SaveOutlined />} loading={saving} onClick={save}>
              保存
            </Button>
            <Popconfirm
              title="删除这个连接？"
              description="使用该连接的任务配置已复制到任务目录，不受影响；新任务将不能再引用它。"
              okText="删除"
              cancelText="取消"
              okButtonProps={{ danger: true }}
              onConfirm={async () => {
                try {
                  await api.deleteConnection(connection.name);
                  message.success("已删除");
                  onChanged();
                } catch (e) {
                  message.error(e instanceof Error ? e.message : "删除失败");
                }
              }}
            >
              <Button danger icon={<DeleteOutlined />} />
            </Popconfirm>
          </div>
        </div>
      )}
    </div>
  );
}

function CloudLogo({ name }: { name: string }) {
  const colors = ["#e8f0ff", "#e8f5f1", "#fff3e0", "#f3e8ff"];
  const index = name.length % colors.length;
  return (
    <div
      style={{
        width: 34,
        height: 34,
        borderRadius: 10,
        background: colors[index],
        display: "grid",
        placeItems: "center",
        color: "var(--primary)",
        flex: "none",
      }}
    >
      {name.toLowerCase().includes("deepseek") ? <ApiOutlined /> : <RobotOutlined />}
    </div>
  );
}

function NewConnectionCard({ api, onCreated, onCancel }: { api: SettingsApi; onCreated: () => void; onCancel: () => void }) {
  const { message } = AntApp.useApp();
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [models, setModels] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);

  const save = async () => {
    if (!name.trim()) return message.warning("请填写连接名称");
    if (!baseUrl.trim()) return message.warning("请填写 Base URL");
    if (models.length === 0) return message.warning("请至少添加一个模型");
    setSaving(true);
    try {
      await api.saveConnection(name.trim(), { baseUrl: baseUrl.trim(), apiKey: apiKey.trim(), models });
      message.success("连接已创建");
      onCreated();
    } catch (e) {
      message.error(e instanceof Error ? e.message : "创建失败");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div style={{ ...cardStyle, marginBottom: 12 }}>
      <div style={headerStyle}>
        <span style={{ fontWeight: 600, fontSize: 16 }}>新增连接</span>
        <div style={{ flex: 1 }} />
        <Button size="small" type="text" onClick={onCancel}>
          取消
        </Button>
      </div>
      <div style={{ ...bodyStyle, borderTop: "none" }}>
        <div style={row}>
          <div style={labelStyle}>名称</div>
          <div style={{ flex: 1 }}>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 通义千问、GLM、本地 Qwen" />
          </div>
        </div>
        <div style={row}>
          <div style={labelStyle}>Base URL</div>
          <div style={{ flex: 1 }}>
            <Input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} placeholder="https://…/v1" />
          </div>
        </div>
        <div style={row}>
          <div style={labelStyle}>API Key</div>
          <div style={{ flex: 1 }}>
            <Input.Password value={apiKey} onChange={(e) => setApiKey(e.target.value)} autoComplete="new-password" />
          </div>
        </div>
        <div style={row}>
          <div style={labelStyle}>模型</div>
          <ModelTags value={models} onChange={setModels} />
        </div>
        <div style={{ display: "flex", justifyContent: "flex-end", paddingBottom: 8 }}>
          <Button type="primary" icon={<SaveOutlined />} loading={saving} onClick={save}>
            保存
          </Button>
        </div>
      </div>
    </div>
  );
}

export function ModelConnections({ api }: { api: SettingsApi }) {
  const { message } = AntApp.useApp();
  const [view, setView] = useState<Awaited<ReturnType<SettingsApi["getSettings"]>>>();
  const [loading, setLoading] = useState(true);
  const [adding, setAdding] = useState(false);

  const load = () => {
    setLoading(true);
    api
      .getSettings()
      .then(setView, (e) => message.error(e instanceof Error ? e.message : "读取设置失败"))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api]);

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", marginBottom: 16 }}>
        <div style={{ flex: 1 }}>
          <h2 style={{ margin: 0, fontSize: 24 }}>模型连接</h2>
          <div style={{ color: "var(--muted)", marginTop: 4 }}>
            任务里的判断步骤用这些连接调用模型；保存后对新的判断步骤生效。
          </div>
        </div>
        {!adding && (
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setAdding(true)}>
            新增连接
          </Button>
        )}
      </div>

      {adding && (
        <NewConnectionCard
          api={api}
          onCreated={() => {
            setAdding(false);
            load();
          }}
          onCancel={() => setAdding(false)}
        />
      )}

      {loading && !view && <div style={{ color: "var(--muted)" }}>读取中…</div>}
      {view?.connections.map((connection) => (
        <ConnectionCard key={connection.name} api={api} connection={connection} onChanged={load} />
      ))}
      {view?.connections.length === 0 && <Text type="secondary">还没有模型连接，点右上角“新增连接”。</Text>}

      <div style={{ display: "flex", gap: 8, alignItems: "center", color: "var(--muted)", fontSize: 13, marginTop: 16 }}>
        <InfoCircleOutlined />
        环境变量 DEEPSEEK_API_KEY 优先于此处设置（{view?.envOverride ? "当前生效中" : "当前未设置"}），密钥不会写入日志和交付物
      </div>
    </div>
  );
}
