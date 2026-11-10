import { describe, expect, test } from "bun:test";
import type { DeliverableVersion } from "../src/api/types";
import { deriveReviewPanel, openMenu, panelLayout, previewSource } from "../src/view/review-panel";

// 真实模型复测里发现：退回重做出了第 2 版，预览却一直停在第 1 版。
// 原因是“最新版”的下载地址不带版本，两版地址相同，预览组件按地址刷新就不会重新加载。
describe("previewSource", () => {
  test("最新版的两个版本得到不同的预览地址", () => {
    const base = "/api/tasks/t1/deliverables/md";
    expect(previewSource(base, 1)).not.toBe(previewSource(base, 2));
    expect(previewSource(base, 2)).toBe("/api/tasks/t1/deliverables/md?rev=2");
  });
  test("下载地址已有参数时用 & 拼接", () => {
    expect(previewSource("/api/tasks/t1/deliverables/docx?version=1", 1)).toBe("/api/tasks/t1/deliverables/docx?version=1&rev=1");
  });
  test("没有地址（模拟模式）或没有版本时原样返回", () => {
    expect(previewSource(undefined, 2)).toBeUndefined();
    expect(previewSource("/x", undefined)).toBe("/x");
  });
});

const files = (round: number) => [
  { kind: "docx" as const, path: `report-r${round}.docx`, sha256: `d${round}` },
  { kind: "qc_record" as const, path: `qc_record-r${round}.json`, sha256: `q${round}` },
];

const V1: DeliverableVersion = { version: 1, reworkRound: 0, createdAt: "2026-11-02T09:00:00Z", files: files(0) };
const V2: DeliverableVersion = {
  version: 2,
  reworkRound: 1,
  createdAt: "2026-11-02T09:10:00Z",
  files: files(1),
  feedback: { comment: "摘要请再精简，突出两个异常", sections: ["summary"] },
  revisionNotes: [
    { comment: "摘要请再精简", sections: ["summary"], change: "已精简本周摘要" },
    { comment: "突出两个异常", sections: ["summary"], change: "突出华东、华南两个异常" },
  ],
};

const CHECKS = [
  { id: "structure", label: "结构完整", passed: true, issues: [] },
  { id: "traceability", label: "数据溯源", passed: true, issues: [] },
];

const snapshot = (patch: Partial<Parameters<typeof deriveReviewPanel>[0]> = {}) => ({
  state: "awaiting_review" as const,
  versions: [V1, V2],
  qc: CHECKS,
  downloadNames: { docx: "经营周报_2026年第44周.docx" },
  ...patch,
});

describe("deriveReviewPanel：右栏交付物和审核区", () => {
  test("默认看最新一版：文件名、检验记录、修改说明，正在审核", () => {
    const view = deriveReviewPanel(snapshot());
    expect(view.versions.map((v) => v.label)).toEqual(["第 1 版", "第 2 版"]);
    expect([view.selected, view.latest, view.reviewing]).toEqual([2, true, true]);
    expect(view.docKind).toBe("docx");
    expect(view.fileName).toBe("经营周报_2026年第44周.docx");
    expect(view.qc && [view.qc.total, view.qc.failed, view.qc.passed]).toEqual([2, 0, true]);
    expect(view.feedback).toBe("摘要请再精简，突出两个异常");
    expect(view.changes).toEqual(["已精简本周摘要", "突出华东、华南两个异常"]);
  });

  test("切到旧版本：不能审核，说明它后来被怎样退回，文件名标出版本，不显示最新的检验记录", () => {
    const view = deriveReviewPanel(snapshot(), 1);
    expect([view.selected, view.latest, view.reviewing]).toEqual([1, false, false]);
    expect(view.returnedWith).toBe("摘要请再精简，突出两个异常");
    expect(view.fileName).toBe("经营周报_2026年第44周_第1版.docx");
    expect(view.qc).toBeUndefined();
    expect(view.changes).toEqual([]);
  });

  test("首版没有修改说明；不认识的版本号回到最新一版", () => {
    const view = deriveReviewPanel(snapshot({ versions: [V1] }), 7);
    expect([view.selected, view.latest, view.feedback, view.changes]).toEqual([1, true, undefined, []]);
  });

  test("还没有交付物", () => {
    const view = deriveReviewPanel(snapshot({ state: "running", versions: [], qc: undefined }));
    expect([view.selected, view.docKind, view.reviewing]).toEqual([undefined, undefined, false]);
  });

  test("已交付后仍可阅读，但不在审核", () => {
    const view = deriveReviewPanel(snapshot({ state: "delivered" }));
    expect([view.latest, view.reviewing]).toEqual([true, false]);
  });

  test("质检有未通过项时如实给出", () => {
    const view = deriveReviewPanel(snapshot({ qc: [...CHECKS, { id: "terms", label: "禁用词", passed: false, issues: ["出现同比"] }] }));
    expect(view.qc && [view.qc.total, view.qc.failed, view.qc.passed]).toEqual([3, 1, false]);
  });
});

describe("panelLayout：右栏宽度与抽屉", () => {
  test("有交付物要读时右栏加宽到一半左右", () => {
    expect(panelLayout(1280, true)).toEqual({ mode: "column", wide: true });
    expect(panelLayout(1280, false)).toEqual({ mode: "column", wide: false });
  });

  test("窗口较窄时右栏收成抽屉", () => {
    expect(panelLayout(1000, true).mode).toBe("drawer");
    expect(panelLayout(1024, false).mode).toBe("column");
  });
});

describe("openMenu：交付物右上角的“打开”", () => {
  const summary = (kind: "docx" | "md", files: ("docx" | "md" | "qc_record")[]) => openMenu(files, kind).map((i) => `${i.group}:${i.kind}:${i.mode}`);

  test("Word 文档：交给系统默认应用（Word）、在文件夹中显示，不给文本编辑器；检验记录可以另外打开", () => {
    expect(summary("docx", ["docx", "qc_record"])).toEqual([
      "main:docx:default",
      "main:docx:reveal",
      "other:qc_record:text",
      "other:qc_record:default",
    ]);
  });

  test("Markdown：默认应用和文本编辑器都行", () => {
    expect(summary("md", ["md", "qc_record"])).toEqual([
      "main:md:default",
      "main:md:text",
      "main:md:reveal",
      "other:qc_record:text",
      "other:qc_record:default",
    ]);
  });

  test("没有交付文档时什么都不给", () => {
    expect(openMenu([], undefined)).toEqual([]);
  });

  test("版本的文件清单进视图，菜单按它列出其他文件", () => {
    const version = { version: 1, files: [{ kind: "docx" }, { kind: "qc_record" }] } as unknown as DeliverableVersion;
    const view = deriveReviewPanel({ state: "delivered", versions: [version] });
    expect(view.files).toEqual(["docx", "qc_record"]);
    expect(openMenu(view.files, view.docKind).length).toBe(4);
  });
});