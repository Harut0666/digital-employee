// 侧栏两个 tab 的数据：工作区（带会话摘要和未归属）和记录。
// 状态点都由后端算好，这里只原样保存；定时刷新，操作之后可以立即 refresh。

import { useEffect, useReducer, useState } from "react";
import type { RecordGroupView, WorkspaceApi, WorkspaceList } from "../api/workspaces";

export interface WorkspaceState {
  list: WorkspaceList | undefined;
  error: string | undefined;
  refresh: () => void;
}

/** 工作区列表（含会话摘要和未归属），定时刷新。 */
export function useWorkspaceList(api: WorkspaceApi, intervalMs = 3000): WorkspaceState {
  const [list, setList] = useState<WorkspaceList>();
  const [error, setError] = useState<string>();
  const [tick, refresh] = useReducer((n: number) => n + 1, 0);
  useEffect(() => {
    let alive = true;
    const load = () =>
      api.list().then(
        (value) => {
          if (!alive) return;
          setList(value);
          setError(undefined);
        },
        (e: unknown) => alive && setError(e instanceof Error ? e.message : "读取工作区失败"),
      );
    void load();
    const timer = setInterval(load, intervalMs);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, [api, intervalMs, tick]);
  return { list, error, refresh };
}

export interface RecordsState {
  groups: RecordGroupView[];
  error: string | undefined;
  refresh: () => void;
}

/** 记录 tab：所有工作区里已开工的任务，按状态分组。只在 tab 打开时读。 */
export function useRecords(api: WorkspaceApi, enabled: boolean, intervalMs = 3000): RecordsState {
  const [groups, setGroups] = useState<RecordGroupView[]>([]);
  const [error, setError] = useState<string>();
  const [tick, refresh] = useReducer((n: number) => n + 1, 0);
  useEffect(() => {
    if (!enabled) return;
    let alive = true;
    const load = () =>
      api.records().then(
        (value) => {
          if (!alive) return;
          setGroups(value.groups);
          setError(undefined);
        },
        (e: unknown) => alive && setError(e instanceof Error ? e.message : "读取记录失败"),
      );
    void load();
    const timer = setInterval(load, intervalMs);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, [api, enabled, intervalMs, tick]);
  return { groups, error, refresh };
}
