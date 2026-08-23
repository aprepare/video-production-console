import { RotateCcw, X } from "lucide-react";
import { useMemo, useState } from "react";
import { assetLabels } from "./asset-labels";
import type { AssetPreview } from "./asset-labels";

type AssetPreviewDialogProps = {
  preview: AssetPreview;
  draft: string;
  onDraftChange: (value: string) => void;
  saving: boolean;
  onClose: () => void;
  onSave: (content: string) => void;
  onRevise: () => void;
};

type KeywordMark = { text: string; kind: "warning" | "number" | string };
type KeywordLine = { line: string; keywords?: KeywordMark[] };
type KeywordDoc = { schema_version?: number; lines?: KeywordLine[] };

const MAX_KEYWORDS_PER_LINE = 2;

export function parseCaptionKeywords(raw: string | undefined): KeywordDoc | null {
  if (!raw?.trim()) return null;
  try {
    const parsed = JSON.parse(raw) as KeywordDoc;
    if (!Array.isArray(parsed.lines)) return null;
    return parsed;
  } catch {
    return null;
  }
}

export function serializeCaptionKeywords(doc: KeywordDoc): string {
  return `${JSON.stringify({
    schema_version: doc.schema_version || 1,
    lines: (doc.lines || []).map((entry) => ({
      line: entry.line,
      keywords: (entry.keywords || []).map((keyword) => ({
        text: keyword.text,
        kind: keyword.kind === "number" ? "number" : "warning",
      })),
    })),
  }, null, 2)}\n`;
}

export function inferKeywordKind(text: string): "warning" | "number" {
  return /[0-9%％]/.test(text) ? "number" : "warning";
}

function normalizeKeywordDoc(doc: KeywordDoc | null) {
  return {
    schema_version: doc?.schema_version || 1,
    lines: (doc?.lines || []).map((entry) => ({
      line: entry.line,
      keywords: (entry.keywords || []).map((keyword) => ({
        text: keyword.text,
        kind: keyword.kind === "number" ? "number" : "warning",
      })),
    })),
  };
}

function highlightLine(line: string, keywords: KeywordMark[] | undefined, onRemove?: (text: string) => void) {
  const marks = (keywords || [])
    .filter((item) => item.text && line.includes(item.text))
    .sort((left, right) => right.text.length - left.text.length);
  if (!marks.length) return line;

  const nodes: Array<string | { text: string; kind: string }> = [line];
  for (const mark of marks) {
    const next: Array<string | { text: string; kind: string }> = [];
    for (const node of nodes) {
      if (typeof node !== "string") {
        next.push(node);
        continue;
      }
      let rest = node;
      let hit = rest.indexOf(mark.text);
      if (hit < 0) {
        next.push(rest);
        continue;
      }
      while (hit >= 0) {
        if (hit > 0) next.push(rest.slice(0, hit));
        next.push({ text: mark.text, kind: mark.kind === "number" ? "number" : "warning" });
        rest = rest.slice(hit + mark.text.length);
        hit = rest.indexOf(mark.text);
      }
      if (rest) next.push(rest);
    }
    nodes.splice(0, nodes.length, ...next);
  }
  return nodes.map((node, index) => (
    typeof node === "string"
      ? <span key={index}>{node}</span>
      : (
        <mark
          key={index}
          className={`caption-keyword caption-keyword--${node.kind}${onRemove ? " is-editable" : ""}`}
          onClick={onRemove ? () => onRemove(node.text) : undefined}
          title={onRemove ? "点击取消这个标注" : undefined}
        >
          {node.text}
        </mark>
      )
  ));
}

