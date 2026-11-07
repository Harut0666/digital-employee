// 右栏：上方交付物预览，下方固定检验记录、修改说明和审核操作。纯函数。
// 版本、修改说明、下载名都来自快照，这里只决定显示哪一版、显示什么。

import type { QcCheckResult, TaskState } from "@de/core/contract";
import type { DeliverableKind, DeliverableVersion } from "../api/types";

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
export const DRAWER_BELOW = 1024;

export interface PanelLayout {
  mode: "column" | "drawer";
  /** 有交付物要读时加宽到一半左右 */
  wide: boolean;
}

export function panelLayout(windowWidth: number, reading: boolean): PanelLayout {
  return { mode: windowWidth < DRAWER_BELOW ? "drawer" : "column", wide: reading };
}
