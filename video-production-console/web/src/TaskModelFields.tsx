import { ModelMultiSelect, ModelSelect } from "./ModelSelect";
import type { ReasoningEffort, TaskModelDefaults, TaskModelOverride, TaskModelPurpose } from "./taskModel";
import { inheritedTaskEffort, inheritedTaskModel, normalizeScriptCount, reasoningEfforts } from "./taskModel";

export function TaskModelFields({
  value,
  onChange,
  defaults,
  labelPrefix = "",
  hideReasoningEffort = false,
  purpose = "codex",
  multiModel = false,
}: {
  value: TaskModelOverride;
  onChange: (value: TaskModelOverride) => void;
  defaults?: TaskModelDefaults;
  labelPrefix?: string;
  hideReasoningEffort?: boolean;
  purpose?: TaskModelPurpose;
  multiModel?: boolean;
}) {
  const actualModel = value.model.trim() || inheritedTaskModel(defaults, purpose);
  const inheritedEffort = inheritedTaskEffort(defaults, purpose);
  const actualEffort = value.reasoningEffort || inheritedEffort;
  const emptyEffortLabel = purpose === "remix" ? `跟随设置（${inheritedEffort}）` : "继承默认强度";
  const selectedModels = value.models || [];
  const scriptCount = normalizeScriptCount(value.scriptCount);
  return (
    <details className="task-model-fields">
      <summary>
        {multiModel
          ? "模型与文案数量（可多选，同时生成多份）"
          : hideReasoningEffort
            ? "模型（可选）"
            : "模型与推理强度（可选）"}
      </summary>
      <div className="task-model-grid">
        {multiModel ? (
          <ModelMultiSelect
            value={selectedModels}
            onChange={(models) => onChange({ ...value, models })}
            inheritedLabel={actualModel}
          />
        ) : (
          <label>
            模型
            <ModelSelect
              aria-label={`${labelPrefix}临时模型`}
              value={value.model}
              emptyLabel="继承默认模型"
              onChange={(model) => onChange({ ...value, model })}
            />
          </label>
        )}
        {multiModel ? (
          <label>
            每模型文案数量
            <select
              aria-label={`${labelPrefix}每模型文案数量`}
              value={scriptCount}
              onChange={(event) =>
                onChange({
                  ...value,
                  scriptCount: normalizeScriptCount(Number(event.target.value)),
                })
              }
            >
              {[1, 2, 3, 4, 5].map((n) => (
                <option key={n} value={n}>
                  {n} 篇
                </option>
              ))}
            </select>
          </label>
        ) : null}
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
        实际将使用：
        {multiModel && selectedModels.length
          ? `${selectedModels.join("、")} × ${scriptCount} 篇 · ${actualEffort}`
          : multiModel && scriptCount > 1
            ? `${actualModel} × ${scriptCount} 篇 · ${actualEffort}`
            : hideReasoningEffort
              ? actualModel
              : `${actualModel} · ${actualEffort}`}
      </p>
    </details>
  );
}
