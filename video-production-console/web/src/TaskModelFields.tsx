import { ModelSelect } from "./ModelSelect";
import type { ReasoningEffort, TaskModelDefaults, TaskModelOverride, TaskModelPurpose } from "./taskModel";
import { inheritedTaskEffort, inheritedTaskModel, resolveSelectableEfforts } from "./taskModel";

export function TaskModelFields({
  value,
  onChange,
  defaults,
  labelPrefix = "",
  hideReasoningEffort = false,
  purpose = "codex",
  models,
  efforts,
}: {
  value: TaskModelOverride;
  onChange: (value: TaskModelOverride) => void;
  defaults?: TaskModelDefaults;
  labelPrefix?: string;
  hideReasoningEffort?: boolean;
  purpose?: TaskModelPurpose;
  models?: readonly string[];
  efforts?: readonly string[];
}) {
  const actualModel = value.model.trim() || inheritedTaskModel(defaults, purpose);
  const inheritedEffort = inheritedTaskEffort(defaults, purpose);
  const actualEffort = value.reasoningEffort || inheritedEffort;
  const emptyEffortLabel = purpose === "remix" ? `跟随设置（${inheritedEffort}）` : "继承默认强度";
  const effortOptions = resolveSelectableEfforts(efforts);
  return (
    <details className="task-model-fields">
      <summary>{hideReasoningEffort ? "模型（可选）" : "模型与推理强度（可选）"}</summary>
      <div className="task-model-grid">
        <label>
          模型
          <ModelSelect
            aria-label={`${labelPrefix}临时模型`}
            value={value.model}
            emptyLabel="继承默认模型"
            models={models}
            onChange={(model) => onChange({ ...value, model })}
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
              <option value="">{emptyEffortLabel}</option>
              {effortOptions.map((effort) => (
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
