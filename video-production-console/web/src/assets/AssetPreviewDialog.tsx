import { RotateCcw, X } from "lucide-react";
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

function highlightLine(line: string, keywords: KeywordMark[] | undefined) {
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
      : <mark key={index} className={`caption-keyword caption-keyword--${node.kind}`}>{node.text}</mark>
  ));
}

function CaptionKeywordsPreview({ text }: { text: string }) {
  const doc = parseCaptionKeywords(text);
  if (!doc) return <pre className="asset-text">{text}</pre>;
  const marked = doc.lines?.filter((line) => (line.keywords || []).length > 0).length || 0;
  const total = doc.lines?.length || 0;
  return (
    <div className="caption-keywords-preview">
      <p className="caption-keywords-preview__summary">
        共 {total} 行，已标注 {marked} 行。红底是警示词，金底是数字。
      </p>
      <ol className="caption-keywords-preview__lines">
        {(doc.lines || []).map((entry, index) => (
          <li key={`${index}-${entry.line}`} className={(entry.keywords || []).length ? "is-marked" : "is-plain"}>
            <span className="caption-keywords-preview__index">{index + 1}</span>
            <p className="caption-keywords-preview__text">{highlightLine(entry.line, entry.keywords)}</p>
            {(entry.keywords || []).length ? (
              <ul className="caption-keywords-preview__tags">
                {(entry.keywords || []).map((keyword, tagIndex) => (
                  <li key={`${keyword.text}-${tagIndex}`} className={`caption-keyword-tag caption-keyword-tag--${keyword.kind === "number" ? "number" : "warning"}`}>
                    {keyword.text}
                    <span>{keyword.kind === "number" ? "数字" : "警示"}</span>
                  </li>
                ))}
              </ul>
            ) : null}
          </li>
        ))}
      </ol>
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
          <CaptionKeywordsPreview text={preview.text || ""} />
        ) : (
          <pre className="asset-text">{preview.text}</pre>
        )}
      </section>
    </div>
  );
}
