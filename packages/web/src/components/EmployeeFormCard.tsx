// 员工表单卡（T49）：Agent 调 run_employee 整理好员工的输入，人在这里核对、补充、确认。
// 卡头写明员工，标出哪些字段和材料是 Agent 预填的、材料来自哪里；人可以直接改字段、补传材料、确认跳过可跳过的开工清单项；
// 必需项没备齐不能确认。两个按钮：“确认开工”和“不是这个员工”（把“用户拒绝，原因：选错员工”交回 Agent）。
// 卡片派生出的东西（标记、来源、不能确认的原因）都在 view/employee-form.ts。

import { useMemo, useRef, useState } from "react";
import Form from "@rjsf/antd";
import validator from "@rjsf/validator-ajv8";
import { getDefaultFormState, type RJSFSchema } from "@rjsf/utils";
import { Alert, Button, Tag, Upload } from "antd";
import { CheckOutlined, CloseOutlined, PaperClipOutlined, SettingOutlined, TeamOutlined, UploadOutlined } from "@ant-design/icons";
import type { SettingsEntry } from "../api";
import type { ChatItem } from "../view/chat-view";
import { deriveEmployeeForm } from "../view/employee-form";
import type { CheckRow } from "../view/checklist";
import { stripFileFields, uploadSlots } from "../view/upload-slots";
import { CheckRows } from "./Checklist";
import { Handoff, type CardActions, type SessionActions } from "./Cards";
import { FORM_FIELDS } from "./WeekField";

const size = (n: number) => (n >= 1024 * 1024 ? `${(n / 1024 / 1024).toFixed(1)} MB` : n >= 1024 ? `${Math.round(n / 1024)} KB` : `${n} B`);

