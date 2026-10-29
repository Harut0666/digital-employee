// 欢迎页“载入公开样例”（T13）：把首次启动释放到工作区里的 samples 一键变成草稿任务。
//
// 样例定义直接复用公开用例 samples/cases/A-01.json（CLI 跑用例用的同一份），
// 不再另写一套样例描述，保证界面上载入的正是自测用例跑的那份数据。

import { existsSync, readFileSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { Elysia } from "elysia";
import type { AttachmentInput } from "@de/core/contract";
import { apiError, ROLE_REQUIRED, roleOf } from "../http";
import type { RouteDeps } from "./deps";

export const PUBLIC_SAMPLE_ID = "scenario-a";
/** 公开样例对应的用例文件（相对 <workspace>/samples） */
const SAMPLE_CASE = join("cases", "A-01.json");

export interface PublicSample {
  id: string;
  employee: string;
  title: string;
  form: Record<string, unknown>;
  /** 样例附件的文件名与字节数（附件本体由服务端直接读工作区，不走浏览器上传） */
  files: { name: string; size: number }[];
}

interface SampleWithAttachments extends PublicSample {
  attachments: AttachmentInput[];
}

/** 读工作区里的公开样例；样例没释放出来或文件损坏时返回 undefined（界面上不显示入口）。 */
export function readPublicSample(workspaceDir: string): SampleWithAttachments | undefined {
  const caseFile = join(workspaceDir, "samples", SAMPLE_CASE);
  if (!existsSync(caseFile)) return undefined;
  let parsed: unknown;
  try {
    parsed = JSON.parse(readFileSync(caseFile, "utf8"));
  } catch {
    return undefined;
  }
  const raw = parsed as Record<string, unknown>;
  if (typeof raw.employee !== "string" || raw.employee === "") return undefined;
  if (typeof raw.form !== "object" || raw.form === null || Array.isArray(raw.form)) return undefined;
  if (!Array.isArray(raw.attachments)) return undefined;

  const baseDir = dirname(caseFile);
  const attachments: AttachmentInput[] = [];
  const files: { name: string; size: number }[] = [];
  for (const item of raw.attachments) {
    const a = item as Record<string, unknown>;
    if (typeof a?.name !== "string" || typeof a?.path !== "string") continue;
    const sourcePath = resolve(baseDir, a.path);
    if (!existsSync(sourcePath)) continue;
    attachments.push({ name: a.name, sourcePath });
    files.push({ name: a.name, size: statSync(sourcePath).size });
  }
  if (attachments.length === 0) return undefined;

  return {
    id: PUBLIC_SAMPLE_ID,
    employee: raw.employee,
    title: typeof raw.title === "string" ? raw.title : "公开样例",
    form: raw.form as Record<string, unknown>,
    files,
    attachments,
  };
}

export function createSampleRoutes(deps: RouteDeps) {
  const sample = () => readPublicSample(deps.workspaceDir);

  return new Elysia()
    .get("/api/samples", () => {
      const found = sample();
      // 列表不带 sourcePath：浏览器只需要知道有哪些样例文件
      return { samples: found ? [{ id: found.id, employee: found.employee, title: found.title, form: found.form, files: found.files }] : [] };
    })
    .post("/api/samples/:id/tasks", async ({ params, headers, set }) => {
      const role = roleOf(headers);
      if (!role) {
        set.status = 403;
        return ROLE_REQUIRED;
      }
      if (role !== "initiator") {
        set.status = 403;
        return apiError("forbidden_role", "只有发起人可以载入公开样例任务");
      }
      const found = params.id === PUBLIC_SAMPLE_ID ? sample() : undefined;
      if (!found) {
        set.status = 404;
        return apiError("not_found", `工作区里没有公开样例：${params.id}（首次启动会自动释放）`);
      }
      const snapshot = await deps.engine.create({
        employee: found.employee,
        form: found.form,
        attachments: found.attachments,
        actor: role,
      });
      set.status = 201;
      return snapshot;
    });
}
