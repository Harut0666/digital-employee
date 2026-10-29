// 请求解析与错误映射：角色头、上传附件落盘、引擎错误码到 HTTP 状态码。
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import type { AttachmentInput, CommandResult, TaskCommand } from "@de/core/contract";

export const REQUEST_ROLES = ["initiator", "reviewer", "operator"] as const;
export type RequestRole = (typeof REQUEST_ROLES)[number];

export interface ApiError {
  ok: false;
  error: "illegal_transition" | "forbidden_role" | "invalid_input" | "not_found";
  message: string;
}

export function apiError(error: ApiError["error"], message: string): ApiError {
  return { ok: false, error, message };
}

const ERROR_STATUS: Record<ApiError["error"], number> = {
  invalid_input: 400,
  forbidden_role: 403,
  not_found: 404,
  illegal_transition: 409,
};

export function statusOf(error: ApiError["error"]): number {
  return ERROR_STATUS[error];
}

export function statusOfResult(result: CommandResult): number {
  return result.ok ? 200 : statusOf(result.error);
}

/** 角色由 x-role 头给出；缺失或取值未知都视为越权，校验本身由引擎完成。 */
export function roleOf(headers: Record<string, string | undefined>): RequestRole | undefined {
  const raw = headers["x-role"];
  return (REQUEST_ROLES as readonly string[]).includes(raw ?? "") ? (raw as RequestRole) : undefined;
}

export const ROLE_REQUIRED: ApiError = apiError("forbidden_role", "缺少或无法识别的角色（请求头 x-role 应为 initiator / reviewer / operator）");

function isFile(v: unknown): v is File {
  return typeof v === "object" && v !== null && typeof (v as File).arrayBuffer === "function" && typeof (v as File).name === "string";
}

export function filesOf(value: unknown): File[] {
  if (Array.isArray(value)) return value.filter(isFile);
  return isFile(value) ? [value] : [];
}

export interface SavedUploads {
  attachments: AttachmentInput[];
  /** 引擎复制完附件后调用，清理临时目录 */
  cleanup(): Promise<void>;
}

/** 把上传的文件写到临时目录，引擎会再把它们复制进任务目录。文件名只保留最后一段，防止路径穿越。 */
export async function saveUploads(files: File[]): Promise<SavedUploads> {
  const dir = await mkdtemp(join(tmpdir(), "de-upload-"));
  const attachments: AttachmentInput[] = [];
  try {
    let i = 0;
    for (const f of files) {
      const name = basename(f.name.replace(/\\/g, "/")) || `file-${i}`;
      const slot = join(dir, String(i++));
      await mkdir(slot);
      const sourcePath = join(slot, name);
      await writeFile(sourcePath, new Uint8Array(await f.arrayBuffer()));
      attachments.push({ name, sourcePath });
    }
  } catch (err) {
    await rm(dir, { recursive: true, force: true });
    throw err;
  }
  return { attachments, cleanup: () => rm(dir, { recursive: true, force: true }) };
}

export function parseJsonField(value: unknown): { ok: true; value: unknown } | { ok: false } {
  if (typeof value !== "string") return { ok: true, value };
  try {
    return { ok: true, value: JSON.parse(value) };
  } catch {
    return { ok: false };
  }
}

const COMMAND_TYPES: TaskCommand["type"][] = [
  "submit",
  "supplement",
  "continue_without_minutes",
  "continue_with_partial",
  "retry",
  "terminate",
  "approve",
  "return",
];

/** 只做形状检查；业务校验（角色、流转）全部交给引擎。 */
export function parseCommand(raw: unknown): { ok: true; command: TaskCommand } | { ok: false; message: string } {
  if (typeof raw !== "object" || raw === null) return { ok: false, message: "命令必须是 JSON 对象" };
  const c = raw as Record<string, unknown>;
  if (!COMMAND_TYPES.includes(c.type as TaskCommand["type"])) return { ok: false, message: `未知命令类型：${String(c.type)}` };
  if (c.type === "return") {
    if (typeof c.comment !== "string" || c.comment.trim() === "") return { ok: false, message: "退回必须填写意见 comment" };
    if (c.sections !== undefined && !(Array.isArray(c.sections) && c.sections.every((s) => typeof s === "string")))
      return { ok: false, message: "sections 必须是字符串数组" };
  }
  if (c.type === "supplement" && c.form !== undefined && (typeof c.form !== "object" || c.form === null || Array.isArray(c.form)))
    return { ok: false, message: "form 必须是对象" };
  return { ok: true, command: c as unknown as TaskCommand };
}
