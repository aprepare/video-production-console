import { X } from "lucide-react";
import { TaskModelFields } from "../TaskModelFields";
import type { TaskModelOverride } from "../taskModel";
import type { PublicSettings } from "../types";

type ReviseDialogProps = {
  notes: string;
  onNotesChange: (value: string) => void;
  taskModel: TaskModelOverride;
  onTaskModelChange: (value: TaskModelOverride) => void;
  taskModelDefaults: PublicSettings | undefined;
  models?: readonly string[];
  efforts?: readonly string[];
  submitDisabled: boolean;
  submitting: boolean;
  onClose: () => void;
  onSubmit: (notes: string) => void;
};

export function ReviseDialog({
  notes,
  onNotesChange,
  taskModel,
  onTaskModelChange,
  taskModelDefaults,
  models,
  efforts,
  submitDisabled,
  submitting,
  onClose,
  onSubmit,
}: ReviseDialogProps) {
  return (
    <div className="modal-backdrop" onClick={onClose}>
      <section
        className="preview-modal revise-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="revise-dialog-title"
        tabIndex={-1}
        onClick={(event) => event.stopPropagation()}
      >
        <div className="modal-head">
          <div>
            <span className="muted">连续文案</span>
            <h2 id="revise-dialog-title">打回重做</h2>
          </div>
          <button className="close" aria-label="关闭打回重做" onClick={onClose}>
            <X size={20} aria-hidden="true" />
          </button>
        </div>
        <label className="revise-modal__notes">
          修改要求
          <textarea
            aria-label="二创修改要求"
            value={notes}
            onChange={(event) => onNotesChange(event.target.value)}
            placeholder="例如：开场更口语、缩短前 20 秒、少用排比…"
          />
        </label>
        <TaskModelFields
          value={taskModel}
          onChange={onTaskModelChange}
          defaults={taskModelDefaults}
          labelPrefix="打回"
          purpose="remix"
          models={models}
          efforts={efforts}
        />
        <button
          type="button"
          className="revise-modal__submit"
          onClick={() => onSubmit(notes)}
          disabled={submitDisabled}
          aria-busy={submitting}
        >
          {submitting ? "正在打回重做…" : "打回重做"}
        </button>
      </section>
    </div>
  );
}
