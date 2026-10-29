import { useMemo, useRef, useState } from "react";
import Form from "@rjsf/antd";
import validator from "@rjsf/validator-ajv8";
import { getDefaultFormState } from "@rjsf/utils";
import type { RJSFSchema } from "@rjsf/utils";
import { Button, Upload } from "antd";
import { CheckCircleFilled, CloseCircleFilled, InboxOutlined, SendOutlined, MinusCircleOutlined } from "@ant-design/icons";
import type { PauseReason } from "@de/core/contract";
import type { EmployeeInfo, UploadItem, UploadSlot } from "../api";
import { computePrecheck, missingKeysOfPause } from "../view/precheck";

interface SlotDef {
  slot: UploadSlot;
  title: string;
  hint: string;
  accept: string;
}

/** 各员工的上传区。契约里没有“员工需要哪些附件”，先在前端按员工写死，T5 之后可改为读配置。 */
const SLOTS: Record<string, SlotDef[]> = {
  "weekly-report": [
    { slot: "sales", title: "销售明细", hint: "支持 Excel、CSV，单个文件不超过 50MB", accept: ".xlsx,.xls,.csv" },
    { slot: "minutes", title: "周会纪要", hint: "支持 Word、PDF，单个文件不超过 50MB", accept: ".docx,.pdf" },
  ],
};
const DEFAULT_SLOTS: SlotDef[] = [{ slot: "other", title: "资料文件", hint: "支持 Word、PDF、Markdown，单个文件不超过 50MB", accept: ".docx,.pdf,.md,.txt" }];

export function UploadBox({ def, files, onChange }: { def: SlotDef; files: File[]; onChange: (f: File[]) => void }) {
  return (
    <div className="panel-card">
      <h4>{def.title}</h4>
      <Upload.Dragger
        multiple
        accept={def.accept}
        showUploadList={{ showRemoveIcon: true }}
        fileList={files.map((f, i) => ({ uid: `${f.name}-${i}`, name: f.name, size: f.size, status: "done" as const, originFileObj: f as never }))}
        beforeUpload={(f) => {
          onChange([...files, f]);
          return false;
        }}
        onRemove={(f) => onChange(files.filter((x, i) => `${x.name}-${i}` !== f.uid))}
      >
        <p style={{ margin: 0 }}>
          <InboxOutlined /> 点击上传或拖拽文件到此处
        </p>
        <p className="chat-sub" style={{ margin: 0 }}>{def.hint}</p>
      </Upload.Dragger>
    </div>
  );
}

export function PrecheckList({ items, missing }: { items: ReturnType<typeof computePrecheck>; missing: string[] }) {
  return (
    <div className="panel-card">
      <h4>准入预检</h4>
      {items.map((i) => {
        const red = !i.ok && missing.includes(i.key);
        return (
          <div key={i.key} className={`check-row ${red ? "missing" : ""}`}>
            {i.ok ? <CheckCircleFilled style={{ color: "var(--primary)" }} /> : red ? <CloseCircleFilled /> : <MinusCircleOutlined style={{ color: "#b8c0c9" }} />}
            {i.label}
            <span className="state">{i.ok ? "已就绪" : red ? "缺失" : "待上传"}</span>
          </div>
        );
      })}
    </div>
  );
}

export function LaunchPanel({
  employee,
  mode,
  initialForm,
  pause,
  submitting,
  disabled,
  onSubmit,
}: {
  employee: EmployeeInfo;
  mode: "launch" | "supplement";
  initialForm?: Record<string, unknown>;
  pause?: PauseReason;
  submitting: boolean;
  disabled?: boolean;
  onSubmit: (form: Record<string, unknown>, uploads: UploadItem[]) => void;
}) {
  const schema = employee.formSchema as RJSFSchema;
  const [formData, setFormData] = useState<Record<string, unknown>>(
    () => initialForm ?? (getDefaultFormState(validator, schema, {}) as Record<string, unknown>),
  );
  const slots = SLOTS[employee.id] ?? DEFAULT_SLOTS;
  const [files, setFiles] = useState<Record<string, File[]>>({});
  const formRef = useRef<{ validateForm: () => boolean }>(null);

  const precheck = useMemo(() => {
    const all = (s: UploadSlot) => (files[s] ?? []).map((f) => ({ name: f.name }));
    const period = (formData as { period?: { year: number; week: number } }).period;
    return computePrecheck({ period, sales: all("sales"), minutes: all("minutes") });
  }, [files, formData]);

  const uploads: UploadItem[] = slots.flatMap((s) => (files[s.slot] ?? []).map((file) => ({ slot: s.slot, file })));
  const missing = pause ? missingKeysOfPause(pause.code) : [];

  return (
    <div>
      <div className="panel-card">
        <h4>{mode === "supplement" ? "补充材料" : "发起表单"}</h4>
        <Form
          ref={formRef as never}
          schema={schema}
          formData={formData}
          validator={validator}
          onChange={(e) => setFormData((e.formData ?? {}) as Record<string, unknown>)}
          uiSchema={{ "ui:submitButtonOptions": { norender: true } }}
          liveValidate={false}
          showErrorList={false}
        />
      </div>
      {slots.map((s) => (
        <UploadBox key={s.slot} def={s} files={files[s.slot] ?? []} onChange={(f) => setFiles((prev) => ({ ...prev, [s.slot]: f }))} />
      ))}
      {employee.id === "weekly-report" && <PrecheckList items={precheck} missing={missing} />}
      <Button
        type="primary"
        size="large"
        block
        icon={<SendOutlined />}
        loading={submitting}
        disabled={disabled}
        onClick={() => {
          if (formRef.current && !formRef.current.validateForm()) return;
          onSubmit(formData, uploads);
        }}
      >
        {mode === "supplement" ? "补充并继续" : "提交"}
      </Button>
    </div>
  );
}

