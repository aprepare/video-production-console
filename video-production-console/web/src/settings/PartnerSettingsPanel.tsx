import { useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { X } from "lucide-react";
import { messageTone } from "../messageTone";
import type { PartnerSettingsUpdate, PartnerSettingsView } from "../partner/types";

const imageRatios = ["3:4", "4:3", "9:16", "1:1"] as const;
const imageStyles = [
  ["finance_documentary", "财经纪实插画"],
  ["red_ink", "赤墨风"],
  ["old_newspaper", "旧报档案风"],
  ["ledger_investigation", "账本调查风"],
  ["dark_crisis", "暗黑危机风"],
  ["city_era", "城市时代感"],
  ["blackboard", "黑板讲解风"],
  ["custom", "自定义风格"],
] as const;

type PartnerSettingsPanelProps = {
  settings: PartnerSettingsView;
  onSave: (update: PartnerSettingsUpdate) => void;
  onClose?: () => void;
  feedback?: string;
};

function Field(props: { label: string; children: ReactNode; wide?: boolean }) {
  return (
    <label className={`settings-field${props.wide ? " settings-field--wide" : ""}`}>
      {props.label}
      {props.children}
    </label>
  );
}

function SliderField(props: {
  label: string;
  value: number;
  min: number;
  max: number;
  step: number;
  onChange: (value: number) => void;
}) {
  return (
    <label className="settings-field settings-slider">
      <span className="settings-slider-head">
        <span>{props.label}</span>
        <input
          type="number"
          aria-label={props.label}
          min={props.min}
          max={props.max}
          step={props.step}
          value={Number.isFinite(props.value) ? props.value : 0}
          onChange={(event) => props.onChange(Number(event.target.value))}
        />
      </span>
      <input
        type="range"
        aria-label={`${props.label}滑杆`}
        min={props.min}
        max={props.max}
        step={props.step}
        value={Number.isFinite(props.value) ? props.value : 0}
        onChange={(event) => props.onChange(Number(event.target.value))}
      />
    </label>
  );
}

export function PartnerSettingsPanel({
  settings,
  onSave,
  onClose,
  feedback,
}: PartnerSettingsPanelProps) {
  const [draft, setDraft] = useState<PartnerSettingsView>(settings);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    onSave({
      media_root: draft.media_root,
      jianying_root: draft.jianying_root,
      machine_profile_path: draft.machine_profile_path,
      default_image_ratio: draft.default_image_ratio,
      default_image_style: draft.default_image_style,
      aura_speed: draft.aura_speed,
      aura_volume: draft.aura_volume,
    });
  };

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <form
        className="settings-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="partner-settings-dialog-title"
        tabIndex={-1}
        onClick={(event) => event.stopPropagation()}
        onSubmit={submit}
      >
        <div className="modal-head">
          <div>
            <span className="muted">本机路径</span>
            <h2 id="partner-settings-dialog-title">伙伴设置</h2>
          </div>
          <button type="button" className="close" aria-label="关闭设置" onClick={onClose}>
            <X size={20} aria-hidden="true" />
          </button>
        </div>
        {feedback ? (
          <div
            className={`settings-feedback settings-feedback--${messageTone(feedback)}`}
            role={messageTone(feedback) === "danger" ? "alert" : "status"}
          >
            {feedback}
          </div>
        ) : null}
        <p className="settings-section-title">已开通能力</p>
        <p className="settings-note">
          文本模型：<span>{(draft.text_models || []).join("、") || "未提供"}</span>
        </p>
        <p className="settings-note">
          生图模型：<span>{draft.image_model || "未提供"}</span>
        </p>
        <p className="settings-note">
          思考强度：<span>{(draft.reasoning_efforts || []).join("、") || "未提供"}</span>
        </p>
        <p className="settings-note">
          配音：<span>{draft.aura_model || "未提供"}</span>
        </p>
        <p className="settings-note">
          音色：<span>{draft.aura_voice_id || "未提供"}</span>
        </p>
        <Field label="媒体素材目录">
          <input
            value={draft.media_root || ""}
            onChange={(event) => setDraft({ ...draft, media_root: event.target.value })}
          />
        </Field>
        <Field label="剪映草稿目录">
          <input
            value={draft.jianying_root || ""}
            onChange={(event) => setDraft({ ...draft, jianying_root: event.target.value })}
          />
        </Field>
        <Field label="混剪机器配置" wide>
          <input
            value={draft.machine_profile_path || ""}
            onChange={(event) => setDraft({ ...draft, machine_profile_path: event.target.value })}
          />
        </Field>
        <Field label="默认图片比例">
          <select
            aria-label="默认图片比例"
            value={draft.default_image_ratio || "3:4"}
            onChange={(event) => setDraft({ ...draft, default_image_ratio: event.target.value })}
          >
            {imageRatios.map((ratio) => (
              <option key={ratio} value={ratio}>
                {ratio}
              </option>
            ))}
          </select>
        </Field>
        <Field label="默认图片风格">
          <select
            aria-label="默认图片风格"
            value={draft.default_image_style || "finance_documentary"}
            onChange={(event) => setDraft({ ...draft, default_image_style: event.target.value })}
          >
            {imageStyles.map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </Field>
        <SliderField
          label="语速"
          value={draft.aura_speed ?? 1}
          min={0.5}
          max={2}
          step={0.01}
          onChange={(aura_speed) => setDraft({ ...draft, aura_speed })}
        />
        <SliderField
          label="音量"
          value={draft.aura_volume ?? 1}
          min={0}
          max={10}
          step={0.01}
          onChange={(aura_volume) => setDraft({ ...draft, aura_volume })}
        />
        <button className="save-settings" type="submit">
          保存设置
        </button>
      </form>
    </div>
  );
}

type PartnerSettingsDialogOptions = {
  api: (path: string, init?: RequestInit) => Promise<Response>;
  setMessage: (message: string) => void;
};

export function usePartnerSettingsDialog({ api, setMessage }: PartnerSettingsDialogOptions) {
  const [open, setOpen] = useState(false);
  const [settings, setSettings] = useState<PartnerSettingsView | null>(null);
  const [feedback, setFeedback] = useState("");

  const openDialog = async () => {
    try {
      const response = await api("/api/settings");
      if (!response.ok) throw new Error("settings_read_failed");
      setSettings((await response.json()) as PartnerSettingsView);
      setFeedback("");
      setOpen(true);
    } catch {
      setMessage("设置读取失败。");
    }
  };

  const save = async (update: PartnerSettingsUpdate) => {
    const response = await api("/api/settings", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(update),
    });
    if (!response.ok) {
      setFeedback("设置保存失败，请检查填写内容。");
      return;
    }
    setSettings((await response.json()) as PartnerSettingsView);
    setFeedback("设置已保存。");
  };

  return {
    open,
    settings,
    feedback,
    openDialog,
    save,
    close: () => {
      setFeedback("");
      setOpen(false);
    },
  };
}
