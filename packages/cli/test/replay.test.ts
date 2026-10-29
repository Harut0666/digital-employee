import { describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { replayLines } from "../src/replay";

const MAIN = join(import.meta.dir, "..", "src", "main.ts");

function workspaceWith(taskId: string, lines: string[]): string {
  const ws = mkdtempSync(join(tmpdir(), "de-cli-ws-"));
  mkdirSync(join(ws, "tasks", taskId), { recursive: true });
  writeFileSync(join(ws, "tasks", taskId, "events.jsonl"), lines.join("\n") + "\n");
  return ws;
}

const ev = (seq: number, rest: Record<string, unknown>) =>
  JSON.stringify({ seq, ts: `2026-10-28T10:00:0${seq}.000Z`, taskId: "T1", ...rest });

const HAND_WRITTEN = [
  ev(1, { type: "task_created", employee: "weekly-report", configHash: "abc", form: {}, attachments: [] }),
  ev(2, { type: "state_changed", from: "draft", to: "admission", trigger: "submit", actor: "initiator" }),
  ev(3, { type: "step_started", stepId: "validate", attempt: 1, reworkRound: 0 }),
  ev(4, { type: "transition_rejected", from: "awaiting_review", trigger: "approve", actor: "initiator", reason: "forbidden_role" }),
];

describe("replayLines", () => {
  test("prints events in seq order, one line each, with seq, time, type and a readable summary", () => {
    const ws = workspaceWith("T1", HAND_WRITTEN);
    const lines = replayLines(ws, "T1");
    expect(lines).toHaveLength(4);
    expect(lines.map((l) => Number(/#(\d+)/.exec(l)![1]))).toEqual([1, 2, 3, 4]);
    expect(lines[1]).toContain("2026-10-28T10:00:02.000Z");
    expect(lines[1]).toContain("state_changed");
    expect(lines[1]).toContain("draft -> admission");
    expect(lines[1]).toContain("submit");
    expect(lines[3]).toContain("forbidden_role");
  });

  test("sorts by seq even if lines are out of order in the file", () => {
    const ws = workspaceWith("T1", [HAND_WRITTEN[2]!, HAND_WRITTEN[0]!, HAND_WRITTEN[1]!]);
    expect(replayLines(ws, "T1").map((l) => Number(/#(\d+)/.exec(l)![1]))).toEqual([1, 2, 3]);
  });

  test("ignores a torn last line left by a crash while writing", () => {
    const ws = workspaceWith("T1", [HAND_WRITTEN[0]!, HAND_WRITTEN[1]!, '{"seq":3,"ts":"2026-10-2']);
    expect(replayLines(ws, "T1")).toHaveLength(2);
  });

  test("unknown event types still print as JSON rather than crashing", () => {
    const ws = workspaceWith("T1", [ev(1, { type: "future_event", x: 1 })]);
    expect(replayLines(ws, "T1")[0]).toContain('"x":1');
  });

  test("throws a clear error for an unknown task", () => {
    const ws = workspaceWith("T1", HAND_WRITTEN);
    expect(() => replayLines(ws, "nope")).toThrow(/nope/);
  });
});

describe("de replay <workspace> <taskId>", () => {
  test("the command prints events in order to stdout and exits 0", async () => {
    const ws = workspaceWith("T1", HAND_WRITTEN);
    const proc = Bun.spawn(["bun", MAIN, "replay", ws, "T1"], { stdout: "pipe", stderr: "pipe" });
    const out = await new Response(proc.stdout).text();
    expect(await proc.exited).toBe(0);
    const seqs = out
      .split(/\r?\n/)
      .filter(Boolean)
      .map((l) => Number(/#(\d+)/.exec(l)![1]));
    expect(seqs).toEqual([1, 2, 3, 4]);
  });

  test("unknown task exits non-zero with a message on stderr", async () => {
    const ws = workspaceWith("T1", HAND_WRITTEN);
    const proc = Bun.spawn(["bun", MAIN, "replay", ws, "nope"], { stdout: "pipe", stderr: "pipe" });
    const err = await new Response(proc.stderr).text();
    expect(await proc.exited).not.toBe(0);
    expect(err).toContain("nope");
  });
});
