// 操作归属：后端在快照里给出可用操作和在等的角色，这里只按当前角色分拣，不推算状态和流转。
// 后端仍是最终裁判。

import type { PauseView, TaskAction, UiRole } from "../api/types";

export const ROLE_LABELS: Record<UiRole, string> = {
  initiator: "发起人",
  reviewer: "审核人",
  operator: "操作员",
};

const roleNames = (roles: readonly UiRole[]) => roles.map((r) => ROLE_LABELS[r]).join("、");

/** 当前角色做不了的事：说明等谁，并给出切换入口。 */
export interface RoleHandoff {
  text: string;
  switchTo: UiRole[];
}

export interface ActionPlan {
  /** 当前角色能点的操作 */
  mine: TaskAction[];
  handoff?: RoleHandoff;
}

export function actionPlan(actions: readonly TaskAction[], waitingFor: readonly UiRole[], role: UiRole): ActionPlan {
  const mine = actions.filter((a) => a.roles.includes(role));
  if (mine.length === 0) {
    if (waitingFor.length === 0) return { mine };
    return { mine, handoff: { text: `等待${roleNames(waitingFor)}处理`, switchTo: [...waitingFor] } };
  }
  const others = actions.filter((a) => !a.roles.includes(role));
  if (others.length === 0) return { mine };
  // 归属相同的操作合并成一句
  const byRoles = new Map<string, { labels: string[]; roles: UiRole[] }>();
  for (const a of others) {
    const key = a.roles.join();
    const entry = byRoles.get(key) ?? { labels: [], roles: a.roles };
    entry.labels.push(`「${a.label}」`);
    byRoles.set(key, entry);
  }
  const text = [...byRoles.values()].map((e) => `${e.labels.join("")}由${roleNames(e.roles)}处理`).join("；");
  return { mine, handoff: { text, switchTo: [...new Set(others.flatMap((a) => a.roles))] } };
}

/** 暂停的根因在模型配置上，要给设置入口。旧工作区里模型调用失败记在工具故障下，靠 detail 识别。 */
export function needsModelSettings(pause: Pick<PauseView, "code" | "detail">): boolean {
  return pause.code === "model_unavailable" || pause.detail?.failure === "model_error";
}
