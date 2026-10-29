// 配置版本：每次保存前把当前配置复制到 <workspace>/config-versions/<员工>/<时间戳>-<哈希短码>/。
// 版本目录里放配置文件副本与 .version.json（时间、整份配置的哈希、每个文件的哈希）。
import { afterEach, describe, expect, test } from "bun:test";
import { cpSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  createConfigVersion,
  diffConfigVersion,
  listConfigVersions,
  readConfigVersion,
  restoreConfigVersion,
  versionsDirFor,
} from "../../src/config/versions";

const repoRoot = join(import.meta.dir, "..", "..", "..", "..");
const employeeSource = join(repoRoot, "employees", "weekly-report");
const tmpDirs: string[] = [];

function setup(): { workspace: string; employeeDir: string } {
  const root = mkdtempSync(join(tmpdir(), "de-versions-"));
  tmpDirs.push(root);
  const employeeDir = join(root, "employees", "weekly-report");
  cpSync(employeeSource, employeeDir, { recursive: true });
  return { workspace: join(root, "workspace"), employeeDir };
}

function fakeClock(startMs = Date.parse("2026-10-29T07:15:00.000Z")): () => Date {
  let t = startMs;
  return () => {
    const d = new Date(t);
    t += 1000;
    return d;
  };
}

afterEach(() => {
  for (const d of tmpDirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

describe("配置版本快照", () => {
  test("快照复制当前配置到 config-versions/<员工>/<时间戳>-<哈希>，列表按时间倒序", () => {
    const { workspace, employeeDir } = setup();
    const now = fakeClock();
    const first = createConfigVersion({ workspaceDir: workspace, employeeId: "weekly-report", employeeDir, now });
    expect(first.id).toMatch(/^\d{8}T\d{9}Z-[0-9a-f]{8}$/);
    expect(first.hash).toMatch(/^[0-9a-f]{64}$/);
    const dir = join(versionsDirFor(workspace, "weekly-report"), first.id);
    expect(readFileSync(join(dir, "workflow.yaml"), "utf8")).toBe(readFileSync(join(employeeDir, "workflow.yaml"), "utf8"));
    expect(readFileSync(join(dir, "prompts", "write_report.md"), "utf8")).toBe(readFileSync(join(employeeDir, "prompts", "write_report.md"), "utf8"));

    const second = createConfigVersion({ workspaceDir: workspace, employeeId: "weekly-report", employeeDir, now });
    const list = listConfigVersions(workspace, "weekly-report");
    expect(list.map((v) => v.id)).toEqual([second.id, first.id]);
    expect(list[0]!.createdAt).toBe(second.createdAt);
  });

  test("同一秒内容相同的两次快照 id 不重复", () => {
    const { workspace, employeeDir } = setup();
    const fixed = () => new Date("2026-10-29T07:15:00.000Z");
    const a = createConfigVersion({ workspaceDir: workspace, employeeId: "weekly-report", employeeDir, now: fixed });
    const b = createConfigVersion({ workspaceDir: workspace, employeeId: "weekly-report", employeeDir, now: fixed });
    expect(a.id).not.toBe(b.id);
    expect(listConfigVersions(workspace, "weekly-report")).toHaveLength(2);
  });

  test("版本可以读回为文件映射", () => {
    const { workspace, employeeDir } = setup();
    const version = createConfigVersion({ workspaceDir: workspace, employeeId: "weekly-report", employeeDir, now: fakeClock() });
    const files = readConfigVersion(workspace, "weekly-report", version.id);
    expect(files).toBeDefined();
    expect(Object.keys(files!).sort()).toEqual(
      expect.arrayContaining(["SKILL.md", "frontdesk.md", "form.schema.json", "prompts/write_report.md", "qc.yaml", "workflow.yaml"]),
    );
    expect(readConfigVersion(workspace, "weekly-report", "19990101T000000000Z-deadbeef")).toBeUndefined();
    expect(readConfigVersion(workspace, "weekly-report", "../weekly-report")).toBeUndefined();
  });
});

describe("版本差异", () => {
  test("与当前版本对比：改过的文件给出差异，没改的不出现", () => {
    const { workspace, employeeDir } = setup();
    const version = createConfigVersion({ workspaceDir: workspace, employeeId: "weekly-report", employeeDir, now: fakeClock() });
    const workflow = readFileSync(join(employeeDir, "workflow.yaml"), "utf8");
    writeFileSync(join(employeeDir, "workflow.yaml"), workflow.replace("modelCalls: 30", "modelCalls: 45"));

    const diff = diffConfigVersion({ workspaceDir: workspace, employeeId: "weekly-report", employeeDir, versionId: version.id });
    expect(diff).toBeDefined();
    expect(diff!.hasChanges).toBe(true);
    expect(diff!.files.map((f) => f.path)).toEqual(["workflow.yaml"]);
    expect(diff!.files[0]!.status).toBe("modified");
    const text = diff!.files[0]!.hunks.join("\n");
    expect(text).toMatch(/^-.*modelCalls: 30$/m);
    expect(text).toMatch(/^\+.*modelCalls: 45$/m);
  });

  test("新增文件与删除文件都算差异", () => {
    const { workspace, employeeDir } = setup();
    const version = createConfigVersion({ workspaceDir: workspace, employeeId: "weekly-report", employeeDir, now: fakeClock() });
    writeFileSync(join(employeeDir, "schemas", "extra.json"), JSON.stringify({ type: "object" }));
    rmSync(join(employeeDir, "schemas", "match_causes.json"));

    const diff = diffConfigVersion({ workspaceDir: workspace, employeeId: "weekly-report", employeeDir, versionId: version.id });
    const byPath = Object.fromEntries(diff!.files.map((f) => [f.path, f.status]));
    expect(byPath["schemas/extra.json"]).toBe("added");
    expect(byPath["schemas/match_causes.json"]).toBe("removed");
  });

  test("没有改动时 hasChanges 为 false", () => {
    const { workspace, employeeDir } = setup();
    const version = createConfigVersion({ workspaceDir: workspace, employeeId: "weekly-report", employeeDir, now: fakeClock() });
    const diff = diffConfigVersion({ workspaceDir: workspace, employeeId: "weekly-report", employeeDir, versionId: version.id });
    expect(diff!.hasChanges).toBe(false);
    expect(diff!.files).toEqual([]);
  });
});

describe("回滚", () => {
  test("回滚把版本内容复制回来，并先给当前内容留一个快照（版本数 +1）", () => {
    const { workspace, employeeDir } = setup();
    const now = fakeClock();
    const version = createConfigVersion({ workspaceDir: workspace, employeeId: "weekly-report", employeeDir, now });
    const originalWorkflow = readFileSync(join(employeeDir, "workflow.yaml"), "utf8");

    // 改坏当前配置
    writeFileSync(join(employeeDir, "workflow.yaml"), `${originalWorkflow}\n# 改坏了\n`);
    writeFileSync(join(employeeDir, "schemas", "extra.json"), JSON.stringify({ type: "object" }));
    const before = listConfigVersions(workspace, "weekly-report").length;

    const result = restoreConfigVersion({ workspaceDir: workspace, employeeId: "weekly-report", employeeDir, versionId: version.id, now });
    expect(result).toBeDefined();
    expect(readFileSync(join(employeeDir, "workflow.yaml"), "utf8")).toBe(originalWorkflow);

    const after = listConfigVersions(workspace, "weekly-report");
    expect(after.length).toBe(before + 1);
    // 回滚前的“改坏了”的内容留在快照里，可以再回滚回去
    const snapshot = after.find((v) => v.id === result!.snapshot.id)!;
    const files = readConfigVersion(workspace, "weekly-report", snapshot.id)!;
    expect(files["workflow.yaml"]).toContain("# 改坏了");
    // 回滚前多出来的 schemas/extra.json 在回滚后不存在（对齐到版本内容）
    expect(readConfigVersion(workspace, "weekly-report", version.id)!["schemas/extra.json"]).toBeUndefined();
    expect(() => readFileSync(join(employeeDir, "schemas", "extra.json"), "utf8")).toThrow();
  });

  test("不存在的版本返回 undefined，不改动任何文件", () => {
    const { workspace, employeeDir } = setup();
    const result = restoreConfigVersion({ workspaceDir: workspace, employeeId: "weekly-report", employeeDir, versionId: "nope", now: fakeClock() });
    expect(result).toBeUndefined();
    expect(listConfigVersions(workspace, "weekly-report")).toEqual([]);
  });
});
