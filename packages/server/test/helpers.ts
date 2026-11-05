import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { EmployeeConfig, FaultHooks, ModelSettings, ToolClient } from "@de/core/contract";
import { createApp } from "../src/index";
import type { ConnectionProbe, FolderOpener } from "../src/routes/deps";
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

export interface SetupOptions {
  webDistDir?: string;
  /** 工作区；设置路由读写 <workspace>/settings.json、备份到 <workspace>/config-versions */
  workspaceDir?: string;
  /** 员工配置目录；默认建一个含 weekly-report / patent-brief 的临时目录 */
  employeesDir?: string;
  /** 恢复默认员工配置的来源 */
  defaultEmployeesDir?: string;
  toolClient?: ToolClient;
  onSettingsSaved?: (settings: ModelSettings) => void | Promise<void>;
  /** 演示开关的运行时可变对象（与引擎共享） */
  faults?: FaultHooks;
  probeConnection?: ConnectionProbe;
  openFolder?: FolderOpener;
  /** 用系统编辑器打开设置文件（设置页“打开设置文件”） */
  openFile?: (path: string) => void | Promise<void>;
  env?: Record<string, string | undefined>;
  now?: () => Date;
}

export function setup(options: SetupOptions = {}) {
  const engine = createFakeEngine();
  const employeesDir = options.employeesDir ?? tempDir();
  if (!options.employeesDir) {
    for (const name of ["weekly-report", "patent-brief"]) {
      mkdirSync(join(employeesDir, name));
      writeFileSync(join(employeesDir, name, "workflow.yaml"), "id: x\n");
    }
    // 没有 workflow.yaml 的目录（如 employees/test）不是员工
    mkdirSync(join(employeesDir, "test"));
  }
  const workspaceDir = options.workspaceDir ?? tempDir();
  const app = createApp({
    engine,
    employeesDir,
    loadEmployee: fakeLoadEmployee,
    ...options,
    workspaceDir,
  });
  const call = (path: string, init?: RequestInit) => app.handle(new Request(`http://localhost${path}`, init));
  return { engine, app, call, employeesDir, workspaceDir };
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

export function jsonRequest(method: string, body: unknown): RequestInit {
  return { method, headers: { "content-type": "application/json" }, body: JSON.stringify(body) };
}
