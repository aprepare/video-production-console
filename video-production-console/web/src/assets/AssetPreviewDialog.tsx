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
        ) : (
          <pre className="asset-text">{preview.text}</pre>
        )}
      </section>
    </div>
  );
}
