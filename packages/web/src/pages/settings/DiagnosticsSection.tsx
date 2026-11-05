// 诊断与关于子页（T22）：版本与运行时、工作区路径、日志级别、复制诊断信息、导出诊断包、打开设置文件。
//
// 诊断接口与诊断包的组合逻辑在 T19 的服务端（GET /api/settings/diagnostics、
// POST /api/settings/diagnostics/export、POST /api/settings/open-file）；这里只做界面与
// 剪贴板/文件管理器调用。复制出去的文本永远是脱敏的（view/diagnostics.ts 里统一拼装）。
import { useEffect, useState } from "react";
import { Alert, App as AntApp, Button, Input, Modal, Select, Space, Tag, Typography } from "antd";
import {
  CopyOutlined,
  DownloadOutlined,
  FileTextOutlined,
  FolderOpenOutlined,
  InfoCircleOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { LOG_LEVELS, type DiagnosticsInfo, type DiagnosticsExportResult, type LogLevel } from "@de/core/contract";
import type { SettingsApi } from "../../api/settings";
import { formatDiagnosticsText } from "../../view/diagnostics";

const { Text } = Typography;

const card: React.CSSProperties = { border: "1px solid var(--line)", borderRadius: 12, background: "var(--card)", padding: "16px 20px", marginBottom: 16 };
const rowStyle: React.CSSProperties = { display: "flex", gap: 16, alignItems: "flex-start", padding: "8px 0" };
const labelStyle: React.CSSProperties = { width: 132, flex: "none", color: "var(--muted)" };
const mono: React.CSSProperties = { fontFamily: "Consolas, monospace", fontSize: 12, wordBreak: "break-all" };
const preStyle: React.CSSProperties = { ...mono, background: "var(--hover)", borderRadius: 8, padding: "10px 12px", margin: "10px 0 0", maxHeight: 260, overflow: "auto", whiteSpace: "pre-wrap" };

const LEVEL_LABEL: Record<LogLevel, string> = {
  error: "error · 只输出错误",
  warn: "warn · 警告与错误",
  info: "info · 常规（默认）",
  debug: "debug · 调试，输出最多",
};

function platformLabel(platform: string): string {
  switch (platform) {
    case "win32":
      return "Windows";
    case "darwin":
      return "macOS";
    case "linux":
      return "Linux";
    default:
      return platform;
  }
}

function InfoRow({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div style={rowStyle}>
      <div style={labelStyle}>{label}</div>
      <div style={{ flex: 1, minWidth: 0 }}>{value}</div>
    </div>
  );
}

export function DiagnosticsSection({ api }: { api: SettingsApi }) {
  const { message } = AntApp.useApp();
  const [info, setInfo] = useState<DiagnosticsInfo>();
  const [savingLevel, setSavingLevel] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [exported, setExported] = useState<DiagnosticsExportResult>();
  const [fallbackText, setFallbackText] = useState<string>();
  const [showPreview, setShowPreview] = useState(false);

  const load = () => {
    api.getDiagnostics().then(setInfo, (e) => message.error(e instanceof Error ? e.message : "读取诊断信息失败"));
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api]);

  if (!info) return <div style={{ color: "var(--muted)" }}>读取中…</div>;

  const text = formatDiagnosticsText(info);
  const logLevel = info.settings.diagnostics.logLevel;

  const saveLogLevel = async (level: LogLevel) => {
    setSavingLevel(true);
    try {
      const view = await api.patchAppSettings({ revision: info.settings.revision, diagnostics: { logLevel: level } });
      setInfo({ ...info, settings: view });
      message.success(`日志级别已切换为 ${level}，立即生效`);
    } catch (e) {
      message.error(e instanceof Error ? e.message : "保存日志级别失败");
      // 冲突时拿到服务端最新 revision / 值再试
      load();
    } finally {
      setSavingLevel(false);
    }
  };

  const copyDiagnostics = async () => {
    try {
      if (!navigator.clipboard?.writeText) throw new Error("当前环境没有剪贴板接口");
      await navigator.clipboard.writeText(text);
      message.success("诊断信息已复制到剪贴板");
    } catch {
      // 浏览器不允许写剪贴板（非安全上下文 / 权限被拒）时降级为可选文本框
      setFallbackText(text);
      message.warning("自动复制失败，请在弹窗里手动复制");
    }
  };

  const exportPackage = async () => {
    setExporting(true);
    try {
      setExported(await api.exportDiagnostics());
      message.success("诊断包已导出");
    } catch (e) {
      message.error(e instanceof Error ? e.message : "导出诊断包失败");
    } finally {
      setExporting(false);
    }
  };

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", marginBottom: 16 }}>
        <div style={{ flex: 1 }}>
          <h2 style={{ margin: 0, fontSize: 24 }}>诊断与关于</h2>
          <div style={{ color: "var(--muted)", marginTop: 4 }}>
            出问题时先看版本与运行时，再把诊断信息或诊断包发给开发者；这里的所有导出都不含明文 API Key。
          </div>
        </div>
        <Button icon={<ReloadOutlined />} onClick={load}>
          刷新
        </Button>
      </div>

      <div style={card}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 6 }}>
          <InfoCircleOutlined style={{ color: "var(--primary)" }} />
          <span style={{ fontWeight: 600, fontSize: 16 }}>关于</span>
        </div>
        <InfoRow label="应用版本" value={`${info.app.version}（设置文档 v${info.app.settingsVersion}）`} />
        <InfoRow label="运行时" value={`Bun ${info.runtime.bun} · ${platformLabel(info.runtime.platform)} · ${info.runtime.arch}`} />
        <InfoRow label="桌面壳" value="Electrobun 未启用（源码 / exe 模式）" />
        <InfoRow label="工作区路径" value={<span style={mono}>{info.workspace.workspaceDir}</span>} />
        <InfoRow
          label="设置文件"
          value={
            <span style={mono}>
              {info.workspace.settingsFile}
              {info.workspace.settingsFileExists ? "（存在）" : "（未创建）"}
            </span>
          }
        />
      </div>

      <div style={card}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <FileTextOutlined style={{ color: "var(--info)" }} />
          <span style={{ fontWeight: 600, fontSize: 16 }}>日志级别</span>
          <Tag color="green">即时生效</Tag>
        </div>
        <div style={{ display: "flex", gap: 16, alignItems: "center", marginTop: 10 }}>
          <div style={{ width: 132, flex: "none", color: "var(--muted)" }}>控制台输出</div>
          <Select<LogLevel>
            value={logLevel}
            loading={savingLevel}
            style={{ width: 240 }}
            onChange={saveLogLevel}
            options={LOG_LEVELS.map((level) => ({ value: level, label: LEVEL_LABEL[level] }))}
          />
        </div>
        <div style={{ color: "var(--muted)", fontSize: 12, marginTop: 8 }}>
          切换后服务端立即按新级别过滤控制台输出，重启后仍按 settings.json 里的值生效；
          debug 会额外打印启动细节、设置保存等行。
        </div>
      </div>

      <div style={card}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 10 }}>
          <DownloadOutlined style={{ color: "var(--primary)" }} />
          <span style={{ fontWeight: 600, fontSize: 16 }}>诊断操作</span>
        </div>
        <Space wrap>
          <Button icon={<CopyOutlined />} onClick={copyDiagnostics}>
            复制诊断信息
          </Button>
          <Button icon={<DownloadOutlined />} loading={exporting} onClick={exportPackage}>
            导出诊断包
          </Button>
          <Button
            icon={<FileTextOutlined />}
            onClick={async () => {
              try {
                const result = await api.openSettingsFile();
                message.success(`已用系统编辑器打开 ${result.path}`);
              } catch (e) {
                message.error(e instanceof Error ? e.message : "打开设置文件失败");
              }
            }}
          >
            打开设置文件
          </Button>
          <Button type="text" size="small" onClick={() => setShowPreview((v) => !v)}>
            {showPreview ? "收起预览" : "预览诊断信息"}
          </Button>
        </Space>
        <div style={{ color: "var(--muted)", fontSize: 12, marginTop: 8 }}>
          复制的内容：版本、运行时、工作区路径与脱敏设置快照；诊断包额外包含最近几个任务各自的最后几条事件摘要
          （落在工作区 diagnostics/ 目录）。设置文件是 JSON 原文本，适合改表单没覆盖的高级字段。
        </div>

        {exported && (
          <Alert
            style={{ marginTop: 12 }}
            type="success"
            showIcon
            message="诊断包已导出"
            description={
              <div>
                <div style={mono}>{exported.path}</div>
                <div style={{ marginTop: 8 }}>
                  <Button
                    size="small"
                    icon={<FolderOpenOutlined />}
                    onClick={async () => {
                      try {
                        await api.openDiagnosticsFolder();
                      } catch (e) {
                        message.error(e instanceof Error ? e.message : "打开目录失败");
                      }
                    }}
                  >
                    打开所在目录
                  </Button>
                </div>
              </div>
            }
          />
        )}

        {showPreview && <pre style={preStyle}>{text}</pre>}
      </div>

      <Modal
        open={fallbackText !== undefined}
        title="复制诊断信息（手动）"
        onCancel={() => setFallbackText(undefined)}
        footer={
          <Button type="primary" onClick={() => setFallbackText(undefined)}>
            关闭
          </Button>
        }
      >
        <div style={{ color: "var(--muted)", fontSize: 12, marginBottom: 8 }}>
          自动复制被浏览器拦下了。下面这段文本已全选，按 Ctrl+C（macOS ⌘C）复制。
        </div>
        <Input.TextArea
          value={fallbackText}
          readOnly
          autoFocus
          autoSize={{ minRows: 8, maxRows: 16 }}
          onFocus={(e) => e.currentTarget.select()}
        />
        <div style={{ marginTop: 8 }}>
          <Text type="secondary" style={{ fontSize: 12 }}>
            文本已脱敏，不含明文 API Key。
          </Text>
        </div>
      </Modal>
    </div>
  );
}
