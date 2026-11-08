import { createContext, useContext, useEffect, useMemo, useReducer, useRef, useState } from "react";
import type { TaskEvent } from "@de/core/contract";
import type { EmployeeInfo, PreflightResult, SessionDetail, TaskSnapshot, WebApi } from "../api";
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

export interface SessionState {
  detail: SessionDetail | undefined;
  /** 读取失败的说明 */
  error: string | undefined;
  reload: () => void;
}

/** 一个会话的全部消息。任务推进时由调用方触发 reload（任务卡片是读取时从事件投影的）。 */
export function useSession(id: string | undefined): SessionState {
  const api = useApi();
  const [detail, setDetail] = useState<SessionDetail>();
  const [error, setError] = useState<string>();
  const [tick, reload] = useReducer((n: number) => n + 1, 0);
  useEffect(() => {
    if (!id) {
      setDetail(undefined);
      return;
    }
    let alive = true;
    api.getSession(id).then(
      (d) => {
        if (!alive) return;
        setDetail(d);
        setError(undefined);
      },
      (e: unknown) => alive && setError(e instanceof Error ? e.message : "读取会话失败"),
    );
    return () => {
      alive = false;
    };
  }, [api, id, tick]);
  // 切换会话时先清空，避免短暂显示上一个会话
  useEffect(() => setDetail(undefined), [id]);
  return { detail: detail?.id === id ? detail : undefined, error, reload };
}

/** 提交前检查：新对话首屏用它提示配置 Key。reload 在设置保存后重查。 */
export function usePreflight(): { preflight: PreflightResult | undefined; reload: () => void } {
  const api = useApi();
  const [preflight, setPreflight] = useState<PreflightResult>();
  const [tick, reload] = useReducer((n: number) => n + 1, 0);
  useEffect(() => {
    let alive = true;
    api.preflight().then((p) => alive && setPreflight(p), () => {});
    return () => {
      alive = false;
    };
  }, [api, tick]);
  return { preflight, reload };
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
      // 快照里有引擎判定的预算（模型调用数、用时）和步骤状态，这些事件之后重新拉取
      if (ev.type === "state_changed" || ev.type === "step_completed" || ev.type === "step_failed") reload();
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



