import { mkdirSync, mkdtempSync } from "node:fs";
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
    qc: {} as EmployeeConfig["qc"],
    formSchema: { type: "object", title: id },
    prompts: {},
    outputSchemas: {},
  };
}

export function setup(options: { webDistDir?: string } = {}) {
  const engine = createFakeEngine();
  const employeesDir = tempDir();
  mkdirSync(join(employeesDir, "weekly-report"));
  mkdirSync(join(employeesDir, "patent-brief"));
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
