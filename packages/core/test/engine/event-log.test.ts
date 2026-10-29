import { afterEach, describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { readEventLog } from "../../src/engine/event-log";

const dirs: string[] = [];
afterEach(() => {
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

function logWith(content: string): string {
  const dir = mkdtempSync(join(tmpdir(), "de-log-"));
  dirs.push(dir);
  const path = join(dir, "events.jsonl");
  writeFileSync(path, content);
  return path;
}

const line = (seq: number) => JSON.stringify({ seq, type: "step_started", stepId: `s${seq}` }) + "\n";

describe("readEventLog", () => {
  test("只丢弃最后一行的半行（torn write），并把文件整理干净", () => {
    const path = logWith(line(1) + line(2) + '{"seq":3,"type":"step_sta');
    expect(readEventLog(path).map((e) => e.seq)).toEqual([1, 2]);
    expect(readFileSync(path, "utf8")).toBe(line(1) + line(2));
  });

  test("中间的坏行是日志损坏：抛出明确的错误，文件原样不动", () => {
    const content = line(1) + "{broken\n" + line(3);
    const path = logWith(content);
    expect(() => readEventLog(path)).toThrow(/损坏.*第 2 行/);
    expect(readFileSync(path, "utf8")).toBe(content);
  });

  test("中间坏行 + 末尾半行同样视为损坏，不改文件", () => {
    const content = line(1) + "{broken\n" + line(3) + '{"seq":4,"ty';
    const path = logWith(content);
    expect(() => readEventLog(path)).toThrow(/损坏/);
    expect(readFileSync(path, "utf8")).toBe(content);
  });

  test("末尾缺换行但内容完整 → 补齐换行，事件保留", () => {
    const path = logWith(line(1) + line(2).trimEnd());
    expect(readEventLog(path).map((e) => e.seq)).toEqual([1, 2]);
    expect(readFileSync(path, "utf8")).toBe(line(1) + line(2));
  });
});
