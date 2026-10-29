import { useMemo, useRef, useState } from "react";
import Form from "@rjsf/antd";
import validator from "@rjsf/validator-ajv8";
import { getDefaultFormState } from "@rjsf/utils";
import type { RJSFSchema } from "@rjsf/utils";
import { Button, Upload } from "antd";
import { CheckCircleFilled, CloseCircleFilled, InboxOutlined, SendOutlined, MinusCircleOutlined } from "@ant-design/icons";
import type { PauseReason } from "@de/core/contract";
import type { EmployeeInfo, UploadItem } from "../api";
import { computePrecheck, missingKeysOfPause } from "../view/precheck";
import { stripFileFields, uploadSlots, type UploadSlotDef } from "../view/upload-slots";

export function UploadBox({ def, files, onChange }: { def: UploadSlotDef; files: File[]; onChange: (f: File[]) => void }) {
  return (
    <div className="panel-card">
      <h4>{def.title}</h4>
      <Upload.Dragger
        multiple={def.multiple}
        accept={def.accept || undefined}
        showUploadList={{ showRemoveIcon: true }}
        fileList={files.map((f, i) => ({ uid: `${f.name}-${i}`, name: f.name, size: f.size, status: "done" as const, originFileObj: f as never }))}
        beforeUpload={(f) => {
          onChange(def.multiple ? [...files, f] : [f]);
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
  // 文件字段由上传区负责，表单里只渲染其余字段
  const { schema: formSchema, uiSchema } = useMemo(() => stripFileFields(employee.formSchema, employee.formUi), [employee]);
  const schema = formSchema as RJSFSchema;
  const [formData, setFormData] = useState<Record<string, unknown>>(
    () => initialForm ?? (getDefaultFormState(validator, schema, {}) as Record<string, unknown>),
  );
  const slots = useMemo(() => uploadSlots(employee.formSchema, employee.formUi), [employee]);
  const [files, setFiles] = useState<Record<string, File[]>>({});
  const formRef = useRef<{ validateForm: () => boolean }>(null);

  const precheck = useMemo(() => {
    const all = (s: string) => (files[s] ?? []).map((f) => ({ name: f.name }));
    const period = (formData as { period?: { year: number; week: number } }).period;
    // 预检按文件字段名取（经营周报员工：salesFile、minutesFiles）
    return computePrecheck({ period, sales: all("salesFile"), minutes: all("minutesFiles") });
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
          uiSchema={{ ...uiSchema, "ui:submitButtonOptions": { norender: true } }}
          formContext={{ labelAlign: "left", labelCol: { span: 24 }, wrapperCol: { span: 24 } }}
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


