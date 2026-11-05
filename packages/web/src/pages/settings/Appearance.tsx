// 外观与交互子页（T21）：主题 / 字号 / 轨迹详细度 / 折叠时机 / 回车 / 链接打开方式。
//
// 每个字段改动即保存：先本地生效（ConfigProvider + :root 变量），再 PATCH /api/settings/app。
// 读取失败时用默认值渲染，不阻塞设置页其它分区。
import { Alert, App as AntApp, Segmented, Select, Skeleton, Tag, Typography } from "antd";
import {
  BgColorsOutlined,
  BranchesOutlined,
  ClockCircleOutlined,
  EnterOutlined,
  FontSizeOutlined,
  LinkOutlined,
} from "@ant-design/icons";
import { FONT_SIZE_MAX, FONT_SIZE_MIN, type CollapseTiming, type EnterBehavior, type LinkOpening, type Theme, type TrajectoryDetail } from "@de/core/contract";
import { useUiPreferences } from "../../components/Preferences";

const { Text } = Typography;

const card: React.CSSProperties = {
  border: "1px solid var(--line)",
  borderRadius: 12,
  background: "var(--card)",
  padding: "16px 20px",
  marginBottom: 16,
};
const row: React.CSSProperties = { display: "flex", gap: 16, alignItems: "flex-start", marginBottom: 18 };
const labelStyle: React.CSSProperties = { width: 150, flex: "none", fontWeight: 500, paddingTop: 4 };

function Field({
  label,
  hint,
  icon,
  children,
}: {
  label: string;
  hint: string;
  icon: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div style={row}>
      <div style={labelStyle}>
        <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
          <span style={{ color: "var(--primary)" }}>{icon}</span>
          {label}
        </span>
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        {children}
        <div style={{ color: "var(--muted)", fontSize: 12, marginTop: 6 }}>{hint}</div>
      </div>
    </div>
  );
}

export function AppearanceSection() {
  const { ui, resolvedTheme, loaded, saving, error, save } = useUiPreferences();
  const { message } = AntApp.useApp();

  if (!loaded) {
    return (
      <div>
        <h2 style={{ margin: 0, fontSize: 24 }}>外观与交互</h2>
        <Skeleton active paragraph={{ rows: 6 }} style={{ marginTop: 24 }} />
      </div>
    );
  }

  const update = (patch: Parameters<typeof save>[0]) => {
    // 保存失败时 provider 已经把错误放进 error 并回滚；这里只提示成功
    void save(patch).then(
      () => message.success("已保存，立即生效"),
      () => {},
    );
  };

  const actualTheme = resolvedTheme === "dark" ? "深色" : "浅色";

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
        <h2 style={{ margin: 0, fontSize: 24 }}>外观与交互</h2>
        {saving && (
          <Tag color="processing" style={{ margin: 0 }}>
            保存中…
          </Tag>
        )}
      </div>
      <div style={{ color: "var(--muted)", margin: "4px 0 16px" }}>所有修改立即生效，并写入工作区的 settings.json。</div>

      {error && (
        <Alert
          style={{ marginBottom: 16 }}
          type="error"
          showIcon
          message={error}
          description="设置没有写入。若是 revision 冲突，请刷新页面后重试（不会静默覆盖其它窗口的修改）。"
        />
      )}

      <div style={card}>
        <Field icon={<BgColorsOutlined />} label="主题" hint={`跟随系统时，系统切换深浅色会立即跟随。当前实际主题：${actualTheme}。`}>
          <Segmented
            value={ui.theme}
            onChange={(value) => update({ theme: value as Theme })}
            options={[
              { label: "浅色", value: "light" },
              { label: "深色", value: "dark" },
              { label: "跟随系统", value: "system" },
            ]}
          />
        </Field>

        <Field icon={<FontSizeOutlined />} label="字号" hint="控制正文与轨迹字号，界面其它元素随之等比适配。">
          <div style={{ display: "flex", alignItems: "center", gap: 12, flexWrap: "wrap" }}>
            <Select
              value={ui.fontSize}
              onChange={(value) => update({ fontSize: value })}
              style={{ width: 100 }}
              options={Array.from({ length: FONT_SIZE_MAX - FONT_SIZE_MIN + 1 }, (_, i) => {
                const size = FONT_SIZE_MIN + i;
                return { value: size, label: `${size} px` };
              })}
            />
            <span style={{ fontSize: ui.fontSize }}>正文预览：经营周报 · 第 44 周</span>
          </div>
        </Field>

        <Field icon={<BranchesOutlined />} label="轨迹详细度" hint="简洁只显示步骤；标准显示耗时与细节摘要；详细默认展开每一步的全部信息。运行中的步骤始终自动展开。">
          <Segmented
            value={ui.trajectoryDetail}
            onChange={(value) => update({ trajectoryDetail: value as TrajectoryDetail })}
            options={[
              { label: "简洁", value: "compact" },
              { label: "标准", value: "standard" },
              { label: "详细", value: "detailed" },
            ]}
          />
        </Field>

        <Field icon={<ClockCircleOutlined />} label="轨迹折叠时机" hint="完成即折叠：一轮跑完自动收起；下次输入时折叠：保留到你在会话里下一次输入，方便点评。">
          <Segmented
            value={ui.collapseTiming}
            onChange={(value) => update({ collapseTiming: value as CollapseTiming })}
            options={[
              { label: "完成即折叠", value: "completion" },
              { label: "下次输入时折叠", value: "next-input" },
            ]}
          />
        </Field>

        <Field icon={<EnterOutlined />} label="回车行为" hint="Ctrl+Enter（macOS 为 ⌘+Enter）在两种模式下都发送。">
          <Segmented
            value={ui.enterBehavior}
            onChange={(value) => update({ enterBehavior: value as EnterBehavior })}
            options={[
              { label: "发送", value: "send" },
              { label: "换行", value: "newline" },
            ]}
          />
        </Field>

        {/*
          最后一行不留 margin-bottom，避免卡片底部空白过大
        */}
        <div style={{ ...row, marginBottom: 0 }}>
          <div style={labelStyle}>
            <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
              <span style={{ color: "var(--primary)" }}>
                <LinkOutlined />
              </span>
              链接打开方式
            </span>
          </div>
          <div style={{ flex: 1, minWidth: 0 }}>
            <Segmented
              value={ui.linkOpening}
              onChange={(value) => update({ linkOpening: value as LinkOpening })}
              options={[
                { label: "当前窗口", value: "in-app" },
                { label: "系统浏览器", value: "system" },
              ]}
            />
            <div style={{ color: "var(--muted)", fontSize: 12, marginTop: 6 }}>
              当前窗口在本应用内跳转；系统浏览器用新窗口打开（报告下载、外部链接）。
            </div>
          </div>
        </div>
      </div>

      <div style={{ color: "var(--muted)", fontSize: 12 }}>
        偏好属于设置文档 <Text code>ui</Text> 分区；导出设置时一并带走，换机器可直接复现。
      </div>
    </div>
  );
}
