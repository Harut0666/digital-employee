import type { TaskEvent } from "@de/core/contract";

type Distribute<T> = T extends unknown ? Omit<T, "seq" | "ts" | "taskId"> : never;
export type EventInput = Distribute<TaskEvent>;

/** 按顺序给事件补上 seq / ts / taskId，ts 每个事件加 1 秒。 */
export function seqOf(inputs: EventInput[], startIso = "2026-10-31T10:00:00.000Z"): TaskEvent[] {
  const start = Date.parse(startIso);
  return inputs.map(
    (input, i) =>
      ({
        ...input,
        seq: i + 1,
        ts: new Date(start + i * 1000).toISOString(),
        taskId: "t1",
      }) as TaskEvent,
  );
}

export const STEP_DEFS = [
  { id: "admission", label: "准入校验", kind: "compute" as const },
  { id: "load", label: "读取数据", kind: "compute" as const },
  { id: "match", label: "匹配原因", kind: "judge" as const },
  { id: "write", label: "撰写周报", kind: "judge" as const },
  { id: "qc", label: "自动质检", kind: "qc" as const },
];
