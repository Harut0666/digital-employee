// 右栏“工作区文件”的纯逻辑：把一层一层列出来的目录摊成一棵树的行、按扩展名决定怎么预览。不依赖 React 与网络。

import type { FolderEntry } from "../api/types";

export interface TreeRow {
  entry: FolderEntry;
  depth: number;
  /** 文件夹是否展开 */
  open: boolean;
}

/**
 * 把已经加载的各层目录摊成行：children 以文件夹路径为键（根是空串），expanded 是展开的文件夹。
 * 没展开的文件夹不列它的内容；展开了但还没加载的文件夹后面没有行（界面显示加载中）。
 */
export function treeRows(children: Readonly<Record<string, readonly FolderEntry[]>>, expanded: ReadonlySet<string>): TreeRow[] {
  const rows: TreeRow[] = [];
  const walk = (dir: string, depth: number) => {
    for (const entry of children[dir] ?? []) {
      const open = Boolean(entry.dir) && expanded.has(entry.path);
      rows.push({ entry, depth, open });
      if (open) walk(entry.path, depth + 1);
    }
  };
  walk("", 0);
  return rows;
}

export type PreviewKind = "markdown" | "docx" | "image" | "pdf" | "text" | "none";

const TEXT_EXT = new Set(["txt", "csv", "tsv", "json", "jsonl", "yaml", "yml", "log", "xml", "html", "htm", "ini", "toml", "py", "js", "ts", "go", "sql", "sh", "ps1", "bat", "css"]);
const IMAGE_EXT = new Set(["png", "jpg", "jpeg", "gif", "webp"]);

/** 一个文件在右栏里怎么预览：Markdown 渲染、Word 文档渲染、图片显示、文本原样；其余（Excel 等）只能用本机应用打开 */
export function previewKind(name: string): PreviewKind {
  const ext = name.includes(".") ? name.slice(name.lastIndexOf(".") + 1).toLowerCase() : "";
  if (ext === "md" || ext === "markdown") return "markdown";
  if (ext === "docx") return "docx";
  if (ext === "pdf") return "pdf";
  if (IMAGE_EXT.has(ext)) return "image";
  if (TEXT_EXT.has(ext)) return "text";
  return "none";
}

/** 文本预览最多显示多少字符；更长的截断并提示用本机应用打开 */
export const TEXT_PREVIEW_LIMIT = 200_000;

export function formatSize(size: number | undefined): string {
  if (size === undefined) return "";
  if (size >= 1024 * 1024) return `${(size / 1024 / 1024).toFixed(1)} MB`;
  if (size >= 1024) return `${Math.round(size / 1024)} KB`;
  return `${size} B`;
}

/** 文件所在的文件夹（根目录是空串） */
export function parentOf(path: string): string {
  const at = path.lastIndexOf("/");
  return at < 0 ? "" : path.slice(0, at);
}
