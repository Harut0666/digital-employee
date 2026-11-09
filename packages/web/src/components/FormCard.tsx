// 表单卡片（只读）：任务开工时的表单（submitted），以及旧会话里 T49 之前发出的草稿表单（superseded）。
// 现在的开工入口是“员工表单”卡（EmployeeFormCard），这里不再有提交按钮。

import { FileTextOutlined, PaperClipOutlined } from "@ant-design/icons";
import type { ChatItem } from "../view/chat-view";
import { formSummary } from "../view/form-summary";
import type { SessionActions } from "./Cards";

const size = (n: number) => (n >= 1024 * 1024 ? `${(n / 1024 / 1024).toFixed(1)} MB` : n >= 1024 ? `${Math.round(n / 1024)} KB` : `${n} B`);

function Attachments({ items }: { items: { name: string; size: number }[] }) {
  if (items.length === 0) return null;
  return (
    <div className="form-attachments">
      {items.map((a) => (
        <span key={a.name} className="chip">
          <PaperClipOutlined /> {a.name}
          {a.size > 0 && <span className="chat-sub"> {size(a.size)}</span>}
        </span>
      ))}
    </div>
  );
}

export function FormCard({ item, session }: { item: Extract<ChatItem, { kind: "form" }>; session: SessionActions }) {
  const employee = session.employees.find((e) => e.id === item.employee);
  const rows = employee ? formSummary(employee.formSchema, employee.formUi, item.draft) : [];
  const submitted = item.mode === "submitted";
  return (
    <div className={`card ${submitted ? "" : "muted"}`}>
      <div className="card-title">
        <FileTextOutlined style={{ color: "var(--primary)" }} /> {submitted ? "任务表单" : `${employee ? `${employee.name} · ` : ""}旧版表单草稿（已不再使用）`}
      </div>
      {submitted && (
        <>
          {rows.map((r) => (
            <div key={r.label} className="card-body">
              <span className="chat-sub">{r.label}：</span>
              {r.value}
            </div>
          ))}
          <Attachments items={item.attachments} />
        </>
      )}
    </div>
  );
}
