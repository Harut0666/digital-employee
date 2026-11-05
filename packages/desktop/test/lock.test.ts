// 单实例锁的纯函数与文件行为：解析、存活判定、接管/阻止、正常退出删锁。
import { afterEach, describe, expect, test } from "bun:test";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  acquireInstanceLock,
  currentLock,
  isPidAlive,
  lockAction,
  lockFilePath,
  parseLockContent,
  releaseInstanceLock,
  serializeLock,
} from "../src/lock";

const roots: string[] = [];

function tempWorkspace(): string {
  const root = mkdtempSync(join(tmpdir(), "de-lock-"));
  roots.push(root);
  return root;
}

afterEach(() => {
  for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true });
});

describe("锁文件解析", () => {
  test("合法 JSON 解析出 pid 与启动时间", () => {
    const raw = JSON.stringify({ pid: 4321, startedAt: "2026-11-05T00:00:00.000Z" });
    expect(parseLockContent(raw)).toEqual({ pid: 4321, startedAt: "2026-11-05T00:00:00.000Z" });
  });

  test("允许首尾空白与换行", () => {
    const raw = `\n ${JSON.stringify({ pid: 1, startedAt: "t" })}\n`;
    expect(parseLockContent(raw)?.pid).toBe(1);
  });

  test("空 / 坏 JSON / 缺字段 / 非法 pid 都视为没有锁", () => {
    expect(parseLockContent("")).toBeNull();
    expect(parseLockContent("   \n")).toBeNull();
    expect(parseLockContent("not-json")).toBeNull();
    expect(parseLockContent("null")).toBeNull();
    expect(parseLockContent("[]")).toBeNull();
    expect(parseLockContent(JSON.stringify({ startedAt: "t" }))).toBeNull();
    expect(parseLockContent(JSON.stringify({ pid: 0, startedAt: "t" }))).toBeNull();
    expect(parseLockContent(JSON.stringify({ pid: -1, startedAt: "t" }))).toBeNull();
    expect(parseLockContent(JSON.stringify({ pid: 1.5, startedAt: "t" }))).toBeNull();
    expect(parseLockContent(JSON.stringify({ pid: 123 }))).toBeNull();
    expect(parseLockContent(JSON.stringify({ pid: 123, startedAt: "" }))).toBeNull();
  });

  test("序列化后可原样解析回来", () => {
    const lock = { pid: 99, startedAt: "2026-11-05T01:02:03.000Z" };
    expect(parseLockContent(serializeLock(lock))).toEqual(lock);
  });
});

describe("存活判定", () => {
  test("kill 不抛错 → 存活", () => {
    expect(isPidAlive(123, () => undefined)).toBe(true);
  });

  test("ESRCH → 不存在；EPERM → 存在但无权限，按存活处理", () => {
    const esrch = Object.assign(new Error("gone"), { code: "ESRCH" });
    const eperm = Object.assign(new Error("denied"), { code: "EPERM" });
    expect(isPidAlive(123, () => {
      throw esrch;
    })).toBe(false);
    expect(isPidAlive(123, () => {
      throw eperm;
    })).toBe(true);
  });

  test("非法 pid 直接判死", () => {
    expect(isPidAlive(0, () => undefined)).toBe(false);
    expect(isPidAlive(-5, () => undefined)).toBe(false);
  });

  test("当前进程按存活处理", () => {
    expect(isPidAlive(process.pid)).toBe(true);
  });
});

describe("接管判定", () => {
  const alive = () => true;
  const dead = () => false;

  test("没有锁 → 接管", () => {
    expect(lockAction(null, alive)).toBe("acquire");
  });

  test("pid 存活 → 阻止", () => {
    expect(lockAction({ pid: 1, startedAt: "t" }, alive)).toBe("blocked");
  });

  test("pid 已死 → 接管", () => {
    expect(lockAction({ pid: 1, startedAt: "t" }, dead)).toBe("acquire");
  });
});

describe("锁文件生命周期", () => {
  test("锁文件位于 <workspace>/.runtime/desktop.lock", () => {
    expect(lockFilePath("D:\\ws")).toBe(join("D:\\ws", ".runtime", "desktop.lock"));
  });

  test("首次启动写锁，正常退出删除", () => {
    const workspace = tempWorkspace();
    const lock = acquireInstanceLock(workspace);
    expect(lock).not.toBeNull();
    const path = lockFilePath(workspace);
    expect(existsSync(path)).toBe(true);
    const written = parseLockContent(readFileSync(path, "utf8"));
    expect(written?.pid).toBe(process.pid);
    expect(typeof written?.startedAt).toBe("string");

    expect(releaseInstanceLock(workspace, lock!)).toBe(true);
    expect(existsSync(path)).toBe(false);
  });

  test("已有存活实例 → 不再写锁", () => {
    const workspace = tempWorkspace();
    const first = acquireInstanceLock(workspace);
    expect(first).not.toBeNull();
    const second = acquireInstanceLock(workspace);
    expect(second).toBeNull();
  });

  test("死锁被接管（pid 不存在）", () => {
    const workspace = tempWorkspace();
    mkdirSync(join(workspace, ".runtime"), { recursive: true });
    writeFileSync(lockFilePath(workspace), serializeLock({ pid: 999_999, startedAt: "old" }));
    const lock = acquireInstanceLock(workspace, { alive: () => false });
    expect(lock?.pid).toBe(process.pid);
    expect(parseLockContent(readFileSync(lockFilePath(workspace), "utf8"))?.pid).toBe(process.pid);
  });

  test("坏锁被接管（无法解析）", () => {
    const workspace = tempWorkspace();
    mkdirSync(join(workspace, ".runtime"), { recursive: true });
    writeFileSync(lockFilePath(workspace), "{ 半截");
    const lock = acquireInstanceLock(workspace);
    expect(lock).not.toBeNull();
    expect(parseLockContent(readFileSync(lockFilePath(workspace), "utf8"))?.pid).toBe(process.pid);
  });

  test("释放时不会误删别人的锁", () => {
    const workspace = tempWorkspace();
    mkdirSync(join(workspace, ".runtime"), { recursive: true });
    writeFileSync(lockFilePath(workspace), serializeLock({ pid: process.pid + 1, startedAt: "other" }));
    const mine = currentLock(process.pid);
    expect(releaseInstanceLock(workspace, mine)).toBe(false);
    expect(existsSync(lockFilePath(workspace))).toBe(true);
  });
});
