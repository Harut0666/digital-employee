// 设置文件导出 / 导入（Spec 0004 §6；T23 把这两个动作纳入危险操作二次确认与审计）。
//
// 放在“权限与审批”分区里：导出含密钥、导入覆盖设置都要先确认，并由服务端写审计；
// 普通（脱敏）导出不需要确认。诊断包永远是脱敏快照，不在这里。
import { useRef, useState } from "react";
import { Alert, App as AntApp, Button, Space, Typography } from "antd";
import { DownloadOutlined, ExportOutlined, ImportOutlined } from "@ant-design/icons";
import type { SettingsApi } from "../../api/settings";
import { useDangerConfirm } from "../../components/ConfirmDialog";

const { Text } = Typography;

/** 把导出的设置文档存成 JSON 文件（浏览器下载）。 */
function downloadJson(filename: string, payload: unknown): void {
  const blob = new Blob([`${JSON.stringify(payload, null, 2)}\n`], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}

export function SettingsTransfer({ api, onImported }: { api: SettingsApi; onImported?: () => void }) {
  const { message } = AntApp.useApp();
  const confirmDanger = useDangerConfirm();
  const fileRef = useRef<HTMLInputElement>(null);
  const [exporting, setExporting] = useState<"plain" | "keys">();
  const [importing, setImporting] = useState(false);

  const exportSettings = async (includeKeys: boolean) => {
    setExporting(includeKeys ? "keys" : "plain");
    try {
      const result = await api.exportSettings(includeKeys);
      downloadJson(result.filename, result.document);
      message.success(includeKeys ? "已导出含密钥设置（文件名带 -with-keys 警示）" : "已导出脱敏设置");
    } catch (e) {
      message.error(e instanceof Error ? e.message : "导出设置失败");
    } finally {
      setExporting(undefined);
    }
  };

  const importFile = async (file: File) => {
    let parsed: unknown;
    try {
      parsed = JSON.parse(await file.text());
    } catch {
      message.error("导入失败：文件不是合法的 JSON");
      return;
    }
    confirmDanger({
      title: "导入设置并覆盖当前配置？",
      content: (
        <div>
          <div>
            文件：<Text code>{file.name}</Text>
          </div>
          <div style={{ marginTop: 8 }}>
            导入会覆盖当前设置文档（含权限预设、模型连接与外观偏好）；旧文件会先备份到
            <Text code>config-versions/settings/</Text>。若文件里含 API Key，会一并写入本机 settings.json。
            服务端会写审计。
          </div>
        </div>
      ),
      okText: "仍要导入",
      onConfirm: async () => {
        setImporting(true);
        try {
          const result = await api.importSettings(parsed);
          message.success(
            result.backupPath
              ? `已导入设置（旧文件已备份到 ${result.backupPath}）`
              : "已导入设置（原先没有设置文件，无需备份）",
          );
          onImported?.();
        } catch (e) {
          message.error(e instanceof Error ? e.message : "导入设置失败");
        } finally {
          setImporting(false);
        }
      },
    });
  };

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", gap: 8, fontWeight: 600, fontSize: 16 }}>
        <ExportOutlined style={{ color: "var(--primary)" }} />
        设置文件导出 / 导入
      </div>
      <div style={{ color: "var(--muted)", fontSize: 13, marginTop: 6 }}>
        导出默认脱敏（API Key 一律空串）；含密钥导出需要确认，文件名会带 <Text code>-with-keys</Text> 警示。
        导入会先备份当前文件，再按 v2 校验写入；非法文件不会写盘。
      </div>
      <Space wrap style={{ marginTop: 12 }}>
        <Button
          icon={<DownloadOutlined />}
          loading={exporting === "plain"}
          onClick={() => void exportSettings(false)}
        >
          导出设置（脱敏）
        </Button>
        <Button
          danger
          icon={<DownloadOutlined />}
          loading={exporting === "keys"}
          onClick={() =>
            confirmDanger({
              title: "导出含明文 API Key 的设置？",
              content:
                "导出的文件会包含明文 API Key，文件名带 -with-keys 警示；任何拿到文件的人都能直接使用这个 Key。" +
                "本次操作会写审计，但审计文件本身不含 Key。仍要导出含密钥设置？",
              okText: "仍要导出含密钥",
              onConfirm: () => exportSettings(true),
            })
          }
        >
          导出设置（含 API Key）
        </Button>
        <Button icon={<ImportOutlined />} loading={importing} onClick={() => fileRef.current?.click()}>
          导入设置文件…
        </Button>
        <input
          ref={fileRef}
          type="file"
          accept="application/json,.json"
          style={{ display: "none" }}
          onChange={(event) => {
            const file = event.target.files?.[0];
            // 允许再次选择同一个文件
            event.target.value = "";
            if (file) void importFile(file);
          }}
        />
      </Space>
      <Alert
        style={{ marginTop: 12 }}
        type="info"
        showIcon
        message="导出的是设置文档本身"
        description="它包含模型连接、外观、执行默认值、权限预设与默认角色；诊断包（诊断与关于）不包含明文 Key，也不能用来恢复设置。"
      />
    </div>
  );
}
