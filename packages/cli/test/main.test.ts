import { describe, expect, test } from "bun:test";
import { join } from "node:path";

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
});
