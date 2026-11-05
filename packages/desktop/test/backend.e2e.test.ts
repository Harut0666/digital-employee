// 接缝 A：共享装配的目录资源来源 + 桌面后端（真实工具服务器 InMemoryTransport + 剧本模型），经 HTTP 跑通 A-01。
// 断言外部行为：窗口要加载的地址可访问、首页/员工接口正确、任务能到 delivered、交付 docx 可下载且内容正确、关窗后端口释放。
import { afterEach, describe, expect, test } from "bun:test";
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { inflateRawSync } from "node:zlib";
import { isPortAvailable } from "@de/server/port";
import { startDesktopBackend, type DesktopBackend } from "../src/backend";

const REPO_ROOT = resolve(import.meta.dir, "..", "..", "..");
const SCENARIO_A = join(REPO_ROOT, "samples", "scenario-a");
const CASES = join(REPO_ROOT, "samples", "cases");
const RESVG_WASM = join(REPO_ROOT, "packages", "tools", "node_modules", "@resvg", "resvg-wasm", "index_bg.wasm");

const open: Array<{ backend: DesktopBackend; root: string }> = [];

afterEach(async () => {
  for (const entry of open.splice(0)) {
    await entry.backend.close().catch(() => {});
    rmSync(entry.root, { recursive: true, force: true });
  }
});

/** 造一份“桌面构建期资源目录”，布局与 electrobun.config.ts 的 build.copy 一致。 */
function stageAssets(root: string): string {
  const assets = join(root, "assets");
  mkdirSync(join(assets, "web"), { recursive: true });
  writeFileSync(join(assets, "web", "index.html"), "<!doctype html><html><body>迷你数字员工</body></html>");
  mkdirSync(join(assets, "employees"), { recursive: true });
  mkdirSync(join(assets, "samples"), { recursive: true });
  cpSync(join(REPO_ROOT, "employees", "weekly-report"), join(assets, "employees", "weekly-report"), { recursive: true });
  cpSync(SCENARIO_A, join(assets, "samples", "scenario-a"), { recursive: true });
  cpSync(CASES, join(assets, "samples", "cases"), { recursive: true });
  mkdirSync(join(assets, "assets"), { recursive: true });
  writeFileSync(join(assets, "assets", "resvg.wasm"), readFileSync(RESVG_WASM));
  return assets;
}

async function freePort(): Promise<number> {
  const probe = Bun.serve({ port: 0, hostname: "127.0.0.1", fetch: () => new Response("") });
  const port = probe.port;
  await probe.stop(true);
  if (port === undefined) throw new Error("没能拿到空闲端口");
  return port;
}

interface Snapshot {
  id: string;
  state: string;
}

async function getJson<T>(url: string): Promise<T> {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`GET ${url} → ${res.status}`);
  return (await res.json()) as T;
}

async function waitForState(url: string, taskId: string, state: string, timeoutMs = 40_000): Promise<Snapshot> {
  const deadline = Date.now() + timeoutMs;
  let last = "";
  while (Date.now() < deadline) {
    const snapshot = await getJson<Snapshot>(`${url}/api/tasks/${taskId}`);
    last = snapshot.state;
    if (snapshot.state === state) return snapshot;
    await Bun.sleep(50);
  }
  throw new Error(`任务没有在 ${timeoutMs}ms 内进入 ${state}（最后是 ${last}）`);
}

async function command(url: string, taskId: string, body: Record<string, unknown>, role: string): Promise<Response> {
  return fetch(`${url}/api/tasks/${taskId}/commands`, {
    method: "POST",
    headers: { "content-type": "application/json", "x-role": role },
    body: JSON.stringify(body),
  });
}

/** 读取 docx 正文纯文本（docx 是 zip，只取 word/document.xml）。 */
function readDocxText(path: string): string {
  const buf = readFileSync(path);
  let eocd = buf.length - 22;
  while (eocd >= 0 && buf.readUInt32LE(eocd) !== 0x06054b50) eocd--;
  if (eocd < 0) throw new Error("不是合法的 docx");
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
      const xml = (method === 8 ? inflateRawSync(buf.subarray(dataStart, dataStart + compressed)) : buf.subarray(dataStart, dataStart + compressed)).toString("utf8");
      return xml.replace(/<\/w:p>/g, "\n").replace(/<[^>]+>/g, "");
    }
    p += 46 + nameLen + extraLen + commentLen;
  }
  throw new Error("docx 里没有 word/document.xml");
}

