// 用例文件（samples/cases/README.md）的解析与执行。
// runCase 只依赖 TaskEngine 契约：测试里用内存假引擎，命令行里用真实引擎。
import { mkdirSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import type {
  AttachmentInput,
  DeliverableRef,
  PauseCode,
  Role,
  TaskCommand,
  TaskEngine,
  TaskSnapshot,
  TaskState,
} from "@de/core/contract";
import { parseChecks, type CaseChecks } from "./checks";

export type CaseActor = Exclude<Role, "system">;
const ACTORS: CaseActor[] = ["initiator", "reviewer", "operator"];
const COMMAND_TYPES: TaskCommand["type"][] = [
  "submit",
  "supplement",
  "continue_without_minutes",
  "continue_with_partial",
  "retry",
  "terminate",
  "approve",
  "return",
];

export interface Expectation {
  state?: TaskState;
  pauseCode?: PauseCode;
  /** 快照里必须存在的交付物种类 */
  deliverables?: DeliverableRef["kind"][];
  /** 判定要点（只在顶层 expect 里写），由 de check / de run-cases 核对交付物 */
  checks?: CaseChecks;
}

export interface StepExpectation extends Expectation {
  /** 命令是否应被接受，缺省为 true */
  ok?: boolean;
  /** ok=false 时引擎应返回的错误码 */
  error?: "illegal_transition" | "forbidden_role" | "invalid_input" | "not_found";
}

export interface CaseStep {
  actor: CaseActor;
  command: TaskCommand;
  expect?: StepExpectation;
}

export interface CaseDef {
  id: string;
  title: string;
  employee: string;
  form: Record<string, unknown>;
  attachments: AttachmentInput[];
  steps: CaseStep[];
  expect?: Expectation;
}

export interface StepResult {
  index: number;
  actor: CaseActor;
  command: TaskCommand["type"];
  ok: boolean;
  error?: string;
  message?: string;
  state?: TaskState;
  pauseCode?: PauseCode;
  failures: string[];
}

export interface CaseResult {
  caseId: string;
  title: string;
  passed: boolean;
  taskId: string;
  finalState: TaskState;
  pauseCode?: PauseCode;
  steps: StepResult[];
  deliverables: DeliverableRef[];
  failures: string[];
}

function fail(file: string, message: string): never {
  throw new Error(`用例文件无效（${file}）：${message}`);
}

function parseAttachments(raw: unknown, baseDir: string, where: string): AttachmentInput[] {
  if (raw === undefined) return [];
  if (!Array.isArray(raw)) fail(where, "attachments 必须是数组");
  return raw.map((a, i) => {
    const o = a as Record<string, unknown>;
    if (typeof o?.name !== "string" || o.name === "") fail(where, `attachments[${i}] 缺少 name`);
    if (typeof o.path !== "string" || o.path === "") fail(where, `attachments[${i}] 缺少 path`);
    return { name: o.name, sourcePath: resolve(baseDir, o.path) };
  });
}

/** 校验并规范化用例；相对路径按用例文件所在目录解析。 */
export function parseCase(raw: unknown, baseDir: string, fileName = "case.json"): CaseDef {
  const c = raw as Record<string, unknown>;
  if (typeof c !== "object" || c === null || Array.isArray(c)) fail(fileName, "顶层必须是对象");
  if (typeof c.id !== "string" || c.id === "") fail(fileName, "缺少 id");
  if (typeof c.employee !== "string" || c.employee === "") fail(fileName, "缺少 employee");
  if (!Array.isArray(c.steps) || c.steps.length === 0) fail(fileName, "steps 必须是非空数组");
  if (c.form !== undefined && (typeof c.form !== "object" || c.form === null || Array.isArray(c.form)))
    fail(fileName, "form 必须是对象");

  const steps = c.steps.map((s, i): CaseStep => {
    const o = s as Record<string, unknown>;
    const where = `steps[${i}]`;
    if (!ACTORS.includes(o?.actor as CaseActor)) fail(fileName, `${where}.actor 必须是 ${ACTORS.join(" / ")}`);
    const cmd = o.command as Record<string, unknown> | undefined;
    if (!cmd || !COMMAND_TYPES.includes(cmd.type as TaskCommand["type"]))
      fail(fileName, `${where}.command.type 必须是 ${COMMAND_TYPES.join(" / ")}`);
    if (cmd.type === "return" && (typeof cmd.comment !== "string" || cmd.comment === ""))
      fail(fileName, `${where}.command 的 return 必须有 comment`);
    if (o.expect !== undefined && typeof o.expect === "object" && o.expect !== null && "checks" in o.expect)
      fail(fileName, `${where}.expect 不支持 checks：判定要点只写在顶层 expect 里`);
    let command = cmd as unknown as TaskCommand;
    if (cmd.type === "supplement" && cmd.attachments !== undefined) {
      command = { ...(cmd as object), type: "supplement", attachments: parseAttachments(cmd.attachments, baseDir, fileName) } as TaskCommand;
    }
    return { actor: o.actor as CaseActor, command, expect: o.expect as StepExpectation | undefined };
  });

  return {
    id: c.id,
    title: typeof c.title === "string" ? c.title : c.id,
    employee: c.employee,
    form: (c.form as Record<string, unknown>) ?? {},
    attachments: parseAttachments(c.attachments, baseDir, fileName),
    steps,
    expect: parseExpectation(c.expect, fileName),
  };
}

/** 顶层 expect：状态类字段原样保留，checks 由 checks.ts 校验（格式错误直接报错）。 */
function parseExpectation(raw: unknown, fileName: string): Expectation | undefined {
  if (raw === undefined) return undefined;
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) fail(fileName, "expect 必须是对象");
  const o = raw as Record<string, unknown>;
  const checks = parseChecks(o.checks, `用例文件无效（${fileName}）：expect.checks`);
  return { ...(o as Expectation), ...(o.checks === undefined ? {} : { checks }) };
}

