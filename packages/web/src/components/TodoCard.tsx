// 待办清单卡（todo_write）：一张紧凑的卡，勾选 / 进行中 / 待做三种图标。
// 最新的一张展开并随 Agent 更新实时变化；更早的收起成一行（点开仍能看）。
import { Collapse } from "antd";
import { CheckCircleFilled, ClockCircleOutlined, LoadingOutlined, UnorderedListOutlined } from "@ant-design/icons";
import type { ChatItem } from "../view/chat-view";

type TodoItem = Extract<ChatItem, { kind: "todo" }>;

function Rows({ item }: { item: TodoItem }) {
  return (
    <ul className="todo-rows">
      {item.rows.map((row) => (
        <li key={row.content} className={`todo-row ${row.status}`} data-status={row.status}>
          <span className="todo-icon">
            {row.icon === "check" ? <CheckCircleFilled /> : row.icon === "progress" ? <LoadingOutlined spin /> : <ClockCircleOutlined />}
          </span>
          <span className="todo-text">{row.content}</span>
        </li>
      ))}
    </ul>
  );
}

export function TodoCard({ item }: { item: TodoItem }) {
  const head = (
    <span className="todo-head">
      <UnorderedListOutlined /> 待办清单 · {item.summary}
    </span>
  );
  if (item.rows.length === 0) return <div className="card muted todo-card done">{head}</div>;
  if (item.latest) {
    return (
      <div className="card todo-card latest">
        <div className="card-title">{head}</div>
        <Rows item={item} />
      </div>
    );
  }
  // 旧的清单收起来：一行摘要，点开看当时的样子
  return (
    <Collapse
      className="card muted todo-card old"
      size="small"
      bordered={false}
      items={[{ key: "rows", label: head, children: <Rows item={item} /> }]}
    />
  );
}
