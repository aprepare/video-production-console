import "./settings-panel.css";
import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { X } from "lucide-react";
import { messageTone } from "../messageTone";
import { ModelOptionsProvider, ModelSelect } from "../ModelSelect";
import { modelOptionsList, reasoningEfforts } from "../taskModel";
import type { ReasoningEffort } from "../taskModel";
import type { BgmLibrary, BgmTrack, MontageStyle, PublicSettings, Settings } from "../types";
import { formatTrackDuration, montageFonts, normalizeStyleColor, withMontageStyleDefaults } from "./montageStyle";
import { AIAccessCard } from "./AIAccessCard";
import { JianyingHealthCard } from "./JianyingHealthCard";

const restartFieldLabels: Partial<Record<keyof PublicSettings, string>> = {
  listen_addr: "监听地址",
  data_root: "数据目录",
  remix_base_url: "二创服务地址",
  remix_model: "二创模型",
  remix_reasoning_effort: "二创思考强度",
  remix_prompt_style: "二创提示词",
  copy_base_url: "口播copy接口",
  image_base_url: "生图服务地址",
  image_text_base_url: "图文文本模型地址",
  codex_binary_path: "Codex 程序路径",
  media_index_path: "素材索引",
  media_root: "媒体素材目录",
  jianying_root: "剪映草稿目录",
  machine_profile_path: "混剪机器配置",
  app_server_enabled: "任务实时交互服务",
  codex_workspace_roots: "Codex 工作目录白名单",
  codex_default_model: "默认模型",
  codex_default_reasoning_effort: "默认推理强度",
  volc_speech_speaker_id: "火山音色 ID",
  volc_speech_resource_id: "火山语音资源 ID",
  aurastd_voice_id: "克隆音色 ID",
  media_catalog_path: "素材库目录",
  ffmpeg_path: "FFmpeg 路径",
  ffprobe_path: "FFprobe 路径",
};

type SecretDraft = {
  grok_api_key: string;
  remix_api_key: string;
  copy_api_key: string;
  pexels_api_key: string;
  volc_speech_api_key: string;
  aurastd_tts_api_key: string;
  image_api_key: string;
  image_text_api_key: string;
  vision_api_key: string;
  embedding_api_key: string;
  pixabay_api_key: string;
  platform_api_token: string;
};

const settingsTabs = [
  { id: "remix", label: "二创" },
  { id: "image", label: "图文" },
  { id: "voice", label: "配音" },
  { id: "montage", label: "混剪" },
  { id: "style", label: "混剪样式" },
  { id: "system", label: "系统" },
] as const;

type SettingsTab = (typeof settingsTabs)[number]["id"];

type SettingsPanelProps = {
  api: (path: string, init?: RequestInit) => Promise<Response>;
  settings: Settings | null;
  draft: PublicSettings;
  onDraftChange: (draft: PublicSettings) => void;
  secretDraft: SecretDraft;
  onSecretDraftChange: (draft: SecretDraft) => void;
  feedback: string;
  onClose: () => void;
  onSubmit: (event: FormEvent) => void;
  onSaveAndRestart: () => void;
};

function Field(props: { label: string; children: ReactNode; wide?: boolean }) {
  return (
    <label className={`settings-field${props.wide ? " settings-field--wide" : ""}`}>
      {props.label}
      {props.children}
    </label>
  );
}

function previewTop(y: number): string {
  return `${Math.min(88, Math.max(8, 50 - y * 38))}%`;
}

