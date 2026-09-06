import { Zap } from "lucide-react";

type Props = {
  value?: string;
  inheritedValue?: string;
  label: string;
  onChange: (tier: string) => void;
};

export function FastModeButton({ value, inheritedValue, label, onChange }: Props) {
  const tier = (value || inheritedValue || "").toLowerCase();
  const enabled = tier === "priority" || tier === "fast";
  return (
    <div className="wf-field">
      <button
        type="button"
        className={enabled ? "primary-button" : "header-button"}
        aria-label={label}
        aria-pressed={enabled}
        onClick={() => onChange(enabled ? "default" : "priority")}
      >
        <Zap size={14} aria-hidden="true" />
        Fast · {enabled ? "已开启" : "未开启"}
      </button>
      <small className="remix-lab-muted">保留思考强度；需上游通道支持，费用按通道规则。</small>
      {inheritedValue !== undefined ? (
        <small className="remix-lab-muted">
          {value ? "已单独设置。" : "跟随默认模型档。"}
          {value ? <button type="button" className="header-button" onClick={() => onChange("")}>跟随默认档</button> : null}
        </small>
      ) : null}
    </div>
  );
}
