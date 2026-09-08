import { useEffect, useMemo, useRef, useState } from "react";
import { BookOpen, Check, Copy, FolderOpen, Pencil, RefreshCw, Save } from "lucide-react";
import {
  defaultWorkspaceFile,
  fetchWorkspaceFile,
  fetchWorkspaceTree,
  saveWorkspaceFile,
  type WorkspaceApi,
  type WorkspaceEntry,
  type WorkspaceFile,
  type WorkspaceTree,
} from "./api";
import { MarkdownView } from "./markdown";
import "./workspace.css";

type Props = {
  api: WorkspaceApi;
  file?: string;
  onNavigate: (href: string) => void;
};

function splitFrontmatter(content: string): { meta: string; body: string } {
  const trimmed = content.replace(/^\uFEFF/, "");
  if (!trimmed.startsWith("---")) return { meta: "", body: trimmed };
  const end = trimmed.indexOf("\n---", 3);
  if (end < 0) return { meta: "", body: trimmed };
  return { meta: trimmed.slice(3, end).trim(), body: trimmed.slice(end + 4).replace(/^\n/, "") };
}

export function WorkspacePage({ api, file, onNavigate }: Props) {
  const [tree, setTree] = useState<WorkspaceTree | null>(null);
  const [current, setCurrent] = useState<WorkspaceFile | null>(null);
  const [draft, setDraft] = useState("");
  const [editing, setEditing] = useState(false);
  const [message, setMessage] = useState("");
  const [copied, setCopied] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const currentRef = useRef<WorkspaceFile | null>(null);
  const draftRef = useRef("");
  const draftVersionRef = useRef(0);
  const fileRequestRef = useRef(0);
  const treeRequestRef = useRef(0);
  const routeVersionRef = useRef(0);
  const routeLoadingRef = useRef(false);
  const savingRef = useRef(false);
  const dirty = current !== null && draft !== current.content;

  const loadTree = async () => {
    const request = ++treeRequestRef.current;
    const next = await fetchWorkspaceTree(api);
    if (request === treeRequestRef.current) setTree(next);
    return next;
  };

  const openFile = async (path: string) => {
    const request = ++fileRequestRef.current;
    const draftVersion = draftVersionRef.current;
    const next = await fetchWorkspaceFile(api, path);
    if (request !== fileRequestRef.current || draftVersion !== draftVersionRef.current) return;
    currentRef.current = next;
    draftRef.current = next.content;
    draftVersionRef.current += 1;
    setCurrent(next);
    setDraft(next.content);
    setEditing(false);
  };

  useEffect(() => {
    let cancelled = false;
    const routeVersion = ++routeVersionRef.current;
    routeLoadingRef.current = true;
    fileRequestRef.current += 1;
    (async () => {
      try {
        const nextTree = await loadTree();
        if (cancelled || routeVersion !== routeVersionRef.current) return;
        const target = file || defaultWorkspaceFile(nextTree);
        if (target) await openFile(target);
      } catch (error) {
        if (!cancelled) setMessage(error instanceof Error ? error.message : "工作区读取失败。");
      } finally {
        if (!cancelled && routeVersion === routeVersionRef.current) {
          routeLoadingRef.current = false;
          setLoading(false);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [api, file]);

  useEffect(() => {
    const onFocus = () => {
      if (dirty || savingRef.current || routeLoadingRef.current) return;
      if (file && current?.path !== file) return;
      void loadTree().catch(() => undefined);
      if (current) void openFile(current.path).catch(() => undefined);
    };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [current, dirty, file]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") {
        event.preventDefault();
        if (dirty) void save();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  const { meta, body } = useMemo(() => splitFrontmatter(draft), [draft]);

  const select = (path: string) => {
    if (dirty && !window.confirm("这篇还没保存，离开会丢掉修改。")) return;
    onNavigate(`/workspace?file=${encodeURIComponent(path)}`);
  };

  const save = async () => {
    if (!current || savingRef.current) return;
    const path = current.path;
    const content = draft;
    const revision = current.revision;
    const draftVersion = draftVersionRef.current;
    const routeVersion = routeVersionRef.current;
    savingRef.current = true;
    setSaving(true);
    try {
      const next = await saveWorkspaceFile(api, path, content, revision);
      if (routeVersion !== routeVersionRef.current || currentRef.current?.path !== path) return;
      currentRef.current = next;
      setCurrent(next);
      if (draftVersion === draftVersionRef.current && draftRef.current === content) {
        draftRef.current = next.content;
        draftVersionRef.current += 1;
        setDraft(next.content);
      }
      setMessage("已写回工作区文件。");
    } catch (error) {
      if (routeVersion === routeVersionRef.current && currentRef.current?.path === path) {
        setMessage(error instanceof Error ? error.message : "保存失败。");
      }
    } finally {
      savingRef.current = false;
      setSaving(false);
    }
  };

  const updateDraft = (value: string) => {
    draftRef.current = value;
    draftVersionRef.current += 1;
    setDraft(value);
  };

  const copy = async (label: string, text: string) => {
    await navigator.clipboard.writeText(text);
    setCopied(label);
    window.setTimeout(() => setCopied(""), 1500);
  };

  return (
    <div className="workspace-page">
      <header>
        <div className="workspace-brand">
          <span className="workspace-mark" aria-hidden="true"><BookOpen size={18} /></span>
          <div>
            <span className="eyebrow">二创工作区</span>
            <h1>文档柜</h1>
            <p>网页上看到的就是磁盘上的文件，改完保存会写回去。</p>
          </div>
        </div>
        <div className="workspace-actions">
          <button type="button" className="workspace-btn" onClick={() => void loadTree()} title="刷新目录">
            <RefreshCw size={15} />刷新
          </button>
          <button type="button" className="workspace-btn" disabled={!current} onClick={() => void copy("全文", draft)}>
            <Copy size={15} />{copied === "全文" ? "已复制" : "复制全文"}
          </button>
          <button type="button" className="workspace-btn" disabled={!current || !current.spoken_body} onClick={() => void copy("口播", current?.spoken_body ?? "")}>
            <Copy size={15} />{copied === "口播" ? "已复制" : "复制口播"}
          </button>
          <button type="button" className="workspace-btn" disabled={!current} onClick={() => setEditing((value) => !value)}>
            <Pencil size={15} />{editing ? "预览" : "编辑"}
          </button>
          <button type="button" className="workspace-btn workspace-btn--primary" disabled={!dirty || saving} onClick={() => void save()}>
            {dirty ? <Save size={15} /> : <Check size={15} />}{saving ? "保存中…" : dirty ? "保存" : "已同步"}
          </button>
        </div>
      </header>
      {message ? <div className="workspace-toast" role="status">{message}<button type="button" onClick={() => setMessage("")}>关闭</button></div> : null}
      <div className="workspace-body">
        <nav className="workspace-tree" aria-label="工作区目录">
          {loading && !tree ? <p className="workspace-muted">正在读取工作区…</p> : null}
          {tree ? <TreeList entries={tree.entries} current={current?.path ?? file} onSelect={select} /> : null}
        </nav>
        <section className="workspace-doc" aria-label="文档">
          {!current ? <p className="workspace-muted">从左边点一篇打开。</p> : editing ? (
            <textarea aria-label="编辑文档" value={draft} onChange={(event) => updateDraft(event.target.value)} />
          ) : (
            <>
              <h2 className="workspace-doc-title">{current.path}</h2>
              {meta ? <pre className="workspace-meta">{meta}</pre> : null}
              <MarkdownView text={body} />
            </>
          )}
        </section>
      </div>
    </div>
  );
}

function TreeList({ entries, current, onSelect }: { entries: WorkspaceEntry[]; current?: string; onSelect: (path: string) => void }) {
  return (
    <ul>
      {entries.map((entry) => (
        <li key={entry.path}>
          {entry.kind === "dir" ? (
            <details open={shouldOpen(entry, current)}>
              <summary><FolderOpen size={14} />{entry.name}</summary>
              {entry.children?.length ? <TreeList entries={entry.children} current={current} onSelect={onSelect} /> : <p className="workspace-muted">空目录</p>}
            </details>
          ) : (
            <button type="button" className={current === entry.path ? "is-active" : undefined} onClick={() => onSelect(entry.path)}>
              {entry.name}
            </button>
          )}
        </li>
      ))}
    </ul>
  );
}

function shouldOpen(entry: WorkspaceEntry, current?: string): boolean {
  if (!current) return entry.name === "项目" || entry.name === "经验库";
  return current === entry.path || current.startsWith(`${entry.path}/`);
}
