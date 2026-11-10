// 右栏：上方交付物预览，下方固定检验记录、修改说明和审核操作。纯函数。
// 版本、修改说明、下载名都来自快照，这里只决定显示哪一版、显示什么。

import type { QcCheckResult, TaskState } from "@de/core/contract";
import type { DeliverableKind, DeliverableVersion, OpenMode } from "../api/types";

export interface ReviewQc {
  passed: boolean;
  total: number;
  failed: number;
  checks: { id: string; label: string; passed: boolean }[];
}

export interface ReviewPanelView {
  versions: { version: number; label: string }[];
  /** 正在看的版本；还没有交付物时为空 */
  selected: number | undefined;
  latest: boolean;
  docKind: "docx" | "md" | undefined;
  fileName: string | undefined;
  /** 这一版有哪些文件（交付文档、检验记录） */
  files: DeliverableKind[];
  /** 检验记录只有最新一版有 */
  qc: ReviewQc | undefined;
  /** 引出这一版的退回意见 */
  feedback: string | undefined;
  /** 这版改了什么 */
  changes: string[];
  /** 旧版本后来被怎样退回 */
  returnedWith: string | undefined;
  /** 任务待审核且正在看最新一版：审核操作在这里 */
  reviewing: boolean;
}

export interface ReviewPanelInput {
  state: TaskState | undefined;
  versions: readonly DeliverableVersion[];
  qc?: readonly QcCheckResult[];
  downloadNames?: Partial<Record<DeliverableKind, string>>;
}

const versionSuffix = (name: string, version: number) => {
  const dot = name.lastIndexOf(".");
  return dot > 0 ? `${name.slice(0, dot)}_第${version}版${name.slice(dot)}` : `${name}_第${version}版`;
};

export function deriveReviewPanel(input: ReviewPanelInput, selected?: number): ReviewPanelView {
  const versions = input.versions;
  const last = versions.at(-1);
  const current = versions.find((v) => v.version === selected) ?? last;
  const latest = current !== undefined && current === last;
  const doc = current?.files.find((f) => f.kind === "docx" || f.kind === "md");
  const docKind = doc?.kind as "docx" | "md" | undefined;
  const name = docKind ? input.downloadNames?.[docKind] : undefined;
  const next = current ? versions.find((v) => v.version === current.version + 1) : undefined;
  const checks = latest ? input.qc ?? [] : [];
  return {
    versions: versions.map((v) => ({ version: v.version, label: `第 ${v.version} 版` })),
    selected: current?.version,
    latest,
    docKind,
    fileName: name && current && !latest ? versionSuffix(name, current.version) : name,
    files: current?.files.map((f) => f.kind) ?? [],
    qc:
      checks.length > 0
        ? {
            passed: checks.every((c) => c.passed),
            total: checks.length,
            failed: checks.filter((c) => !c.passed).length,
            checks: checks.map((c) => ({ id: c.id, label: c.label, passed: c.passed })),
          }
        : undefined,
    feedback: latest ? current?.feedback?.comment : undefined,
    changes: latest ? (current?.revisionNotes ?? []).map((n) => n.change) : [],
    returnedWith: latest ? undefined : next?.feedback?.comment,
    reviewing: latest && input.state === "awaiting_review",
  };
}

/** 窗口宽度低于这个值时，右栏收成抽屉 */
/**
 * 预览用的地址：在下载地址后面带上当前版本号。
 * “最新版”的下载地址不带版本参数，重做出了第 2 版后地址和第 1 版一模一样，预览组件（按地址刷新）不会重新加载，
 * 会一直停在旧版内容上（真实模型复测里发现的）；浏览器也可能直接用缓存。带上版本号就是两个不同的地址。
 */
export function previewSource(url: string | undefined, version: number | undefined): string | undefined {
  if (!url || version === undefined) return url;
  return `${url}${url.includes("?") ? "&" : "?"}rev=${version}`;
}

export const DRAWER_BELOW = 1024;

export interface PanelLayout {
  mode: "column" | "drawer";
  /** 有交付物要读时加宽到一半左右 */
  wide: boolean;
}

export function panelLayout(windowWidth: number, reading: boolean): PanelLayout {
  return { mode: windowWidth < DRAWER_BELOW ? "drawer" : "column", wide: reading };
}

/** 交付物右上角“打开”菜单的一项 */
export interface OpenMenuItem {
  key: string;
  kind: DeliverableKind;
  mode: OpenMode;
  label: string;
  /** main：交付文档本身；other：同一版的其他文件（检验记录等） */
  group: "main" | "other";
}

const KIND_NAMES: Record<string, string> = { docx: "Word 文档", md: "Markdown 简报", qc_record: "检验记录" };

/**
 * 用本机其他应用打开交付物：Word 文档交给系统默认应用（Word）；Markdown 可以用默认应用，也可以用文本编辑器；
 * 同一版的其他文件（检验记录）也能打开；都能在文件夹里显示。Word 文档不给“文本编辑器”，打开只会是乱码。
 */
export function openMenu(files: readonly DeliverableKind[], docKind: "docx" | "md" | undefined): OpenMenuItem[] {
  const items: OpenMenuItem[] = [];
  const add = (kind: DeliverableKind, mode: OpenMode, label: string, group: OpenMenuItem["group"]) =>
    items.push({ key: `${kind}:${mode}`, kind, mode, label, group });
  if (docKind === "docx") add("docx", "default", "用 Word 打开（系统默认应用）", "main");
  if (docKind === "md") {
    add("md", "default", "用默认应用打开", "main");
    add("md", "text", "用文本编辑器打开", "main");
  }
  if (docKind) add(docKind, "reveal", "在文件夹中显示", "main");
  for (const kind of files) {
    if (kind === docKind) continue;
    const name = KIND_NAMES[kind] ?? kind;
    add(kind, "text", `${name}：用文本编辑器打开`, "other");
    add(kind, "default", `${name}：用默认应用打开`, "other");
  }
  return items;
}