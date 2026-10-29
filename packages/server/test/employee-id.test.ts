import { describe, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { setup } from "./helpers";

describe("员工 id 校验（HTTP 层）", () => {
  test("POST /api/tasks：非法 employee id 返回 400，不落到引擎", async () => {
    const { call, engine } = setup();
    for (const employee of ["Mini", "../weekly-report", "bad_name", "a/b", "weekly report"]) {
      const fd = new FormData();
      fd.set("employee", employee);
      fd.set("form", "{}");
      const res = await call("/api/tasks", { method: "POST", headers: { "x-role": "initiator" }, body: fd });
      expect(res.status).toBe(400);
      expect(((await res.json()) as { error: string }).error).toBe("invalid_input");
    }
    expect(engine.created).toHaveLength(0);
  });

  test("GET /api/employees：目录名不合法的员工不列出", async () => {
    const { call, employeesDir } = setup();
    for (const name of ["Mini", "bad_name"]) {
      mkdirSync(join(employeesDir, name), { recursive: true });
      writeFileSync(join(employeesDir, name, "workflow.yaml"), "id: x\n");
    }
    const list = (await (await call("/api/employees")).json()) as { id: string }[];
    expect(list.map((e) => e.id).sort()).toEqual(["patent-brief", "weekly-report"]);
  });
});
