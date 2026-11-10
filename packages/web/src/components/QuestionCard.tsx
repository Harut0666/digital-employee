// 提问卡（ask_user_question）：Agent 向发起人提的结构化问题。每题 2–4 个选项加一个“其他”自由输入，
// 单选或多选；选完提交，答案交还给 Agent。答复后收成一行（点开看每题的答案）；停止时显示“已取消”。
import { useState } from "react";
import { Button, Checkbox, Collapse, Input, Radio, Space } from "antd";
import { CheckCircleFilled, CloseCircleFilled, QuestionCircleOutlined } from "@ant-design/icons";
import type { ChatItem } from "../view/chat-view";
import { buildAnswers, canSubmit, emptyDraft, setCustom, toggleOption, type QuestionDraft } from "../view/question";
import type { CardActions, SessionActions } from "./Cards";
import { Handoff } from "./Cards";

type QuestionItem = Extract<ChatItem, { kind: "question" }>;

export function QuestionCard({ item, actions, session }: { item: QuestionItem; actions: CardActions; session: SessionActions }) {
  const { view } = item;
  const [draft, setDraft] = useState<QuestionDraft>(() => emptyDraft(view.questions));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  if (view.state !== "pending") {
    const head = (
      <span className={`question-done ${view.state}`}>
        {view.state === "answered" ? <CheckCircleFilled /> : <CloseCircleFilled />} 提问 · {view.stateText}
      </span>
    );
    if (view.state === "cancelled") return <div className="card muted question-done-card cancelled">{head}</div>;
    return (
      <Collapse
        className="card muted question-done-card"
        size="small"
        bordered={false}
        items={[
          {
            key: "answers",
            label: head,
            children: (
              <ul className="question-answers">
                {view.answered.map((a) => (
                  <li key={a.id}>
                    <b>{a.header}</b>：{a.text}
                  </li>
                ))}
              </ul>
            ),
          },
        ]}
      />
    );
  }

  const submit = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const res = await session.answerQuestion(view.requestId, buildAnswers(view.questions, draft));
      if (!res.ok) setError(res.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="card warn question-card">
      <div className="card-title">
        <QuestionCircleOutlined /> 提问
        <span className="pill" data-tone="waiting">{view.stateText}</span>
      </div>
      {view.questions.map((q) => {
        const d = draft[q.id] ?? { selected: [], custom: "" };
        return (
          <div key={q.id} className="question-block">
            {q.header && <div className="question-header">{q.header}</div>}
            <div className="card-body">{q.question}</div>
            {view.canAnswer ? (
              <div className="question-options">
                {q.multiSelect ? (
                  <Space orientation="vertical" size={4}>
                    {q.options.map((o) => (
                      <Checkbox key={o.label} checked={d.selected.includes(o.label)} onChange={() => setDraft(toggleOption(draft, q, o.label))}>
                        {o.label}
                        {o.description && <span className="chat-sub"> · {o.description}</span>}
                      </Checkbox>
                    ))}
                  </Space>
                ) : (
                  <Radio.Group value={d.selected[0]} onChange={(e) => setDraft(toggleOption({ ...draft, [q.id]: { ...d, selected: [] } }, q, e.target.value as string))}>
                    <Space orientation="vertical" size={4}>
                      {q.options.map((o) => (
                        <Radio key={o.label} value={o.label}>
                          {o.label}
                          {o.description && <span className="chat-sub"> · {o.description}</span>}
                        </Radio>
                      ))}
                    </Space>
                  </Radio.Group>
                )}
                <Input
                  className="question-custom"
                  size="small"
                  placeholder="其他（自己写）"
                  value={d.custom}
                  maxLength={2000}
                  onChange={(e) => setDraft(setCustom(draft, q.id, e.target.value))}
                />
              </div>
            ) : (
              <ul className="question-options-readonly">
                {q.options.map((o) => (
                  <li key={o.label}>{o.label}</li>
                ))}
              </ul>
            )}
          </div>
        );
      })}
      {error && <div className="card-body danger-text">{error}</div>}
      {view.canAnswer ? (
        <div className="card-actions">
          <Button type="primary" loading={busy} disabled={!canSubmit(view.questions, draft)} onClick={() => void submit()}>
            提交回答
          </Button>
        </div>
      ) : (
        <Handoff handoff={{ text: view.stateText, switchTo: actions.role === "initiator" ? [] : ["initiator"] }} onRole={actions.onRole} />
      )}
    </div>
  );
}
