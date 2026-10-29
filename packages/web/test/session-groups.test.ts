import { describe, expect, test } from "bun:test";
import { groupSessions } from "../src/view/session-groups";

const s = (id: string, state: string, updatedAt: string) => ({ id, employee: "weekly-report", title: id, state, updatedAt }) as never;

const tasks = [
  s("a", "awaiting_operator", "2026-10-28T14:22:00Z"),
  s("b", "awaiting_review", "2026-10-29T09:00:00Z"),
  s("c", "running", "2026-10-28T16:40:00Z"),
  s("d", "delivered", "2026-10-21T18:20:00Z"),
  s("e", "terminated", "2026-10-22T10:00:00Z"),
  s("f", "awaiting_initiator", "2026-10-27T10:00:00Z"),
];

const ids = (g: { items: { id: string }[] }) => g.items.map((x) => x.id);

describe("groupSessions", () => {
  test("操作员：待我处理只含待人工处理的会话，其余暂停算进行中", () => {
    const [mine, active, done] = groupSessions(tasks, "operator");
    expect([mine?.key, active?.key, done?.key]).toEqual(["mine", "active", "done"]);
    expect(ids(mine!)).toEqual(["a"]);
    expect(ids(active!)).toEqual(["b", "c", "f"]);
    expect(ids(done!)).toEqual(["e", "d"]);
  });

  test("审核人：待我处理是待审核", () => {
    const [mine] = groupSessions(tasks, "reviewer");
    expect(ids(mine!)).toEqual(["b"]);
  });

  test("每组内按更新时间倒序，并带状态色点", () => {
    const [mine] = groupSessions(tasks, "initiator");
    expect(ids(mine!)).toEqual(["f"]);
    const [, , done] = groupSessions(tasks, "initiator");
    expect(done!.items[0]?.dot).toBe("default");
    expect(done!.items[1]?.dot).toBe("success");
  });
});
