import { useMemo } from "react";
import type { FormEvent } from "react";
import { X } from "lucide-react";
import { messageTone } from "../messageTone";
import { reasoningEfforts } from "../taskModel";
import type { ReasoningEffort } from "../taskModel";
import type { PublicSettings, PublicStringSettingKey, Settings } from "../types";

const settingFields: Array<[PublicStringSettingKey, string, string]> = [
  ["baokuan_base_url", "爆款库地址", "http://127.0.0.1:2022"],
  ["obsidian_vault", "Obsidian Vault", "本地 Vault 目录"],
  ["topic_cards_dir", "选题卡目录", "Vault 内选题卡目录"],
  ["grok_base_url", "Grok 服务地址", "OpenAI 兼容 API 地址"],
  ["grok_model", "Grok 模型", "模型名称"],
  ["image_base_url", "生图服务地址", "OpenAI 兼容 Base URL"],
  ["image_model", "生图模型", "gpt-image-2"],
  ["image_text_base_url", "图文文本模型地址", "OpenAI 兼容 Chat Base URL"],
  ["image_text_model", "图文文本模型", "用于分段建议和提示词"],
  ["codex_binary_path", "Codex CLI 路径", "codex 可执行文件路径"],
  ["media_index_path", "素材索引", "媒体索引文件"],
  ["media_root", "媒体素材目录", "本地媒体根目录"],
  ["jianying_root", "剪映草稿目录", "剪映草稿根目录"],
  ["machine_profile_path", "混剪机器配置", "machine profile JSON 文件"],
  ["volc_speech_speaker_id", "火山音色 ID", "复刻音色 ID"],
  ["volc_speech_resource_id", "火山语音资源 ID", "seed-icl-2.0"],
];

// 素材库（混剪媒体智能库）设置组：目录、FFmpeg 与分析模型。
const mediaLibraryFields: Array<[PublicStringSettingKey, string, string]> = [
  ["media_catalog_path", "素材库目录", "媒体素材目录内的 catalog.db 路径"],
  ["ffmpeg_path", "FFmpeg 路径", "ffmpeg 可执行文件路径"],
  ["ffprobe_path", "FFprobe 路径", "ffprobe 可执行文件路径"],
  ["vision_base_url", "视觉分析服务地址", "OpenAI 兼容 Base URL"],
  ["vision_model", "视觉分析模型", "用于镜头画面理解"],
  ["embedding_base_url", "向量服务地址", "OpenAI 兼容 Base URL"],
  ["embedding_model", "向量模型", "用于镜头语义检索"],
];

const restartFieldLabels: Partial<Record<keyof PublicSettings, string>> = {
  listen_addr: "监听地址",
  data_root: "数据目录",
  baokuan_base_url: "爆款库地址",
  baokuan_mcp_executable: "爆款库连接程序",
  obsidian_vault: "Obsidian 目录",
  topic_cards_dir: "选题卡目录",
  grok_base_url: "Grok 服务地址",
  grok_model: "Grok 模型",
  image_base_url: "生图服务地址",
  image_model: "生图模型",
  image_text_base_url: "图文文本模型地址",
  image_text_model: "图文文本模型",
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
  vision_base_url: "视觉分析服务地址",
  vision_model: "视觉分析模型",
  embedding_base_url: "向量服务地址",
  embedding_model: "向量模型",
  pexels_api_base_url: "Pexels API 地址",
  pixabay_api_base_url: "Pixabay API 地址",
  max_external_results_per_query: "外部搜索单次结果上限",
};

type SecretDraft = {
  grok_api_key: string;
  pexels_api_key: string;
  volc_speech_api_key: string;
  image_api_key: string;
  image_text_api_key: string;
  vision_api_key: string;
  embedding_api_key: string;
  pixabay_api_key: string;
};

const secretFields: Array<[keyof SecretDraft, string]> = [
  ["grok_api_key", "Grok API 密钥"],
  ["pexels_api_key", "Pexels API 密钥"],
  ["volc_speech_api_key", "火山语音 API Key"],
  ["image_api_key", "生图 API Key"],
  ["image_text_api_key", "图文文本模型 API Key"],
  ["vision_api_key", "视觉分析 API Key"],
  ["embedding_api_key", "向量模型 API Key"],
  ["pixabay_api_key", "Pixabay API 密钥"],
];