export function EmployeeFormCard({
  item,
  actions,
  session,
}: {
  item: Extract<ChatItem, { kind: "employee_form" }>;
  actions: CardActions;
  session: SessionActions;
}) {
  const card = item.card;
  const employee = session.employees.find((e) => e.id === card.employee);
  const name = card.employeeName ?? employee?.name ?? card.employee ?? "员工";
  const { schema, uiSchema } = useMemo(
    () => (employee ? stripFileFields(employee.formSchema, employee.formUi) : { schema: {}, uiSchema: {} }),
    [employee],
  );
  const slots = useMemo(() => (employee ? uploadSlots(employee.formSchema, employee.formUi) : []), [employee]);
  const [formData, setFormData] = useState<Record<string, unknown>>(
    () => getDefaultFormState(validator, schema as RJSFSchema, card.draft ?? {}) as Record<string, unknown>,
  );
  const [skipped, setSkipped] = useState<ReadonlySet<string>>(new Set());
  const [busy, setBusy] = useState<"upload" | "confirm" | "reject">();
  const [refused, setRefused] = useState<{ message: string; settings?: SettingsEntry }>();
  const formRef = useRef<{ validateForm: () => boolean }>(null);

  const derived = deriveEmployeeForm({ card, formData, sessionAttachments: session.attachments, skipped, role: actions.role, writable: item.writable });
  const titleOf = (field: string) => {
    const props = (schema.properties ?? {}) as Record<string, { title?: string }>;
    return props[field]?.title ?? field;
  };

  if (derived.state !== "pending") {
    return (
      <div className={`card muted employee-form-done ${derived.state}`}>
        <TeamOutlined /> {name} · 员工表单 · {derived.stateText}
      </div>
    );
  }

  const initiator = derived.canAnswer;
  const marks = Object.entries(derived.fieldMarks);
  const rows: CheckRow[] = derived.checklist.map((row) => ({
    key: row.id,
    label: row.label,
    tone: row.status === "done" ? "ok" : row.status === "skipped" ? "skip" : "todo",
    stateText: row.status === "done" ? "已备齐" : row.status === "skipped" ? (row.skipLabel ? `已跳过` : "已跳过") : row.required ? "还缺" : "可跳过",
    skipLabel: row.skipLabel,
  }));

  const upload = async (files: File[]) => {
    setBusy("upload");
    try {
      await session.upload(files);
    } finally {
      setBusy(undefined);
    }
  };
  const confirm = async () => {
    if (formRef.current && !formRef.current.validateForm()) return;
    setBusy("confirm");
    setRefused(undefined);
    try {
      const res = await session.answerForm(card.requestId ?? "", {
        decision: "confirm",
        form: formData,
        attachments: derived.attachments,
        skipped: [...skipped],
      });
      if (!res.ok) setRefused({ message: res.message, settings: res.settings });
    } finally {
      setBusy(undefined);
    }
  };
  const reject = async () => {
    setBusy("reject");
    try {
      await session.answerForm(card.requestId ?? "", { decision: "reject" });
    } finally {
      setBusy(undefined);
    }
  };

  return (
    <div className="card form-card employee-form-card">
      <div className="card-title">
        <TeamOutlined style={{ color: "var(--primary)" }} /> {name} · 员工表单
      </div>
      <div className="card-body chat-sub">
        {card.reason ? `${card.reason}。` : ""}
        {card.standalone ? "" : "下面是 Agent 从对话和目录文件里整理的，"}请核对、补充，确认后{name}才开工。
      </div>
      {marks.length > 0 && (
        <div className="card-body employee-form-marks">
          {marks.map(([field, mark]) => (
            <Tag key={field} color={mark === "prefilled" ? "blue" : "gold"}>
              {titleOf(field)}：{mark === "prefilled" ? "Agent 预填" : "Agent 预填，已改"}
            </Tag>
          ))}
        </div>
      )}
      {employee ? (
        <Form
          ref={formRef as never}
          schema={schema as RJSFSchema}
          uiSchema={{ ...uiSchema, "ui:submitButtonOptions": { norender: true } }}
          formData={formData}
          validator={validator}
          fields={FORM_FIELDS}
          disabled={!initiator}
          onChange={(e) => setFormData((e.formData ?? {}) as Record<string, unknown>)}
          formContext={{ labelAlign: "left", labelCol: { span: 24 }, wrapperCol: { span: 24 } }}
          liveValidate={false}
          showErrorList={false}
        />
      ) : (
        <div className="card-body">员工配置读取中…</div>
      )}

      <div className="employee-form-materials">
        <div className="card-body">材料</div>
        {derived.materials.length === 0 && <div className="card-body chat-sub">还没有材料。</div>}
        {derived.materials.map((m) => (
          <div key={m.name} className="employee-form-material">
            <PaperClipOutlined /> {m.name}
            {m.size ? <span className="chat-sub"> {size(m.size)}</span> : null}
            <span className="chat-sub">　{m.origin}</span>
            {m.prefilled && <Tag color="blue">Agent 预填</Tag>}
          </div>
        ))}
        {slots.length > 0 && (
          <div className="form-uploads">
            {slots.map((s) => (
              <div key={s.slot} className="form-upload">
                <div>
                  <b>补传{s.title}</b>
                  {s.hint && <div className="chat-sub">{s.hint}</div>}
                </div>
                <Upload
                  multiple={s.multiple}
                  accept={s.accept || undefined}
                  showUploadList={false}
                  disabled={!initiator || busy !== undefined}
                  beforeUpload={(file, list) => {
                    // 多选时 antd 对每个文件各调一次；只在最后一个上统一上传
                    if (file === list[list.length - 1]) void upload(list as File[]);
                    return false;
                  }}
                >
                  <Button icon={<UploadOutlined />} loading={busy === "upload"} disabled={!initiator}>
                    上传
                  </Button>
                </Upload>
              </div>
            ))}
          </div>
        )}
      </div>

      {rows.length > 0 && (
        <div className="checklist employee-form-checklist">
          <div className="checklist-line">开工清单</div>
          <CheckRows
            rows={rows}
            busy={busy !== undefined}
            onSkip={initiator ? (key) => setSkipped(new Set([...skipped, key])) : undefined}
          />
          {skipped.size > 0 && initiator && (
            <Button size="small" type="link" onClick={() => setSkipped(new Set())}>
              撤销跳过
            </Button>
          )}
          <div className="checklist-note">由程序按表单和材料判定；跳过的项要你确认，会在会话里留痕。文件内容是否符合要求，开工后由准入校验检查。</div>
        </div>
      )}

      {initiator && derived.blockers.length > 0 && <div className="card-body chat-sub employee-form-blockers">{derived.blockers.join("；")}。</div>}
      {refused && (
        <Alert
          type="warning"
          showIcon
          style={{ marginTop: 10 }}
          message={refused.message}
          action={
            refused.settings && (
              <Button size="small" icon={<SettingOutlined />} onClick={actions.openSettings}>
                {refused.settings.label}
              </Button>
            )
          }
        />
      )}
      {initiator ? (
        <div className="card-actions" style={{ justifyContent: "flex-end" }}>
          <Button danger icon={<CloseOutlined />} loading={busy === "reject"} disabled={busy !== undefined && busy !== "reject"} onClick={() => void reject()}>
            不是这个员工
          </Button>
          <Button type="primary" icon={<CheckOutlined />} loading={busy === "confirm"} disabled={!derived.canConfirm || (busy !== undefined && busy !== "confirm")} onClick={() => void confirm()}>
            确认开工
          </Button>
        </div>
      ) : (
        <Handoff handoff={{ text: "等待发起人核对并确认", switchTo: ["initiator"] }} onRole={actions.onRole} />
      )}
    </div>
  );
}
