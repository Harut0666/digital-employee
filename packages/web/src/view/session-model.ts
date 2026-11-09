// 会话模型（T39）：输入框右下角的模型下拉，“连接 / 模型”。切换模型不在对话里插说明（下拉本身显示当前模型，轨迹里保留“模型已切换”）。
// 新对话页的模型下拉（T45）用同一份数据，选中的模型随建会话的请求发出，从第一句话起生效。
// 纯函数，不依赖 React 与网络；选项来自后端 GET /api/settings/models（只列有 Key 的连接和它们列出的模型）。
import type { ModelRef, ModelConnectionOption } from "../api/types";

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

/** 没有可用模型时下拉里的说法，和去设置里的入口（T45）。 */
export const NO_MODELS_LABEL = "还没有可用的模型";
export const MODELS_SETTINGS_ENTRY = "去设置里配置模型连接";

/** 下拉菜单的一项：选中模型的分组、空状态或“去设置”。 */
export type ModelMenuItem =
  | { type: "group"; key: string; label: string; children: { key: string; label: string }[] }
  | { type: "item"; key: string; label: string; disabled?: boolean }
  | { type: "settings"; key: "settings"; label: string };

export interface ModelMenu {
  /** 没有可用模型：按钮写“还没有可用的模型”，菜单只有空状态和设置入口 */
  empty: boolean;
  groups: ModelMenuGroup[];
  /** 菜单项：没有可用模型时是空状态 + 设置入口，否则是按连接分组的模型 + 设置入口 */
  items: ModelMenuItem[];
  /** 按钮上的字 */
  label: string;
  /** “去设置里配置模型连接” */
  settingsEntry: string;
  /** 菜单里选中的值（“连接/模型”） */
  selectedValue?: string;
}

/** 新对话页的模型下拉（T45）：和会话里的下拉同一份数据，没有可用模型时给空状态和设置入口。 */
export function newSessionModelMenu(options: readonly ModelConnectionOption[], current: ModelRef | undefined): ModelMenu {
  const groups = modelMenuItems(options, current);
  const selectedValue = current && current.connection && current.model ? `${current.connection}/${current.model}` : undefined;
  if (groups.length === 0) {
    return {
      empty: true,
      groups,
      items: [
        { type: "item", key: "empty", label: NO_MODELS_LABEL, disabled: true },
        { type: "settings", key: "settings", label: MODELS_SETTINGS_ENTRY },
      ],
      label: NO_MODELS_LABEL,
      settingsEntry: MODELS_SETTINGS_ENTRY,
      selectedValue: undefined,
    };
  }
  return {
    empty: false,
    groups,
    items: [
      ...groups.map((group): ModelMenuItem => ({
        type: "group",
        key: `group:${group.label}`,
        label: group.label,
        children: group.options.map((o) => ({ key: o.value, label: o.label })),
      })),
      { type: "settings", key: "settings", label: MODELS_SETTINGS_ENTRY },
    ],
    label: modelLabel(current),
    settingsEntry: MODELS_SETTINGS_ENTRY,
    selectedValue,
  };
}

/** 下拉里选中的值（“连接/模型”）→ 连接和模型；连接名里可能有空格，以第一个“/”分开。 */
export function parseModelValue(value: string): { connection: string; model: string } {
  const at = value.indexOf("/");
  if (at <= 0) return { connection: value, model: "" };
  return { connection: value.slice(0, at), model: value.slice(at + 1) };
}
