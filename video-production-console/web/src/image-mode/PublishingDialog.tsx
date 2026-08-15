import { useEffect, useRef, useState } from "react";
import { Copy, Save, X } from "lucide-react";
import type { PublishingCandidate } from "../types";

type API = (path: string, init?: RequestInit) => Promise<Response>;

export type PublishingDetailSource = {
  project?: { publishing_candidates?: PublishingCandidate[]; selected_position?: number };
  publishing_candidates?: PublishingCandidate[];
  selected_position?: number;
  publishing?: { current?: PublishingCandidate; all?: PublishingCandidate[] };
};

export function publishingCandidatesFrom(detail: PublishingDetailSource): PublishingCandidate[] {
  return detail.project?.publishing_candidates || detail.publishing_candidates || detail.publishing?.all || [];
}

export function selectedPublishingFrom(detail: PublishingDetailSource, fallback = 1): number {
  return detail.project?.selected_position || detail.selected_position || detail.publishing?.current?.position || fallback;
}

export type PublishingDialogProps = {
  api: API;
  projectID: string;
  candidates: PublishingCandidate[];
  selected: number;
  publishingError?: string;
  onClose: () => void;
  onSaved: (candidates: PublishingCandidate[], selected: number) => void;
};

function runeCount(value: string) {
  return Array.from(value).length;
}

function excerpt(value: string) {
  const text = value.replace(/\s+/g, " ").trim();
  return text.length > 42 ? `${text.slice(0, 42)}…` : text;
}

export function PublishingDialog({
  api,
  projectID,
  candidates,
  selected,
  publishingError,
  onClose,
  onSaved,
}: PublishingDialogProps) {
  const [items, setItems] = useState(candidates);
  const [position, setPosition] = useState(selected);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const closeRef = useRef<HTMLButtonElement | null>(null);
  const current = items.find((item) => item.position === position) || items[0];

  useEffect(() => {
    setItems(candidates);
  }, [candidates]);

  useEffect(() => {
    setPosition(selected);
  }, [selected]);

  useEffect(() => {
    closeRef.current?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  const update = (key: "title" | "description", value: string) => {
    if (!current) return;
    setItems((all) => all.map((item) => item.position === current.position ? { ...item, [key]: value } : item));
  };

  const copy = async (value: string) => {
    await navigator.clipboard?.writeText(value);
  };

  const save = async () => {
    if (!current) return;
    if (runeCount(current.title) > 22 || runeCount(current.description) > 1000) return;
    setBusy(true);
    setError("");
    try {
      const response = await api(`/api/image-projects/${projectID}/publishing-candidates/${current.position}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title: current.title, description: current.description }),
      });
      if (!response.ok) throw new Error("patch failed");
      const selectedResponse = await api(`/api/image-projects/${projectID}/publishing-candidates/select`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ position: current.position }),
      });
      if (!selectedResponse.ok) throw new Error("select failed");
      onSaved(items, current.position);
      onClose();
    } catch {
      setError("保存候选失败，请稍后重试");
    } finally {
      setBusy(false);
    }
  };

  const regenerate = async () => {
    setBusy(true);
    setError("");
    try {
      const response = await api(`/api/image-projects/${projectID}/publishing-candidates/generate`, { method: "POST" });
      if (!response.ok) throw new Error("generate failed");
      const data = await response.json() as PublishingDetailSource;
      const next = publishingCandidatesFrom(data);
      setItems(next);
      if (next[0]) setPosition(selectedPublishingFrom(data, next[0].position));
      onSaved(next, selectedPublishingFrom(data, next[0]?.position || 1));
    } catch {
      setError("生成候选失败，请稍后重试");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="image-preview-backdrop" onClick={onClose}>
      <div
        className="image-preview-dialog publishing-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="publishing-dialog-title"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="publishing-dialog-head">
          <h2 id="publishing-dialog-title">图文标题及描述</h2>
          <button ref={closeRef} type="button" onClick={onClose} aria-label="关闭">
            <X size={18} aria-hidden="true" />
            关闭
          </button>
        </div>
        {!current ? (
          <div className="publishing-empty">
            <p className="image-kicker">发布文案</p>
            {publishingError ? <p role="alert">{publishingError}</p> : <p>暂无候选文案</p>}
            <p className="publishing-empty__hint">一次生成 5 条标题和描述。</p>
            {error ? <p role="alert">{error}</p> : null}
            <button type="button" className="primary" disabled={busy} onClick={() => void regenerate()}>重新生成发布文案</button>
          </div>
        ) : (
          <>
            <div className="publishing-dialog-body">
              <div className="publishing-candidate-list" role="radiogroup" aria-label="发布候选">
                {items.map((item) => (
                  <label key={item.position} className={item.position === position ? "is-selected" : undefined}>
                    <input type="radio" name="publishing" checked={item.position === position} onChange={() => setPosition(item.position)} />
                    <span>
                      <strong>候选 {item.position}</strong>
                      <em>{item.title}</em>
                      <small>{excerpt(item.description)}</small>
                    </span>
                  </label>
                ))}
              </div>
              <div className="publishing-candidate-editor">
                <label>
                  标题
                  <textarea aria-label="标题" rows={2} value={current.title} onChange={(event) => update("title", event.target.value)} />
                  <small>{runeCount(current.title)}/22</small>
                </label>
                <label>
                  描述
                  <textarea aria-label="描述" rows={10} value={current.description} onChange={(event) => update("description", event.target.value)} />
                  <small>{runeCount(current.description)}/1000</small>
                </label>
              </div>
            </div>
            {error ? <p role="alert">{error}</p> : null}
            <div className="publishing-dialog-footer">
              <button type="button" onClick={() => void copy(current.title)}>
                <Copy size={16} aria-hidden="true" />
                复制标题
              </button>
              <button type="button" onClick={() => void copy(current.description)}>
                <Copy size={16} aria-hidden="true" />
                复制描述
              </button>
              <button type="button" onClick={() => void copy(`${current.title}\n${current.description}`)}>
                <Copy size={16} aria-hidden="true" />
                复制全部
              </button>
              <button
                type="button"
                className="primary"
                disabled={busy || runeCount(current.title) > 22 || runeCount(current.description) > 1000}
                onClick={() => void save()}
              >
                <Save size={16} aria-hidden="true" />
                保存当前候选
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
