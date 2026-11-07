import { useMemo, useRef, useState } from "react";
import Form from "@rjsf/antd";
import validator from "@rjsf/validator-ajv8";
import { getDefaultFormState } from "@rjsf/utils";
import type { RJSFSchema } from "@rjsf/utils";
import { Button, Upload } from "antd";
import { InboxOutlined, SendOutlined } from "@ant-design/icons";
import type { EmployeeInfo, UploadItem } from "../api";
import { stripFileFields, uploadSlots, type UploadSlotDef } from "../view/upload-slots";
import { FORM_FIELDS } from "./WeekField";

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

// 材料对不对由服务端的准入校验判定，暂停说明会写明缺什么；这里不再按文件名猜。
export function LaunchPanel({
  employee,
  mode,
  initialForm,
  submitting,
  disabled,
  onSubmit,
}: {
  employee: EmployeeInfo;
  mode: "launch" | "supplement";
  initialForm?: Record<string, unknown>;
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

  const uploads: UploadItem[] = slots.flatMap((s) => (files[s.slot] ?? []).map((file) => ({ slot: s.slot, file })));

  return (
    <div>
      <div className="panel-card">
        <h4>{mode === "supplement" ? "补充材料" : "发起表单"}</h4>
        <Form
          ref={formRef as never}
          schema={schema}
          formData={formData}
          validator={validator}
          fields={FORM_FIELDS}
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


