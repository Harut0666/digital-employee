// T1 实现：员工配置加载与校验。
import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { parse as parseYaml } from "yaml";
import { EmployeeWorkflowSchema, QcConfigSchema, type EmployeeConfig } from "../contract";
import { workflowConsistencyProblems } from "./consistency";

export { loadSettings, resolveWorkspace } from "./workspace";
export { collectWritingRequirements, validateFormSchemaPurposes } from "./form";
export { CONFIG_ROOT_FILES, describeConfigFiles, isWritableConfigPath, listConfigFiles, normalizeConfigPath, readConfigFileText, readConfigFiles } from "./files";
export { prepareConfigText, type ConfigFileInput, type PrepareConfigResult } from "./write";
export {
  createConfigVersion,
  diffConfigVersion,
  listConfigVersions,
  readConfigVersion,
  removeConfigVersion,
  restoreConfigVersion,
  versionsDirFor,
} from "./versions";
export { formatFrontdesk, joinSkill, parseFrontdesk, splitSkill, FRONTDESK_TOOLS_HEADING } from "./markdown";
export { workflowConsistencyProblems } from "./consistency";

function readText(path: string): string {
  return readFileSync(path, "utf8");
}

function requireFile(dir: string, name: string): string {
  const path = join(dir, name);
  if (!existsSync(path)) throw new Error(`员工配置缺少文件 ${name}（目录 ${dir}）`);
  return readText(path);
}

function parseYamlFile(dir: string, name: string): unknown {
  const text = requireFile(dir, name);
  try {
    return parseYaml(text);
  } catch (error) {
    throw new Error(`${name} 不是合法的 YAML：${(error as Error).message}`);
  }
}

function parseJson(text: string, name: string): Record<string, unknown> {
  try {
    return JSON.parse(text) as Record<string, unknown>;
  } catch (error) {
    throw new Error(`${name} 不是合法的 JSON：${(error as Error).message}`);
  }
}

/** 递归列出目录内的全部文件，返回相对路径（正斜杠），按字典序。 */
function listFiles(root: string, rel = ""): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(join(root, rel)).sort()) {
    const relPath = rel ? `${rel}/${entry}` : entry;
    if (statSync(join(root, relPath)).isDirectory()) out.push(...listFiles(root, relPath));
    else out.push(relPath);
  }
  return out;
}

function normalizeRef(ref: string): string {
  return ref.replace(/\\/g, "/").replace(/^\.\//, "");
}

function hashDirectory(dir: string): string {
  const hash = createHash("sha256");
  for (const rel of listFiles(dir)) {
    hash.update(rel);
    hash.update("\0");
    hash.update(readFileSync(join(dir, rel)));
    hash.update("\0");
  }
  return hash.digest("hex");
}

export async function loadEmployeeConfig(dir: string): Promise<EmployeeConfig> {
  return loadEmployeeConfigSync(dir);
}

/** 同步版本，供引擎在内部使用（配置文件很小，读取是同步的）。 */
export function loadEmployeeConfigSync(dir: string): EmployeeConfig {
  const root = resolve(dir);
  if (!existsSync(root) || !statSync(root).isDirectory()) throw new Error(`员工配置目录不存在：${root}`);

  const workflowRaw = parseYamlFile(root, "workflow.yaml");
  const workflowResult = EmployeeWorkflowSchema.safeParse(workflowRaw);
  if (!workflowResult.success) throw new Error(`workflow.yaml 不符合契约：${workflowResult.error.message}`);
  const workflow = workflowResult.data;

  const qcResult = QcConfigSchema.safeParse(parseYamlFile(root, "qc.yaml"));
  if (!qcResult.success) throw new Error(`qc.yaml 不符合契约：${qcResult.error.message}`);

  const problems = workflowConsistencyProblems(workflow);
  if (problems.length > 0) throw new Error(problems[0]!);

  const prompts: Record<string, string> = {};
  const outputSchemas: Record<string, Record<string, unknown>> = {};
  for (const rel of listFiles(root)) {
    if (rel.startsWith("prompts/") && rel.endsWith(".md")) prompts[rel] = readText(join(root, rel));
    if (rel.startsWith("schemas/") && rel.endsWith(".json")) outputSchemas[rel] = parseJson(readText(join(root, rel)), rel);
  }
  for (const step of workflow.steps) {
    if (step.kind !== "judge") continue;
    const promptKey = normalizeRef(step.prompt);
    if (!(promptKey in prompts)) prompts[promptKey] = requireFile(root, step.prompt);
    const schemaKey = normalizeRef(step.outputSchema);
    if (!(schemaKey in outputSchemas)) outputSchemas[schemaKey] = parseJson(requireFile(root, step.outputSchema), step.outputSchema);
  }

  const formPath = join(root, "form.schema.json");
  const formUiPath = join(root, "form.ui.json");
  return {
    id: workflow.id,
    dir: root,
    hash: hashDirectory(root),
    skill: existsSync(join(root, "SKILL.md")) ? readText(join(root, "SKILL.md")) : "",
    frontdesk: existsSync(join(root, "frontdesk.md")) ? readText(join(root, "frontdesk.md")) : "",
    workflow,
    qc: qcResult.data,
    formSchema: existsSync(formPath) ? parseJson(readText(formPath), "form.schema.json") : {},
    formUi: existsSync(formUiPath) ? parseJson(readText(formUiPath), "form.ui.json") : undefined,
    prompts,
    outputSchemas,
  };
}
