// T48 权限模式：输入框左下角的模式菜单、权限确认卡、写文件的工具行。纯函数，不依赖 React 与网络。
import { describe, expect, test } from "bun:test";
import type { SessionCard, SessionDetail, SessionMessage } from "../src/api/types";
import { deriveChat } from "../src/view/chat-view";
import {
  derivePermissionCard,
  fileChangeOf,
  permissionHint,
  permissionMenuItems,
  permissionModeLabel,
  PERMISSION_MODES,
  traceOf,
} from "../src/view/permission-mode";

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
  employee: "weekly-report",
  readOnly: false,
  title: "整理要点",
  attachments: [],
  createdAt: "2026-11-02T09:00:00.000Z",
  updatedAt: "2026-11-02T09:10:00.000Z",
  waitingFor: [],
  statusDot: "idle",
  unread: false,
  archived: false,
  permissionMode: "ask",
  messages,
  ...patch,
});

const BUTTONS: NonNullable<SessionCard["choices"]> = [
  { decision: "allow_once", label: "允许一次", style: "primary" },
  { decision: "always_allow", label: "本会话总是允许这一类", style: "default" },
  { decision: "deny", label: "拒绝", style: "danger" },
];

const pendingCard = (patch: Partial<SessionCard> = {}): SessionCard => ({
  type: "permission",
  requestId: "00112233445566ff",
  action: "write_file",
  actionLabel: "写入文件",
  tool: "write_file",
  object: "notes/周会要点.md",
  path: "notes/周会要点.md",
  detail: "新建文件，写入 36 字节",
  pending: true,
  choices: BUTTONS,
  ...patch,
});

describe("权限模式菜单", () => {
  test("五个模式，从严到松，每个带一句简短说明", () => {
    expect(PERMISSION_MODES.map((m) => [m.value, m.label])).toEqual([
      ["plan", "计划"],
      ["ask", "询问"],
      ["accept_edits", "接受编辑"],
      ["auto", "自动"],
      ["yolo", "放开"],
    ]);
    for (const mode of PERMISSION_MODES) expect(mode.hint.length).toBeGreaterThan(4);
    expect(PERMISSION_MODES[0]!.hint).toContain("只读");
  });

  test("当前模式标出来；没有模式（旧数据）按询问算", () => {
    expect(permissionMenuItems("accept_edits").map((i) => [i.value, i.selected])).toEqual([
      ["plan", false],
      ["ask", false],
      ["accept_edits", true],
      ["auto", false],
      ["yolo", false],
    ]);
    expect(permissionMenuItems(undefined).find((i) => i.selected)?.value).toBe("ask");
    expect(permissionMenuItems("乱写的").find((i) => i.selected)?.value).toBe("ask");
  });

  test("下拉上显示模式名；提示条说明当前模式", () => {
    expect(permissionModeLabel("yolo")).toBe("放开");
    expect(permissionModeLabel(undefined)).toBe("询问");
    expect(permissionHint("plan")).toContain("只读");
    expect(permissionHint("ask")).toContain("写文件");
  });
});

describe("权限确认卡派生", () => {
  test("等待答复：发起人看到三个按钮，卡上写明动作和对象", () => {
    const view = derivePermissionCard(pendingCard(), { writable: true, role: "initiator" });
    expect(view).toMatchObject({
      requestId: "00112233445566ff",
      actionLabel: "写入文件",
      object: "notes/周会要点.md",
      detail: "新建文件，写入 36 字节",
      state: "pending",
      canAnswer: true,
    });
    expect(view.buttons.map((b) => [b.decision, b.label, b.style])).toEqual([
      ["allow_once", "允许一次", "primary"],
      ["always_allow", "本会话总是允许这一类", "default"],
      ["deny", "拒绝", "danger"],
    ]);
  });

  test("不是发起人、只读会话：不给按钮，说明在等发起人", () => {
    for (const opts of [
      { writable: true, role: "reviewer" as const },
      { writable: false, role: "initiator" as const },
    ]) {
      const view = derivePermissionCard(pendingCard(), opts);
      expect(view.canAnswer).toBe(false);
      expect(view.buttons).toEqual([]);
      expect(view.stateText).toContain("发起人");
    }
  });

  test("已答复：允许一次、本会话总是允许、拒绝、已取消各有说法，没有按钮", () => {
    const cases: [string, string, string][] = [
      ["allow_once", "allowed", "已允许一次"],
      ["always_allow", "always", "本会话总是允许"],
      ["deny", "denied", "已拒绝"],
      ["cancelled", "cancelled", "已取消"],
    ];
    for (const [decision, state, text] of cases) {
      const view = derivePermissionCard(pendingCard({ pending: false, decision, choices: undefined }), { writable: true, role: "initiator" });
      expect(view.state).toBe(state as typeof view.state);
      expect(view.stateText).toContain(text);
      expect(view.buttons).toEqual([]);
      expect(view.canAnswer).toBe(false);
    }
  });

  test("运行员工的确认写明员工；没有对象时不留空白", () => {
    const view = derivePermissionCard(
      pendingCard({ action: "run_employee", actionLabel: "运行员工", tool: "run_employee", object: "经营周报员工", path: undefined, detail: undefined }),
      { writable: true, role: "initiator" },
    );
    expect(view.actionLabel).toBe("运行员工");
    expect(view.object).toBe("经营周报员工");
    expect(view.detail).toBeUndefined();
  });
});

