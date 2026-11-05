// 权限与审批分区（Spec 0004「权限与审批」、T23）。
//
// 本机模式，诚实边界：三档预设只写设置文档，用于审批确认与审计留痕；
// 不提供系统级文件沙箱，也不改变操作系统文件权限。切到“完全访问”“从不询问”
// 需要二次确认；确认后服务端写 audit.jsonl（成功/失败都留痕）。
import { useEffect, useState } from "react";
import { Alert, App as AntApp, Button, Divider, Radio, Select, Space, Tag, Typography } from "antd";
import {
  AuditOutlined,
  LockOutlined,
  SafetyCertificateOutlined,
  UserOutlined,
} from "@ant-design/icons";
import type { AppSettingsView, ApprovalPolicy, DefaultRole, SandboxMode } from "@de/core/contract";
import type { SettingsApi } from "../../api/settings";
import { useSetApprovalPolicy } from "../../components/ApprovalPolicy";
import { useDangerConfirm } from "../../components/ConfirmDialog";
import { ROLE_LABELS } from "../../view/permissions";
import { SettingsTransfer } from "./SettingsTransfer";
import {
  APPROVAL_OPTIONS,
  DEFAULT_ROLE_OPTIONS,
  SANDBOX_PRESETS,
  SECURITY_DETAIL,
  SECURITY_NOTE,
  approvalConfirmContent,
  approvalLabel,
  isDangerousApprovalPolicy,
  isDangerousSandboxMode,
  presetOf,
  sandboxConfirmContent,
} from "./permissions";

const { Text } = Typography;

const card: React.CSSProperties = { border: "1px solid var(--line)", borderRadius: 12, background: "var(--card)", padding: "16px 20px", marginBottom: 16 };
const labelStyle: React.CSSProperties = { width: 132, flex: "none", color: "var(--muted)" };

