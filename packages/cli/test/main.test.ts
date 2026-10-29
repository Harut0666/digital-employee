import { describe, expect, test } from "bun:test";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { makeRunDir } from "./checks-fixture";

const MAIN = join(import.meta.dir, "..", "src", "main.ts");

async function de(...args: string[]) {
  const proc = Bun.spawn(["bun", MAIN, ...args], { stdout: "pipe", stderr: "pipe" });
  const [out, err] = await Promise.all([new Response(proc.stdout).text(), new Response(proc.stderr).text()]);
  return { code: await proc.exited, out, err };
}

describe("de argument handling", () => {
  test("no arguments prints usage", async () => {
    const r = await de();
    expect(r.code).toBe(0);
    expect(r.err).toContain("run-case");
  });
  test("unknown command exits 2", async () => {
    const r = await de("dance");
    expect(r.code).toBe(2);
    expect(r.err).toContain("dance");
  });
  test("run-case without a file exits 2; with a missing file exits 1 and names the file", async () => {
    expect((await de("run-case")).code).toBe(2);
    const r = await de("run-case", "no-such-case.json");
    expect(r.code).toBe(1);
    expect(r.err).toContain("no-such-case.json");
  });
  test("usage mentions the new subcommands", async () => {
    const r = await de();
    for (const command of ["run-cases", "check", "fixtures"]) expect(r.err).toContain(command);
  });
});

describe("de check", () => {
  test("核对一次运行的结果目录：全部通过为 0，有不通过项为 1 并指出哪一项", async () => {
    const dir = mkdtempSync(join(tmpdir(), "de-check-"));
    await makeRunDir(dir);
    const good = join(dir, "good.json");
    writeFileSync(good, JSON.stringify({ checks: { numbers: [{ path: "table:全公司/实收（元）", expected: 100000 }], mustContain: ["以数据为准"] } }));
    const ok = await de("check", dir, good);
    expect(ok.code).toBe(0);
    expect(ok.out).toContain("2 项通过");

    const bad = join(dir, "bad.json");
    writeFileSync(bad, JSON.stringify({ checks: { numbers: [{ path: "table:全公司/实收（元）", expected: 123 }] } }));
    const fail = await de("check", dir, bad);
    expect(fail.code).toBe(1);
    expect(fail.out).toContain("table:全公司/实收（元）");
    expect(fail.out).toContain("1 项未通过");
  });

  test("参数或文件不对时给出清楚的原因", async () => {
    expect((await de("check")).code).toBe(2);
    const empty = mkdtempSync(join(tmpdir(), "de-check-none-"));
    const checksFile = join(empty, "checks.json");
    writeFileSync(checksFile, JSON.stringify({ checks: { mustContain: ["本周摘要"] } }));
    const noResult = await de("check", empty, checksFile);
    expect(noResult.code).toBe(1);
    expect(noResult.err).toContain("result.json");

    const missing = await de("check", empty, join(empty, "no-such-expect.json"));
    expect(missing.code).toBe(1);
    expect(missing.err).toContain("no-such-expect.json");

    const noChecks = join(empty, "no-checks.json");
    writeFileSync(noChecks, JSON.stringify({ id: "X" }));
    const r = await de("check", empty, noChecks);
    expect(r.code).toBe(1);
    expect(r.err).toContain("checks");
  });
});
