// 事件日志：每个任务一份 events.jsonl，追加写，每条落盘（fsync）。
import { closeSync, existsSync, fsyncSync, openSync, readFileSync, writeFileSync, writeSync } from "node:fs";
import type { TaskEvent } from "../contract";

/**
 * 读取日志。进程在写入中途被杀时，最后一行可能只写了一半：
 * 遇到第一行无法解析的内容就丢弃它及其后的内容，并把文件整理成干净状态，保证后续追加不会接在半行后面。
 */
export function readEventLog(path: string): TaskEvent[] {
  if (!existsSync(path)) return [];
  const text = readFileSync(path, "utf8");
  const events: TaskEvent[] = [];
  let clean = true;
  for (const line of text.split("\n")) {
    if (line === "") continue;
    try {
      events.push(JSON.parse(line) as TaskEvent);
    } catch {
      clean = false;
      break;
    }
  }
  if (!clean || (text.length > 0 && !text.endsWith("\n"))) {
    writeFileSync(path, events.map((e) => JSON.stringify(e) + "\n").join(""));
    syncFile(path);
  }
  return events;
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
