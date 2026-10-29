import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { jsonPost, setup } from "./helpers";

async function newTask(call: ReturnType<typeof setup>["call"]) {
  const fd = new FormData();
  fd.set("employee", "weekly-report");
  fd.set("form", "{}");
  const res = await call("/api/tasks", { method: "POST", headers: { "x-role": "initiator" }, body: fd });
  return ((await res.json()) as { id: string }).id;
}

describe("POST /api/tasks/:id/commands", () => {
  test("initiator submits and the task runs to awaiting_review; reviewer approves it", async () => {
    const { call, engine } = setup();
    const id = await newTask(call);

    const submit = await call(...jsonPost(`/api/tasks/${id}/commands`, { type: "submit" }, "initiator"));
    expect(submit.status).toBe(200);
    const body = (await submit.json()) as { ok: true; snapshot: { state: string } };
    expect(body.ok).toBe(true);
    expect(body.snapshot.state).toBe("awaiting_review");

    const approve = await call(...jsonPost(`/api/tasks/${id}/commands`, { type: "approve" }, "reviewer"));
    expect(approve.status).toBe(200);
    expect(((await approve.json()) as { snapshot: { state: string } }).snapshot.state).toBe("delivered");
    expect(engine.commands.map((c) => [c.command.type, c.actor])).toEqual([
      ["submit", "initiator"],
      ["approve", "reviewer"],
    ]);
  });

  test("a wrong role gets 403 forbidden_role and the rejection is traced in the event log", async () => {
    const { call } = setup();
    const id = await newTask(call);
    await call(...jsonPost(`/api/tasks/${id}/commands`, { type: "submit" }, "initiator"));

    const res = await call(...jsonPost(`/api/tasks/${id}/commands`, { type: "approve" }, "initiator"));
    expect(res.status).toBe(403);
    expect(await res.json()).toMatchObject({ ok: false, error: "forbidden_role" });

    const events = (await (await call(`/api/tasks/${id}/events`)).json()) as { type: string; reason?: string }[];
    expect(events.some((e) => e.type === "transition_rejected" && e.reason === "forbidden_role")).toBe(true);
  });

  test("an illegal transition gets 409", async () => {
    const { call } = setup();
    const id = await newTask(call);
    const res = await call(...jsonPost(`/api/tasks/${id}/commands`, { type: "approve" }, "reviewer"));
    expect(res.status).toBe(409);
    expect(await res.json()).toMatchObject({ ok: false, error: "illegal_transition" });
  });

  test("a missing role header is 403 and never reaches the engine", async () => {
    const { call, engine } = setup();
    const id = await newTask(call);
    const res = await call(...jsonPost(`/api/tasks/${id}/commands`, { type: "submit" }));
    expect(res.status).toBe(403);
    expect(engine.commands).toHaveLength(0);
  });

  test("unknown task is 404; malformed command is 400", async () => {
    const { call, engine } = setup();
    const missing = await call(...jsonPost("/api/tasks/nope/commands", { type: "submit" }, "initiator"));
    expect(missing.status).toBe(404);

    const id = await newTask(call);
    for (const bad of [{ type: "explode" }, { type: "return" }, { type: "return", comment: "  " }, "str"]) {
      const res = await call(...jsonPost(`/api/tasks/${id}/commands`, bad, "reviewer"));
      expect(res.status).toBe(400);
      expect(((await res.json()) as { error: string }).error).toBe("invalid_input");
    }
    expect(engine.commands).toHaveLength(1); // 只有 nope 那次进了引擎
  });

  test("return carries comment and sections through to the engine", async () => {
    const { call, engine } = setup();
    const id = await newTask(call);
    await call(...jsonPost(`/api/tasks/${id}/commands`, { type: "submit" }, "initiator"));
    const res = await call(...jsonPost(`/api/tasks/${id}/commands`, { type: "return", comment: "数字再核对", sections: ["summary"] }, "reviewer"));
    expect(res.status).toBe(200);
    expect(engine.commands.at(-1)!.command).toEqual({ type: "return", comment: "数字再核对", sections: ["summary"] });
  });

  test("supplement via multipart saves attachments and passes them to the engine", async () => {
    const { call, engine } = setup();
    const id = await newTask(call);
    const fd = new FormData();
    fd.set("command", JSON.stringify({ type: "supplement", form: { note: "补传" } }));
    fd.append("attachments", new File(["xlsx-bytes"], "销售明细.xlsx"));
    let content = "";
    const orig = engine.command.bind(engine);
    engine.command = async (taskId, command, actor) => {
      if (command.type === "supplement") {
        const p = command.attachments![0]!.sourcePath;
        expect(existsSync(p)).toBe(true);
        content = readFileSync(p, "utf8");
      }
      return orig(taskId, command, actor);
    };
    await call(`/api/tasks/${id}/commands`, { method: "POST", headers: { "x-role": "initiator" }, body: fd });
    const sent = engine.commands.at(-1)!.command;
    expect(sent.type).toBe("supplement");
    expect(sent).toMatchObject({ form: { note: "补传" }, attachments: [{ name: "销售明细.xlsx" }] });
    expect(content).toBe("xlsx-bytes");
  });
});

describe("GET /api/tasks/:id/events", () => {
  test("returns events in order; ?after= skips earlier ones; unknown task is 404", async () => {
    const { call } = setup();
    const id = await newTask(call);
    await call(...jsonPost(`/api/tasks/${id}/commands`, { type: "submit" }, "initiator"));

    const all = (await (await call(`/api/tasks/${id}/events`)).json()) as { seq: number }[];
    expect(all.map((e) => e.seq)).toEqual(all.map((_, i) => i + 1));
    expect(all.length).toBeGreaterThan(3);

    const after = (await (await call(`/api/tasks/${id}/events?after=3`)).json()) as { seq: number }[];
    expect(after.map((e) => e.seq)).toEqual(all.slice(3).map((e) => e.seq));

    expect((await call("/api/tasks/nope/events")).status).toBe(404);
    expect((await call(`/api/tasks/${id}/events?after=abc`)).status).toBe(400);
  });
});