export function PermissionsSection({ api }: { api: SettingsApi }) {
  const { message } = AntApp.useApp();
  const confirmDanger = useDangerConfirm();
  const setApprovalPolicy = useSetApprovalPolicy();
  const [view, setView] = useState<AppSettingsView>();
  const [defaultRole, setDefaultRole] = useState<DefaultRole>("initiator");
  const [saving, setSaving] = useState<string>();
  const [savingRole, setSavingRole] = useState(false);

  const load = () => {
    api.getAppSettings().then(
      (next) => {
        setView(next);
        setDefaultRole(next.permissions.defaultRole);
        // 导入设置或别处改动后，确认框要按最新审批策略走
        setApprovalPolicy(next.permissions.approvalPolicy);
      },
      (e) => message.error(e instanceof Error ? e.message : "读取权限设置失败"),
    );
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api]);

  if (!view) return <div style={{ color: "var(--muted)" }}>读取中…</div>;

  const current = view.permissions;

  /** 保存一个权限字段；失败（含 revision 冲突）回读服务端值，不假装成功。 */
  const patch = async (key: string, body: { sandboxMode?: SandboxMode; approvalPolicy?: ApprovalPolicy }) => {
    setSaving(key);
    try {
      const next = await api.patchAppSettings({ revision: view.revision, permissions: body });
      setView(next);
      setApprovalPolicy(next.permissions.approvalPolicy);
      message.success("已保存");
    } catch (e) {
      message.error(e instanceof Error ? e.message : "保存权限设置失败");
      load();
    } finally {
      setSaving(undefined);
    }
  };

  const changeSandbox = (mode: SandboxMode) => {
    if (current.sandboxMode === mode) return;
    if (isDangerousSandboxMode(mode)) {
      confirmDanger({
        title: "切到「完全访问」？",
        content: sandboxConfirmContent(mode),
        okText: "仍要切换",
        onConfirm: () => patch("sandbox", { sandboxMode: mode }),
      });
      return;
    }
    void patch("sandbox", { sandboxMode: mode });
  };

  const changeApproval = (policy: ApprovalPolicy) => {
    if (current.approvalPolicy === policy) return;
    if (isDangerousApprovalPolicy(policy)) {
      confirmDanger({
        title: "切到「从不询问」？",
        content: approvalConfirmContent(policy),
        okText: "仍要切换",
        onConfirm: () => patch("approval", { approvalPolicy: policy }),
      });
      return;
    }
    void patch("approval", { approvalPolicy: policy });
  };

  const saveDefaultRole = async () => {
    setSavingRole(true);
    try {
      const result = await api.setDefaultRole(defaultRole, view.revision);
      setView({ ...view, revision: result.revision, permissions: result.permissions });
      message.success(`默认角色已设为「${ROLE_LABELS[defaultRole]}」；下次打开应用即为该身份`);
    } catch (e) {
      message.error(e instanceof Error ? e.message : "保存默认角色失败");
      load();
    } finally {
      setSavingRole(false);
    }
  };

  return (
    <div>
      <h2 style={{ margin: 0, fontSize: 24 }}>权限与审批</h2>
      <div style={{ color: "var(--muted)", margin: "4px 0 16px" }}>
        关键控制在谁手里：预设档位、危险操作确认、默认角色与审计留痕。
      </div>

      <Alert
        type="warning"
        showIcon
        message={SECURITY_NOTE}
        description={SECURITY_DETAIL}
        style={{ marginBottom: 16 }}
      />

      <div style={card}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <SafetyCertificateOutlined style={{ color: "var(--primary)" }} />
          <span style={{ fontWeight: 600, fontSize: 16 }}>权限预设</span>
          {isDangerousSandboxMode(current.sandboxMode) && <Tag color="red">完全访问</Tag>}
        </div>
        <div style={{ display: "flex", gap: 16, alignItems: "center", marginTop: 10 }}>
          <div style={labelStyle}>当前档位</div>
          <Space wrap>
            {SANDBOX_PRESETS.map((preset) => (
              <Button
                key={preset.mode}
                type={current.sandboxMode === preset.mode ? "primary" : "default"}
                danger={preset.mode === "danger-full-access" && current.sandboxMode === preset.mode}
                loading={saving === "sandbox" && current.sandboxMode !== preset.mode}
                onClick={() => changeSandbox(preset.mode)}
              >
                {preset.label}
              </Button>
            ))}
          </Space>
        </div>
        <div style={{ color: "var(--muted)", fontSize: 13, marginTop: 10 }}>{presetOf(current.sandboxMode).description}</div>
        {isDangerousSandboxMode(current.sandboxMode) && (
          <Alert
            style={{ marginTop: 10 }}
            type="error"
            showIcon
            message="当前是完全访问：审批可能被跳过，误操作会直接落到本机文件"
            description="本机模式仍没有系统级沙箱，也不改变文件权限；演示结束后建议切回「工作区可写」。"
          />
        )}
      </div>

      <div style={card}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <LockOutlined style={{ color: "var(--warn)" }} />
          <span style={{ fontWeight: 600, fontSize: 16 }}>审批策略</span>
          {isDangerousApprovalPolicy(current.approvalPolicy) && <Tag color="red">从不询问</Tag>}
        </div>
        <div style={{ display: "flex", gap: 16, alignItems: "flex-start", marginTop: 10 }}>
          <div style={labelStyle}>危险操作前</div>
          <div style={{ flex: 1, minWidth: 0 }}>
            <Radio.Group
              value={current.approvalPolicy}
              onChange={(event) => changeApproval(event.target.value as ApprovalPolicy)}
            >
              <Space direction="vertical">
                {APPROVAL_OPTIONS.map((option) => (
                  <Radio key={option.value} value={option.value}>
                    <span style={{ fontWeight: 500 }}>{option.label}</span>
                    <div style={{ color: "var(--muted)", fontSize: 12 }}>{option.description}</div>
                  </Radio>
                ))}
              </Space>
            </Radio.Group>
            <div style={{ color: "var(--muted)", fontSize: 12, marginTop: 8 }}>
              当前：{approvalLabel(current.approvalPolicy)}。无论选哪档，危险操作都会留审计。
            </div>
          </div>
        </div>
      </div>

      <div style={card}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <UserOutlined style={{ color: "var(--info)" }} />
          <span style={{ fontWeight: 600, fontSize: 16 }}>默认角色</span>
          <Tag color="blue">打开应用时的身份</Tag>
        </div>
        <div style={{ display: "flex", gap: 16, alignItems: "center", marginTop: 10 }}>
          <div style={labelStyle}>默认角色</div>
          <Select<DefaultRole>
            style={{ width: 200 }}
            value={defaultRole}
            options={DEFAULT_ROLE_OPTIONS}
            onChange={setDefaultRole}
          />
          <Button type="primary" loading={savingRole} disabled={defaultRole === current.defaultRole} onClick={saveDefaultRole}>
            设为默认
          </Button>
          <Text type="secondary" style={{ fontSize: 12 }}>
            当前默认：{ROLE_LABELS[current.defaultRole]}
          </Text>
        </div>
        <div style={{ color: "var(--muted)", fontSize: 12, marginTop: 10 }}>
          只决定打开应用时的初始身份。在左侧栏切换角色只影响当前界面，不会改写这里；任务推进仍由服务端按请求角色校验，前端不能越权。
        </div>
      </div>

      <div style={card}>
        <SettingsTransfer api={api} onImported={load} />
      </div>

      <Divider style={{ margin: "8px 0 12px" }} />
      <div style={{ display: "flex", gap: 8, alignItems: "flex-start", color: "var(--muted)", fontSize: 12 }}>
        <AuditOutlined style={{ marginTop: 3 }} />
        <div>
          审计留痕：危险操作（模拟崩溃、恢复默认员工配置、导入设置、导出含密钥、切到完全访问 / 从不询问）会由服务端追加写
          <Text code>audit.jsonl</Text>，一行一条 JSON（时间、角色、操作、目标、结果）；失败也留痕，取消不发请求。审计文件里不含明文 API Key。
        </div>
      </div>
    </div>
  );
}
