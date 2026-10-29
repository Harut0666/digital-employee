import { createContext, useContext, useEffect, useMemo, useReducer, useRef, useState } from "react";
import type { TaskEvent, TaskSnapshot, TaskSummary } from "@de/core/contract";
import type { EmployeeInfo, WebApi } from "../api";
import { deriveSessionView, type SessionView } from "../view/session-view";

export const ApiContext = createContext<WebApi | null>(null);
export function useApi(): WebApi {
  const api = useContext(ApiContext);
  if (!api) throw new Error("ApiContext 未提供");
  return api;
}

export function useEmployees(): EmployeeInfo[] {
  const api = useApi();
  const [list, setList] = useState<EmployeeInfo[]>([]);
  useEffect(() => {
    let alive = true;
    api.listEmployees().then((l) => alive && setList(l), () => {});
    return () => {
      alive = false;
    };
  }, [api]);
  return list;
}

/** 会话列表：定时刷新，另外暴露 refresh 供操作后立即刷新。 */
export function useTaskList(intervalMs = 2000): { tasks: TaskSummary[]; refresh: () => void } {
  const api = useApi();
  const [tasks, setTasks] = useState<TaskSummary[]>([]);
  const [tick, refresh] = useReducer((n: number) => n + 1, 0);
  useEffect(() => {
    let alive = true;
    const load = () => api.listTasks().then((l) => alive && setTasks(l), () => {});
    void load();
    const timer = setInterval(load, intervalMs);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, [api, intervalMs, tick]);
  return { tasks, refresh };
}

export interface TaskState {
  snapshot: TaskSnapshot | undefined;
  view: SessionView;
  events: TaskEvent[];
  loading: boolean;
  /** 重新拉快照（预算上限、附件、步骤定义） */
  reload: () => void;
}

/** 一个任务的快照 + SSE 事件流 → 会话视图。 */
export function useTask(id: string | undefined): TaskState {
  const api = useApi();
  const [snapshot, setSnapshot] = useState<TaskSnapshot>();
  const [events, setEvents] = useState<TaskEvent[]>([]);
  const [loading, setLoading] = useState(false);
  const [tick, reload] = useReducer((n: number) => n + 1, 0);
  const lastSeq = useRef(0);

  // 快照：切换任务或 reload 时拉取
  useEffect(() => {
    if (!id) {
      setSnapshot(undefined);
      return;
    }
    let alive = true;
    api.getTask(id).then((s) => alive && setSnapshot(s), () => {});
    return () => {
      alive = false;
    };
  }, [api, id, tick]);

  // 事件流：切换任务时重新订阅；状态变化后刷新快照
  useEffect(() => {
    setEvents([]);
    lastSeq.current = 0;
    if (!id) return;
    setLoading(true);
    const stop = api.subscribe(id, 0, (ev) => {
      if (ev.seq <= lastSeq.current) return;
      lastSeq.current = ev.seq;
      setEvents((prev) => [...prev, ev]);
      setLoading(false);
      if (ev.type === "state_changed") reload();
    });
    const t = setTimeout(() => setLoading(false), 1500);
    return () => {
      stop();
      clearTimeout(t);
    };
  }, [api, id]);

  const view = useMemo(() => deriveSessionView(events, snapshot?.steps ?? []), [events, snapshot?.steps]);
  return { snapshot, view, events, loading, reload };
}


