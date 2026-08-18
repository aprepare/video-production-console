import { isSelectableModel, selectableModels } from "./taskModel";

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
  const extra = value && !isSelectableModel(value) ? value : "";
  return (
    <select aria-label={ariaLabel} value={value} onChange={(event) => onChange(event.target.value)}>
      {emptyLabel != null ? <option value="">{emptyLabel}</option> : null}
      {extra ? <option value={extra}>{extra}</option> : null}
      {selectableModels.map((model) => (
        <option key={model} value={model}>
          {model}
        </option>
      ))}
    </select>
  );
}
