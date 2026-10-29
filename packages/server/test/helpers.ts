import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { EmployeeConfig } from "@de/core/contract";
import { createApp } from "../src/index";
import { createFakeEngine } from "./fake-engine";

export function tempDir(prefix = "de-server-test-"): string {
  return mkdtempSync(join(tmpdir(), prefix));
}

/** 假员工加载器：只填服务端用到的字段。 */
export async function fakeLoadEmployee(dir: string): Promise<EmployeeConfig> {
  const id = dir.split(/[\\/]/).pop()!;
  return {
    id,
    dir,
    hash: "h",
    skill: "",
    frontdesk: "",
    workflow: { id, name: `员工-${id}`, description: `描述-${id}` } as EmployeeConfig["workflow"],
    qc: { builtin: { structure: { sections: [{ id: "summary", heading: `摘要-${id}` }] } } } as unknown as EmployeeConfig["qc"],
    formSchema: { type: "object", title: id },
    formUi: { "ui:order": ["a"] },
    prompts: {},
    outputSchemas: {},
  };
}

export function setup(options: { webDistDir?: string } = {}) {
  const engine = createFakeEngine();
  const employeesDir = tempDir();
  for (const name of ["weekly-report", "patent-brief"]) {
    mkdirSync(join(employeesDir, name));
    writeFileSync(join(employeesDir, name, "workflow.yaml"), "id: x\n");
  }
  // 没有 workflow.yaml 的目录（如 employees/test）不是员工
  mkdirSync(join(employeesDir, "test"));
  const app = createApp({ engine, employeesDir, loadEmployee: fakeLoadEmployee, ...options });
  const call = (path: string, init?: RequestInit) => app.handle(new Request(`http://localhost${path}`, init));
  return { engine, app, call, employeesDir };
}

export function jsonPost(path: string, body: unknown, role?: string): [string, RequestInit] {
  return [
    path,
    {
      method: "POST",
      headers: { "content-type": "application/json", ...(role ? { "x-role": role } : {}) },
      body: JSON.stringify(body),
    },
  ];
}
