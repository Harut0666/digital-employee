import { useState, type ReactNode } from "react";
import {
  CheckSquareOutlined,
  CodeOutlined,
  DownOutlined,
  EditOutlined,
  FileTextOutlined,
  FolderOpenOutlined,
  QuestionCircleOutlined,
  RightOutlined,
  SearchOutlined,
  TeamOutlined,
  ToolOutlined,
} from "@ant-design/icons";
import type { AgentToolView } from "../api/types";
import type { ChatItem } from "../view/chat-view";
import { errorLine, formatInput, formatOutput, lineDelta, toolStatus, toolStyle, toolSummary, turnSummary, type ToolIconKey } from "../view/tool-rows";

const ICONS: Record<ToolIconKey, ReactNode> = {
  file: <FileTextOutlined />,
  edit: <EditOutlined />,
  folder: <FolderOpenOutlined />,
  search: <SearchOutlined />,
  terminal: <CodeOutlined />,
  todo: <CheckSquareOutlined />,
  question: <QuestionCircleOutlined />,
  team: <TeamOutlined />,
  tool: <ToolOutlined />,
};

/** 展开体：输入（参数 JSON）和输出（结果文本，过长截断可展开）。只在打开时挂载。 */
function ToolBody({ call }: { call: AgentToolView }) {
  const [all, setAll] = useState(false);
  const output = formatOutput(call);
  return (
    <div className="tr-body">
      <div className="tr-label">输入</div>
      <pre className="tr-pre">{formatInput(call)}</pre>
      <div className="tr-label">输出</div>
      {output.full === "" ? (
        <div className="tr-none">{call.ended ? "（没有输出）" : "还在运行…"}</div>
      ) : (
        <>
          <pre className="tr-pre">{all ? output.full : output.text}</pre>
          {output.truncated && (
            <button type="button" className="tr-more" onClick={() => setAll(!all)}>
              {all ? "收起" : `展开全部（${output.full.length} 字）`}
            </button>
          )}
        </>
      )}
    </div>
  );
}

/** 一次工具调用：约 24px 的一行，点击展开输入和输出。状态靠样式：运行中整行闪烁，出错时摘要变错误色并显示结果首行。 */
function ToolRow({ call, turnRunning }: { call: AgentToolView; turnRunning: boolean }) {
  const [open, setOpen] = useState(false);
  const style = toolStyle(call.tool);
  // 这一轮已经结束（停止等）但这次调用没有结果：不再闪烁\n  const status = !call.ended && !turnRunning ? "done" : toolStatus(call);
  const summary = toolSummary(call);
  const delta = lineDelta(call);
  const failed = status === "error" ? errorLine(call) : "";
  return (
    <div className={`tr ${status}`}>
      <button type="button" className="tr-row" aria-expanded={open} onClick={() => setOpen(!open)}>
        <span className="tr-icon" aria-hidden>
          {ICONS[style.icon]}
        </span>
        <span className="tr-verb">{style.verb}</span>
        {summary && (
          <>
            <span className="tr-dot" aria-hidden>
              ·
            </span>
            <span className="tr-sum">{summary}</span>
          </>
        )}
        {delta && (
          <span className="tr-delta">
            <span className="tr-add">+{delta.added}</span>
            {delta.removed > 0 && <span className="tr-del">-{delta.removed}</span>}
          </span>
        )}
        {failed && <span className="tr-err">{failed}</span>}
        <span className="tr-caret" aria-hidden>
          {open ? <DownOutlined /> : <RightOutlined />}
        </span>
      </button>
      {open && <ToolBody call={call} />}
    </div>
  );
}

/**
 * 一轮里的过程行。这一轮还在进行时逐行列出；结束后折成“已完成 · 用时 X”的一行按钮（默认折叠，点击展开）。
 */
export function ToolTurn({ item }: { item: Extract<ChatItem, { kind: "tools" }> }) {
  const [open, setOpen] = useState(false);
  const expanded = item.running || open;
  return (
    <div className="tr-turn">
      {!item.running && (
        <button type="button" className="tr-fold" aria-expanded={open} onClick={() => setOpen(!open)}>
          <span className="tr-caret" aria-hidden>
            {open ? <DownOutlined /> : <RightOutlined />}
          </span>
          {turnSummary(item.items, item.startedAt, item.endedAt)}
        </button>
      )}
      {expanded && (
        <div className="tr-list">
          {item.items.map((call) => (
            <ToolRow key={call.id || call.tool} call={call} turnRunning={item.running} />
          ))}
        </div>
      )}
    </div>
  );
}
