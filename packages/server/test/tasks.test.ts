import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { setup } from "./helpers";

function createForm(extra: Record<string, string> = {}, files: [string, string][] = []) {
  const fd = new FormData();
  fd.set("employee", "weekly-report");
  fd.set("form", JSON.stringify({ period: { year: 2026, week: 44 } }));
  for (const [name, content] of files) fd.append("attachments", new File([content], name));
  for (const [k, v] of Object.entries(extra)) fd.set(k, v);
  return fd;
}

describe("POST /api/tasks", () => {
  test("creates a draft task from multipart (employee, form JSON, attachments)", async () => {
    const { call, engine } = setup();
    let seenContent = "";
    const origCreate = engine.create.bind(engine);
    engine.create = async (input) => {
      // 引擎被调用时附件必须已落盘到 sourcePath
      seenContent = readFileSync(input.attachments[0]!.sourcePath, "utf8");
      expect(existsSync(input.attachments[0]!.sourcePath)).toBe(true);
      return origCreate(input);
    };
    const res = await call("/api/tasks", {
      method: "POST",
      headers: { "x-role": "initiator" },
      body: createForm({}, [["销售明细.csv", "a,b\n1,2"]]),
    });
    expect(res.status).toBe(201);
    const snap = (await res.json()) as { id: string; state: string; employee: string };
    expect(snap.state).toBe("draft");
    expect(snap.employee).toBe("weekly-report");
    expect(engine.created[0]).toMatchObject({
      employee: "weekly-report",
      form: { period: { year: 2026, week: 44 } },
      actor: "initiator",
    });
    expect(engine.created[0]!.attachments.map((a) => a.name)).toEqual(["销售明细.csv"]);
    expect(seenContent).toBe("a,b\n1,2");
  });

  test("works without attachments", async () => {
    const { call } = setup();
    const res = await call("/api/tasks", { method: "POST", headers: { "x-role": "initiator" }, body: createForm() });
    expect(res.status).toBe(201);
  });

  test("rejects a missing or unknown role with 403", async () => {
    const { call } = setup();
    const none = await call("/api/tasks", { method: "POST", body: createForm() });
    expect(none.status).toBe(403);
    const bad = await call("/api/tasks", { method: "POST", headers: { "x-role": "admin" }, body: createForm() });
    expect(bad.status).toBe(403);
    expect(((await bad.json()) as { error: string }).error).toBe("forbidden_role");
  });

  test("rejects malformed form JSON or missing employee with 400", async () => {
    const { call } = setup();
    const badJson = await call("/api/tasks", { method: "POST", headers: { "x-role": "initiator" }, body: createForm({ form: "{not json" }) });
    expect(badJson.status).toBe(400);
    expect(((await badJson.json()) as { error: string }).error).toBe("invalid_input");
    const fd = new FormData();
    fd.set("form", "{}");
    const noEmployee = await call("/api/tasks", { method: "POST", headers: { "x-role": "initiator" }, body: fd });
    expect(noEmployee.status).toBe(400);
  });

  test("path components in uploaded file names do not escape the upload directory", async () => {
    const { call, engine } = setup();
    await call("/api/tasks", {
      method: "POST",
      headers: { "x-role": "initiator" },
      body: createForm({}, [["..\\..\\evil.txt", "x"]]),
    });
    const a = engine.created[0]!.attachments[0]!;
    expect(a.name).toBe("evil.txt");
    expect(a.sourcePath.replace(/\\/g, "/")).not.toContain("..");
  });
});

describe("GET /api/tasks", () => {
  test("lists tasks and fetches one by id; unknown id is 404", async () => {
    const { call } = setup();
    const created = (await (await call("/api/tasks", { method: "POST", headers: { "x-role": "initiator" }, body: createForm() })).json()) as {
      id: string;
    };
    const list = (await (await call("/api/tasks")).json()) as { id: string; state: string }[];
    expect(list).toEqual([expect.objectContaining({ id: created.id, state: "draft" })]);

    const one = await call(`/api/tasks/${created.id}`);
    expect(one.status).toBe(200);
    expect(((await one.json()) as { id: string }).id).toBe(created.id);

    const missing = await call("/api/tasks/nope");
    expect(missing.status).toBe(404);
    expect(((await missing.json()) as { error: string }).error).toBe("not_found");
  });
});

