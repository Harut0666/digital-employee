// Markdown 编辑器：默认显示渲染后的样子（预览）；切到“编辑”改内容，“并排”左边写、右边即时渲染。
// 员工配置里的岗位说明、前台人设、提示词和 .md 源文件都用它，改一个字右边马上跟着变。
import { useState } from "react";
import { Input, Segmented } from "antd";
import { Markdown } from "./Markdown";

type Mode = "split" | "edit" | "preview";

const MODES: { label: string; value: Mode }[] = [
  { label: "预览", value: "preview" },
  { label: "编辑", value: "edit" },
  { label: "并排", value: "split" },
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
  // 默认看渲染后的样子，要改再切到“编辑”或“并排”
  const [mode, setMode] = useState<Mode>("preview");
  // 编辑区和渲染区同高：行数 × 行高，渲染区超出时单独滚动
  const height = `calc(${rows} * 1.6em + 16px)`;
  return (
    <div className="md-editor">
      <div className="md-editor-bar">
        <Segmented size="small" value={mode} options={MODES} onChange={(v) => setMode(v as Mode)} />
        <span className="md-editor-hint">{mode === "preview" ? "点“编辑”修改内容" : mode === "edit" ? "只编辑" : "右侧即时渲染"}</span>
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
