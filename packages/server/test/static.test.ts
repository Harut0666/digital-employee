import { describe, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { setup, tempDir } from "./helpers";

async function newTask(call: ReturnType<typeof setup>["call"]) {
  const fd = new FormData();
  fd.set("employee", "weekly-report");
  fd.set("form", "{}");
  return ((await (await call("/api/tasks", { method: "POST", headers: { "x-role": "initiator" }, body: fd })).json()) as { id: string }).id;
}

describe("GET /api/tasks/:id/deliverables/:kind", () => {
  test("downloads the deliverable file with a proper content type and file name", async () => {
    const { call, engine } = setup();
    const id = await newTask(call);
    const dir = tempDir();
    const docx = join(dir, "周报_W44.docx");
    writeFileSync(docx, "fake-docx-bytes");
    engine.setDeliverable(id, "docx", docx);

    const res = await call(`/api/tasks/${id}/deliverables/docx`);
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toBe("application/vnd.openxmlformats-officedocument.wordprocessingml.document");
    expect(res.headers.get("content-disposition")).toContain(encodeURIComponent("周报_W44.docx"));
    expect(await res.text()).toBe("fake-docx-bytes");
  });

  test("404 for unknown task, unknown kind, or deliverable not yet produced", async () => {
    const { call, engine } = setup();
    expect((await call("/api/tasks/nope/deliverables/docx")).status).toBe(404);
    const id = await newTask(call);
    expect((await call(`/api/tasks/${id}/deliverables/docx`)).status).toBe(404);
    expect((await call(`/api/tasks/${id}/deliverables/exe`)).status).toBe(404);
    engine.setDeliverable(id, "md", join(tempDir(), "missing.md")); // 记录在案但文件不在
    expect((await call(`/api/tasks/${id}/deliverables/md`)).status).toBe(404);
  });
});

describe("web static hosting", () => {
  function webDist() {
    const dir = tempDir("de-web-");
    writeFileSync(join(dir, "index.html"), "<html>INDEX</html>");
    mkdirSync(join(dir, "assets"));
    writeFileSync(join(dir, "assets", "app.js"), "console.log(1)");
    return dir;
  }

  test("serves files, falls back to index.html for client routes, leaves /api alone", async () => {
    const { call } = setup({ webDistDir: webDist() });
    const root = await call("/");
    expect(root.status).toBe(200);
    expect(await root.text()).toBe("<html>INDEX</html>");

    const js = await call("/assets/app.js");
    expect(js.status).toBe(200);
    expect(js.headers.get("content-type")).toContain("javascript");

    const route = await call("/tasks/T-1");
    expect(await route.text()).toBe("<html>INDEX</html>");

    const api = await call("/api/nothing-here");
    expect(api.status).toBe(404);
    expect(await api.text()).not.toContain("INDEX");
  });

  test("path traversal cannot escape the dist directory", async () => {
    const dist = webDist();
    writeFileSync(join(dist, "..", "secret.txt"), "SECRET");
    const { call } = setup({ webDistDir: dist });
    const res = await call("/..%2Fsecret.txt");
    expect(await res.text()).not.toContain("SECRET");
  });

  test("without webDistDir nothing is hosted", async () => {
    const { call } = setup();
    expect((await call("/")).status).toBe(404);
  });
});

describe("CORS", () => {
  test("preflight from the Vite dev server may send the x-role header", async () => {
    const { call } = setup();
    const res = await call("/api/tasks", {
      method: "OPTIONS",
      headers: { origin: "http://localhost:5173", "access-control-request-method": "POST", "access-control-request-headers": "x-role,content-type" },
    });
    expect(res.status).toBeLessThan(300);
    expect(res.headers.get("access-control-allow-headers")?.toLowerCase()).toContain("x-role");
  });
});
