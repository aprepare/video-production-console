import { resolveSelectableModels } from "./taskModel";

type ModelSelectProps = {
  value: string;
  onChange: (value: string) => void;
  "aria-label"?: string;
  emptyLabel?: string;
  models?: readonly string[];
};

export function ModelSelect({
  value,
  onChange,
  "aria-label": ariaLabel,
  emptyLabel,
  models,
}: ModelSelectProps) {
  const options = resolveSelectableModels(models);
  const extra = value && !options.includes(value) ? value : "";
  return (
    <select aria-label={ariaLabel} value={value} onChange={(event) => onChange(event.target.value)}>
      {emptyLabel != null ? <option value="">{emptyLabel}</option> : null}
      {extra ? <option value={extra}>{extra}</option> : null}
      {options.map((model) => (
        <option key={model} value={model}>
          {model}
        </option>
      ))}
    </select>
  );
}
