// 事件日志：每个任务一份 events.jsonl，追加写，每条落盘（fsync）。
import { closeSync, existsSync, fsyncSync, openSync, readFileSync, writeFileSync, writeSync } from "node:fs";
import type { TaskEvent } from "../contract";

interface EventLogScan {
  events: TaskEvent[];
  /** 中间行无法解析：日志损坏，错误说明；未损坏时没有 */
  corruption?: string;
  /** 末尾是无法解析的半行（进程在写入中途被杀） */
  tornFinalLine: boolean;
}

function scanLog(text: string, path: string): EventLogScan {
  const events: TaskEvent[] = [];
  const lines = text.split("\n");
  let last = lines.length - 1;
  while (last >= 0 && lines[last] === "") last--;
  for (let i = 0; i <= last; i++) {
    const line = lines[i]!;
    if (line === "") continue;
    try {
      events.push(JSON.parse(line) as TaskEvent);
    } catch {
      if (i !== last) {
        return {
          events,
          corruption: `事件日志损坏：${path} 第 ${i + 1} 行无法解析，其后仍有内容。已保持文件不变，请人工检查。`,
          tornFinalLine: false,
        };
      }
      return { events, tornFinalLine: true };
    }
  }
  return { events, tornFinalLine: false };
}

/**
 * 读取日志。进程在写入中途被杀时，最后一行可能只写了一半：只丢弃无法解析的“最后一行”，
 * 并把文件整理成干净状态，保证后续追加不会接在半行后面。
 * 中间的行无法解析不是写入中断，而是日志损坏：抛出错误且不改动文件，避免丢掉其后的有效事件。
 */
export function readEventLog(path: string): TaskEvent[] {
  if (!existsSync(path)) return [];
  const text = readFileSync(path, "utf8");
  const scan = scanLog(text, path);
  if (scan.corruption) throw new Error(scan.corruption);
  if (scan.tornFinalLine || (text.length > 0 && !text.endsWith("\n"))) {
    writeFileSync(path, scan.events.map((e) => JSON.stringify(e) + "\n").join(""));
    syncFile(path);
  }
  return scan.events;
}

/**
 * 尽力读取（只读，不整理文件）：遇到中间坏行就停在它之前，返回已解析的事件与错误说明。
 * 供 list() 在日志损坏时仍能给出任务的最后已知状态。
 */
export function readEventLogSalvage(path: string): { events: TaskEvent[]; error?: string } {
  if (!existsSync(path)) return { events: [] };
  const scan = scanLog(readFileSync(path, "utf8"), path);
  return scan.corruption ? { events: scan.events, error: scan.corruption } : { events: scan.events };
}

function syncFile(path: string): void {
  const fd = openSync(path, "r+");
  try {
    fsyncSync(fd);
  } finally {
    closeSync(fd);
  }
}

/** 追加一条事件并落盘。 */
export function appendEventLine(path: string, event: TaskEvent): void {
  const fd = openSync(path, "a");
  try {
    writeSync(fd, JSON.stringify(event) + "\n");
    fsyncSync(fd);
  } finally {
    closeSync(fd);
  }
}
