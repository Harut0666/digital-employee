// 引擎测试的公共装置：临时工作区、员工配置副本、桩判断执行器、桩质检引擎。
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { parse, stringify } from "yaml";
import { createTaskEngine } from "../../../src/engine";
import type {
  EngineDeps,
  FaultHooks,
  JudgmentRunner,
  JudgmentStepInput,
  JudgmentStepResult,
  QcCheckResult,
  QcEngine,
  QcInput,
  Role,
  TaskCommand,
  TaskEngine,
  TaskEvent,
  TaskSnapshot,
} from "../../../src/contract";
import { createMiniTools, type MiniTools } from "./mini-tools";

export const FIXTURE_EMPLOYEE_DIR = join(import.meta.dir, "..", "..", "fixtures", "employees", "mini");

// ---- 桩判断执行器 ----

export interface StubJudgmentOptions {
  /** 覆盖某步骤的输出；n 为本次真实模型调用的序号 */
  outputs?: Record<string, (input: JudgmentStepInput, n: number) => unknown>;
  /** 返回非 undefined 时直接作为本次运行的结果 */
  override?: (input: JudgmentStepInput) => JudgmentStepResult | undefined | Promise<JudgmentStepResult | undefined>;
  /** 每次运行开始时回调（用于推进假时钟等） */
  onRun?: (input: JudgmentStepInput) => void;
}

export function createStubJudgment(options: StubJudgmentOptions = {}) {
  const runs: JudgmentStepInput[] = [];
  let realCalls = 0;
  const runner: JudgmentRunner = {
    async run(input) {
      runs.push(input);
      options.onRun?.(input);
      const overridden = await options.override?.(input);
      if (overridden) return overridden;
      let response: unknown;
      if (input.replay.length > 0) {
        response = input.replay[0];
      } else {
        response = { stub: input.step.id, n: ++realCalls };
        await input.onModelCalled({ turn: 1, response, usage: { input: 10, output: 5 }, durationMs: 1 });
      }
      const n = (response as { n: number }).n;
      const custom = options.outputs?.[input.step.id];
      const output = custom
        ? custom(input, n)
        : input.step.id === "match_causes"
          ? { causes: [{ n }] }
          : { title: `报告#${n}` };
      return { ok: true, output, modelCalls: 1 };
    },
  };
  return { runner, runs, realCalls: () => realCalls };
}

// ---- 桩质检引擎 ----

export const passingChecks: QcCheckResult[] = [{ id: "structure", label: "结构", passed: true, issues: [] }];

export function failingChecks(fixStep: string | undefined, ...issues: string[]): QcCheckResult[] {
  return [{ id: "traceability", label: "数据溯源", passed: false, issues, fixStep }];
}

export function createStubQc(queue: QcCheckResult[][] = []) {
  const inputs: QcInput[] = [];
  const engine: QcEngine = {
    run(input) {
      inputs.push(input);
      return queue.shift() ?? passingChecks;
    },
  };
  return { engine, inputs, queue };
}

// ---- 工作区 ----

export interface HarnessOptions {
  /** 修改 workflow.yaml 的解析结果后写回 */
  patchWorkflow?: (workflow: Record<string, any>) => void;
  judgment?: JudgmentRunner;
  qc?: QcEngine;
  now?: () => Date;
  faults?: FaultHooks;
}

export interface Harness {
  workspaceDir: string;
  employeesDir: string;
  tools: MiniTools;
  engine: TaskEngine;
  /** 在同一工作区上新建引擎实例（模拟进程重启） */
  newEngine(overrides?: Partial<HarnessOptions>): TaskEngine;
  /** 建任务并提交，等到停下 */
  run(form?: Record<string, unknown>): Promise<TaskSnapshot>;
  create(form?: Record<string, unknown>): Promise<TaskSnapshot>;
  cmd(taskId: string, command: TaskCommand, actor: Role): ReturnType<TaskEngine["command"]>;
  cleanup(): Promise<void>;
}

const harnesses: Harness[] = [];

export async function cleanupAll(): Promise<void> {
  for (const h of harnesses.splice(0)) await h.cleanup();
}

export async function createHarness(options: HarnessOptions = {}): Promise<Harness> {
  const root = mkdtempSync(join(tmpdir(), "de-engine-"));
  const workspaceDir = join(root, "workspace");
  const employeesDir = join(root, "employees");
  mkdirSync(workspaceDir, { recursive: true });
  cpSync(FIXTURE_EMPLOYEE_DIR, join(employeesDir, "mini"), { recursive: true });
  if (options.patchWorkflow) {
    const file = join(employeesDir, "mini", "workflow.yaml");
    const workflow = parse(readFileSync(file, "utf8")) as Record<string, any>;
    options.patchWorkflow(workflow);
    writeFileSync(file, stringify(workflow));
  }
  const tools = await createMiniTools();

  const build = (overrides: Partial<HarnessOptions> = {}): TaskEngine => {
    const merged = { ...options, ...overrides };
    const deps: EngineDeps = {
      workspaceDir,
      employeesDir,
      toolClient: tools.client,
      model: { kind: "scripted" },
      now: merged.now,
      faults: merged.faults,
    };
    return createTaskEngine(deps, {
      judgment: merged.judgment ?? createStubJudgment().runner,
      qc: merged.qc ?? createStubQc().engine,
    });
  };

  const engine = build();
  const harness: Harness = {
    workspaceDir,
    employeesDir,
    tools,
    engine,
    newEngine: build,
    async create(form = {}) {
      return engine.create({
        employee: "mini",
        form: { sales: true, n: 3, period: { year: 2026, week: 44 }, ...form },
        attachments: [],
        actor: "initiator",
      });
    },
    async run(form = {}) {
      const draft = await harness.create(form);
      const submitted = await engine.command(draft.id, { type: "submit" }, "initiator");
      if (!submitted.ok) throw new Error(`submit 失败：${submitted.message}`);
      return engine.waitForSettled(draft.id);
    },
    cmd: (taskId, command, actor) => engine.command(taskId, command, actor),
    async cleanup() {
      await tools.client.close();
      rmSync(root, { recursive: true, force: true });
    },
  };
  harnesses.push(harness);
  return harness;
}

// ---- 事件断言辅助 ----

export function types(events: TaskEvent[]): string[] {
  return events.map((e) => e.type);
}

export function stateTrail(events: TaskEvent[]): string[] {
  const trail: string[] = [];
  for (const e of events) {
    if (e.type !== "state_changed") continue;
    if (trail.length === 0) trail.push(e.from);
    trail.push(e.to);
  }
  return trail;
}

export function startedSteps(events: TaskEvent[]): string[] {
  return events.flatMap((e) => (e.type === "step_started" ? [e.stepId] : []));
}

export function expectContiguousSeq(events: TaskEvent[]): void {
  events.forEach((e, i) => {
    if (e.seq !== i + 1) throw new Error(`seq 不连续：第 ${i} 条事件 seq=${e.seq}`);
  });
}
