// de run-case 的接线：真实引擎 + 进程内工具服务器 + 真实或剧本式模型。
// 真实引擎在 T1 合并前会抛 "not implemented"，由 T7 集成验证。
import { existsSync, mkdirSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import {
  createMcpToolClient,
  createModelProvider,
  createScriptedModelProvider,
  createTaskEngine,
  loadSettings,
  resolveWorkspace,
  type ModelScript,
} from "@de/core";
import { createToolsServer } from "@de/tools";
import { parseCase, runCase } from "./case";

export interface RunCaseCommandArgs {
  caseFile: string;
  scriptedFile?: string;
  outDir?: string;
}

function readJson(file: string): unknown {
  if (!existsSync(file)) throw new Error(`找不到文件：${file}`);
  try {
    return JSON.parse(readFileSync(file, "utf8"));
  } catch (err) {
    throw new Error(`${file} 不是合法的 JSON：${err instanceof Error ? err.message : String(err)}`);
  }
}

export async function runCaseCommand(args: RunCaseCommandArgs): Promise<number> {
  const caseFile = resolve(args.caseFile);
  const def = parseCase(readJson(caseFile), dirname(caseFile), caseFile);
  const outDir = resolve(args.outDir ?? join("out", def.id));
  // 每次跑用例都用全新的工作区（放在输出目录里），回放时用 de replay <out>/workspace <taskId>
  const workspaceDir = process.env.DE_WORKSPACE ? resolve(process.env.DE_WORKSPACE) : join(outDir, "workspace");
  mkdirSync(join(workspaceDir, "tasks"), { recursive: true });

  const repoRoot = fileURLToPath(new URL("../../..", import.meta.url));
  const employeesDir = process.env.DE_EMPLOYEES_DIR ?? join(repoRoot, "employees");

  const model = args.scriptedFile
    ? createScriptedModelProvider(readJson(resolve(args.scriptedFile)) as ModelScript)
    : createModelProvider(loadSettings(resolveWorkspace()));

  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  await createToolsServer().server.connect(serverTransport);
  const toolClient = await createMcpToolClient(clientTransport);

  try {
    const engine = createTaskEngine({ workspaceDir, employeesDir, toolClient, model });
    const result = await runCase(engine, def, { outDir });
    console.log(`${result.caseId} ${result.passed ? "通过" : "失败"}  状态 ${result.finalState}  任务 ${result.taskId}`);
    for (const f of result.failures) console.log(`  - ${f}`);
    for (const d of result.deliverables) console.log(`  交付物 ${d.kind}: ${d.path}`);
    console.log(`  结果已写入 ${outDir}`);
    return result.passed ? 0 : 1;
  } finally {
    await toolClient.close();
  }
}