function MontageStylePreview({ style }: { style: Required<MontageStyle> }) {
  const captionY = style.caption_position === "bottom"
    ? -0.3
    : style.caption_position === "custom"
      ? style.caption_y
      : 0;
  return (
    <div className="montage-style-preview" aria-label="混剪样式预览">
      <div className="montage-style-preview__stage">
        {style.title_hidden ? null : (
          <span
            className="montage-style-preview__title"
            style={{ color: style.title_color, fontSize: `${style.title_size}px`, top: previewTop(style.title_y) }}
          >
            主标题样例
          </span>
        )}
        {style.subtitle_hidden ? null : (
          <span
            className="montage-style-preview__subtitle"
            style={{ color: style.subtitle_color, fontSize: `${style.subtitle_size}px`, top: previewTop(style.subtitle_y) }}
          >
            副标题样例
          </span>
        )}
        <p className="montage-style-preview__caption" style={{ top: previewTop(captionY) }}>
          <span style={{ color: style.caption_color, fontSize: `${style.plain_size}px` }}>往后两个月</span>
          <span style={{ color: style.keyword_color, fontSize: `${style.keyword_size}px` }}>发财</span>
        </p>
      </div>
    </div>
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

export function SettingsPanel({
  api,
  settings,
  draft,
  onDraftChange,
  secretDraft,
  onSecretDraftChange,
  feedback,
  onClose,
  onSubmit,
  onSaveAndRestart,
}: SettingsPanelProps) {
  const [tab, setTab] = useState<SettingsTab>("remix");
  const [newModelName, setNewModelName] = useState("");
  const [bgmTracks, setBgmTracks] = useState<BgmTrack[] | null>(null);
  const [bgmBusy, setBgmBusy] = useState(false);
  const [bgmMessage, setBgmMessage] = useState("");
  const montageStyle = withMontageStyleDefaults(draft.montage_style);
  const patchMontageStyle = (patch: Partial<MontageStyle>) => {
    const next = { ...patch };
    if (next.caption_color) next.caption_color = normalizeStyleColor(next.caption_color);
    if (next.keyword_color) next.keyword_color = normalizeStyleColor(next.keyword_color);
    if (next.title_color) next.title_color = normalizeStyleColor(next.title_color);
    if (next.subtitle_color) next.subtitle_color = normalizeStyleColor(next.subtitle_color);
    onDraftChange({ ...draft, montage_style: { ...montageStyle, ...next } });
  };

  const loadBgmLibrary = useCallback(async () => {
    setBgmBusy(true);
    setBgmMessage("");
    try {
      const response = await api("/api/bgm-library");
      if (!response.ok) throw new Error("bgm library request failed");
      const payload = (await response.json()) as BgmLibrary;
      setBgmTracks(payload.tracks || []);
    } catch {
      setBgmTracks([]);
      setBgmMessage("BGM 列表读取失败。");
    } finally {
      setBgmBusy(false);
    }
  }, [api]);

  useEffect(() => {
    if (tab === "style" && bgmTracks === null) void loadBgmLibrary();
  }, [tab, bgmTracks, loadBgmLibrary]);

  const rescanBgmLibrary = async () => {
    setBgmBusy(true);
    setBgmMessage("");
    try {
      const response = await api("/api/bgm-library/rescan", { method: "POST" });
      if (!response.ok) throw new Error("bgm rescan failed");
      const payload = (await response.json()) as BgmLibrary;
      setBgmTracks(payload.tracks || []);
      setBgmMessage(`扫描完成，共 ${(payload.tracks || []).length} 首。`);
    } catch {
      setBgmMessage("扫描失败，请检查 BGM 目录。");
    } finally {
      setBgmBusy(false);
    }
  };
  const modelOptions = modelOptionsList(draft.model_options);
  const addModelOption = () => {
    const name = newModelName.trim();
    if (!name || modelOptions.includes(name)) return;
    onDraftChange({ ...draft, model_options: [...modelOptions, name].join("\n") });
    setNewModelName("");
  };
  const removeModelOption = (name: string) =>
    onDraftChange({
      ...draft,
      model_options: modelOptions.filter((option) => option !== name).join("\n"),
    });
  const restartChangedFields = useMemo(() => {
    if (!settings?.restart_required || !settings.active_public) return [];
    const configured = settings.configured_public || settings.public;
    return (Object.keys(configured) as Array<keyof PublicSettings>)
      .filter(
        (key) =>
          JSON.stringify(configured[key]) !== JSON.stringify(settings.active_public?.[key]),
      )
      .map((key) => restartFieldLabels[key] || String(key));
  }, [settings]);

  const setText = (key: keyof PublicSettings) => (event: { target: { value: string } }) =>
    onDraftChange({ ...draft, [key]: event.target.value });

  const httpOnTab = (tab === "remix" && (draft.remix_base_url || "").toLowerCase().startsWith("http://"))
    || (tab === "image" && (draft.image_base_url || "").toLowerCase().startsWith("http://"))
    || (tab === "voice" && (draft.aurastd_base_url || "").toLowerCase().startsWith("http://"));

  return (
    <ModelOptionsProvider options={modelOptions}>
    <div className="modal-backdrop" onClick={onClose}>
      <form
        className="settings-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="settings-dialog-title"
        tabIndex={-1}
        onClick={(event) => event.stopPropagation()}
        onSubmit={onSubmit}
      >
        <div className="modal-head">
          <div>
            <span className="muted">本地配置</span>
            <h2 id="settings-dialog-title">控制台设置</h2>
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
        {settings?.restart_required ? (
          <div className="restart-required" role="status">
            <strong>已保存，重启后生效</strong>
            {restartChangedFields.length ? <p>等待重启：{restartChangedFields.join("、")}</p> : null}
          </div>
        ) : null}
        <div className="settings-tabs" role="tablist" aria-label="设置分类">
          {settingsTabs.map((item) => (
            <button
              key={item.id}
              type="button"
              role="tab"
              id={`settings-tab-${item.id}`}
              aria-selected={tab === item.id}
              aria-controls={`settings-panel-${item.id}`}
              className={tab === item.id ? "settings-tab is-active" : "settings-tab"}
              tabIndex={tab === item.id ? 0 : -1}
              onClick={() => setTab(item.id)}
              onKeyDown={(event) => {
                const index = settingsTabs.findIndex((candidate) => candidate.id === item.id);
                const nextIndex = event.key === "Home" ? 0
                  : event.key === "End" ? settingsTabs.length - 1
                  : event.key === "ArrowRight" ? (index + 1) % settingsTabs.length
                  : event.key === "ArrowLeft" ? (index - 1 + settingsTabs.length) % settingsTabs.length
                  : -1;
                if (nextIndex < 0) return;
                event.preventDefault();
                setTab(settingsTabs[nextIndex].id);
                const tabs = event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="tab"]');
                tabs?.[nextIndex]?.focus();
              }}
            >
              {item.label}
            </button>
          ))}
        </div>
        <div
          className="settings-tab-panel"
          role="tabpanel"
          id={`settings-panel-${tab}`}
          aria-labelledby={`settings-tab-${tab}`}
        >
          {tab === "remix" ? (
            <>
              <Field label="二创服务地址">
                <input
                  value={draft.remix_base_url || ""}
                  placeholder="http://127.0.0.1:2001/v1"
                  onChange={setText("remix_base_url")}
                />
              </Field>
              <Field label="二创模型">
                <ModelSelect
                  value={draft.remix_model || ""}
                  emptyLabel="不设置"
                  onChange={(remix_model) => onDraftChange({ ...draft, remix_model })}
                />
              </Field>
              <Field label="口播稿/字幕关键词模型（留空=跟随二创模型）">
                <ModelSelect
                  aria-label="口播稿模型"
                  value={draft.spoken_lines_model || ""}
                  emptyLabel="跟随二创模型"
                  onChange={(spoken_lines_model) => onDraftChange({ ...draft, spoken_lines_model })}
                />
              </Field>
              <Field label="二创思考强度">
                <select
                  aria-label="二创思考强度"
                  value={draft.remix_reasoning_effort || ""}
                  onChange={(event) =>
                    onDraftChange({
                      ...draft,
                      remix_reasoning_effort: event.target.value as PublicSettings["remix_reasoning_effort"],
                    })
                  }
                >
                  <option value="">不设置</option>
                  {reasoningEfforts.map((effort) => (
                    <option key={effort} value={effort}>{effort}</option>
                  ))}
                </select>
              </Field>
              <Field label="二创 API 密钥">
                <small>
                  {settings?.secrets.remix_api_key?.configured ? "已配置，输入新值才会替换" : "未配置"}
                </small>
                <input
                  type="password"
                  aria-label="二创 API 密钥"
                  value={secretDraft.remix_api_key}
                  placeholder="留空保持不变"
                  onChange={(event) =>
                    onSecretDraftChange({ ...secretDraft, remix_api_key: event.target.value })
                  }
                />
              </Field>
              <Field label="口播copy接口">
                <input
                  value={draft.copy_base_url || ""}
                  placeholder="http://127.0.0.1:8866"
                  onChange={setText("copy_base_url")}
                />
              </Field>
              <Field label="口播copy密钥">
                <small>
                  {settings?.secrets.copy_api_key?.configured ? "已配置，输入新值才会替换" : "未配置"}
                </small>
                <input
                  type="password"
                  aria-label="口播copy密钥"
                  value={secretDraft.copy_api_key}
                  placeholder="留空保持不变"
                  onChange={(event) =>
                    onSecretDraftChange({ ...secretDraft, copy_api_key: event.target.value })
                  }
                />
              </Field>
              <div className="settings-field settings-field--wide model-options-manager">
                <span>可选模型列表</span>
                <ul className="model-options-list">
                  {modelOptions.map((option) => (
                    <li key={option}>
                      <code>{option}</code>
                      <button
                        type="button"
                        aria-label={`删除模型 ${option}`}
                        onClick={() => removeModelOption(option)}
                      >
                        删除
                      </button>
                    </li>
                  ))}
                  {modelOptions.length === 0 ? <li className="muted">使用内置默认列表</li> : null}
                </ul>
                <div className="model-options-add">
                  <input
                    aria-label="新模型名称"
                    value={newModelName}
                    placeholder="输入模型名称"
                    onChange={(event) => setNewModelName(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") {
                        event.preventDefault();
                        addModelOption();
                      }
                    }}
                  />
                  <button type="button" onClick={addModelOption}>
                    添加模型
                  </button>
                </div>
              </div>
            </>
          ) : null}
          {tab === "image" ? (
            <>
              <Field label="生图服务地址">
                <input value={draft.image_base_url || ""} placeholder={draft.image_text_base_url ? `留空则用图文文本地址 ${draft.image_text_base_url}` : "OpenAI 兼容地址"} onChange={setText("image_base_url")} />
              </Field>
              <Field label="默认图片模型">
                <input aria-label="默认图片模型" value={draft.image_model || ""} placeholder="grok-imagine-image-2.0" onChange={setText("image_model")} />
                <small>图文项目和单张出图都默认用它；单次出图时也能临时换。</small>
              </Field>
              <div className="settings-field settings-field--wide" role="group" aria-label="生图 Key">
                生图 Key
                <label className="settings-inline-check">
                  <input
                    type="checkbox"
                    aria-label="和图文文本模型共用同一把 Key"
                    checked={draft.image_use_text_key !== false}
                    onChange={(event) => onDraftChange({ ...draft, image_use_text_key: event.target.checked })}
                  />
                  和图文文本模型共用同一把 Key（推荐，换 Key 只要改一处）
                </label>
                <small>
                  {draft.image_use_text_key !== false
                    ? settings?.secrets.image_text_api_key?.configured ? "正在使用图文文本模型的 Key。" : "图文文本模型的 Key 还没配置，请到图文制作页的「文本模型连接」填写。"
                    : settings?.secrets.image_api_key?.configured ? "单独的生图 Key 已配置，输入新值才会替换" : "单独的生图 Key 未配置"}
                </small>
                {draft.image_use_text_key === false ? (
                  <input
                    type="password"
                    aria-label="生图 API Key"
                    value={secretDraft.image_api_key}
                    placeholder="留空保持不变"
                    onChange={(event) =>
                      onSecretDraftChange({ ...secretDraft, image_api_key: event.target.value })
                    }
                  />
                ) : null}
              </div>
            </>
          ) : null}
          {tab === "voice" ? (
            <>
              <Field label="配音接口">
                <select
                  aria-label="配音接口"
                  value={draft.tts_provider || "aurastd"}
                  onChange={(event) =>
                    onDraftChange({
                      ...draft,
                      tts_provider: event.target.value as PublicSettings["tts_provider"],
                    })
                  }
                >
                  <option value="aurastd">Aura Studio（当前）</option>
                  <option value="volc">火山语音（备用）</option>
                </select>
              </Field>
              {(draft.tts_provider || "aurastd") === "aurastd" ? (
                <>
                  <Field label="Aura Studio API Key">
                    <small>
                      {settings?.secrets.aurastd_tts_api_key?.configured ? "已配置，输入新值才会替换" : "未配置"}
                    </small>
                    <input
                      type="password"
                      aria-label="Aura Studio API Key"
                      value={secretDraft.aurastd_tts_api_key}
                      placeholder="留空保持不变"
                      onChange={(event) =>
                        onSecretDraftChange({ ...secretDraft, aurastd_tts_api_key: event.target.value })
                      }
                    />
                  </Field>
                  <Field label="克隆音色 ID">
                    <input
                      value={draft.aurastd_voice_id || ""}
                      placeholder="moss_audio_..."
                      onChange={setText("aurastd_voice_id")}
                    />
                  </Field>
                  <Field label="配音模型">
                    <select
                      aria-label="配音模型"
                      value={draft.aurastd_model || "speech-2.8-hd"}
                      onChange={(event) => onDraftChange({ ...draft, aurastd_model: event.target.value })}
                    >
                      <option value="speech-2.8-hd">speech-2.8-hd</option>
                      <option value="speech-2.8-turbo">speech-2.8-turbo</option>
                      <option value="speech-2.6-hd">speech-2.6-hd</option>
                      <option value="speech-2.6-turbo">speech-2.6-turbo</option>
                    </select>
                  </Field>
                  <p className="settings-section-title">音色调节</p>
                  <SliderField
                    label="语速"
                    value={draft.aurastd_speed}
                    min={0.5}
                    max={2}
                    step={0.01}
                    onChange={(value) => onDraftChange({ ...draft, aurastd_speed: value })}
                  />
                  <SliderField
                    label="音调"
                    value={draft.aurastd_pitch}
                    min={-12}
                    max={12}
                    step={1}
                    onChange={(value) => onDraftChange({ ...draft, aurastd_pitch: value })}
                  />
                  <SliderField
                    label="音量"
                    value={draft.aurastd_volume}
                    min={0}
                    max={10}
                    step={0.01}
                    onChange={(value) => onDraftChange({ ...draft, aurastd_volume: value })}
                  />
                  <p className="settings-section-title">声音效果器（高级）</p>
                  <SliderField
                    label="音高（低沉/明亮）"
                    value={draft.aurastd_modify_pitch}
                    min={-100}
                    max={100}
                    step={1}
                    onChange={(value) => onDraftChange({ ...draft, aurastd_modify_pitch: value })}
                  />
                  <SliderField
                    label="强度（力量感/柔和）"
                    value={draft.aurastd_modify_intensity}
                    min={-100}
                    max={100}
                    step={1}
                    onChange={(value) => onDraftChange({ ...draft, aurastd_modify_intensity: value })}
                  />
                  <SliderField
                    label="音色（磁性/清脆）"
                    value={draft.aurastd_modify_timbre}
                    min={-100}
                    max={100}
                    step={1}
                    onChange={(value) => onDraftChange({ ...draft, aurastd_modify_timbre: value })}
                  />
                  <Field label="音效">
                    <select
                      aria-label="音效"
                      value={draft.aurastd_sound_effects || ""}
                      onChange={(event) => onDraftChange({ ...draft, aurastd_sound_effects: event.target.value })}
                    >
                      <option value="">无</option>
                      <option value="spacious_echo">空旷回音</option>
                      <option value="auditorium_echo">礼堂广播</option>
                      <option value="lofi_telephone">电话失真</option>
                      <option value="robotic">电音</option>
                    </select>
                  </Field>
                </>
              ) : (
                <>
                  <Field label="火山音色 ID">
                    <input value={draft.volc_speech_speaker_id || ""} placeholder="复刻音色 ID" onChange={setText("volc_speech_speaker_id")} />
                  </Field>
                  <Field label="火山语音资源 ID">
                    <input value={draft.volc_speech_resource_id || ""} placeholder="seed-icl-2.0" onChange={setText("volc_speech_resource_id")} />
                  </Field>
                  <Field label="火山语音 API Key">
                    <small>
                      {settings?.secrets.volc_speech_api_key?.configured ? "已配置，输入新值才会替换" : "未配置"}
                    </small>
                    <input
                      type="password"
                      aria-label="火山语音 API Key"
                      value={secretDraft.volc_speech_api_key}
                      placeholder="留空保持不变"
                      onChange={(event) =>
                        onSecretDraftChange({ ...secretDraft, volc_speech_api_key: event.target.value })
                      }
                    />
                  </Field>
                </>
              )}
            </>
          ) : null}
          {tab === "montage" ? (
            <>
              <p className="settings-section-title settings-section-title--first">素材来源</p>
              <p className="settings-section-note">城市建筑混剪使用素材目录和素材索引；电影混剪另用 catalog.db 片段库。</p>
              <Field label="电影片段库（可选）">
                <input value={draft.media_catalog_path || ""} placeholder="catalog.db 路径" onChange={setText("media_catalog_path")} />
              </Field>
              <Field label="媒体素材目录">
                <input value={draft.media_root || ""} placeholder="本地媒体根目录" onChange={setText("media_root")} />
              </Field>
              <Field label="素材索引">
                <input value={draft.media_index_path || ""} placeholder="媒体索引文件" onChange={setText("media_index_path")} />
              </Field>
              <p className="settings-section-title">剪映输出</p>
              <p className="settings-section-note">草稿写入这里配置的真实目录，剪映连接状态用于核验注册能力。</p>
              <Field label="剪映草稿目录">
                <input value={draft.jianying_root || ""} placeholder="剪映草稿根目录" onChange={setText("jianying_root")} />
              </Field>
              {api ? <JianyingHealthCard api={api} /> : null}
              <details className="settings-advanced settings-field--wide">
                <summary>高级工具与机器配置</summary>
                <p>通常无需填写：FFmpeg 和 FFprobe 留空时自动使用控制台自带工具；机器配置用于指定混剪执行器、素材索引和剪映注册环境。</p>
                <div className="settings-advanced__grid">
                  <Field label="FFmpeg 路径">
                    <input value={draft.ffmpeg_path || ""} placeholder="留空自动探测" onChange={setText("ffmpeg_path")} />
                  </Field>
                  <Field label="FFprobe 路径">
                    <input value={draft.ffprobe_path || ""} placeholder="留空自动探测" onChange={setText("ffprobe_path")} />
                  </Field>
                  <Field label="混剪机器配置" wide>
                    <input value={draft.machine_profile_path || ""} placeholder="machine profile JSON" onChange={setText("machine_profile_path")} />
                  </Field>
                </div>
              </details>
            </>
          ) : null}
          {tab === "style" ? (
            <>
              <MontageStylePreview style={montageStyle} />
              <p className="settings-section-note">这里设置全局默认样式；已开启账号专属样式的账号，以创作台“账号配置”为准。</p>
              <p className="settings-section-title">字幕与关键词</p>
              <p className="settings-section-note">控制口播字幕的字体、位置，以及重点词的强调方式。</p>
              <Field label="字幕字号">
                <input
                  type="number"
                  aria-label="字幕字号"
                  min={5}
                  max={60}
                  step={0.1}
                  value={montageStyle.caption_size}
                  onChange={(event) => patchMontageStyle({ caption_size: Number(event.target.value) })}
                />
              </Field>
              <Field label="字幕颜色">
                <input
                  type="color"
                  aria-label="字幕颜色"
                  value={montageStyle.caption_color}
                  onChange={(event) => patchMontageStyle({ caption_color: event.target.value })}
                />
              </Field>
              <Field label="字幕位置">
                <select
                  aria-label="字幕位置"
                  value={montageStyle.caption_position}
                  onChange={(event) => patchMontageStyle({ caption_position: event.target.value })}
                >
                  <option value="middle">中间</option>
                  <option value="bottom">底部</option>
                  <option value="custom">自定义</option>
                </select>
              </Field>
              {montageStyle.caption_position === "custom" ? (
                <Field label="字幕纵向位置">
                  <input
                    type="number"
                    aria-label="字幕纵向位置"
                    min={-1}
                    max={1}
                    step={0.01}
                    value={montageStyle.caption_y}
                    onChange={(event) => patchMontageStyle({ caption_y: Number(event.target.value) })}
                  />
                </Field>
              ) : null}
              <Field label="字幕字体">
                <select
                  aria-label="字幕字体"
                  value={montageStyle.caption_font}
                  onChange={(event) => patchMontageStyle({ caption_font: event.target.value })}
                >
                  {montageFonts.map((font) => (
                    <option key={font} value={font}>{font}</option>
                  ))}
                </select>
              </Field>
              <Field label="标注字幕关键词">
                <input
                  type="checkbox"
                  aria-label="标注字幕关键词"
                  checked={!montageStyle.keywords_hidden}
                  onChange={(event) => patchMontageStyle({ keywords_hidden: !event.target.checked })}
                />
              </Field>
              <Field label="关键词字号">
                <input
                  type="number"
                  aria-label="关键词字号"
                  min={5}
                  max={60}
                  step={0.1}
                  value={montageStyle.keyword_size}
                  onChange={(event) => patchMontageStyle({ keyword_size: Number(event.target.value) })}
                />
              </Field>
              <Field label="关键词颜色">
                <input
                  type="color"
                  aria-label="关键词颜色"
                  value={montageStyle.keyword_color}
                  onChange={(event) => patchMontageStyle({ keyword_color: event.target.value })}
                />
              </Field>
              <Field label="非关键词字号">
                <input
                  type="number"
                  aria-label="非关键词字号"
                  min={5}
                  max={60}
                  step={0.1}
                  value={montageStyle.plain_size}
                  onChange={(event) => patchMontageStyle({ plain_size: Number(event.target.value) })}
                />
              </Field>
              <p className="settings-section-title">画面标题</p>
              <p className="settings-section-note">控制画面中的主标题和副标题；关闭后不会写入混剪草稿。</p>
              <Field label="显示主标题">
                <input
                  type="checkbox"
                  aria-label="显示主标题"
                  checked={!montageStyle.title_hidden}
                  onChange={(event) => patchMontageStyle({ title_hidden: !event.target.checked })}
                />
              </Field>
              <Field label="主标题字号">
                <input
                  type="number"
                  aria-label="主标题字号"
                  min={5}
                  max={60}
                  step={0.1}
                  value={montageStyle.title_size}
                  onChange={(event) => patchMontageStyle({ title_size: Number(event.target.value) })}
                />
              </Field>
              <Field label="主标题颜色">
                <input
                  type="color"
                  aria-label="主标题颜色"
                  value={montageStyle.title_color}
                  onChange={(event) => patchMontageStyle({ title_color: event.target.value })}
                />
              </Field>
              <Field label="主标题纵向位置">
                <input
                  type="number"
                  aria-label="主标题纵向位置"
                  min={-1}
                  max={1}
                  step={0.01}
                  value={montageStyle.title_y}
                  onChange={(event) => patchMontageStyle({ title_y: Number(event.target.value) })}
                />
              </Field>
              <Field label="显示副标题">
                <input
                  type="checkbox"
                  aria-label="显示副标题"
                  checked={!montageStyle.subtitle_hidden}
                  onChange={(event) => patchMontageStyle({ subtitle_hidden: !event.target.checked })}
                />
              </Field>
              <Field label="副标题字号">
                <input
                  type="number"
                  aria-label="副标题字号"
                  min={5}
                  max={60}
                  step={0.1}
                  value={montageStyle.subtitle_size}
                  onChange={(event) => patchMontageStyle({ subtitle_size: Number(event.target.value) })}
                />
              </Field>
              <Field label="副标题颜色">
                <input
                  type="color"
                  aria-label="副标题颜色"
                  value={montageStyle.subtitle_color}
                  onChange={(event) => patchMontageStyle({ subtitle_color: event.target.value })}
                />
              </Field>
              <Field label="副标题纵向位置">
                <input
                  type="number"
                  aria-label="副标题纵向位置"
                  min={-1}
                  max={1}
                  step={0.01}
                  value={montageStyle.subtitle_y}
                  onChange={(event) => patchMontageStyle({ subtitle_y: Number(event.target.value) })}
                />
              </Field>
              <p className="settings-section-title">背景音乐</p>
              <p className="settings-section-note">选择曲目并管理本地音乐目录。内置验证曲目使用固定音量。</p>
              <Field label="BGM 曲目">
                <select
                  aria-label="BGM 曲目"
                  value={montageStyle.bgm_id}
                  onChange={(event) => patchMontageStyle({ bgm_id: event.target.value })}
                >
                  <option value="builtin">内置默认</option>
                  {montageStyle.bgm_id !== "builtin"
                    && !(bgmTracks || []).some((track) => track.id === montageStyle.bgm_id) ? (
                    <option value={montageStyle.bgm_id}>当前：{montageStyle.bgm_id}</option>
                  ) : null}
                  {(bgmTracks || []).map((track) => (
                    <option key={track.id} value={track.id}>
                      {track.name}（{formatTrackDuration(track.duration_s)}）
                    </option>
                  ))}
                </select>
              </Field>
              {montageStyle.bgm_id === "builtin" ? (
                <Field label="BGM 音量">
                  <span className="settings-hint">内置验证曲目音量固定（0.2512），选音乐库曲目后可调。</span>
                </Field>
              ) : (
                <SliderField
                  label="BGM 音量"
                  value={montageStyle.bgm_volume}
                  min={0.01}
                  max={1}
                  step={0.01}
                  onChange={(value) => patchMontageStyle({ bgm_volume: value })}
                />
              )}
              <Field label="BGM 目录" wide>
                <input
                  value={draft.bgm_dir || ""}
                  placeholder="本地 BGM 目录绝对路径"
                  onChange={setText("bgm_dir")}
                />
              </Field>
              <div className="settings-field">
                <span>BGM 库</span>
                <button type="button" disabled={bgmBusy} onClick={() => void rescanBgmLibrary()}>
                  {bgmBusy ? "扫描中…" : "重新扫描"}
                </button>
                {bgmTracks === null && bgmBusy ? <small role="status">读取中…</small> : null}
                {bgmMessage ? <small role="status">{bgmMessage}</small> : null}
              </div>
            </>
          ) : null}
          {tab === "system" ? (
            <>
              {api ? <AIAccessCard api={api} /> : null}
              <p className="settings-section-title">平台接口（对标采集与口播转写）</p>
              <Field label="平台接口地址">
                <input
                  aria-label="平台接口地址"
                  value={draft.platform_api_base_url || ""}
                  placeholder="http://165.254.188.202:8790"
                  onChange={setText("platform_api_base_url")}
                />
              </Field>
              <Field label="平台接口 Token">
                <small>
                  {settings?.secrets.platform_api_token?.configured ? "已配置，输入新值才会替换" : "未配置"}
                </small>
                <input
                  type="password"
                  aria-label="平台接口 Token"
                  value={secretDraft.platform_api_token}
                  placeholder="留空保持不变"
                  onChange={(event) =>
                    onSecretDraftChange({ ...secretDraft, platform_api_token: event.target.value })
                  }
                />
              </Field>
              <p className="settings-section-title">任务默认值</p>
              <Field label="默认模型">
                <ModelSelect
                  value={draft.codex_default_model || ""}
                  emptyLabel="不设置"
                  onChange={(codex_default_model) => onDraftChange({ ...draft, codex_default_model })}
                />
              </Field>
              <Field label="默认推理强度">
                <select
                  aria-label="默认推理强度"
                  value={draft.codex_default_reasoning_effort || "medium"}
                  onChange={(event) =>
                    onDraftChange({
                      ...draft,
                      codex_default_reasoning_effort: event.target.value as ReasoningEffort,
                    })
                  }
                >
                  {reasoningEfforts.map((effort) => (
                    <option key={effort} value={effort}>{effort}</option>
                  ))}
                </select>
              </Field>
              <Field label="同时运行任务数">
                <select
                  value={draft.max_codex_concurrency}
                  onChange={(event) =>
                    onDraftChange({ ...draft, max_codex_concurrency: Number(event.target.value) })
                  }
                >
                  {[1, 2, 3, 4].map((value) => (
                    <option key={value} value={value}>{value}</option>
                  ))}
                </select>
              </Field>
            </>
          ) : null}
        </div>
        {httpOnTab ? (
          <p className="settings-feedback settings-feedback--danger" role="alert">
            当前地址使用 HTTP，密钥会明文传输。
          </p>
        ) : null}
        <div className="settings-actions">
          <button className="save-settings" type="submit">
            保存设置
          </button>
          <button
            className="save-settings save-settings--restart"
            type="button"
            onClick={onSaveAndRestart}
          >
            保存并重启
          </button>
        </div>
      </form>
    </div>
    </ModelOptionsProvider>
  );
}
