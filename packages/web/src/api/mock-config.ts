// 模拟数据层里的员工配置（VITE_MOCK=1）：内存里的配置文件、保存、版本与回滚。
// 只服务界面联调：保存时不做契约校验，差异用整段行差异表示。

import {
  FORM_PARAMS,
  FORM_PARAM_NAMES,
  FRONTDESK_READONLY_TOOLS,
  QC_TEMPLATES,
  type ConfigDiff,
  type ConfigDiffFile,
  type ConfigFileInfo,
  type ConfigVersion,
  type EmployeeConfigResponse,
  type EmployeeWorkflow,
  type FrontdeskDoc,
  type QcConfig,
  type RestoreConfigResponse,
  type SaveConfigRequest,
  type SaveConfigResponse,
  type SkillSections,
} from "@de/core/contract";
import { EMPLOYEES, STEP_DEFS } from "./mock-data";
import type { EmployeeInfo } from "./types";

type Json = Record<string, any>;

interface Content {
  skill: SkillSections;
  workflow: Json;
  qc: Json;
  formSchema: Json;
  formUi: Json;
  frontdesk: FrontdeskDoc;
  prompts: Record<string, string>;
  schemas: Record<string, Json>;
}

interface VersionEntry {
  version: ConfigVersion;
  snapshot: Content;
}

interface State {
  content: Content;
  versions: VersionEntry[];
}

class MockConfigError extends Error {
  readonly errors: string[];
  constructor(message: string, errors: string[] = []) {
    super(message);
    this.name = "MockConfigError";
    this.errors = errors;
  }
}

function clone<T>(value: T): T {
  return structuredClone(value);
}

function dump(value: unknown): string {
  return `${JSON.stringify(value, null, 2)}\n`;
}

function hashOf(text: string): string {
  let hash = 5381;
  for (let i = 0; i < text.length; i++) hash = ((hash << 5) + hash + text.charCodeAt(i)) | 0;
  return `mock-${(hash >>> 0).toString(16)}`;
}

function skillText(skill: SkillSections): string {
  return ["## 岗位职责", "", skill.duties, "", skill.writing, "", "## 注意事项", "", skill.cautions].join("\n");
}

function frontdeskText(doc: FrontdeskDoc): string {
  const list = doc.tools.map((tool) => `- ${tool}`).join("\n");
  return `${doc.body.replace(/\s+$/, "")}\n\n## 开放给你的只读工具\n\n${list}\n`;
}

function fileTexts(content: Content): Record<string, string> {
  const texts: Record<string, string> = {
    "SKILL.md": skillText(content.skill),
    "workflow.yaml": dump(content.workflow),
    "qc.yaml": dump(content.qc),
    "form.schema.json": dump(content.formSchema),
    "form.ui.json": dump(content.formUi),
    "frontdesk.md": frontdeskText(content.frontdesk),
    ...content.prompts,
  };
  for (const [path, schema] of Object.entries(content.schemas)) texts[path] = dump(schema);
  return texts;
}

function kindOf(path: string): ConfigFileInfo["kind"] {
  return path.endsWith(".yaml") ? "yaml" : path.endsWith(".json") ? "json" : "markdown";
}

function filesOf(content: Content): ConfigFileInfo[] {
  return Object.entries(fileTexts(content)).map(([path, text]) => {
    const kind = kindOf(path);
    const info: ConfigFileInfo = { path, kind, content: text };
    if (kind === "yaml" || kind === "json") {
      try {
        info.parsed = JSON.parse(text);
      } catch {
        info.error = "解析失败";
      }
    }
    return info;
  });
}

function judgeSteps(employee: EmployeeInfo): Json[] {
  return (STEP_DEFS[employee.id] ?? []).filter((s) => s.kind === "judge");
}