describe("桌面后端（共享装配 · 目录资源 · InMemoryTransport 工具）", () => {
  test(
    "A-01 经 HTTP 跑到 delivered，首页/员工接口/docx 下载正确，关窗后端口释放",
    async () => {
      const root = mkdtempSync(join(tmpdir(), "de-desktop-e2e-"));
      const assetsDir = stageAssets(root);
      const workspaceDir = join(root, "workspace");
      const port = await freePort();
      const backend = await startDesktopBackend({
        assetsDir,
        workspaceDir,
        port,
        scriptFile: join(assetsDir, "samples", "cases", "scripts", "A-01.json"),
      });
      open.push({ backend, root });

      // 地址只监听回环，窗口加载的就是它
      expect(backend.url).toBe(`http://127.0.0.1:${port}`);

      // 首次启动释放了工作区资源
      expect(existsSync(join(workspaceDir, "employees", "weekly-report", "workflow.yaml"))).toBe(true);
      expect(existsSync(join(workspaceDir, "samples", "scenario-a", "销售明细.xlsx"))).toBe(true);
      expect(existsSync(join(workspaceDir, ".runtime", "web", "index.html"))).toBe(true);
      expect(existsSync(join(workspaceDir, "settings.json"))).toBe(true);

      // 前端与员工接口
      const home = await fetch(`${backend.url}/`);
      expect(home.status).toBe(200);
      expect(await home.text()).toContain("迷你数字员工");
      const employees = await getJson<{ id: string }[]>(`${backend.url}/api/employees`);
      expect(employees.map((e) => e.id)).toContain("weekly-report");

      // 建任务（multipart 上传两份真实样例）
      const form = new FormData();
      form.append("employee", "weekly-report");
      form.append("form", JSON.stringify({ period: { year: 2026, week: 44 } }));
      form.append(
        "attachments",
        new File([readFileSync(join(assetsDir, "samples", "scenario-a", "销售明细.xlsx"))], "销售明细.xlsx"),
      );
      form.append(
        "attachments",
        new File([readFileSync(join(assetsDir, "samples", "scenario-a", "周会纪要_2026W44.docx"))], "周会纪要_2026W44.docx"),
      );
      const created = await fetch(`${backend.url}/api/tasks`, {
        method: "POST",
        headers: { "x-role": "initiator" },
        body: form,
      });
      expect(created.status).toBe(201);
      const draft = (await created.json()) as Snapshot;
      expect(draft.state).toBe("draft");

      expect((await command(backend.url, draft.id, { type: "submit" }, "initiator")).status).toBe(200);
      const review = await waitForState(backend.url, draft.id, "awaiting_review");
      expect(review.state).toBe("awaiting_review");

      expect((await command(backend.url, draft.id, { type: "approve" }, "reviewer")).status).toBe(200);
      await waitForState(backend.url, draft.id, "delivered");

      // 交付 docx：下载响应正确，内容里的数字是剧本事实
      const download = await fetch(`${backend.url}/api/tasks/${draft.id}/deliverables/docx`);
      expect(download.status).toBe(200);
      expect(download.headers.get("content-type")).toContain("wordprocessingml.document");
      const bytes = new Uint8Array(await download.arrayBuffer());
      expect(new TextDecoder().decode(bytes.subarray(0, 2))).toBe("PK");
      const downloaded = join(root, "downloaded.docx");
      writeFileSync(downloaded, bytes);
      const text = readDocxText(downloaded);
      expect(text).toContain("573,635 元");
      expect(text).toContain("-12.7%");

      // 关窗/退出：端口释放、工具客户端关闭
      await backend.close();
      await expect(fetch(`${backend.url}/`)).rejects.toThrow();
      expect(await isPortAvailable(port)).toBe(true);
    },
    60_000,
  );
});
