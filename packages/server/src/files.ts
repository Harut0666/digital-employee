// 文件响应：交付物下载与前端静态资源托管。
import { stat } from "node:fs/promises";
import { basename, extname, join, resolve, sep } from "node:path";

const TYPES: Record<string, string> = {
  ".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  ".md": "text/markdown; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".jsonl": "application/x-ndjson; charset=utf-8",
};

async function isFile(path: string): Promise<boolean> {
  return stat(path).then((s) => s.isFile(), () => false);
}

function typeOf(path: string): string {
  return TYPES[extname(path).toLowerCase()] ?? Bun.file(path).type;
}

/** 下载交付物；文件不存在返回 undefined。 */
export async function downloadResponse(path: string): Promise<Response | undefined> {
  if (!(await isFile(path))) return undefined;
  const name = basename(path);
  return new Response(Bun.file(path), {
    headers: {
      "content-type": typeOf(path),
      "content-disposition": `attachment; filename*=UTF-8''${encodeURIComponent(name)}`,
    },
  });
}

/** 托管静态资源；找不到具体文件时回退到 index.html（前端路由）。目录穿越返回 undefined。 */
export async function staticResponse(distDir: string, urlPath: string): Promise<Response | undefined> {
  const root = resolve(distDir);
  let rel: string;
  try {
    rel = decodeURIComponent(urlPath);
  } catch {
    return undefined;
  }
  const target = resolve(join(root, rel));
  if (target !== root && !target.startsWith(root + sep)) return undefined;
  if (await isFile(target)) return new Response(Bun.file(target), { headers: { "content-type": typeOf(target) } });
  // 带扩展名的请求（缺失的资源文件）不回退，避免把 index.html 当成 js 返回
  if (extname(rel) !== "") return undefined;
  const index = join(root, "index.html");
  if (await isFile(index)) return new Response(Bun.file(index), { headers: { "content-type": "text/html; charset=utf-8" } });
  return undefined;
}
