// 会话模型（T39）：输入框右下角的模型下拉，“连接 / 模型”，切换后在对话里插一条说明。
// 纯函数，不依赖 React 与网络；选项来自后端 GET /api/settings/models（只列有 Key 的连接和它们列出的模型）。
import type { TaskEvent } from "@de/core/contract";
import type { ModelRef, ModelConnectionOption, SessionDetail } from "../api/types";

export interface ModelChoice {
  /** 下拉的一项的值：连接名与模型名 */
  value: string;
  label: string;
  selected: boolean;
  connection: string;
  model: string;
}

export interface ModelMenuGroup {
  label: string;
  values: string[];
  options: ModelChoice[];
}

/** 下拉按钮上的说法：“连接 / 模型”；还没选时提示去选。 */
export function modelLabel(model: ModelRef | undefined): string {
  if (!model || !model.connection || !model.model) return "选择模型";
  return `${model.connection} / ${model.model}`;
}

/** 按连接分组的模型选项；当前会话的模型标为 selected。 */
export function modelMenuItems(options: readonly ModelConnectionOption[], current: ModelRef | undefined): ModelMenuGroup[] {
  return options.map((connection) => {
    const models = connection.models.map((model) => {
      const selected = Boolean(current && current.connection === connection.name && current.model === model);
      return { value: `${connection.name}/${model}`, label: model, selected, connection: connection.name, model };
    });
    return { label: connection.name, values: models.map((m) => m.value), options: models };
  });
}

/** 下拉里选中的值（“连接/模型”）→ 连接和模型；连接名里可能有空格，以第一个“/”分开。 */
export function parseModelValue(value: string): { connection: string; model: string } {
  const at = value.indexOf("/");
  if (at <= 0) return { connection: value, model: "" };
  return { connection: value.slice(0, at), model: value.slice(at + 1) };
}

/** 切换模型的说明（对话里插一条）。任务运行中还要说明从下一个判断步骤生效。 */
export function modelSwitchNotice(model: ModelRef, taskRunning: boolean): string {
  const label = modelLabel(model);
  return taskRunning ? `模型已切换为 ${label}，从下一个判断步骤开始生效。` : `模型已切换为 ${label}。`;
}

/** 任务事件里的“模型已切换”：每条一条说明。 */
export function modelSwitchedNotices(events: readonly TaskEvent[]): { key: string; ts: string; text: string }[] {
  return events.flatMap((event) =>
    event.type === "model_switched" && event.model
      ? [{ key: `model:${event.seq}`, ts: event.ts, text: modelSwitchNotice(event.model, true) }]
      : [],
  );
}

/** 对话里的模型切换说明（任务运行中每次切换一条）。 */
export function modelNotices(events: readonly TaskEvent[]): { key: string; ts: string; text: string }[] {
  return modelSwitchedNotices(events);
}

/**
 * 会话里还没有任务时，选过模型就补一条切换说明。绑定任务之后由任务事件说明（见 modelNotices）：
 * 会话记录那次选择是在发起之前，对话里已经说过了。
 */
export function selectedModelNotice(detail: Pick<SessionDetail, "model" | "modelSelected" | "taskId">): { key: string; ts: string; text: string } | undefined {
  if (detail.taskId || !detail.modelSelected || !detail.model) return undefined;
  return {
    key: `model:selected:${detail.model.connection}:${detail.model.model}`,
    // 排在这个会话已有的消息之后
    ts: "9999-12-31T23:59:59.999Z",
    text: modelSwitchNotice(detail.model, false),
  };
}
