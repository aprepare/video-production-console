import { createContext, useContext, useState } from "react";
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
  const configured = useModelOptions();
  // 已勾选但不在设置清单里的模型（临时手输的）也要显示出来，不然勾了看不见。
  const models = [...configured, ...value.filter((item) => !configured.includes(item))];
  const [custom, setCustom] = useState("");
  const toggle = (model: string) => {
    onChange(value.includes(model) ? value.filter((item) => item !== model) : [...value, model]);
  };
  const addCustom = () => {
    const name = custom.trim();
    if (!name) return;
    if (!value.includes(name)) onChange([...value, name]);
    setCustom("");
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
      <span className="model-multi-custom">
        <input
          type="text"
          aria-label="临时添加模型"
          placeholder="输入模型名回车添加，如 claude-sonnet-4-6"
          value={custom}
          onChange={(event) => setCustom(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && !event.nativeEvent.isComposing) {
              event.preventDefault();
              addCustom();
            }
          }}
        />
        <button type="button" className="header-button" disabled={!custom.trim()} onClick={addCustom}>
          添加
        </button>
      </span>
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

// ModelCombo：既能从设置里的模型清单挑，也能直接敲任意模型名；空值表示用默认。
// 给不想跑去设置页加模型的场合用（AI 短片的拆分镜模型）。
export function ModelCombo({
  value,
  onChange,
  "aria-label": ariaLabel,
  placeholder,
  id,
}: {
  value: string;
  onChange: (value: string) => void;
  "aria-label"?: string;
  placeholder?: string;
  id: string;
}) {
  const models = useModelOptions();
  const listID = `${id}-models`;
  return (
    <span className="model-combo">
      <input
        id={id}
        type="text"
        list={listID}
        aria-label={ariaLabel}
        value={value}
        placeholder={placeholder}
        autoComplete="off"
        onChange={(event) => onChange(event.target.value.trim())}
      />
      <datalist id={listID}>
        {models.map((model) => <option key={model} value={model} />)}
      </datalist>
      {value ? (
        <button type="button" className="header-button model-combo__clear" aria-label="改回默认模型" onClick={() => onChange("")}>
          默认
        </button>
      ) : null}
    </span>
  );
}

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
