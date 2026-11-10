import { Children, isValidElement, memo, useEffect, useMemo, useRef, useState, type ReactElement, type ReactNode } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { CheckOutlined, CopyOutlined } from "@ant-design/icons";
import { closeOpenMarkdown, splitBlocks } from "../view/markdown-stream";
import { useLinkProps } from "./Preferences";

// 不渲染原始 HTML：不加 rehype-raw，react-markdown 把 HTML 当成普通文字；链接地址走默认的 urlTransform（挡掉 javascript: 等）。
const PLUGINS = [remarkGfm];

/** 外链：新窗口打开，rel 带 noopener；打开方式跟随“链接打开方式”偏好（桌面窗口里交给系统浏览器） */
function Link({ href, children }: { href?: string; children?: ReactNode }) {
  const linkProps = useLinkProps();
  return (
    <a href={href} {...linkProps(href)} target="_blank" rel="noopener noreferrer">
      {children}
    </a>
  );
}

/** 代码块：语言标签 + 复制按钮，等宽字体 */
function CodeBlock({ language, code }: { language: string; code: string }) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(code);
    } catch {
      // 剪贴板不可用（非安全上下文等）：退回到选中再复制
      const area = document.createElement("textarea");
      area.value = code;
      area.style.position = "fixed";
      area.style.opacity = "0";
      document.body.appendChild(area);
      area.select();
      try {
        document.execCommand("copy");
      } catch {
        /* 复制不了就算了 */
      }
      area.remove();
    }
    setCopied(true);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(false), 1500);
  };
  return (
    <div className="md-code">
      <div className="md-code-bar">
        <span className="md-code-lang">{language || "文本"}</span>
        <button type="button" className="md-code-copy" onClick={() => void copy()} aria-label={copied ? "已复制" : "复制代码"}>
          {copied ? <CheckOutlined /> : <CopyOutlined />}
          <span>{copied ? "已复制" : "复制"}</span>
        </button>
      </div>
      <pre>
        <code>{code}</code>
      </pre>
    </div>
  );
}

/** react-markdown 的 pre：取出里面 code 的语言和文本，换成 CodeBlock */
function Pre({ children }: { children?: ReactNode }) {
  const child = Children.toArray(children)[0];
  if (isValidElement(child)) {
    const props = (child as ReactElement<{ className?: string; children?: ReactNode }>).props;
    const language = /language-([\w-]+)/.exec(props.className ?? "")?.[1] ?? "";
    const code = Children.toArray(props.children).join("").replace(/\n$/, "");
    return <CodeBlock language={language} code={code} />;
  }
  return <pre>{children}</pre>;
}

export const MARKDOWN_PLUGINS = PLUGINS;

export const COMPONENTS: Components = {
  a: Link,
  pre: Pre,
  table: ({ children }) => (
    <div className="md-table">
      <table>{children}</table>
    </div>
  ),
};

/** 一个块的渲染：文字不变就不重渲染（流式时前面稳定的块靠它跳过） */
const Block = memo(
  function Block({ text }: { text: string }) {
    return (
      <ReactMarkdown remarkPlugins={PLUGINS} components={COMPONENTS}>
        {text}
      </ReactMarkdown>
    );
  },
  (a, b) => a.text === b.text,
);

/** 每帧最多取一次最新的文字：流式时 setState 的频率被限制在一帧一次 */
export function useFrameText(text: string, active: boolean): string {
  const [shown, setShown] = useState(text);
  const latest = useRef(text);
  latest.current = text;
  const frame = useRef<number | undefined>(undefined);
  useEffect(() => {
    if (!active) return;
    if (frame.current !== undefined) return;
    frame.current = requestAnimationFrame(() => {
      frame.current = undefined;
      setShown(latest.current);
    });
  }, [text, active]);
  useEffect(
    () => () => {
      if (frame.current !== undefined) cancelAnimationFrame(frame.current);
    },
    [],
  );
  // 不在流式时直接用最新的文字（说完那一刻马上显示完整内容）
  return active ? shown : text;
}

/**
 * Markdown 渲染：流式文字和最终消息用同一个组件。
 * 流式时先补全没写完的结构、按块切分，只有最后的块在变；说完后整段交给一次渲染。
 */
export function Markdown({ text, streaming = false }: { text: string; streaming?: boolean }) {
  const shown = useFrameText(text, streaming);
  const blocks = useMemo(() => (streaming ? splitBlocks(closeOpenMarkdown(shown)) : [text]), [streaming, shown, text]);
  return (
    <div className={`md ${streaming ? "md-live" : ""}`}>
      {blocks.map((block, index) => (
        <Block key={index} text={block} />
      ))}
    </div>
  );
}
