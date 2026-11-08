import { describe, expect, test } from "bun:test";
import type { SessionCard, SessionDetail, SessionMessage, TaskAction } from "../src/api/types";
import { deriveChat } from "../src/view/chat-view";
import { atQuery, filterFolderPaths, insertAtPath } from "../src/view/folder-files";

let clock = Date.parse("2026-11-02T09:00:00.000Z");
const msg = (input: Omit<SessionMessage, "id" | "seq" | "ts">): SessionMessage => {
  clock += 1000;
  const seq = clock / 1000;
  return { id: `s${seq}`, seq, ts: new Date(clock).toISOString(), ...input };
};

const session = (messages: SessionMessage[], patch: Partial<SessionDetail> = {}): SessionDetail => ({
  id: "s-1",
  workspaceId: "ws-1",
  status: "drafting",
  readOnly: false,
  title: "帮我出第 44 周的周报",
  employee: "weekly-report",
  attachments: [],
  createdAt: "2026-11-02T09:00:00.000Z",
  updatedAt: "2026-11-02T09:10:00.000Z",
  waitingFor: [],
  messages,
  ...patch,
});

describe("@ 选目录文件", () => {
  test("@ 开头的词才触发；取光标所在的那一个", () => {
    expect(atQuery("@", 1)).toBe("");
    expect(atQuery("用 @销售", 6)).toBe("销售");
    expect(atQuery("用 @资料/销售明细.xlsx 汇总", 15)).toBe("资料/销售明细.xlsx");
    expect(atQuery("价格 5@100", 7)).toBeUndefined();
    expect(atQuery("用 @a @b", 7)).toBe("b");
    expect(atQuery("@a b", 4)).toBeUndefined();
  });

  test("按子串过滤目录文件，大小写不敏感", () => {
    const files = ["销售明细.xlsx", "资料/周会纪要_2026W44.docx", "资料/Sales.csv"];
    expect(filterFolderPaths(files, "")).toEqual(files);
    expect(filterFolderPaths(files, "销售")).toEqual(["销售明细.xlsx"]);
    expect(filterFolderPaths(files, "sales")).toEqual(["资料/Sales.csv"]);
  });

  test("选中后把那一个 @词 换成 @路径 ", () => {
    // 光标在“销”后面：@词 从 @ 一直到光标，后面没删的字原样留在后面
    expect(insertAtPath("用 @销售 出周报", 4, "资料/销售明细.xlsx")).toEqual({ text: "用 @资料/销售明细.xlsx 售 出周报", caret: "用 @资料/销售明细.xlsx ".length });
    // 词打完了再把光标放到词尾：整段替换
    expect(insertAtPath("用 @销售 出周报", 6, "资料/销售明细.xlsx")).toEqual({ text: "用 @资料/销售明细.xlsx 出周报", caret: "用 @资料/销售明细.xlsx ".length });
    expect(insertAtPath("@a", 2, "a.txt")).toEqual({ text: "@a.txt", caret: 6 });
    // 后面还有字时只补一格空格；末尾不留空格，免得一按发送就把什么也没选中的 @ 发出去
    expect(insertAtPath("@销 出周报", 2, "销售.xlsx")).toEqual({ text: "@销售.xlsx 出周报", caret: 8 });
    expect(insertAtPath("@销", 2, "销售.xlsx")).toEqual({ text: "@销售.xlsx", caret: 8 });
  });
});

describe("使用目录文件卡片", () => {
  // 目录确认不是任务命令：后端给的按钮 command 是 use_folder_file（前端只读 roles）
  const offered: TaskAction[] = [{ command: "use_folder_file", label: "用这个文件", roles: ["initiator"], style: "primary" } as unknown as TaskAction];
  const offer = (path: string): SessionMessage => msg({ kind: "card", card: { type: "folder_file", path, name: path.split("/").at(-1)!, size: 10, actions: offered } as SessionCard });

  test("员工接手、还没提交时可以确认；提过之后或只读会话里失效", () => {
    const drafting = deriveChat(session([offer("资料/销售明细.xlsx")]));
    expect(drafting).toHaveLength(1);
    expect(drafting[0]).toMatchObject({ kind: "use_file", path: "资料/销售明细.xlsx", name: "销售明细.xlsx", size: 10, active: true });

    expect(deriveChat(session([offer("a.xlsx")], { taskId: "T-1", status: "task" }))[0]).toMatchObject({ active: false });
    expect(deriveChat(session([offer("a.xlsx")], { readOnly: true, status: "drafting" }))[0]).toMatchObject({ active: false });
    // 后端没给按钮时（已确认、文件已不在）也不能点
    const noActions = msg({ kind: "card", card: { type: "folder_file", path: "a.xlsx", name: "a.xlsx" } });
    expect(deriveChat(session([noActions]))[0]).toMatchObject({ kind: "use_file", active: false });
    const confirmed = msg({ kind: "card", card: { type: "folder_file", path: "a.xlsx", name: "a.xlsx", confirmed: true } });
    expect(deriveChat(session([confirmed]))[0]).toMatchObject({ active: false, confirmed: true });
  });
});