function CaptionKeywordsEditor({
  text,
  draft,
  onDraftChange,
  saving,
  onSave,
}: {
  text: string;
  draft: string;
  onDraftChange: (value: string) => void;
  saving: boolean;
  onSave: (content: string) => void;
}) {
  const source = draft || text;
  const doc = parseCaptionKeywords(source);
  const [draftText, setDraftText] = useState<Record<number, string>>({});
  const [error, setError] = useState("");
  const dirty = useMemo(() => {
    const current = parseCaptionKeywords(source);
    const original = parseCaptionKeywords(text);
    return JSON.stringify(normalizeKeywordDoc(current)) !== JSON.stringify(normalizeKeywordDoc(original));
  }, [source, text]);

  if (!doc) return <pre className="asset-text">{source}</pre>;

  const update = (next: KeywordDoc) => {
    setError("");
    onDraftChange(serializeCaptionKeywords(next));
  };

  const removeKeyword = (lineIndex: number, textValue: string) => {
    const next: KeywordDoc = {
      schema_version: doc.schema_version || 1,
      lines: (doc.lines || []).map((entry, index) => (
        index === lineIndex
          ? { ...entry, keywords: (entry.keywords || []).filter((item) => item.text !== textValue) }
          : entry
      )),
    };
    update(next);
  };

  const addKeyword = (lineIndex: number) => {
    const entry = doc.lines?.[lineIndex];
    if (!entry) return;
    const value = (draftText[lineIndex] || "").trim();
    if (!value) {
      setError("先输入要标注的词。");
      return;
    }
    if (!entry.line.includes(value)) {
      setError(`「${value}」不在这行口播里。`);
      return;
    }
    const current = entry.keywords || [];
    if (current.some((item) => item.text === value)) {
      setError("这行已经标过这个词。");
      return;
    }
    if (current.length >= MAX_KEYWORDS_PER_LINE) {
      setError("每行最多标 2 个词。");
      return;
    }
    const next: KeywordDoc = {
      schema_version: doc.schema_version || 1,
      lines: (doc.lines || []).map((item, index) => (
        index === lineIndex
          ? { ...item, keywords: [...current, { text: value, kind: inferKeywordKind(value) }] }
          : item
      )),
    };
    setDraftText((currentDraft) => ({ ...currentDraft, [lineIndex]: "" }));
    update(next);
  };

  const marked = doc.lines?.filter((line) => (line.keywords || []).length > 0).length || 0;
  const total = doc.lines?.length || 0;
  return (
    <div className="caption-keywords-preview">
      <p className="caption-keywords-preview__summary">
        共 {total} 行，已标注 {marked} 行。点红/金词可取消，输入框可加词，每行最多 2 个。改完点保存。
      </p>
      <ol className="caption-keywords-preview__lines">
        {(doc.lines || []).map((entry, index) => (
          <li key={`${index}-${entry.line}`} className={(entry.keywords || []).length ? "is-marked" : "is-plain"}>
            <span className="caption-keywords-preview__index">{index + 1}</span>
            <p className="caption-keywords-preview__text">{highlightLine(entry.line, entry.keywords, (value) => removeKeyword(index, value))}</p>
            <div className="caption-keywords-preview__edit">
              {(entry.keywords || []).length ? (
                <ul className="caption-keywords-preview__tags">
                  {(entry.keywords || []).map((keyword, tagIndex) => (
                    <li key={`${keyword.text}-${tagIndex}`} className={`caption-keyword-tag caption-keyword-tag--${keyword.kind === "number" ? "number" : "warning"}`}>
                      {keyword.text}
                      <span>{keyword.kind === "number" ? "数字" : "警示"}</span>
                      <button type="button" aria-label={`取消标注${keyword.text}`} onClick={() => removeKeyword(index, keyword.text)}>
                        ×
                      </button>
                    </li>
                  ))}
                </ul>
              ) : null}
              {(entry.keywords || []).length < MAX_KEYWORDS_PER_LINE ? (
                <div className="caption-keywords-preview__add">
                  <input
                    aria-label={`第 ${index + 1} 行新增关键词`}
                    value={draftText[index] || ""}
                    placeholder="输入这行里要标的词"
                    onChange={(event) => setDraftText((currentDraft) => ({ ...currentDraft, [index]: event.target.value }))}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") {
                        event.preventDefault();
                        addKeyword(index);
                      }
                    }}
                  />
                  <button type="button" onClick={() => addKeyword(index)}>添加</button>
                </div>
              ) : null}
            </div>
          </li>
        ))}
      </ol>
      {error ? <p className="caption-keywords-preview__error" role="alert">{error}</p> : null}
      <div className="asset-editor-actions">
        <button
          type="button"
          className="asset-editor-save"
          onClick={() => onSave(serializeCaptionKeywords(doc).trim())}
          disabled={saving || !dirty}
          aria-busy={saving}
        >
          {saving ? "正在保存…" : "保存修改"}
        </button>
      </div>
    </div>
  );
}

export function AssetPreviewDialog({
  preview,
  draft,
  onDraftChange,
  saving,
  onClose,
  onSave,
  onRevise,
}: AssetPreviewDialogProps) {
  return (
    <div className="modal-backdrop" onClick={onClose}>
      <section
        className="preview-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="preview-dialog-title"
        tabIndex={-1}
        onClick={(event) => event.stopPropagation()}
      >
        <div className="modal-head">
          <div>
            <span className="muted">{assetLabels[preview.asset.type] || preview.asset.type}</span>
            <h2 id="preview-dialog-title">{preview.asset.filename}</h2>
          </div>
          <button className="close" aria-label="关闭素材预览" onClick={onClose}>
            <X size={20} aria-hidden="true" />
          </button>
        </div>
        {preview.asset.type === "continuous_script" ? (
          <>
            <textarea
              className="asset-editor"
              aria-label="连续文案正文"
              value={draft}
              onChange={(event) => onDraftChange(event.target.value)}
            />
            <div className="asset-editor-actions">
              <button
                type="button"
                className="asset-editor-save"
                onClick={() => onSave(draft.trim())}
                disabled={!draft.trim() || draft === preview.text || saving}
                aria-busy={saving}
              >
                {saving ? "正在保存…" : "保存修改"}
              </button>
              <button type="button" className="asset-editor-revise" onClick={onRevise}>
                <RotateCcw size={15} aria-hidden="true" />
                打回重做
              </button>
            </div>
          </>
        ) : preview.asset.type === "caption_keywords" ? (
          <CaptionKeywordsEditor
            text={preview.text || ""}
            draft={draft}
            onDraftChange={onDraftChange}
            saving={saving}
            onSave={onSave}
          />
        ) : (
          <pre className="asset-text">{preview.text}</pre>
        )}
      </section>
    </div>
  );
}
