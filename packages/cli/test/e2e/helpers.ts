// 端到端测试的装置：真实引擎 + 真实工具服务器（MCP InMemoryTransport）+ 真实 employees/weekly-report 配置 + 剧本式假模型。
// 没有任何桩：质检、槽位渲染、Word 生成都是真的。
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { inflateRawSync } from "node:zlib";
import { join, resolve } from "node:path";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import {
  createMcpToolClient,
  createScriptedModelProvider,
  createTaskEngine,
  type AttachmentInput,
  type FaultConfig,
  type FaultHooks,
  type ModelScript,
  type Role,
  type TaskCommand,
  type TaskEngine,
  type TaskEvent,
  type TaskSnapshot,
  type ToolClient,
} from "@de/core";
import { createToolsServer, type ToolsServer } from "@de/tools";

export const REPO_ROOT = resolve(import.meta.dir, "..", "..", "..", "..");
export const EMPLOYEES_DIR = join(REPO_ROOT, "employees");
export const CASES_DIR = join(REPO_ROOT, "samples", "cases");
const SCENARIO_A = join(REPO_ROOT, "samples", "scenario-a");

export const SAMPLE = {
  sales: { name: "销售明细.xlsx", sourcePath: join(SCENARIO_A, "销售明细.xlsx") } satisfies AttachmentInput,
  minutes: (week: number): AttachmentInput => ({
    name: `周会纪要_2026W${week}.docx`,
    sourcePath: join(SCENARIO_A, `周会纪要_2026W${week}.docx`),
  }),
};

export function loadScript(name: string): ModelScript {
  return JSON.parse(readFileSync(join(CASES_DIR, "scripts", `${name}.json`), "utf8")) as ModelScript;
}

export interface E2E {
  workspaceDir: string;
  engine: TaskEngine;
  tools: ToolsServer;
  toolClient: ToolClient;
  /** 在同一工作区上新建引擎实例（模拟进程重启）；工具服务器沿用 */
  restart(options?: { script?: ModelScript; faults?: FaultHooks }): TaskEngine;
  /** 建任务、提交，等到停下 */
  launch(input: { week: number; attachments: AttachmentInput[]; form?: Record<string, unknown> }): Promise<TaskSnapshot>;
  cmd(taskId: string, command: TaskCommand, actor: Role): ReturnType<TaskEngine["command"]>;
  events(taskId: string): Promise<TaskEvent[]>;
  close(): Promise<void>;
}

const open: E2E[] = [];

export async function cleanupE2E(): Promise<void> {
  for (const e of open.splice(0)) await e.close();
}

export async function createE2E(options: { script?: ModelScript; faults?: FaultHooks; toolFaults?: FaultConfig } = {}): Promise<E2E> {
  const root = mkdtempSync(join(tmpdir(), "de-e2e-"));
  const workspaceDir = join(root, "workspace");

  const tools = createToolsServer({ faults: options.toolFaults, faultTimeoutMs: 50 });
  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  await tools.server.connect(serverTransport);
  const toolClient = await createMcpToolClient(clientTransport);

  const build = (script: ModelScript | undefined, faults: FaultHooks | undefined): TaskEngine =>
    createTaskEngine({
      workspaceDir,
      employeesDir: EMPLOYEES_DIR,
      toolClient,
      model: createScriptedModelProvider(script ?? {}),
      faults,
    });

  const state = { engine: build(options.script, options.faults) };
  const e2e: E2E = {
    workspaceDir,
    get engine() {
      return state.engine;
    },
    tools,
    toolClient,
    restart(next = {}) {
      state.engine = build(next.script, next.faults);
      return state.engine;
    },
    async launch({ week, attachments, form }) {
      const draft = await state.engine.create({
        employee: "weekly-report",
        form: { period: { year: 2026, week }, ...form },
        attachments,
        actor: "initiator",
      });
      const res = await state.engine.command(draft.id, { type: "submit" }, "initiator");
      if (!res.ok) throw new Error(`submit 被拒绝：${res.message}`);
      return state.engine.waitForSettled(draft.id);
    },
    cmd: (taskId, command, actor) => state.engine.command(taskId, command, actor),
    events: (taskId) => state.engine.events(taskId),
    async close() {
      await toolClient.close();
      rmSync(root, { recursive: true, force: true });
    },
  };
  open.push(e2e);
  return e2e;
}

// ---- 断言辅助 ----

export const ofType = <T extends TaskEvent["type"]>(events: TaskEvent[], type: T) =>
  events.filter((e): e is Extract<TaskEvent, { type: T }> => e.type === type);

export const startedSteps = (events: TaskEvent[]) => ofType(events, "step_started").map((e) => e.stepId);

export function stateTrail(events: TaskEvent[]): string[] {
  const trail: string[] = [];
  for (const e of ofType(events, "state_changed")) {
    if (trail.length === 0) trail.push(e.from);
    trail.push(e.to);
  }
  return trail;
}

export function expectFileExists(path: string | undefined): void {
  if (!path || !existsSync(path)) throw new Error(`文件不存在：${path}`);
}

/** 读出 .docx 正文的纯文本（docx 是 zip；只取 word/document.xml，去掉标签）。 */
export function readDocxText(path: string): string {
  const buf = readFileSync(path);
  // 找到中央目录结束记录
  let eocd = buf.length - 22;
  while (eocd >= 0 && buf.readUInt32LE(eocd) !== 0x06054b50) eocd--;
  if (eocd < 0) throw new Error("不是合法的 zip / docx");
  const entries = buf.readUInt16LE(eocd + 10);
  let p = buf.readUInt32LE(eocd + 16);
  for (let i = 0; i < entries; i++) {
    const method = buf.readUInt16LE(p + 10);
    const compressed = buf.readUInt32LE(p + 20);
    const nameLen = buf.readUInt16LE(p + 28);
    const extraLen = buf.readUInt16LE(p + 30);
    const commentLen = buf.readUInt16LE(p + 32);
    const localOffset = buf.readUInt32LE(p + 42);
    const name = buf.toString("utf8", p + 46, p + 46 + nameLen);
    if (name === "word/document.xml") {
      const dataStart = localOffset + 30 + buf.readUInt16LE(localOffset + 26) + buf.readUInt16LE(localOffset + 28);
      const raw = buf.subarray(dataStart, dataStart + compressed);
      const xml = (method === 8 ? inflateRawSync(raw) : raw).toString("utf8");
      return xml.replace(/<\/w:p>/g, "\n").replace(/<[^>]+>/g, "");
    }
    p += 46 + nameLen + extraLen + commentLen;
  }
  throw new Error("docx 里没有 word/document.xml");
}

/** 读任务目录里的产物（步骤输出）。 */
export function readArtifact<T = unknown>(path: string): T {
  return JSON.parse(readFileSync(path, "utf8")) as T;
}

export function stepOutputPath(events: TaskEvent[], stepId: string): string | undefined {
  return ofType(events, "step_completed")
    .filter((e) => e.stepId === stepId)
    .at(-1)?.outputPath;
}
