// Markdown 编辑器：左边写、右边即时渲染（并排），也可以只看编辑或只看渲染结果。
// 员工配置里的岗位说明、前台人设、提示词和 .md 源文件都用它，改一个字右边马上跟着变。
import { useState } from "react";
import { Input, Segmented } from "antd";
import { Markdown } from "./Markdown";

type Mode = "split" | "edit" | "preview";

const MODES: { label: string; value: Mode }[] = [
  { label: "并排", value: "split" },
  { label: "编辑", value: "edit" },
  { label: "预览", value: "preview" },
];

export function MarkdownEditor({
  value,
  onChange,
  rows = 12,
  placeholder,
  mono = false,
}: {
  value: string;
  onChange: (text: string) => void;
  rows?: number;
  placeholder?: string;
  /** 源文件视图用等宽字体 */
  mono?: boolean;
}) {
  const [mode, setMode] = useState<Mode>("split");
  // 编辑区和渲染区同高：行数 × 行高，渲染区超出时单独滚动
  const height = `calc(${rows} * 1.6em + 16px)`;
  return (
    <div className="md-editor">
      <div className="md-editor-bar">
        <Segmented size="small" value={mode} options={MODES} onChange={(v) => setMode(v as Mode)} />
        <span className="md-editor-hint">{mode === "edit" ? "只编辑" : "右侧即时渲染"}</span>
      </div>
      <div className={`md-editor-body ${mode}`}>
        {mode !== "preview" && (
          <Input.TextArea
            className={`md-editor-input ${mono ? "mono" : ""}`}
            value={value}
            onChange={(event) => onChange(event.target.value)}
            placeholder={placeholder}
            spellCheck={false}
            style={{ height, resize: "none" }}
          />
        )}
        {mode !== "edit" && (
          <div className="md-editor-render" style={{ height }}>
            {value.trim() ? <Markdown text={value} /> : <span className="md-editor-empty">还没有内容，写点什么右边就会渲染出来。</span>}
          </div>
        )}
      </div>
    </div>
  );
}
