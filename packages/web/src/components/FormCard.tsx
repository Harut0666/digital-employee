// 表单卡片：由员工的 form.schema.json / form.ui.json 驱动。草稿由前台 Agent 填，人检查、上传材料后点提交。
// 文件字段渲染成上传按钮，文件上传到会话，提交时才进任务。

import { useMemo, useRef, useState } from "react";
import Form from "@rjsf/antd";
import validator from "@rjsf/validator-ajv8";
import { getDefaultFormState, type RJSFSchema } from "@rjsf/utils";
import { Alert, Button, Upload } from "antd";
import { FileTextOutlined, PaperClipOutlined, SendOutlined, SettingOutlined, UploadOutlined } from "@ant-design/icons";
import type { SettingsEntry } from "../api";
import type { ChatItem } from "../view/chat-view";
import { formSummary } from "../view/form-summary";
import { stripFileFields, uploadSlots } from "../view/upload-slots";
import { Handoff, type CardActions, type SessionActions } from "./Cards";
import { FORM_FIELDS } from "./WeekField";

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

export function FormCard({ item, actions, session }: { item: Extract<ChatItem, { kind: "form" }>; actions: CardActions; session: SessionActions }) {
  const employee = session.employees.find((e) => e.id === item.employee);
  const { schema, uiSchema } = useMemo(
    () => (employee ? stripFileFields(employee.formSchema, employee.formUi) : { schema: {}, uiSchema: {} }),
    [employee],
  );
  const slots = useMemo(() => (employee ? uploadSlots(employee.formSchema, employee.formUi) : []), [employee]);
  const [formData, setFormData] = useState<Record<string, unknown>>(
    () => getDefaultFormState(validator, schema as RJSFSchema, item.draft) as Record<string, unknown>,
  );
  const [busy, setBusy] = useState<"upload" | "submit">();
  const [refused, setRefused] = useState<{ message: string; settings?: SettingsEntry }>();
  const formRef = useRef<{ validateForm: () => boolean }>(null);

  const title = employee ? `${employee.name} · 表单` : "表单";

  if (item.mode !== "editable") {
    const rows = employee ? formSummary(employee.formSchema, employee.formUi, item.draft) : [];
    return (
      <div className={`card ${item.mode === "submitted" ? "" : "muted"}`}>
        <div className="card-title">
          <FileTextOutlined style={{ color: "var(--primary)" }} /> {item.mode === "submitted" ? "任务表单" : `${title}（已更新，见下方最新表单）`}
        </div>
        {item.mode === "submitted" && (
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

  const initiator = actions.role === "initiator";
  const submit = async () => {
    if (formRef.current && !formRef.current.validateForm()) return;
    setBusy("submit");
    setRefused(undefined);
    try {
      const res = await session.submit(formData);
      if (!res.ok) setRefused({ message: res.message, settings: res.settings });
    } finally {
      setBusy(undefined);
    }
  };
  const upload = async (files: File[]) => {
    setBusy("upload");
    try {
      await session.upload(files);
    } finally {
      setBusy(undefined);
    }
  };

  return (
    <div className="card form-card">
      <div className="card-title">
        <FileTextOutlined style={{ color: "var(--primary)" }} /> {title}
      </div>
      <div className="card-body chat-sub">检查员工填好的草稿，上传材料后提交。提交后才开始工作。</div>
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
      <div className="form-uploads">
        {slots.map((s) => (
          <div key={s.slot} className="form-upload">
            <div>
              <b>{s.title}</b>
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
      <Attachments items={item.attachments} />
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
          <Button type="primary" icon={<SendOutlined />} loading={busy === "submit"} disabled={busy === "upload"} onClick={submit}>
            提交
          </Button>
        </div>
      ) : (
        <Handoff handoff={{ text: "等待发起人检查并提交", switchTo: ["initiator"] }} onRole={actions.onRole} />
      )}
    </div>
  );
}
