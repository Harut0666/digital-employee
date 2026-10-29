// 周会纪要解析：用 mammoth 转 HTML，再切成带稳定 ID 的段落。
import mammoth from "mammoth";
import type { IsoWeek, MinutesParagraph } from "@de/core/contract";

export interface ParsedMinutes {
  path: string;
  week: IsoWeek | null;
  title: string;
  paragraphs: MinutesParagraph[];
}

const ENTITIES: Record<string, string> = { amp: "&", lt: "<", gt: ">", quot: '"', apos: "'", nbsp: " " };

function text(html: string): string {
  return html
    .replace(/<[^>]+>/g, "")
    .replace(/&(#x?[0-9a-f]+|[a-z]+);/gi, (m, e: string) => {
      if (e[0] === "#") {
        const code = e[1]?.toLowerCase() === "x" ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10);
        return Number.isFinite(code) ? String.fromCodePoint(code) : m;
      }
      return ENTITIES[e.toLowerCase()] ?? m;
    })
    .replace(/\s+/g, " ")
    .trim();
}

type Block = { kind: "p"; text: string } | { kind: "table"; rows: string[][] };

function blocks(html: string): Block[] {
  const out: Block[] = [];
  const re = /<table>([\s\S]*?)<\/table>|<p>([\s\S]*?)<\/p>/g;
  for (let m = re.exec(html); m; m = re.exec(html)) {
    if (m[1] !== undefined) {
      const rows = [...m[1].matchAll(/<tr>([\s\S]*?)<\/tr>/g)].map((r) =>
        [...(r[1] ?? "").matchAll(/<td[^>]*>([\s\S]*?)<\/td>/g)].map((c) => text(c[1] ?? "")),
      );
      out.push({ kind: "table", rows });
    } else {
      const t = text(m[2] ?? "");
      if (t) out.push({ kind: "p", text: t });
    }
  }
  return out;
}

const WEEK_RE = /(\d{4})\s*年\s*第\s*(\d{1,2})\s*周/;
const SECTION_RE = /^[一二三四五六七八九十]+、\s*(.+)$/;

/** 从标题类文字识别周次，如“（2026 年第 44 周）” */
export function detectWeek(s: string): IsoWeek | null {
  const m = WEEK_RE.exec(s);
  return m ? { year: Number(m[1]), week: Number(m[2]) } : null;
}

export async function parseMinutesFile(path: string): Promise<ParsedMinutes> {
  const { value } = await mammoth.convertToHtml({ path });
  const bs = blocks(value);
  const first = bs.find((b): b is Extract<Block, { kind: "p" }> => b.kind === "p");
  const title = first?.text ?? "";
  // 周次只看正文开头的标题，不看文件名
  const head = bs.filter((b): b is Extract<Block, { kind: "p" }> => b.kind === "p").slice(0, 3);
  let week: IsoWeek | null = null;
  for (const h of head) {
    week = detectWeek(h.text);
    if (week) break;
  }
  const prefix = week ? `W${String(week.week).padStart(2, "0")}` : "Wxx";
  const paragraphs: MinutesParagraph[] = [];
  let section: string | undefined;
  const push = (t: string) => {
    const p: MinutesParagraph = { id: `${prefix}-p${String(paragraphs.length + 1).padStart(2, "0")}`, text: t };
    if (section) p.section = section;
    paragraphs.push(p);
  };
  for (const b of bs.slice(bs.indexOf(first as Block) + 1)) {
    if (b.kind === "p") {
      const sec = SECTION_RE.exec(b.text);
      if (sec) section = sec[1];
      else push(b.text);
    } else {
      const [header, ...rows] = b.rows;
      for (const r of rows) {
        // 表格行转成“列名：内容”的一段，便于引用
        push(r.map((c, i) => (header?.[i] ? `${header[i]}：${c}` : c)).join("；"));
      }
    }
  }
  return { path, week, title, paragraphs };
}
