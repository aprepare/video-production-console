import type { ReasoningEffort, TaskModelDefaults, TaskModelOverride, TaskModelPurpose } from "./taskModel";
import { inheritedTaskModel, reasoningEfforts } from "./taskModel";

export function TaskModelFields({
  value,
  onChange,
  defaults,
  labelPrefix = "",
  hideReasoningEffort = false,
  purpose = "codex",
}: {
  value: TaskModelOverride;
  onChange: (value: TaskModelOverride) => void;
  defaults?: TaskModelDefaults;
  labelPrefix?: string;
  hideReasoningEffort?: boolean;
  purpose?: TaskModelPurpose;
}) {
  const actualModel = value.model.trim() || inheritedTaskModel(defaults, purpose);
  const actualEffort =
    value.reasoningEffort || defaults?.codex_default_reasoning_effort || "Codex 默认强度";
  return (
    <details className="task-model-fields">
      <summary>{hideReasoningEffort ? "模型（可选）" : "模型与推理强度（可选）"}</summary>
      <div className="task-model-grid">
        <label>
          模型
          <input
            aria-label={`${labelPrefix}临时模型`}
            value={value.model}
            placeholder="继承默认模型"
            onChange={(event) => onChange({ ...value, model: event.target.value })}
          />
        </label>
        {hideReasoningEffort ? null : (
          <label>
            推理强度
            <select
              aria-label={`${labelPrefix}临时推理强度`}
              value={value.reasoningEffort}
              onChange={(event) =>
                onChange({
                  ...value,
                  reasoningEffort: event.target.value as ReasoningEffort | "",
                })
              }
            >
              <option value="">继承默认强度</option>
              {reasoningEfforts.map((effort) => (
                <option key={effort} value={effort}>
                  {effort}
                </option>
              ))}
            </select>
          </label>
        )}
      </div>
      <p>
        实际将使用：{hideReasoningEffort ? actualModel : `${actualModel} · ${actualEffort}`}
      </p>
    </details>
  );
}
