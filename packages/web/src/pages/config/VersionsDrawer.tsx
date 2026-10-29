// 版本历史：列出配置版本、查看与当前配置的差异、一键回滚（回滚本身也生成一个新版本）。
import { useCallback, useEffect, useState } from "react";
import { App, Button, Drawer, Empty, List, Modal, Popconfirm, Space, Tag, Typography } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import type { ConfigDiff, ConfigVersion } from "@de/core/contract";
import { useApi } from "../../hooks/data";
import { formatTime, shortHash } from "./helpers";

const STATUS_LABEL: Record<string, string> = { added: "新增", removed: "删除", modified: "修改" };

export function VersionsDrawer({
  open,
  employeeId,
  onClose,
  onRestored,
}: {
  open: boolean;
  employeeId: string;
  onClose: () => void;
  onRestored: () => void;
}) {
  const api = useApi();
  const { message } = App.useApp();
  const [versions, setVersions] = useState<ConfigVersion[]>([]);
  const [loading, setLoading] = useState(false);
  const [diff, setDiff] = useState<ConfigDiff | undefined>();
  const [diffOpen, setDiffOpen] = useState(false);
  const [busy, setBusy] = useState<string | undefined>();

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setVersions(await api.listConfigVersions(employeeId));
    } catch (error) {
      message.error(error instanceof Error ? error.message : "读取版本失败");
    } finally {
      setLoading(false);
    }
  }, [api, employeeId, message]);

  useEffect(() => {
    if (open) void load();
  }, [open, load]);

  const showDiff = async (versionId: string) => {
    try {
      setDiff(await api.diffConfigVersion(employeeId, versionId));
      setDiffOpen(true);
    } catch (error) {
      message.error(error instanceof Error ? error.message : "读取差异失败");
    }
  };

  const restore = async (versionId: string) => {
    setBusy(versionId);
    try {
      await api.restoreConfigVersion(employeeId, versionId);
      message.success("已回滚到该版本（回滚本身也记为一个新版本）");
      onRestored();
      await load();
    } catch (error) {
      message.error(error instanceof Error ? error.message : "回滚失败");
    } finally {
      setBusy(undefined);
    }
  };

  return (
    <>
      <Drawer
        open={open}
        onClose={onClose}
        width={560}
        title="版本历史"
        extra={
          <Button size="small" icon={<ReloadOutlined />} onClick={() => void load()} loading={loading}>
            刷新
          </Button>
        }
      >
        <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
          每次保存前都会把当前配置复制成一个版本（时间戳 + 哈希短码）。回滚等于把某个版本复制回来再保存一次，所以回滚也会产生新版本。
        </Typography.Paragraph>
        <List
          loading={loading}
          dataSource={versions}
          locale={{ emptyText: <Empty description="还没有历史版本（第一次保存后出现）" /> }}
          renderItem={(version) => (
            <List.Item
              actions={[
                <Button key="diff" size="small" onClick={() => void showDiff(version.id)}>
                  查看差异
                </Button>,
                <Popconfirm key="restore" title="回滚到这个版本？" description="当前配置会先被保存为一个新版本。" onConfirm={() => void restore(version.id)}>
                  <Button size="small" danger loading={busy === version.id}>
                    回滚
                  </Button>
                </Popconfirm>,
              ]}
            >
              <List.Item.Meta
                title={
                  <Space size={8} wrap>
                    <Typography.Text code>{version.id}</Typography.Text>
                    <Tag>{shortHash(version.hash)}</Tag>
                  </Space>
                }
                description={formatTime(version.createdAt)}
              />
            </List.Item>
          )}
        />
      </Drawer>

      <Modal open={diffOpen} onCancel={() => setDiffOpen(false)} footer={null} width={760} title={`与当前配置的差异${diff ? `（版本 ${diff.version.id}）` : ""}`}>
        {diff && diff.files.length > 0 ? (
          <Space direction="vertical" size={14} style={{ width: "100%" }}>
            {diff.files.map((file) => (
              <div key={file.path}>
                <Space size={8}>
                  <Tag color={file.status === "added" ? "green" : file.status === "removed" ? "red" : "orange"}>{STATUS_LABEL[file.status]}</Tag>
                  <Typography.Text code>{file.path}</Typography.Text>
                </Space>
                <pre
                  style={{
                    marginTop: 6,
                    padding: 10,
                    background: "#f7f8fa",
                    borderRadius: 8,
                    maxHeight: 280,
                    overflow: "auto",
                    fontSize: 12,
                    lineHeight: 1.6,
                  }}
                >
                  {file.hunks.join("\n")}
                </pre>
              </div>
            ))}
          </Space>
        ) : (
          <Empty description="与当前配置没有差异" />
        )}
      </Modal>
    </>
  );
}