const imageStyles = [
  ["finance_documentary", "财经纪实插画"],
  ["red_ink", "赤墨风"],
  ["old_newspaper", "旧报档案风"],
  ["ledger_investigation", "账本调查风"],
  ["dark_crisis", "暗黑危机风"],
  ["city_era", "城市时代感"],
  ["blackboard", "黑板讲解风"],
] as const;

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
  // Only fields that differ from what the running process loaded still need a
  // restart, so the banner names those rather than every configured field.
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
        <p className="settings-note">密钥不会回显；留空表示保持现有值不变。</p>
        <p className="settings-note">
          默认值只影响之后新建的任务，不会修改运行中任务，也不会改写本机 Codex 全局配置。
        </p>
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
            <strong>配置已保存，重启控制台后生效</strong>
            <p>
              并发数等热更新项已经立即生效；路径、任务实时交互服务、密钥等启动配置会在重启后启用。
            </p>
            {restartChangedFields.length ? (
              <p>等待重启：{restartChangedFields.join("、")}</p>
            ) : null}
          </div>
        ) : null}
        <label className="settings-field">
          默认模型
          <input
            value={draft.codex_default_model || ""}
            onChange={(event) =>
              onDraftChange({ ...draft, codex_default_model: event.target.value })
            }
          />
        </label>
        <label className="settings-field">
          默认推理强度
          <select
            value={draft.codex_default_reasoning_effort || "medium"}
            onChange={(event) =>
              onDraftChange({
                ...draft,
                codex_default_reasoning_effort: event.target.value as ReasoningEffort,
              })
            }
          >
            {reasoningEfforts.map((effort) => (
              <option key={effort} value={effort}>
                {effort}
              </option>
            ))}
          </select>
        </label>
        <label className="settings-field">
          同时运行任务数
          <select
            value={draft.max_codex_concurrency}
            onChange={(event) =>
              onDraftChange({ ...draft, max_codex_concurrency: Number(event.target.value) })
            }
          >
            {[1, 2, 3, 4].map((value) => (
              <option key={value} value={value}>
                {value}
              </option>
            ))}
          </select>
        </label>
        <label className="settings-field">
          同时生成图片数
          <select
            value={draft.max_image_concurrency || 3}
            onChange={(event) => onDraftChange({ ...draft, max_image_concurrency: Number(event.target.value) })}
          >
            {Array.from({ length: 18 }, (_, index) => index + 1).map((value) => <option key={value} value={value}>{value}</option>)}
          </select>
          <small>图文模式批量生成的最大并发，最多 18。</small>
        </label>
        <label className="settings-field">
          默认图片比例
          <select
            value={draft.default_image_ratio || "3:4"}
            onChange={(event) => onDraftChange({ ...draft, default_image_ratio: event.target.value as PublicSettings["default_image_ratio"] })}
          >
            {["3:4", "4:3", "9:16", "1:1"].map((value) => <option key={value} value={value}>{value}</option>)}
          </select>
        </label>
        <label className="settings-field">
          默认视觉风格
          <select
            value={draft.default_image_style || "finance_documentary"}
            onChange={(event) => onDraftChange({ ...draft, default_image_style: event.target.value })}
          >
            {imageStyles.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
        </label>
        <label className="settings-field checkbox-field">
          <input
            type="checkbox"
            checked={draft.app_server_enabled || false}
            onChange={(event) =>
              onDraftChange({ ...draft, app_server_enabled: event.target.checked })
            }
          />
          启用任务实时交互服务
          <small>用于正在运行的生产任务追问、回答与恢复；保存后需要重启控制台。</small>
        </label>
        <label className="settings-field">
          Codex 工作目录白名单（每行一个绝对路径）
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
        </label>
        {settingFields.map(([key, label, placeholder]) => (
          <label className="settings-field" key={key}>
            {label}
            <input
              value={draft[key] || ""}
              placeholder={placeholder}
              onChange={(event) => onDraftChange({ ...draft, [key]: event.target.value })}
            />
          </label>
        ))}
        <h3 className="settings-group-title">素材库</h3>
        <p className="settings-note">混剪素材智能库：目录建库、FFmpeg 探测与镜头分析模型。</p>
        {mediaLibraryFields.map(([key, label, placeholder]) => (
          <label className="settings-field" key={key}>
            {label}
            <input
              value={draft[key] || ""}
              placeholder={placeholder}
              onChange={(event) => onDraftChange({ ...draft, [key]: event.target.value })}
            />
          </label>
        ))}
        <div className="secret-grid">
          {secretFields.map(([key, label]) => (
            <label className="settings-field" key={key}>
              {label}
              <small>
                {settings?.secrets[key]?.configured ? "已配置，输入新值才会替换" : "未配置"}
              </small>
              <input
                type="password"
                value={secretDraft[key]}
                placeholder="留空保持不变"
                onChange={(event) =>
                  onSecretDraftChange({ ...secretDraft, [key]: event.target.value })
                }
              />
            </label>
          ))}
        </div>
        {(draft.image_base_url || "").toLowerCase().startsWith("http://") || (draft.image_text_base_url || "").toLowerCase().startsWith("http://") ? (
          <p className="settings-feedback settings-feedback--danger" role="alert">
            HTTP 会明文传输生图或图文文本模型 API Key。系统允许保存；仅在你已明确接受风险且信任该服务与网络链路时继续，其他情况请改用 HTTPS。
          </p>
        ) : null}
        <button className="save-settings" type="submit">
          保存设置
        </button>
      </form>
    </div>
  );
}
