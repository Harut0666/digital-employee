// 单实例锁：工作区 .runtime/desktop.lock 记录 pid 与启动时间。
// 纯解析/存活判定与文件操作分开，纯函数可在任意 Bun 进程里单测（Spec 0003 / T17）。
import {
  closeSync,
  existsSync,
  mkdirSync,
  openSync,
  readFileSync,
  renameSync,
  unlinkSync,
  writeSync,
} from "node:fs";
import { join } from "node:path";

export interface DesktopLock {
  pid: number;
  /** ISO 时间戳；同时用于排查“锁是谁留下的” */
  startedAt: string;
}

export type LockAction = "acquire" | "blocked";

/** 锁文件位置：<workspace>/.runtime/desktop.lock */
export function lockFilePath(workspaceDir: string): string {
  return join(workspaceDir, ".runtime", "desktop.lock");
}

/** 写盘内容；JSON 一行，便于人工查看。 */
export function serializeLock(lock: DesktopLock): string {
  return `${JSON.stringify({ pid: lock.pid, startedAt: lock.startedAt })}\n`;
}

/** 解析锁文件；无法解析、pid 非法、缺少启动时间都当作“没有锁 / 坏锁”。 */
export function parseLockContent(raw: string | null | undefined): DesktopLock | null {
  if (typeof raw !== "string" || raw.trim() === "") return null;
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof value !== "object" || value === null || Array.isArray(value)) return null;
  const record = value as Record<string, unknown>;
  const pid = record.pid;
  const startedAt = record.startedAt;
  if (typeof pid !== "number" || !Number.isSafeInteger(pid) || pid <= 0) return null;
  if (typeof startedAt !== "string" || startedAt.trim() === "") return null;
  return { pid, startedAt };
}

/**
 * pid 是否存活。Node/Bun 的 `process.kill(pid, 0)` 在 pid 不存在时抛 ESRCH；
 * EPERM 说明进程存在但没权限发信号，按存活处理。
 */
export function isPidAlive(pid: number, kill: (pid: number, signal: number) => unknown = process.kill): boolean {
  if (!Number.isSafeInteger(pid) || pid <= 0) return false;
  try {
    kill(pid, 0);
    return true;
  } catch (error) {
    const code = (error as { code?: unknown } | undefined)?.code;
    return code === "EPERM";
  }
}

/** 已有锁 + 存活判定 → 是否应接管。坏锁（null）直接接管。 */
export function lockAction(existing: DesktopLock | null, alive: (pid: number) => boolean = isPidAlive): LockAction {
  if (!existing) return "acquire";
  return alive(existing.pid) ? "blocked" : "acquire";
}

/** 本进程的锁内容（启动时间用注入的时钟，便于测试）。 */
export function currentLock(pid = process.pid, now: () => Date = () => new Date()): DesktopLock {
  return { pid, startedAt: now().toISOString() };
}

function readLockFile(path: string): DesktopLock | null {
  try {
    return parseLockContent(readFileSync(path, "utf8"));
  } catch {
    return null;
  }
}

/**
 * 尝试取得单实例锁。
 * 返回锁对象表示本进程持有；返回 null 表示已有实例在运行（调用方提示后退出）。
 * 已存在的锁若 pid 已死 / 内容损坏，会被接管；写入用临时文件 + 重命名，避免半截内容。
 */
export function acquireInstanceLock(
  workspaceDir: string,
  options: { pid?: number; now?: () => Date; alive?: (pid: number) => boolean } = {},
): DesktopLock | null {
  const path = lockFilePath(workspaceDir);
  mkdirSync(join(workspaceDir, ".runtime"), { recursive: true });

  if (existsSync(path)) {
    const existing = readLockFile(path);
    if (lockAction(existing, options.alive) === "blocked") return null;
    // 坏锁 / 死锁：删掉后重建
    try {
      unlinkSync(path);
    } catch {
      // 其他进程刚删掉也算可以继续
    }
  }

  const lock = currentLock(options.pid, options.now);
  const temp = `${path}.${process.pid}.tmp`;
  const fd = openSync(temp, "w");
  try {
    writeSync(fd, serializeLock(lock));
  } finally {
    closeSync(fd);
  }
  try {
    // rename 覆盖目标：上面已确认没有存活实例，这里尽可能原子落盘
    renameSync(temp, path);
  } catch (error) {
    try {
      unlinkSync(temp);
    } catch {
      // ignore
    }
    throw error;
  }

  // 极小概率：两次启动同时通过检查，后写者覆盖前者；再读一次确认是自己
  const confirmed = readLockFile(path);
  if (!confirmed || confirmed.pid !== lock.pid) return null;
  return lock;
}

/** 正常退出时删除自己留下的锁；不是自己的锁不动。 */
export function releaseInstanceLock(workspaceDir: string, lock: DesktopLock): boolean {
  const path = lockFilePath(workspaceDir);
  const existing = readLockFile(path);
  if (!existing || existing.pid !== lock.pid) return false;
  try {
    unlinkSync(path);
    return true;
  } catch {
    return false;
  }
}
