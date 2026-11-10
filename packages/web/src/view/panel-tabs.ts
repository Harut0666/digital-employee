// 右栏标签页（像浏览器）：一个默认页（交付 / 补充材料 / 待开工，不能关），其余页点了才打开、可以关。
// 纯函数：状态怎么变都在这里，界面只负责画。

export type ExtraTab = "steps" | "files";
export type TabId = "main" | ExtraTab;

export interface PanelTabs {
  /** 已打开的其他页，按打开顺序排在默认页后面 */
  extra: ExtraTab[];
  active: TabId;
}

export const INITIAL_TABS: PanelTabs = { extra: [], active: "main" };

/** 其他页能打开的全部种类，也是“+”菜单里的顺序 */
export const EXTRA_TABS: { id: ExtraTab; label: string; hint: string }[] = [
  { id: "steps", label: "任务步骤", hint: "每一步的进度；点一步，下面看它做了什么" },
  { id: "files", label: "工作区文件", hint: "浏览工作区里的文件夹和文件，预览、打开" },
];

export const tabLabel = (id: ExtraTab): string => EXTRA_TABS.find((t) => t.id === id)?.label ?? id;

/** 打开一页并切过去；已经开着就只是切过去 */
export function openTab(tabs: PanelTabs, id: TabId): PanelTabs {
  if (id === "main") return { ...tabs, active: "main" };
  return { extra: tabs.extra.includes(id) ? tabs.extra : [...tabs.extra, id], active: id };
}

/** 关一页：正看着它就切到左边相邻的一页（左边没有就是默认页） */
export function closeTab(tabs: PanelTabs, id: ExtraTab): PanelTabs {
  const at = tabs.extra.indexOf(id);
  if (at < 0) return tabs;
  const extra = tabs.extra.filter((t) => t !== id);
  if (tabs.active !== id) return { ...tabs, extra };
  return { extra, active: extra[at - 1] ?? "main" };
}

/** 还没打开的页：“+”菜单里列这些 */
export function closedTabs(tabs: PanelTabs): ExtraTab[] {
  return EXTRA_TABS.map((t) => t.id).filter((id) => !tabs.extra.includes(id));
}
