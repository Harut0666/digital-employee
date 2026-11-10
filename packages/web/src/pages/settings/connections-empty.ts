// T46 设置不再预置厂商：没有任何连接时设置页的空状态，以及“添加连接”表单的字段。纯数据，视图层只负责渲染。

/** 新建连接入口的按钮文字：空状态和页头共用，保证叫法一致。 */
export const ADD_CONNECTION_LABEL = "添加连接";

export interface ConnectionsEmptyState {
  title: string;
  hint: string;
  actionLabel: string;
}

/** 没有连接时的空状态；已经有连接就不显示。 */
export function connectionsEmptyState(connectionCount: number): ConnectionsEmptyState | null {
  if (connectionCount > 0) return null;
  return {
    title: "还没有模型连接",
    hint: "任务里的判断步骤需要模型。点“添加连接”，填名称、地址和 Key（任何 OpenAI 兼容的服务都行），再拉取模型并勾选。",
    actionLabel: ADD_CONNECTION_LABEL,
  };
}

export interface NewConnectionField {
  key: "name" | "baseUrl" | "apiKey";
  label: string;
  placeholder: string;
}

/** “添加连接”表单：名称、地址、Key（OpenAI 兼容）；模型靠“拉取模型”勾选。 */
export const NEW_CONNECTION_FIELDS: readonly NewConnectionField[] = [
  { key: "name", label: "名称", placeholder: "给这个连接起个名字，例如 我的厂商、本地 Qwen" },
  { key: "baseUrl", label: "地址", placeholder: "OpenAI 兼容的接口地址，例如 https://…/v1" },
  { key: "apiKey", label: "Key", placeholder: "API Key" },
];
