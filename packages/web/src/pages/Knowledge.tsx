// 知识库页面：浏览、查看、删除（Spec 0005“知识库”）。入库在对话里的入库确认卡片上完成，这里不做手动上传。
import { useCallback, useEffect, useState } from "react";
import { App as AntApp, Button, Card, Drawer, Empty, Popconfirm, Space, Spin, Tag, Typography } from "antd";
import { BookOutlined, DeleteOutlined, EyeOutlined, ReloadOutlined } from "@ant-design/icons";
import type { EmployeeInfo, KnowledgeDetail, KnowledgeEntry, UiRole } from "../api";
import { useApi } from "../hooks/data";

const day = (iso: string) => new Date(iso).toLocaleString("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });

export function KnowledgePage({ role, employees }: { role: UiRole; employees: EmployeeInfo[] }) {
  const api = useApi();
  const { message } = AntApp.useApp();
  const [entries, setEntries] = useState<KnowledgeEntry[]>();
  const [error, setError] = useState<string>();
  const [viewing, setViewing] = useState<KnowledgeDetail>();
  const [opening, setOpening] = useState<string>();

  const load = useCallback(async () => {
    try {
      setEntries(await api.listKnowledge());
      setError(undefined);
    } catch (e) {
      setError(e instanceof Error ? e.message : "读取知识库失败");
    }
  }, [api]);

  useEffect(() => {
    void load();
  }, [load]);

  const open = async (id: string) => {
    setOpening(id);
    try {
      setViewing(await api.readKnowledge(id));
    } catch (e) {
      message.error(e instanceof Error ? e.message : "读取条目失败");
    } finally {
      setOpening(undefined);
    }
  };

  const remove = async (entry: KnowledgeEntry) => {
    const res = await api.deleteKnowledge(entry.id);
    if (res.ok) message.success(`已删除：${entry.title}`);
    else message.error(res.message);
    void load();
  };

  const employeeName = (id: string) => employees.find((e) => e.id === id)?.name ?? id;
  const initiator = role === "initiator";
  // 新入库的在前
  const listed = [...(entries ?? [])].reverse();

  return (
    <div className="chat-body" style={{ padding: "24px 32px" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 8 }}>
        <h2 style={{ margin: 0 }}>
          <BookOutlined /> 知识库
        </h2>
        <Button icon={<ReloadOutlined />} size="small" onClick={() => void load()}>
          刷新
        </Button>
      </div>
      <p className="chat-sub" style={{ marginBottom: 16 }}>
        已交付的文件经发起人确认后放在这里，供之后的员工查询：员工先看标题和描述，需要时再读正文。知识库只作背景参考，周报里的数字仍然只来自本次数据。
        {!initiator && " 只有发起人能删除条目。"}
      </p>
      {error && <Typography.Text type="danger">{error}</Typography.Text>}
      {entries === undefined && !error ? (
        <Spin />
      ) : listed.length === 0 ? (
        <Empty description="知识库还是空的。任务交付后，可以在对话里的入库卡片上把交付物放进来。" />
      ) : (
        <Space direction="vertical" size={12} style={{ width: "100%" }}>
          {listed.map((entry) => (
            <Card
              key={entry.id}
              size="small"
              title={entry.title}
              extra={
                <Space size={4}>
                  <Button size="small" icon={<EyeOutlined />} loading={opening === entry.id} onClick={() => void open(entry.id)}>
                    查看
                  </Button>
                  {initiator && (
                    <Popconfirm title="删除这个条目？" description="删除后员工就查不到它了，交付物本身不受影响。" okText="删除" cancelText="取消" onConfirm={() => remove(entry)}>
                      <Button size="small" danger icon={<DeleteOutlined />}>
                        删除
                      </Button>
                    </Popconfirm>
                  )}
                </Space>
              }
            >
              <div>{entry.description}</div>
              <div className="chat-sub" style={{ marginTop: 6 }}>
                {entry.tags.map((tag) => (
                  <Tag key={tag}>{tag}</Tag>
                ))}
                来自{employeeName(entry.sourceEmployee)} · {day(entry.createdAt)}
              </div>
            </Card>
          ))}
        </Space>
      )}
      <Drawer open={viewing !== undefined} width={640} title={viewing?.title} onClose={() => setViewing(undefined)}>
        {viewing && (
          <>
            <p>{viewing.description}</p>
            <p className="chat-sub">
              原文件 {viewing.fileName} · 来自{employeeName(viewing.sourceEmployee)} · 任务 {viewing.sourceTask}
            </p>
            <div style={{ whiteSpace: "pre-wrap", lineHeight: 1.7 }}>{viewing.text || "（没有可显示的正文）"}</div>
          </>
        )}
      </Drawer>
    </div>
  );
}
