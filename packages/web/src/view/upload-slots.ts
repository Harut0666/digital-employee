// 发起表单里的上传项：取自员工配置，而不是前端按员工写死。
// 约定：form.schema.json 里的某个字段在 form.ui.json 中配了 ui:widget 为 "file"（单个）或 "files"（多个），
// 它就是一个上传项，不再当普通字段渲染，上传的文件作为任务附件提交。

export interface UploadSlotDef {
  /** 字段名，也是 UploadItem.slot */
  slot: string;
  title: string;
  hint: string;
  accept: string;
  multiple: boolean;
}

type Json = Record<string, unknown>;

const isObject = (v: unknown): v is Json => typeof v === "object" && v !== null && !Array.isArray(v);

function fileFields(schema: Json, ui: Json | undefined): { name: string; widget: "file" | "files" }[] {
  const props = isObject(schema.properties) ? schema.properties : {};
  // 按 ui:order 排；Go 服务端返回的 Schema 字段是字母序
  const order = Array.isArray(ui?.["ui:order"]) ? (ui["ui:order"] as unknown[]).map(String).filter((k) => k in props) : [];
  const names = [...order, ...Object.keys(props).filter((k) => !order.includes(k))];
  return names.flatMap((name) => {
    const widget = isObject(ui?.[name]) ? (ui![name] as Json)["ui:widget"] : undefined;
    return widget === "file" || widget === "files" ? [{ name, widget }] : [];
  });
}

const GENERIC_SLOT: UploadSlotDef = {
  slot: "other",
  title: "资料文件",
  hint: "支持 Word、PDF、Markdown，单个文件不超过 50MB",
  accept: ".docx,.pdf,.md,.txt",
  multiple: true,
};

export function uploadSlots(formSchema: Json, formUi: Json | undefined): UploadSlotDef[] {
  const props = isObject(formSchema.properties) ? formSchema.properties : {};
  const slots = fileFields(formSchema, formUi).map(({ name, widget }): UploadSlotDef => {
    const field = isObject(props[name]) ? (props[name] as Json) : {};
    const ui = (formUi?.[name] ?? {}) as Json;
    const options = isObject(ui["ui:options"]) ? (ui["ui:options"] as Json) : {};
    return {
      slot: name,
      title: typeof field.title === "string" ? field.title : name,
      hint: typeof ui["ui:help"] === "string" ? ui["ui:help"] : typeof field.description === "string" ? field.description : "",
      accept: typeof options.accept === "string" ? options.accept : "",
      multiple: widget === "files",
    };
  });
  return slots.length > 0 ? slots : [GENERIC_SLOT];
}

/** 去掉文件字段后交给 @rjsf 渲染的 schema 和 uiSchema（文件由上传区负责，也不再要求这些字段）。 */
export function stripFileFields(formSchema: Json, formUi: Json | undefined): { schema: Json; uiSchema: Json } {
  const names = new Set(fileFields(formSchema, formUi).map((f) => f.name));
  const props = isObject(formSchema.properties) ? formSchema.properties : {};
  const schema: Json = {
    ...formSchema,
    properties: Object.fromEntries(Object.entries(props).filter(([k]) => !names.has(k))),
  };
  if (Array.isArray(formSchema.required)) schema.required = formSchema.required.filter((k) => !names.has(String(k)));

  const uiSchema: Json = Object.fromEntries(Object.entries(formUi ?? {}).filter(([k]) => !names.has(k)));
  if (Array.isArray(uiSchema["ui:order"])) uiSchema["ui:order"] = uiSchema["ui:order"].filter((k) => !names.has(String(k)));
  return { schema, uiSchema };
}
