// 嵌入资源的归档格式：把一组文件打成一个二进制（构建期由 scripts/gen-embedded.ts 生成）。
// 用 Bun 的 `import ... with { type: "file" }` 把归档文件本身嵌进 exe，运行时在这个归档里按 key 取文件。
//
// 布局：magic "DEA1"(4) | 头部长度 uint32 LE(4) | 头部 JSON | 各文件内容
// 头部：{ entries: [{ key, offset, size }] }，offset 相对内容区起点。条目按 key 的码元顺序排序，保证构建可复现。

const MAGIC = "DEA1";
const PREFIX = 8;

export interface ArchiveEntry {
  /** 工作区相对路径，统一用 "/" 分隔，例如 "weekly-report/workflow.yaml" */
  key: string;
  data: Uint8Array;
}

interface HeaderEntry {
  key: string;
  offset: number;
  size: number;
}

function corrupt(reason: string): never {
  throw new Error(`归档文件损坏：${reason}`);
}

/** 按 code unit 排序，避免 localeCompare 受语言环境影响（构建结果要可复现）。 */
const byKey = (a: { key: string }, b: { key: string }) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0);

export function encodeArchive(entries: ArchiveEntry[]): Uint8Array {
  const sorted = [...entries].sort(byKey);
  const header: HeaderEntry[] = [];
  let offset = 0;
  for (const entry of sorted) {
    header.push({ key: entry.key, offset, size: entry.data.length });
    offset += entry.data.length;
  }
  const headerBytes = new TextEncoder().encode(JSON.stringify({ entries: header }));
  const out = new Uint8Array(PREFIX + headerBytes.length + offset);
  out.set(new TextEncoder().encode(MAGIC), 0);
  new DataView(out.buffer).setUint32(4, headerBytes.length, true);
  out.set(headerBytes, PREFIX);
  let at = PREFIX + headerBytes.length;
  for (const entry of sorted) {
    out.set(entry.data, at);
    at += entry.data.length;
  }
  return out;
}

export function decodeArchive(bytes: Uint8Array): ArchiveEntry[] {
  if (bytes.length < PREFIX) corrupt(`长度 ${bytes.length} 不足文件头`);
  if (new TextDecoder().decode(bytes.subarray(0, 4)) !== MAGIC) corrupt("文件头不是 DEA1");
  const headerLength = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength).getUint32(4, true);
  if (headerLength === 0 || PREFIX + headerLength > bytes.length) corrupt(`头部长度 ${headerLength} 越界`);
  let parsed: unknown;
  try {
    parsed = JSON.parse(new TextDecoder().decode(bytes.subarray(PREFIX, PREFIX + headerLength)));
  } catch (err) {
    corrupt(`头部不是合法 JSON（${err instanceof Error ? err.message : String(err)}）`);
  }
  const entries = (parsed as { entries?: unknown })?.entries;
  if (!Array.isArray(entries)) corrupt("头部缺少 entries");
  const payload = bytes.subarray(PREFIX + headerLength);
  return (entries as unknown[]).map((raw, i) => {
    const e = raw as Partial<HeaderEntry> | undefined;
    if (typeof e?.key !== "string" || typeof e.offset !== "number" || typeof e.size !== "number") corrupt(`第 ${i} 条缺少 key/offset/size`);
    if (e.offset < 0 || e.size < 0 || e.offset + e.size > payload.length) corrupt(`第 ${i} 条（${e.key}）内容越界`);
    return { key: e.key, data: payload.subarray(e.offset, e.offset + e.size) };
  });
}