function check(snapshot: TaskSnapshot, expect: Expectation | undefined): string[] {
  const failures: string[] = [];
  if (!expect) return failures;
  if (expect.state !== undefined && snapshot.state !== expect.state)
    failures.push(`期望状态 ${expect.state}，实际 ${snapshot.state}`);
  if (expect.pauseCode !== undefined && snapshot.pause?.code !== expect.pauseCode)
    failures.push(`期望暂停原因 ${expect.pauseCode}，实际 ${snapshot.pause?.code ?? "无"}`);
  for (const kind of expect.deliverables ?? []) {
    if (!snapshot.deliverables.some((d) => d.kind === kind)) failures.push(`期望有交付物 ${kind}，实际没有`);
  }
  return failures;
}

export interface RunCaseOptions {
  /** 结果输出目录：snapshot.json、events.jsonl、result.json */
  outDir: string;
}

export async function runCase(engine: TaskEngine, def: CaseDef, options: RunCaseOptions): Promise<CaseResult> {
  const created = await engine.create({
    employee: def.employee,
    form: def.form,
    attachments: def.attachments,
    actor: "initiator",
  });
  const taskId = created.id;
  const steps: StepResult[] = [];
  let snapshot: TaskSnapshot = created;

  for (const [index, step] of def.steps.entries()) {
    const expectOk = step.expect?.ok ?? true;
    const res = await engine.command(taskId, step.command, step.actor);
    const failures: string[] = [];
    const result: StepResult = { index, actor: step.actor, command: step.command.type, ok: res.ok, failures };

    if (res.ok) {
      // 命令被接受后，引擎可能还在后台执行，等它停在暂停态或终态
      snapshot = await engine.waitForSettled(taskId);
      if (!expectOk) failures.push(`期望命令被拒绝，实际被接受`);
    } else {
      result.error = res.error;
      result.message = res.message;
      snapshot = (await engine.get(taskId)) ?? snapshot;
      if (expectOk) failures.push(`命令被拒绝：${res.error}（${res.message}）`);
      else if (step.expect?.error !== undefined && step.expect.error !== res.error)
        failures.push(`期望错误码 ${step.expect.error}，实际 ${res.error}`);
    }
    result.state = snapshot.state;
    result.pauseCode = snapshot.pause?.code;
    failures.push(...check(snapshot, step.expect));
    steps.push(result);
    if (failures.length > 0) break;
  }

  const stepFailures = steps.flatMap((s) => s.failures.map((f) => `第 ${s.index + 1} 步：${f}`));
  const finalFailures = steps.length === def.steps.length && stepFailures.length === 0 ? check(snapshot, def.expect) : [];
  const failures = [...stepFailures, ...finalFailures];

  const outcome: CaseResult = {
    caseId: def.id,
    title: def.title,
    passed: failures.length === 0,
    taskId,
    finalState: snapshot.state,
    pauseCode: snapshot.pause?.code,
    steps,
    deliverables: snapshot.deliverables,
    failures,
  };

  const events = await engine.events(taskId);
  mkdirSync(options.outDir, { recursive: true });
  writeFileSync(join(options.outDir, "snapshot.json"), JSON.stringify(snapshot, null, 2));
  writeFileSync(join(options.outDir, "events.jsonl"), events.map((e) => JSON.stringify(e)).join("\n") + (events.length ? "\n" : ""));
  writeFileSync(join(options.outDir, "result.json"), JSON.stringify(outcome, null, 2));
  return outcome;
}
