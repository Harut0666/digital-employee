// 右栏：像浏览器一样的标签页。第一页是默认页（交付 / 补充材料 / 待开工，不能关），
// 任务步骤、任务详情放在右上角的“+”里，点了才打开，可以关。整栏右端的 × 关掉整个右栏。
import { useMemo } from "react";
import { Dropdown } from "antd";
import { BranchesOutlined, CloseOutlined, OrderedListOutlined, PlusOutlined } from "@ant-design/icons";
import type { TaskEvent } from "@de/core/contract";
import type { StepStatusView } from "../api";
import { deriveStepRows } from "../view/step-rows";
import { deriveTrajectory } from "../view/trajectory";
import { closedTabs, EXTRA_TABS, tabLabel, type ExtraTab, type PanelTabs, type TabId } from "../view/panel-tabs";
import { StepList } from "./StepList";
import { TrajectoryDetails, type TraceFile } from "./Trajectory";
import { PanelClose } from "./PanelClose";

/** 任务步骤、任务详情用的任务数据（没有任务时这两页不可用） */
export interface PanelTask {
  events: TaskEvent[];
  steps: StepStatusView[];
  elapsedMs: number;
  modelCalls: { used: number; limit: number };
  /** 任务附件和各版交付物，任务详情里列出 */
  attachments: string[];
  artifacts: TraceFile[];
  /** 步骤行里没有记下模型时显示的模型名 */
  fallbackModel?: string;
}

const TAB_ICONS: Record<ExtraTab, React.ReactNode> = {
  steps: <OrderedListOutlined />,
  detail: <BranchesOutlined />,
};

export function RightPanel({
  tabs,
  onTabs,
  mainTitle,
  main,
  mainFlush,
  task,
  focusedStep,
  onFocusStep,
  onClose,
}: {
  tabs: PanelTabs;
  onTabs: (next: { open?: TabId; close?: ExtraTab }) => void;
  /** 默认页的标题：交付、补充材料、待开工 */
  mainTitle: string;
  main: React.ReactNode;
  /** 默认页自己管滚动和边距（交付物预览）；否则这里给边距、整页滚动 */
  mainFlush: boolean;
  task?: PanelTask;
  /** 从任务步骤里点进任务详情时要定位的步骤 */
  focusedStep?: string;
  onFocusStep: (stepId: string | undefined) => void;
  onClose: () => void;
}) {
  const trajectory = useMemo(() => (task ? deriveTrajectory(task.events, task.steps) : undefined), [task]);
  const stepRows = useMemo(
    () => (task ? deriveStepRows(task.events, task.steps, { nowMs: Date.now(), fallbackModel: task.fallbackModel }) : []),
    [task],
  );
  const available = task ? closedTabs(tabs) : [];
  const tabButton = (id: TabId, label: string, closable: ExtraTab | undefined) => (
    <div key={id} className={`rp-tab ${tabs.active === id ? "active" : ""}`} role="presentation">
      <button type="button" role="tab" aria-selected={tabs.active === id} className="rp-tab-main" onClick={() => onTabs({ open: id })}>
        {id !== "main" && <span className="rp-tab-icon">{TAB_ICONS[id]}</span>}
        {label}
      </button>
      {closable && (
        <button type="button" className="rp-tab-close" aria-label={`关闭${label}`} title={`关闭${label}`} onClick={() => onTabs({ close: closable })}>
          <CloseOutlined />
        </button>
      )}
    </div>
  );

  let body: React.ReactNode = main;
  if (tabs.active === "steps" && task) {
    body = (
      <StepList
        rows={stepRows}
        defaultOpen
        title="任务步骤"
        onOpen={(stepId) => {
          onFocusStep(stepId);
          onTabs({ open: "detail" });
        }}
      />
    );
  } else if (tabs.active === "detail" && task && trajectory) {
    body = (
      <TrajectoryDetails
        view={trajectory}
        elapsedMs={task.elapsedMs}
        modelCalls={task.modelCalls}
        attachments={task.attachments}
        artifacts={task.artifacts}
        focusStepId={focusedStep}
      />
    );
  }
  const flush = tabs.active === "main" && mainFlush;

  return (
    <div className="rp">
      <div className="rp-tabs" role="tablist" aria-label="右栏">
        {tabButton("main", mainTitle, undefined)}
        {tabs.extra.map((id) => tabButton(id, tabLabel(id), id))}
        {available.length > 0 && (
          <Dropdown
            trigger={["click"]}
            menu={{
              items: EXTRA_TABS.filter((t) => available.includes(t.id)).map((t) => ({
                key: t.id,
                icon: TAB_ICONS[t.id],
                label: (
                  <span className="rp-menu-item">
                    {t.label}
                    <span className="rp-menu-hint">{t.hint}</span>
                  </span>
                ),
              })),
              onClick: ({ key }) => onTabs({ open: key as ExtraTab }),
            }}
          >
            <button type="button" className="rp-add" aria-label="打开任务步骤或任务详情" title="打开任务步骤、任务详情">
              <PlusOutlined />
            </button>
          </Dropdown>
        )}
        <span className="rp-spacer" />
        <PanelClose onClose={onClose} />
      </div>
      <div className={`rp-body ${flush ? "flush" : "scroll"}`}>{body}</div>
    </div>
  );
}