describe("写文件的工具行", () => {
  test("write_file：新建了哪个文件、多少字节；点开能看写进去的内容", () => {
    const view = fileChangeOf(
      msg({
        kind: "tool_called",
        tool: "write_file",
        args: { path: "notes/周会要点.md", content: "# 周会要点\n- 华南环比下降\n" },
        result: { path: "notes/周会要点.md", action: "created", bytes: 36 },
      }),
    );
    expect(view).toMatchObject({ ok: true, path: "notes/周会要点.md", action: "created", bytes: 36 });
    expect(view?.summary).toBe("新建 notes/周会要点.md · 36 字节");
    expect(view?.body).toBe("# 周会要点\n- 华南环比下降\n");
    expect(view?.bodyKind).toBe("content");
  });

  test("edit_file：修改了哪个文件，点开看差异摘要", () => {
    const view = fileChangeOf(
      msg({
        kind: "tool_called",
        tool: "edit_file",
        args: { path: "notes.md", oldText: "持平", newText: "上升" },
        result: { path: "notes.md", action: "modified", bytes: 40, diff: "@@ 第 3 行 @@\n-- 华东持平\n+- 华东上升" },
      }),
    );
    expect(view?.summary).toBe("修改 notes.md · 40 字节");
    expect(view?.body).toContain("+- 华东上升");
    expect(view?.bodyKind).toBe("diff");
  });

  test("失败：写明原因，没有可看的改动", () => {
    const view = fileChangeOf(
      msg({
        kind: "tool_called",
        tool: "edit_file",
        args: { path: "notes.md", oldText: "没有的话", newText: "x" },
        reason: "在 notes.md 里没有找到要替换的原文。",
        result: { error: "not_found", message: "在 notes.md 里没有找到要替换的原文。" },
      }),
    );
    expect(view).toMatchObject({ ok: false, path: "notes.md" });
    expect(view?.summary).toBe("没能改 notes.md：在 notes.md 里没有找到要替换的原文。");
    expect(view?.body).toBeUndefined();
  });

  test("被停止的写文件和别的工具不算写文件的工具行", () => {
    expect(fileChangeOf(msg({ kind: "tool_called", tool: "write_file", args: { path: "a.md" }, result: "已取消：这一轮被停止，工具没有执行。" }))).toBeUndefined();
    expect(fileChangeOf(msg({ kind: "tool_called", tool: "read_file", args: { path: "a.md" }, result: { text: "x" } }))).toBeUndefined();
    expect(fileChangeOf(msg({ kind: "user", text: "写文件" }))).toBeUndefined();
  });
});

describe("权限留痕", () => {
  test("自动放行和被模式拒绝都能在对话里看到；用户拒绝已经体现在确认卡上，不重复", () => {
    expect(traceOf(msg({ kind: "permission_allowed", tool: "write_file", args: { path: "a.md" }, reason: "接受编辑模式：写入文件不用确认" }))).toEqual({
      tone: "allowed",
      text: "已放行 write_file a.md：接受编辑模式：写入文件不用确认",
    });
    expect(traceOf(msg({ kind: "tool_denied", tool: "write_file", args: { path: "交付/x.md" }, reason: "“交付”文件夹由程序写入交付物，Agent 不能写：交付/x.md" }))).toEqual({
      tone: "denied",
      text: "已拒绝 write_file 交付/x.md：“交付”文件夹由程序写入交付物，Agent 不能写：交付/x.md",
    });
    expect(traceOf(msg({ kind: "tool_denied", tool: "write_file", args: { path: "a.md" }, reason: "用户拒绝：发起人没有允许写入文件：a.md。" }))).toBeUndefined();
    // 越权调用别的工具（approve 之类）的拒绝只进日志，不进对话
    expect(traceOf(msg({ kind: "tool_denied", tool: "approve", reason: "没有开放" }))).toBeUndefined();
  });
});

describe("deriveChat 里的权限内容", () => {
  test("权限确认卡、写文件的工具行、放行留痕按时间排在对话里", () => {
    const items = deriveChat(
      session([
        msg({ kind: "user", role: "initiator", text: "把要点写成文件" }),
        msg({ kind: "card", card: pendingCard({ pending: false, decision: "allow_once", choices: undefined }) }),
        msg({ kind: "tool_called", tool: "write_file", args: { path: "a.md", content: "甲" }, result: { path: "a.md", action: "created", bytes: 3 } }),
        msg({ kind: "permission_allowed", tool: "write_file", args: { path: "b.md" }, reason: "本会话已选择总是允许「写入文件」" }),
        msg({ kind: "tool_called", tool: "propose_card", args: {}, result: {} }),
        msg({ kind: "assistant", text: "写好了" }),
      ]),
    );
    expect(items.map((i) => i.kind)).toEqual(["user", "permission", "file_change", "trace", "agent"]);
    const card = items[1];
    expect(card?.kind === "permission" && card.state).toBe("allowed");
  });

  test("等答复的卡：发起人能点；换成审核人就只能看", () => {
    const messages = [msg({ kind: "card", card: pendingCard() })];
    const mine = deriveChat(session(messages), undefined, [], undefined, "initiator")[0];
    expect(mine?.kind === "permission" && mine.canAnswer).toBe(true);
    const theirs = deriveChat(session(messages), undefined, [], undefined, "reviewer")[0];
    expect(theirs?.kind === "permission" && theirs.canAnswer).toBe(false);
    const readOnly = deriveChat(session(messages, { readOnly: true }), undefined, [], undefined, "initiator")[0];
    expect(readOnly?.kind === "permission" && readOnly.canAnswer).toBe(false);
  });

  test("选择权限模式：留一行说明", () => {
    const items = deriveChat(session([msg({ kind: "permission_mode_set", mode: "accept_edits" })]));
    expect(items.map((i) => [i.kind, "text" in i ? i.text : ""])).toEqual([["notice", "权限模式切换为「接受编辑」"]]);
  });
});
