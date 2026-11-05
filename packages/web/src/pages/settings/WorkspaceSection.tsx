// 工作区子页：查看目录、打开工作区文件夹、恢复默认员工配置（先备份到 config-versions/）。
import { useEffect, useState } from "react";
import { Alert, App as AntApp, Button, Popconfirm, Typography } from "antd";
import { FolderOpenOutlined, ReloadOutlined, HistoryOutlined } from "@ant-design/icons";
import type { ResetEmployeesResult, SettingsApi, WorkspaceView } from "../../api/settings";

const { Text } = Typography;

const card: React.CSSProperties = { border: "1px solid var(--line)", borderRadius: 12, background: "var(--card)", padding: "16px 20px", marginBottom: 16 };
const pathStyle: React.CSSProperties = {
  fontFamily: "Consolas, monospace",
  fontSize: 12,
  background: "var(--hover)",
  borderRadius: 6,
  padding: "6px 10px",
  marginTop: 6,
  wordBreak: "break-all",
};

export function WorkspaceSection({ api }: { api: SettingsApi }) {
  const { message } = AntApp.useApp();
  const [view, setView] = useState<WorkspaceView>();
  const [resetting, setResetting] = useState(false);
  const [resetResult, setResetResult] = useState<ResetEmployeesResult>();

  useEffect(() => {
    api.getWorkspace().then(setView, (e) => message.error(e instanceof Error ? e.message : "读取工作区失败"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api]);

  if (!view) return <div style={{ color: "var(--muted)" }}>读取中…</div>;

  const reset = async () => {
    setResetting(true);
    try {
      const result = await api.resetEmployees();
      setResetResult(result);
      message.success(`已恢复默认员工配置（${result.employees.join("、")}）`);
    } catch (e) {
      message.error(e instanceof Error ? e.message : "恢复失败");
    } finally {
      setResetting(false);
    }
  };

  return (
    <div>
      <h2 style={{ margin: 0, fontSize: 24 }}>工作区</h2>
      <div style={{ color: "var(--muted)", margin: "4px 0 16px" }}>
        工作区保存任务事件、附件、交付物、员工配置与配置版本。
      </div>

      <div style={card}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <FolderOpenOutlined style={{ color: "var(--primary)" }} />
          <span style={{ fontWeight: 600, fontSize: 16 }}>工作区文件夹</span>
        </div>
        <div style={pathStyle}>{view.workspaceDir}</div>
        <div style={{ display: "flex", justifyContent: "flex-end", marginTop: 12 }}>
          <Button
            icon={<FolderOpenOutlined />}
            onClick={async () => {
              try {
                await api.openWorkspace();
              } catch (e) {
                message.error(e instanceof Error ? e.message : "打开失败");
              }
            }}
          >
            打开文件夹
          </Button>
        </div>
      </div>

      <div style={card}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <ReloadOutlined style={{ color: "var(--warn)" }} />
          <span style={{ fontWeight: 600, fontSize: 16 }}>恢复默认员工配置</span>
        </div>
        <div style={{ color: "var(--muted)", fontSize: 13, marginTop: 6 }}>
          当前员工配置会先备份到 config-versions/，然后恢复为默认配置。运行中的任务使用发起时的配置副本，不受影响；
          新任务使用恢复后的配置。
        </div>
        <div style={{ marginTop: 10 }}>
          <Text type="secondary" style={{ fontSize: 12 }}>
            当前员工目录：
          </Text>
          <div style={pathStyle}>{view.employeesDir}</div>
        </div>
        <div style={{ display: "flex", justifyContent: "flex-end", marginTop: 12 }}>
          <Popconfirm
            title="恢复默认员工配置？"
            description="会先把当前配置备份到 config-versions/，再覆盖员工目录，恢复后不可撤销（但可从备份恢复）。"
            okText="恢复"
            cancelText="取消"
            okButtonProps={{ danger: true }}
            onConfirm={reset}
          >
            <Button danger icon={<ReloadOutlined />} loading={resetting}>
              恢复默认员工配置
            </Button>
          </Popconfirm>
        </div>
        {resetResult && (
          <Alert
            style={{ marginTop: 12 }}
            type="success"
            showIcon
            icon={<HistoryOutlined />}
            message={`已恢复：${resetResult.employees.join("、")}`}
            description={
              resetResult.backupPath ? (
                <span>
                  原配置已备份到 <Text code>{resetResult.backupPath}</Text>
                </span>
              ) : (
                "原先没有员工配置，已直接创建默认配置。"
              )
            }
          />
        )}
      </div>

      <div style={{ color: "var(--muted)", fontSize: 12 }}>
        配置版本目录：<Text code>{view.configVersionsDir}</Text>
      </div>
    </div>
  );
}