function buildContent(employee: EmployeeInfo): Content {
  const defs = STEP_DEFS[employee.id] ?? [];
  const steps: Json[] = defs.map((def) => {
    if (def.kind === "judge") {
      return {
        id: def.id,
        label: def.label,
        kind: "judge",
        prompt: `prompts/${def.id}.md`,
        input: {},
        tools: [],
        outputSchema: `schemas/${def.id}.json`,
        limits: { rounds: 6, durationSec: 90 },
        model: { connection: "default", model: "default" },
        rework: true,
        when: "always",
        optional: false,
      };
    }
    if (def.kind === "compute") {
      return { id: def.id, label: def.label, kind: "compute", tool: "tool", input: {}, optional: false };
    }
    return { id: def.id, label: def.label, kind: "qc" };
  });
  const prompts = Object.fromEntries(judgeSteps(employee).map((step) => [step.prompt as string, `# ${step.label}\n\n模拟提示词。\n`]));
  const schemas = Object.fromEntries(judgeSteps(employee).map((step) => [step.outputSchema as string, { type: "object", properties: {} }]));
  const sections = employee.reviewSections ?? [{ id: "summary", heading: "摘要" }];
  return {
    skill: {
      duties: `你是${employee.name}。\n`,
      writing: "## 写作要求\n\n模拟的写作要求。\n",
      cautions: "模拟的注意事项。\n",
    },
    workflow: {
      id: employee.id,
      name: employee.name,
      description: employee.description,
      limits: { task: { modelCalls: 30, durationSec: 600 }, toolRetry: { attempts: 3, backoffMs: [1000, 2000, 4000] }, qcAutofix: 3 },
      admissionStep: defs[0]?.id ?? "admission",
      steps,
    },
    qc: {
      builtin: {
        structure: { enabled: true, sections, summaryMaxSentences: 5, nextWeekMaxItems: 5, minCharts: 2 },
        traceability: { enabled: true },
        anomaly_coverage: { enabled: true, threshold: 0.1 },
        quote_integrity: { enabled: true },
        contradiction_coverage: { enabled: true },
        forbidden_terms: { enabled: true, terms: ["客单价", "同比"] },
        length: { enabled: true, maxPages: 3 },
        feedback_response: { enabled: true },
      },
      contradiction: { flatTolerance: 0.05, directionMin: 0.02, magnitudeTolerance: 0.1 },
      custom: [],
    },
    formSchema: clone(employee.formSchema),
    formUi: clone(employee.formUi ?? {}),
    frontdesk: { body: `# 前台 Agent · ${employee.name}\n\n说话平实、简短。\n`, tools: FRONTDESK_READONLY_TOOLS.map((t) => t.name) },
    prompts,
    schemas,
  };
}

function responseOf(employee: EmployeeInfo, state: State): EmployeeConfigResponse {
  const content = state.content;
  const texts = fileTexts(content);
  return {
    id: employee.id,
    name: employee.name,
    dir: `mock://${employee.id}`,
    hash: hashOf(JSON.stringify(content)),
    files: filesOf(content),
    config: {
      skill: texts["SKILL.md"]!,
      frontdesk: texts["frontdesk.md"]!,
      workflow: content.workflow as unknown as EmployeeWorkflow,
      qc: content.qc as unknown as QcConfig,
      formSchema: content.formSchema,
      formUi: content.formUi,
      prompts: content.prompts,
      outputSchemas: content.schemas,
    },
    views: {
      skill: content.skill,
      frontdesk: content.frontdesk,
      formParams: FORM_PARAM_NAMES.map((name) => ({ name, label: FORM_PARAMS[name] })),
      qcTemplates: QC_TEMPLATES,
    },
    formSchemaErrors: [],
    versions: state.versions.map((entry) => entry.version),
  };
}

