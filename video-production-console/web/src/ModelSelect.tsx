import { createContext, useContext } from "react";
import type { ReactNode } from "react";
import { selectableModels } from "./taskModel";

// All model dropdowns share one option list, maintained in settings
// (model_options). An empty list falls back to the built-in defaults.
const ModelOptionsContext = createContext<string[]>([]);

export function ModelOptionsProvider({
  options,
  children,
}: {
  options: string[];
  children: ReactNode;
}) {
  return <ModelOptionsContext.Provider value={options}>{children}</ModelOptionsContext.Provider>;
}

export function useModelOptions(): string[] {
  const configured = useContext(ModelOptionsContext);
  return configured.length > 0 ? configured : [...selectableModels];
}

// ModelMultiSelect lets 二创 fan out to several models at once: each checked
// model becomes its own task so the drafts can be compared side by side.
export function ModelMultiSelect({
  value,
  onChange,
  inheritedLabel,
}: {
  value: string[];
  onChange: (models: string[]) => void;
  inheritedLabel: string;
}) {
  const models = useModelOptions();
  const toggle = (model: string) => {
    onChange(value.includes(model) ? value.filter((item) => item !== model) : [...value, model]);
  };
  return (
    <div className="model-multi-select" role="group" aria-label="二创模型多选">
      {models.map((model) => (
        <label key={model} className={`model-multi-option${value.includes(model) ? " is-checked" : ""}`}>
          <input
            type="checkbox"
            checked={value.includes(model)}
            onChange={() => toggle(model)}
          />
          {model}
        </label>
      ))}
      <p className="model-multi-hint">
        {value.length
          ? `将同时启动 ${value.length} 个任务：${value.join("、")}`
          : `不勾选则用默认模型（${inheritedLabel}）生成一份`}
      </p>
    </div>
  );
}

type ModelSelectProps = {
  value: string;
  onChange: (value: string) => void;
  "aria-label"?: string;
  emptyLabel?: string;
};

export function ModelSelect({
  value,
  onChange,
  "aria-label": ariaLabel,
  emptyLabel,
}: ModelSelectProps) {
  const configured = useContext(ModelOptionsContext);
  const models = configured.length > 0 ? configured : [...selectableModels];
  const extra = value && !models.includes(value) ? value : "";
  return (
    <select aria-label={ariaLabel} value={value} onChange={(event) => onChange(event.target.value)}>
      {emptyLabel != null ? <option value="">{emptyLabel}</option> : null}
      {extra ? <option value={extra}>{extra}</option> : null}
      {models.map((model) => (
        <option key={model} value={model}>
          {model}
        </option>
      ))}
    </select>
  );
}
