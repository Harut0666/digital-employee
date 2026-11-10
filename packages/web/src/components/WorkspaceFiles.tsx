// 右栏“工作区文件”：浏览会话所在工作区目录里的文件夹和文件（一层一层展开），点文件预览，
// 预览不了的（Excel 等）用本机应用打开。只读：这里不改、不删工作区里的任何东西。
import { useCallback, useEffect, useRef, useState } from "react";
import { App as AntApp, Alert, Button, Spin, Tooltip } from "antd";
import {
  CaretDownOutlined,
  CaretRightOutlined,
  ExportOutlined,
  FileExcelOutlined,
  FileImageOutlined,
  FileMarkdownOutlined,
  FileOutlined,
  FilePdfOutlined,
  FileTextOutlined,
  FileWordOutlined,
  FolderFilled,
  FolderOpenOutlined,
  FolderOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import type { FolderEntry } from "../api/types";
import { formatSize, previewKind, TEXT_PREVIEW_LIMIT, treeRows, type PreviewKind } from "../view/workspace-files";
import { useApi } from "../hooks/data";
import { DocxPreview } from "./DocxPreview";
import { Markdown } from "./Markdown";

type Preview =
  | { state: "idle" }
  | { state: "loading" }
  | { state: "error"; message: string }
  | { state: "none"; name: string }
  | { state: "text"; kind: "markdown" | "text"; text: string; truncated: boolean }
  | { state: "blob"; kind: "docx" | "image" | "pdf"; url: string };

function iconOf(entry: FolderEntry, open: boolean): React.ReactNode {
  if (entry.dir) return open ? <FolderOpenOutlined /> : <FolderFilled />;
  const kind: PreviewKind = previewKind(entry.name);
  const ext = entry.name.slice(entry.name.lastIndexOf(".") + 1).toLowerCase();
  if (kind === "markdown") return <FileMarkdownOutlined />;
  if (kind === "docx") return <FileWordOutlined />;
  if (kind === "image") return <FileImageOutlined />;
  if (kind === "pdf") return <FilePdfOutlined />;
  if (ext === "xlsx" || ext === "xls") return <FileExcelOutlined />;
  if (kind === "text") return <FileTextOutlined />;
  return <FileOutlined />;
}

export function WorkspaceFiles({ sessionId }: { sessionId: string }) {
  const api = useApi();
  const { message } = AntApp.useApp();
  /** 已经加载的各层目录：文件夹路径（根是空串）→ 这一层的项 */
  const [children, setChildren] = useState<Record<string, FolderEntry[]>>({});
  const [truncated, setTruncated] = useState<Set<string>>(new Set());
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState<Set<string>>(new Set());
  const [error, setError] = useState<string>();
  const [selected, setSelected] = useState<FolderEntry>();
  const [preview, setPreview] = useState<Preview>({ state: "idle" });
  const blobUrl = useRef<string | undefined>(undefined);
  /** 防止慢的旧请求盖掉新的选择 */
  const pickSeq = useRef(0);

  const releaseBlob = () => {
    if (blobUrl.current) URL.revokeObjectURL(blobUrl.current);
    blobUrl.current = undefined;
  };

  const load = useCallback(
    async (dir: string) => {
      setLoading((prev) => new Set(prev).add(dir));
      try {
        const listing = await api.browseWorkspace(sessionId, dir);
        setChildren((prev) => ({ ...prev, [dir]: listing.entries }));
        setTruncated((prev) => {
          const next = new Set(prev);
          if (listing.truncated) next.add(dir);
          else next.delete(dir);
          return next;
        });
        setError(undefined);
      } catch (e) {
        setError(e instanceof Error ? e.message : "读取失败");
      } finally {
        setLoading((prev) => {
          const next = new Set(prev);
          next.delete(dir);
          return next;
        });
      }
    },
    [api, sessionId],
  );

  // 换会话（换工作区）：清空重来
  useEffect(() => {
    pickSeq.current += 1;
    releaseBlob();
    setChildren({});
    setTruncated(new Set());
    setExpanded(new Set());
    setSelected(undefined);
    setPreview({ state: "idle" });
    void load("");
    return releaseBlob;
  }, [sessionId, load]);

  const toggle = (entry: FolderEntry) => {
    const open = expanded.has(entry.path);
    setExpanded((prev) => {
      const next = new Set(prev);
      if (open) next.delete(entry.path);
      else next.add(entry.path);
      return next;
    });
    if (!open && children[entry.path] === undefined) void load(entry.path);
  };

  const refresh = () => {
    // 已经看过的每一层都重读（工作区里的文件随时可能变）
    for (const dir of ["", ...expanded]) void load(dir);
    if (selected) void pick(selected);
  };

  const pick = async (entry: FolderEntry) => {
    const seq = ++pickSeq.current;
    setSelected(entry);
    releaseBlob();
    const kind = previewKind(entry.name);
    if (kind === "none") {
      setPreview({ state: "none", name: entry.name });
      return;
    }
    setPreview({ state: "loading" });
    try {
      const blob = await api.readWorkspaceFile(sessionId, entry.path);
      if (seq !== pickSeq.current) return;
      if (kind === "markdown" || kind === "text") {
        const text = await blob.text();
        if (seq !== pickSeq.current) return;
        setPreview({ state: "text", kind, text: text.slice(0, TEXT_PREVIEW_LIMIT), truncated: text.length > TEXT_PREVIEW_LIMIT });
        return;
      }
      const url = URL.createObjectURL(blob);
      blobUrl.current = url;
      setPreview({ state: "blob", kind, url });
    } catch (e) {
      if (seq === pickSeq.current) setPreview({ state: "error", message: e instanceof Error ? e.message : "读取失败" });
    }
  };

  const open = async (path: string, mode: "default" | "reveal") => {
    const res = await api.openWorkspacePath(sessionId, path, mode);
    if (!res.ok) message.error(res.message);
  };

  const rows = treeRows(children, expanded);
  const rootLoading = loading.has("") && children[""] === undefined;

  return (
    <div className="wf">
      <div className="wf-bar">
        <span className="wf-title">工作区文件</span>
        <span className="wf-bar-actions">
          <Tooltip title="重新读取">
            <Button type="text" size="small" icon={<ReloadOutlined />} aria-label="重新读取" onClick={refresh} />
          </Tooltip>
          <Tooltip title="在文件管理器里打开工作区">
            <Button type="text" size="small" icon={<FolderOutlined />} aria-label="打开工作区文件夹" onClick={() => void open("", "default")} />
          </Tooltip>
        </span>
      </div>
      {error && <Alert type="warning" showIcon message="读不了工作区文件" description={error} />}
      <div className="wf-tree" role="tree" aria-label="工作区文件">
        {rootLoading && (
          <div className="wf-empty">
            <Spin size="small" />
          </div>
        )}
        {!rootLoading && children[""] !== undefined && rows.length === 0 && <div className="wf-empty">工作区里还没有文件</div>}
        {rows.map(({ entry, depth, open: isOpen }) => (
          <div key={entry.path}>
            <button
              type="button"
              role="treeitem"
              aria-expanded={entry.dir ? isOpen : undefined}
              className={`wf-row ${selected?.path === entry.path ? "selected" : ""}`}
              style={{ paddingLeft: 8 + depth * 16 }}
              onClick={() => (entry.dir ? toggle(entry) : void pick(entry))}
              onDoubleClick={() => !entry.dir && void open(entry.path, "default")}
              title={entry.dir ? entry.path : `${entry.path}（双击用本机应用打开）`}
            >
              <span className="wf-caret">{entry.dir ? isOpen ? <CaretDownOutlined /> : <CaretRightOutlined /> : null}</span>
              <span className="wf-icon">{iconOf(entry, isOpen)}</span>
              <span className="wf-name">{entry.name}</span>
              {!entry.dir && <span className="wf-size">{formatSize(entry.size)}</span>}
            </button>
            {entry.dir && isOpen && loading.has(entry.path) && children[entry.path] === undefined && (
              <div className="wf-empty" style={{ paddingLeft: 24 + depth * 16 }}>
                <Spin size="small" />
              </div>
            )}
            {entry.dir && isOpen && children[entry.path]?.length === 0 && (
              <div className="wf-empty" style={{ paddingLeft: 24 + depth * 16 }}>
                空文件夹
              </div>
            )}
            {entry.dir && isOpen && truncated.has(entry.path) && (
              <div className="wf-empty" style={{ paddingLeft: 24 + depth * 16 }}>
                只列出前 200 项
              </div>
            )}
          </div>
        ))}
        {truncated.has("") && <div className="wf-empty">只列出前 200 项</div>}
      </div>
      <div className="wf-preview">
        {preview.state === "idle" && <div className="wf-empty">点一个文件预览；Excel 等不能预览的双击用本机应用打开。</div>}
        {selected && !selected.dir && (
          <div className="wf-preview-head">
            <span className="wf-preview-name" title={selected.path}>
              {selected.path}
            </span>
            <Button size="small" icon={<ExportOutlined />} onClick={() => void open(selected.path, "default")}>
              用本机应用打开
            </Button>
            <Button size="small" onClick={() => void open(selected.path, "reveal")}>
              在文件夹中显示
            </Button>
          </div>
        )}
        {preview.state === "loading" && (
          <div className="wf-empty">
            <Spin />
          </div>
        )}
        {preview.state === "error" && <Alert type="warning" showIcon message="预览不可用" description={preview.message} />}
        {preview.state === "none" && <Alert type="info" showIcon message={`「${preview.name}」不能在这里预览`} description="请用上面的“用本机应用打开”。" />}
        {preview.state === "text" && preview.kind === "markdown" && (
          <div className="md-preview">
            <Markdown text={preview.text} />
          </div>
        )}
        {preview.state === "text" && preview.kind === "text" && <pre className="wf-text">{preview.text}</pre>}
        {preview.state === "text" && preview.truncated && <div className="wf-empty">内容太长，只显示了前面一部分，完整内容请用本机应用打开。</div>}
        {preview.state === "blob" && preview.kind === "image" && <img className="wf-image" src={preview.url} alt={selected?.name ?? ""} />}
        {preview.state === "blob" && preview.kind === "pdf" && <iframe className="wf-pdf" src={preview.url} title={selected?.name ?? "PDF"} />}
        {preview.state === "blob" && preview.kind === "docx" && <DocxPreview key={preview.url} url={preview.url} reflow />}
      </div>
    </div>
  );
}