function applySave(content: Content, path: string, input: SaveConfigRequest): void {
  if (input.data !== undefined) {
    const data = input.data;
    if (path === "SKILL.md") content.skill = data as SkillSections;
    else if (path === "frontdesk.md") content.frontdesk = data as FrontdeskDoc;
    else if (path === "workflow.yaml") content.workflow = data as Json;
    else if (path === "qc.yaml") content.qc = data as Json;
    else if (path === "form.schema.json") content.formSchema = data as Json;
    else if (path === "form.ui.json") content.formUi = data as Json;
    else if (path.startsWith("schemas/")) content.schemas[path] = data as Json;
    else throw new MockConfigError(`模拟数据模式不支持保存 ${path}`);
    return;
  }
  const text = input.content ?? "";
  if (path.startsWith("prompts/")) content.prompts[path] = text;
  else if (path.startsWith("schemas/")) {
    try {
      content.schemas[path] = JSON.parse(text) as Json;
    } catch {
      throw new MockConfigError(`${path} 不是合法的 JSON`);
    }
  } else if (path === "SKILL.md") content.skill = { duties: text, writing: "", cautions: "" };
  else if (path === "frontdesk.md") content.frontdesk = { body: text, tools: content.frontdesk.tools };
  else throw new MockConfigError(`模拟数据模式不支持保存 ${path} 原文`);
}

function lineDiff(before: string, after: string): string[] {
  const a = before.split("\n");
  const b = after.split("\n");
  const changed = a.filter((line, i) => b[i] !== line);
  const added = b.filter((line, i) => a[i] !== line);
  return ["@@ 模拟差异 @@", ...changed.map((line) => `-${line}`), ...added.map((line) => `+${line}`)].slice(0, 200);
}

export function createMockConfigStore() {
  const states = new Map<string, State>();
  let counter = 0;

  const employeeOf = (id: string): EmployeeInfo => {
    const employee = EMPLOYEES.find((e) => e.id === id);
    if (!employee) throw new MockConfigError(`员工不存在：${id}`);
    return employee;
  };

  const stateOf = (id: string): State => {
    let state = states.get(id);
    if (!state) {
      state = { content: buildContent(employeeOf(id)), versions: [] };
      states.set(id, state);
    }
    return state;
  };

  const nextVersion = (content: Content): ConfigVersion => {
    counter += 1;
    return { id: `mock-v${String(counter).padStart(2, "0")}`, createdAt: new Date().toISOString(), hash: hashOf(JSON.stringify(content)) };
  };

  return {
    get(id: string): EmployeeConfigResponse {
      return responseOf(employeeOf(id), stateOf(id));
    },

    save(id: string, path: string, input: SaveConfigRequest): SaveConfigResponse {
      const state = stateOf(id);
      const snapshot = clone(state.content);
      const version = nextVersion(state.content);
      applySave(state.content, path, input);
      state.versions.unshift({ version, snapshot });
      return { ok: true, path, version, hash: hashOf(JSON.stringify(state.content)) };
    },

    versions(id: string): ConfigVersion[] {
      return stateOf(id).versions.map((entry) => entry.version);
    },

    diff(id: string, versionId: string): ConfigDiff {
      const state = stateOf(id);
      const entry = state.versions.find((v) => v.version.id === versionId);
      if (!entry) throw new MockConfigError(`配置版本不存在：${versionId}`);
      const before = fileTexts(entry.snapshot);
      const current = fileTexts(state.content);
      const files: ConfigDiffFile[] = [];
      for (const path of [...new Set([...Object.keys(before), ...Object.keys(current)])].sort()) {
        const oldText = before[path];
        const newText = current[path];
        if (oldText === undefined && newText !== undefined) files.push({ path, status: "added", hunks: lineDiff("", newText) });
        else if (oldText !== undefined && newText === undefined) files.push({ path, status: "removed", hunks: lineDiff(oldText, "") });
        else if (oldText !== undefined && newText !== undefined && oldText !== newText) files.push({ path, status: "modified", hunks: lineDiff(oldText, newText) });
      }
      return { version: entry.version, hasChanges: files.length > 0, files };
    },

    restore(id: string, versionId: string): RestoreConfigResponse {
      const state = stateOf(id);
      const entry = state.versions.find((v) => v.version.id === versionId);
      if (!entry) throw new MockConfigError(`配置版本不存在：${versionId}`);
      const snapshot = clone(state.content);
      const version = nextVersion(state.content);
      state.content = clone(entry.snapshot);
      state.versions.unshift({ version, snapshot });
      return {
        ok: true,
        version,
        restoredFrom: versionId,
        versions: state.versions.map((v) => v.version),
        hash: hashOf(JSON.stringify(state.content)),
      };
    },
  };
}
