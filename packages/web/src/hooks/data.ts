import { createContext, useContext, useEffect, useMemo, useReducer, useRef, useState } from "react";
import type { TaskEvent } from "@de/core/contract";
import type { AgentToolView, EmployeeInfo, PreflightResult, SessionAgentEvent, SessionDetail, TaskSnapshot, WebApi } from "../api";
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

export interface LiveState {
  /** 正在说的一轮：逐字出现的文字和已经调用的工具（说完会变成会话里的消息） */
  live?: { turn: number; text: string; tools: AgentToolView[]; running: boolean };
}

/**
 * 订阅一个会话的事件流（T40）：说的话逐字出现，工具调用逐个列出，一轮结束时让调用方重读会话。
 * 切走会话时断开；正在说的那一轮在切回来时会重读会话补上。
 */
export function useSessionStream(id: string | undefined, onTurnEnd: () => void): LiveState {
  const api = useApi();
  const [live, setLive] = useState<LiveState["live"]>();
  const ended = useRef(onTurnEnd);
  ended.current = onTurnEnd;
  useEffect(() => {
    setLive(undefined);
    if (!id) return;
    const apply = (event: SessionAgentEvent) => {
      setLive((prev) => {
        switch (event.type) {
          case "turn_start":
            return { turn: event.turn, text: "", tools: [], running: true };
          case "text_delta":
            if (prev && prev.turn === event.turn) return { ...prev, running: true, text: prev.text + (event.text ?? "") };
            return { turn: event.turn, text: event.text ?? "", tools: [], running: true };
          case "tool_start": {
            const base = prev && prev.turn === event.turn ? prev : { turn: event.turn, text: "", tools: [], running: true };
            const tool: AgentToolView = { id: event.id ?? "", tool: event.tool ?? "", args: event.args, ended: false };
            return { ...base, running: true, tools: [...base.tools.filter((t) => t.id !== tool.id), tool] };
          }
          case "tool_end": {
            if (!prev || prev.turn !== event.turn) return prev;
            return {
              ...prev,
              tools: prev.tools.map((t) =>
                t.id === event.id ? { ...t, ended: true, result: event.result, reason: event.reason, isErr: event.isErr } : t,
              ),
            };
          }
          case "turn_end":
            if (prev && prev.turn === event.turn) return { ...prev, running: false };
            return prev;
          default:
            // message：写进会话的卡片和其它消息，由一轮结束时重读会话补齐
            return prev;
        }
      });
      if (event.type === "turn_end") ended.current();
    };
    return api.subscribeSession(id, apply);
  }, [api, id]);
  return { live };
}

/** 会话工作区目录里可选的文件（@ 选择）：打开输入框时读一次，员工接手后（目录里多了上传的“材料”）重读。 */
export function useFolderFiles(id: string | undefined, enabled: boolean, status: string | undefined): string[] {
  const api = useApi();
  const [paths, setPaths] = useState<string[]>([]);
  useEffect(() => {
    if (!id || !enabled) {
      setPaths([]);
      return;
    }
    let alive = true;
    api
      .listFolderFiles(id)
      .then((listing) => alive && setPaths(listing.entries.filter((e) => !e.dir).map((e) => e.path)), () => alive && setPaths([]));
    return () => {
      alive = false;
    };
  }, [api, id, enabled, status]);
  return paths;
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
      // 判断步骤的模型输出增量只推送、不落盘：它没有 seq，也不影响快照（T43）。
      // 同一轮的分片并成一条，事件数组不会随输出长度无限变长。
      if (ev.seq > 0) {
        if (ev.seq <= lastSeq.current) return;
        lastSeq.current = ev.seq;
      }
      setEvents((prev) => {
        if (ev.type === "model_delta") {
          const last = prev.at(-1);
          if (last && last.type === "model_delta" && last.stepId === ev.stepId && last.turn === ev.turn) {
            return [...prev.slice(0, -1), { ...last, text: last.text + ev.text }];
          }
        }
        return [...prev, ev];
      });
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



