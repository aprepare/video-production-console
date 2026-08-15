import { useMemo, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { X } from "lucide-react";
import { messageTone } from "../messageTone";
import { reasoningEfforts } from "../taskModel";
import type { ReasoningEffort } from "../taskModel";
import type { PublicSettings, Settings } from "../types";

const restartFieldLabels: Partial<Record<keyof PublicSettings, string>> = {
  listen_addr: "监听地址",
  data_root: "数据目录",
  remix_base_url: "二创服务地址",
  remix_model: "二创模型",
  remix_reasoning_effort: "二创思考强度",
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
  media_catalog_path: "素材库目录",
  ffmpeg_path: "FFmpeg 路径",
  ffprobe_path: "FFprobe 路径",
};

type SecretDraft = {
  grok_api_key: string;
  remix_api_key: string;
  pexels_api_key: string;
  volc_speech_api_key: string;
  image_api_key: string;
  image_text_api_key: string;
  vision_api_key: string;
  embedding_api_key: string;
  pixabay_api_key: string;
};

const settingsTabs = [
  { id: "remix", label: "二创" },
  { id: "image", label: "图文" },
  { id: "voice", label: "配音" },
  { id: "montage", label: "混剪" },
  { id: "system", label: "系统" },
] as const;

type SettingsTab = (typeof settingsTabs)[number]["id"];

type SettingsPanelProps = {
  settings: Settings | null;
  draft: PublicSettings;
  onDraftChange: (draft: PublicSettings) => void;
  secretDraft: SecretDraft;
  onSecretDraftChange: (draft: SecretDraft) => void;
  feedback: string;
  onClose: () => void;
  onSubmit: (event: FormEvent) => void;
};

function Field(props: { label: string; children: ReactNode; wide?: boolean }) {
  return (
    <label className={`settings-field${props.wide ? " settings-field--wide" : ""}`}>
      {props.label}
      {props.children}
    </label>
  );
}

export function SettingsPanel({
  settings,
  draft,
  onDraftChange,
  secretDraft,
  onSecretDraftChange,
  feedback,
  onClose,
  onSubmit,
}: SettingsPanelProps) {
  const [tab, setTab] = useState<SettingsTab>("remix");
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
    || (tab === "image" && ["image_base_url", "image_text_base_url"].some((key) =>
      String(draft[key as keyof PublicSettings] || "").toLowerCase().startsWith("http://")));

  return (
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
        <p className="settings-note">密钥留空表示不改。</p>
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
              onClick={() => setTab(item.id)}
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
                <input
                  value={draft.remix_model || ""}
                  placeholder="gpt-5.6-sol"
                  onChange={setText("remix_model")}
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
            </>
          ) : null}
          {tab === "image" ? (
            <>
              <Field label="生图服务地址">
                <input value={draft.image_base_url || ""} placeholder="OpenAI 兼容地址" onChange={setText("image_base_url")} />
              </Field>
              <Field label="生图 API Key">
                <small>
                  {settings?.secrets.image_api_key?.configured ? "已配置，输入新值才会替换" : "未配置"}
                </small>
                <input
                  type="password"
                  aria-label="生图 API Key"
                  value={secretDraft.image_api_key}
                  placeholder="留空保持不变"
                  onChange={(event) =>
                    onSecretDraftChange({ ...secretDraft, image_api_key: event.target.value })
                  }
                />
              </Field>
              <Field label="图文文本模型地址">
                <input value={draft.image_text_base_url || ""} placeholder="OpenAI 兼容地址" onChange={setText("image_text_base_url")} />
              </Field>
              <Field label="图文文本模型 API Key">
                <small>
                  {settings?.secrets.image_text_api_key?.configured ? "已配置，输入新值才会替换" : "未配置"}
                </small>
                <input
                  type="password"
                  aria-label="图文文本模型 API Key"
                  value={secretDraft.image_text_api_key}
                  placeholder="留空保持不变"
                  onChange={(event) =>
                    onSecretDraftChange({ ...secretDraft, image_text_api_key: event.target.value })
                  }
                />
              </Field>
              <p className="settings-note">模型和思考强度在进入图文项目后选择或填写。</p>
            </>
          ) : null}
          {tab === "voice" ? (
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
          ) : null}
          {tab === "montage" ? (
            <>
              <Field label="素材库目录">
                <input value={draft.media_catalog_path || ""} placeholder="catalog.db 路径" onChange={setText("media_catalog_path")} />
              </Field>
              <Field label="媒体素材目录">
                <input value={draft.media_root || ""} placeholder="本地媒体根目录" onChange={setText("media_root")} />
              </Field>
              <Field label="素材索引">
                <input value={draft.media_index_path || ""} placeholder="媒体索引文件" onChange={setText("media_index_path")} />
              </Field>
              <Field label="剪映草稿目录">
                <input value={draft.jianying_root || ""} placeholder="剪映草稿根目录" onChange={setText("jianying_root")} />
              </Field>
              <Field label="FFmpeg 路径">
                <input value={draft.ffmpeg_path || ""} placeholder="ffmpeg 可执行文件" onChange={setText("ffmpeg_path")} />
              </Field>
              <Field label="FFprobe 路径">
                <input value={draft.ffprobe_path || ""} placeholder="ffprobe 可执行文件" onChange={setText("ffprobe_path")} />
              </Field>
              <Field label="混剪机器配置" wide>
                <input value={draft.machine_profile_path || ""} placeholder="machine profile JSON" onChange={setText("machine_profile_path")} />
              </Field>
            </>
          ) : null}
          {tab === "system" ? (
            <>
              <Field label="默认模型">
                <input
                  value={draft.codex_default_model || ""}
                  onChange={(event) => onDraftChange({ ...draft, codex_default_model: event.target.value })}
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
              <Field label="Codex CLI 路径">
                <input value={draft.codex_binary_path || ""} placeholder="codex 可执行文件" onChange={setText("codex_binary_path")} />
              </Field>
              <label className="settings-field checkbox-field settings-field--wide">
                <input
                  type="checkbox"
                  checked={draft.app_server_enabled || false}
                  onChange={(event) =>
                    onDraftChange({ ...draft, app_server_enabled: event.target.checked })
                  }
                />
                启用任务实时交互服务
              </label>
              <Field label="Codex 工作目录白名单" wide>
                <textarea
                  value={(draft.codex_workspace_roots || []).join("\n")}
                  onChange={(event) =>
                    onDraftChange({
                      ...draft,
                      codex_workspace_roots: event.target.value
                        .split("\n")
                        .map((value) => value.trim())
                        .filter(Boolean),
                    })
                  }
                />
              </Field>
            </>
          ) : null}
        </div>
        {httpOnTab ? (
          <p className="settings-feedback settings-feedback--danger" role="alert">
            当前地址使用 HTTP，密钥会明文传输。
          </p>
        ) : null}
        <button className="save-settings" type="submit">
          保存设置
        </button>
      </form>
    </div>
  );
}
