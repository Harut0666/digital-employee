// 侧栏顶部固定的“+ 新对话”主按钮（全局入口，永远可见）。走向由 view/new-chat.ts 决定：
// 只有一个工作区就直接在它下面开；有多个就弹出小列表选在哪个工作区（默认当前会话所在的）；没有工作区时引导先添加。
import { useEffect, useRef, useState } from "react";
import { Button } from "antd";
import { FolderOutlined, PlusOutlined } from "@ant-design/icons";
import type { NewChatEntry } from "../view/new-chat";

export function NewChatButton({
  entry,
  onStart,
  onAddWorkspace,
}: {
  entry: NewChatEntry;
  /** 在某个工作区开新对话；workspaceId 为空表示交给服务端取唯一的工作区 */
  onStart: (workspaceId?: string) => void;
  onAddWorkspace: (entry: Extract<NewChatEntry, { kind: "add-workspace" }>) => void;
}) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const choosing = entry.kind === "choose";

  // 点别处或按 Esc 收起
  useEffect(() => {
    if (!open) return;
    const away = (e: MouseEvent) => {
      if (!box.current?.contains(e.target as Node)) setOpen(false);
    };
    const esc = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", esc);
    return () => {
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", esc);
    };
  }, [open]);
  useEffect(() => {
    if (!choosing) setOpen(false);
  }, [choosing]);

  const click = () => {
    if (entry.kind === "choose") setOpen((value) => !value);
    else if (entry.kind === "direct") onStart(entry.workspaceId);
    else onAddWorkspace(entry);
  };

  return (
    <div className="newchat" ref={box}>
      <Button
        type="primary"
        block
        icon={<PlusOutlined />}
        aria-haspopup={choosing ? "menu" : undefined}
        aria-expanded={choosing ? open : undefined}
        onClick={click}
      >
        新对话
      </Button>
      {open && entry.kind === "choose" && (
        <div className="newchat-pop" role="menu" aria-label="在哪个工作区开新对话">
          <div className="newchat-pop-title">在哪个工作区开新对话？</div>
          {entry.options.map((option) => (
            <button
              key={option.id}
              type="button"
              role="menuitem"
              className={`newchat-opt ${option.id === entry.defaultId ? "default" : ""}`}
              disabled={option.disabled}
              autoFocus={option.id === entry.defaultId}
              title={option.path}
              onClick={() => {
                setOpen(false);
                onStart(option.id);
              }}
            >
              <FolderOutlined className="newchat-opt-icon" />
              <span className="newchat-opt-main">
                <span className="newchat-opt-name">{option.name}</span>
                <span className="newchat-opt-path">{option.disabled ? "目录缺失，不能在这里开新对话" : option.path}</span>
              </span>
              {option.id === entry.defaultId && <span className="newchat-opt-tag">当前</span>}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
