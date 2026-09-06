import { useRef } from "react";
import { ModelCombo } from "../ModelSelect";
import { FastModeButton } from "./FastModeButton";
import type { RemixLabRerunModel } from "./api";

export function RoleModelFields({ id, label, value, onChange, modelLabel = `${label}模型` }: {
  id: string; label: string; value: RemixLabRerunModel;
  onChange: (value: RemixLabRerunModel) => void; modelLabel?: string;
}) {
  const configuredModel = useRef(value.model);
  return <>
    <label className="wf-field"><span>模型</span><ModelCombo id={id} aria-label={modelLabel} value={value.model}
      onChange={model => onChange({...value, model:model || configuredModel.current})} /></label>
    <label className="wf-field"><span>推理强度</span><select aria-label={`${label}推理强度`} value={value.reasoning_effort}
      onChange={event => onChange({...value, reasoning_effort:event.target.value})}>
      {["", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"].map(effort =>
        <option key={effort} value={effort}>{effort || "沿用默认强度"}</option>)}
    </select></label>
    <FastModeButton label={`${label} Fast`} value={value.service_tier}
      onChange={service_tier => onChange({...value, service_tier})} />
  </>;
}
