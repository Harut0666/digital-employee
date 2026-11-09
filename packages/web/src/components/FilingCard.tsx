// 入库确认卡片：交付后由程序发出。模型起草一句描述，人确认或修改后才放进知识库；模型不可用时由人手写（ADR-0005）。
import { useEffect, useState } from "react";
import { Button, Input, Select } from "antd";
import { BookOutlined, CheckCircleFilled, EditOutlined } from "@ant-design/icons";
import type { ChatItem } from "../view/chat-view";
import type { CardActions, SessionActions } from "./Cards";
import { Handoff } from "./Cards";

type FilingItem = Extract<ChatItem, { kind: "filing" }>;

/** 已请模型起草过的任务：卡片重新挂载（描述变化会换 key）或 StrictMode 重复执行时不再请求 */
const requested = new Set<string>();

export function FilingCard({ item, actions, session }: { item: FilingItem; actions: CardActions; session: SessionActions }) {
  const [title, setTitle] = useState(item.title);
  const [description, setDescription] = useState(item.description);
  const [tags, setTags] = useState<string[]>([]);
  const [drafting, setDrafting] = useState(false);
  const [filing, setFiling] = useState(false);
  const initiator = actions.role === "initiator";

  const draft = async () => {
    setDrafting(true);
    try {
      await session.draftFiling(item.taskId);
    } finally {
      setDrafting(false);
    }
  };

  // 卡片第一次出现时自动请模型起草；起草失败过就等人手写，不反复请求
  useEffect(() => {
    if (!item.active || !item.canDraft || !initiator || item.description || item.draftFailed || requested.has(item.taskId)) return;
    requested.add(item.taskId);
    void draft();
  }, [item.active, item.canDraft, initiator, item.description, item.draftFailed, item.taskId]);

  if (!item.active) {
    return (
      <div className="card muted">
        <CheckCircleFilled /> 已放入知识库：{item.title}
        <Button type="link" size="small" onClick={session.openKnowledge}>
          查看知识库
        </Button>
      </div>
    );
  }

  const submit = async () => {
    setFiling(true);
    try {
      await session.fileKnowledge({ taskId: item.taskId, title: title.trim() || undefined, description: description.trim(), tags });
    } finally {
      setFiling(false);
    }
  };

  const placeholder = drafting ? "模型正在起草描述…" : item.draftFailed ? "模型暂时不可用，请自己写一句描述" : "写一句描述：这是什么文件、能回答什么问题";
  return (
    <div className="card">
      <div className="card-title">
        <BookOutlined style={{ color: "var(--primary)" }} /> 放入知识库
      </div>
      <div className="card-body chat-sub">交付物放进知识库后，之后的员工先看描述再决定要不要读正文。知识库只作背景参考，不会成为周报的数字来源。</div>
      {initiator ? (
        <>
          <div className="card-body">
            <div>标题</div>
            <Input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="不填时用交付物的文件名" style={{ marginTop: 6 }} />
          </div>
          <div className="card-body">
            <div>描述（必填，可以修改模型起草的内容）</div>
            <Input.TextArea
              rows={3}
              maxLength={200}
              showCount
              value={description}
              disabled={drafting}
              onChange={(e) => setDescription(e.target.value)}
              placeholder={placeholder}
              style={{ marginTop: 6 }}
            />
          </div>
          <div className="card-body">
            <div>标签（可选）</div>
            <Select mode="tags" value={tags} onChange={setTags} open={false} placeholder="输入后回车，例如：周报" style={{ width: "100%", marginTop: 6 }} />
          </div>
          <div className="card-actions">
            <Button type="primary" icon={<BookOutlined />} loading={filing} disabled={drafting || !description.trim()} onClick={() => void submit()}>
              放入知识库
            </Button>
            {item.canDraft && (
              <Button icon={<EditOutlined />} loading={drafting} disabled={filing} onClick={() => void draft()}>
                {item.description || item.draftFailed ? "重新起草描述" : "请模型起草描述"}
              </Button>
            )}
          </div>
        </>
      ) : (
        <Handoff handoff={{ text: "等待发起人确认是否放入知识库", switchTo: ["initiator"] }} onRole={actions.onRole} />
      )}
    </div>
  );
}
