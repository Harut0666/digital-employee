// 右栏：像浏览器一样的标签页。第一页是默认页（交付 / 补充材料 / 待开工，不能关），
// 任务步骤、任务详情放在右上角的“+”里，点了才打开，可以关。整栏右端的 × 关掉整个右栏。
import { useMemo } from "react";
import { Button, Dropdown } from "antd";
import { CloseOutlined, FolderOpenOutlined, OrderedListOutlined, PlusOutlined } from "@ant-design/icons";
import type { TaskEvent } from "@de/core/contract";
import type { StepStatusView } from "../api";
import { deriveStepRows } from "../view/step-rows";
import { deriveTrajectory } from "../view/trajectory";
import { closedTabs, EXTRA_TABS, tabLabel, type ExtraTab, type PanelTabs, type TabId } from "../view/panel-tabs";
import { StepList } from "./StepList";
import { TrajectoryDetails, type TraceFile } from "./Trajectory";
import { PanelClose } from "./PanelClose";
import { WorkspaceFiles } from "./WorkspaceFiles";

/** 任务步骤页里“看全部步骤的详情（含附件和产物）”用的特殊值 */
export const ALL_STEPS = "*";

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
  files: <FolderOpenOutlined />,
};

export function RightPanel({
  tabs,
  onTabs,
  mainTitle,
  main,
  mainFlush,
  task,
  sessionId,
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
  /** 工作区文件页要读哪个会话所在的工作区 */
  sessionId?: string;
  /** 任务步骤页里点中的那一步（ALL_STEPS 是看全部）；它的详情显示在步骤列表下面 */
  focusedStep?: string;
  onFocusStep: (stepId: string | undefined) => void;
  onClose: () => void;
}) {
  const trajectory = useMemo(() => (task ? deriveTrajectory(task.events, task.steps) : undefined), [task]);
  const stepRows = useMemo(
    () => (task ? deriveStepRows(task.events, task.steps, { nowMs: Date.now(), fallbackModel: task.fallbackModel }) : []),
    [task],
  );
  // 任务步骤要有任务，工作区文件要有会话
  const available = closedTabs(tabs).filter((id) => (id === "steps" ? Boolean(task) : Boolean(sessionId)));
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
  if (tabs.active === "steps" && task && trajectory) {
    const focusedRow = stepRows.find((row) => row.id === focusedStep);
    body = (
      <div className="rp-steps">
        <StepList
          rows={stepRows}
          defaultOpen
          title="任务步骤"
          selected={focusedStep === ALL_STEPS ? undefined : focusedStep}
          onOpen={(stepId) => onFocusStep(stepId === focusedStep ? undefined : stepId)}
        />
        <div className="rp-detail">
          <div className="rp-detail-head">
            <span>{focusedStep === ALL_STEPS ? "任务详情 · 全部步骤" : focusedRow ? `任务详情 · ${focusedRow.label}` : "任务详情"}</span>
            {focusedStep === undefined ? (
              <Button size="small" type="link" onClick={() => onFocusStep(ALL_STEPS)}>
                查看全部
              </Button>
            ) : (
              <Button size="small" type="link" onClick={() => onFocusStep(undefined)}>
                收起详情
              </Button>
            )}
          </div>
          {focusedStep === undefined ? (
            <div className="rp-detail-hint">点上面的一步，这里显示它做了什么：工具调用、模型轮次、质检。</div>
          ) : (
            <TrajectoryDetails
              view={trajectory}
              elapsedMs={task.elapsedMs}
              modelCalls={task.modelCalls}
              attachments={task.attachments}
              artifacts={task.artifacts}
              focusStepId={focusedStep === ALL_STEPS ? undefined : focusedStep}
              onlyStepId={focusedStep === ALL_STEPS ? undefined : focusedStep}
            />
          )}
        </div>
      </div>
    );
  } else if (tabs.active === "files" && sessionId) {
    body = <WorkspaceFiles sessionId={sessionId} />;
  }  const flush = tabs.active === "main" && mainFlush;

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
            <button type="button" className="rp-add" aria-label="打开任务步骤或工作区文件" title="打开任务步骤、工作区文件">
              <PlusOutlined />
            </button>
          </Dropdown>
        )}
        <span className="rp-spacer" />
        <PanelClose onClose={onClose} />
      </div>
      <div className={`rp-body ${flush || tabs.active === "files" ? "flush" : "scroll"}`}>{body}</div>
    </div>
  );
}
