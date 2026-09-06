import { X } from "lucide-react";
import { useEffect, useState } from "react";
import type {
  Account,
  AccountOverrides,
  BgmLibrary,
  BgmTrack,
  JianyingDraftsView,
  JianyingStyleExtraction,
  MontageStyle,
} from "../types";
import { fontOptions, montageStyleDefaults, normalizeStyleColor, withMontageStyleDefaults } from "../settings/montageStyle";
import "./account-overrides.css";

type Api = (path: string, init?: RequestInit) => Promise<Response>;

type AccountOverridesDialogProps = {
  account: Account;
  api: Api;
  /** 全局混剪样式，启用账号专属样式时作为起始值。 */
  globalStyle?: MontageStyle;
  onSaved: (account: Account) => void;
  onClose: () => void;
};

// 账号制作配置弹窗：账号专属混剪样式 + 配音音色。矩阵账号靠字体、颜色、
// BGM 和音色的差异拉开内容指纹，避免多号同模板被平台查重连坐。
export function AccountOverridesDialog({ account, api, globalStyle, onSaved, onClose }: AccountOverridesDialogProps) {
  const saved = account.overrides ?? undefined;
  const [styleEnabled, setStyleEnabled] = useState(Boolean(saved?.montage_style));
  const [style, setStyle] = useState<Required<MontageStyle>>(
    withMontageStyleDefaults(saved?.montage_style ?? globalStyle),
  );
  const [auraVoice, setAuraVoice] = useState(saved?.voice?.aurastd_voice_id ?? "");
  const [volcVoice, setVolcVoice] = useState(saved?.voice?.volc_speech_speaker_id ?? "");
  const [bgmTracks, setBgmTracks] = useState<BgmTrack[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // 从剪映草稿导入：草稿列表 → 选一个 → 抽样式预览 → 应用到上面的表单。
  const [draftsView, setDraftsView] = useState<JianyingDraftsView | null>(null);
  const [draftsError, setDraftsError] = useState("");
  const [draftName, setDraftName] = useState("");
  const [extracting, setExtracting] = useState(false);
  const [extraction, setExtraction] = useState<JianyingStyleExtraction | null>(null);

  const loadDrafts = async () => {
    setDraftsError("");
    try {
      const response = await api("/api/jianying-drafts");
      if (!response.ok) {
        const body = (await response.json().catch(() => ({}))) as { message?: string };
        throw new Error(body.message || `草稿列表读取失败：${response.status}`);
      }
      const view = (await response.json()) as JianyingDraftsView;
      setDraftsView(view);
      // 默认选中和账号同名前缀的最新草稿，省一次翻找。
      const mine = view.drafts.find((item) => item.name.startsWith(`${account.name}_`));
      setDraftName((mine ?? view.drafts[0])?.name ?? "");
    } catch (err) {
      setDraftsError(err instanceof Error ? err.message : "草稿列表读取失败。");
    }
  };

  const extractFromDraft = async () => {
    if (!draftName || extracting) return;
    setExtracting(true);
    setDraftsError("");
    try {
      const response = await api("/api/jianying-drafts/extract-style", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: draftName }),
      });
      if (!response.ok) {
        const body = (await response.json().catch(() => ({}))) as { message?: string };
        throw new Error(body.message || `样式读取失败：${response.status}`);
      }
      setExtraction((await response.json()) as JianyingStyleExtraction);
    } catch (err) {
      setDraftsError(err instanceof Error ? err.message : "样式读取失败。");
    } finally {
      setExtracting(false);
    }
  };

  const [importingBGM, setImportingBGM] = useState(false);
  const importBGM = async () => {
    if (!draftName || importingBGM) return;
    setImportingBGM(true);
    setDraftsError("");
    try {
      const response = await api("/api/jianying-drafts/import-bgm", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: draftName }),
      });
      if (!response.ok) {
        const body = (await response.json().catch(() => ({}))) as { message?: string };
        throw new Error(body.message || `BGM 导入失败：${response.status}`);
      }
      const result = (await response.json()) as { bgm_id: string; name: string };
      const library = await api("/api/bgm-library");
      if (library.ok) {
        const payload = (await library.json()) as BgmLibrary;
        setBgmTracks(payload.tracks || []);
      }
      patchStyle({ bgm_id: result.bgm_id });
      setStyleEnabled(true);
    } catch (err) {
      setDraftsError(err instanceof Error ? err.message : "BGM 导入失败。");
    } finally {
      setImportingBGM(false);
    }
  };

  const applyExtraction = () => {
    if (!extraction) return;
    // 以策略默认为底整体覆盖（后端省略的零值字段也要回到默认，比如描边隐藏
    // 开关）；草稿里读不到的 BGM 保留表单现值。
    setStyle((current) => withMontageStyleDefaults({
      ...montageStyleDefaults,
      ...extraction.style,
      bgm_id: current.bgm_id,
      bgm_volume: current.bgm_volume,
    }));
    setStyleEnabled(true);
  };

  useEffect(() => {
    let active = true;
    void api("/api/bgm-library").then(async (response) => {
      if (!response.ok) return;
      const payload = (await response.json()) as BgmLibrary;
      if (active) setBgmTracks(payload.tracks || []);
    }).catch(() => undefined);
    return () => { active = false; };
  }, [api]);

  const patchStyle = (patch: Partial<MontageStyle>) => {
    const next = { ...patch };
    if (next.caption_color) next.caption_color = normalizeStyleColor(next.caption_color);
    if (next.keyword_color) next.keyword_color = normalizeStyleColor(next.keyword_color);
    if (next.title_color) next.title_color = normalizeStyleColor(next.title_color);
    if (next.subtitle_color) next.subtitle_color = normalizeStyleColor(next.subtitle_color);
    for (const key of ["caption_border_color", "caption_bg_color", "title_bg_color", "subtitle_bg_color"] as const) {
      if (next[key]) next[key] = normalizeStyleColor(next[key] as string);
    }
    setStyle((current) => ({ ...current, ...next }));
  };

  const save = async () => {
    setBusy(true);
    setError("");
    const body: AccountOverrides = {};
    if (styleEnabled) body.montage_style = withMontageStyleDefaults(style);
    if (auraVoice.trim() || volcVoice.trim()) {
      body.voice = {
        aurastd_voice_id: auraVoice.trim() || undefined,
        volc_speech_speaker_id: volcVoice.trim() || undefined,
      };
    }
    try {
      const response = await api(`/api/accounts/${account.id}/overrides`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!response.ok) throw new Error(`overrides save failed: ${response.status}`);
      const updated = (await response.json()) as Account;
      onSaved(updated);
      onClose();
    } catch {
      setError("保存失败，请重试。");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      className="source-script-dialog-backdrop"
      role="presentation"
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <section
        className="source-script-dialog account-overrides-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="account-overrides-title"
      >
        <header>
          <div>
            <span className="panel-kicker">ACCOUNT PROFILE</span>
            <h2 id="account-overrides-title">账号制作配置 · {account.name}</h2>
            <p>给这个账号配置专属混剪样式和配音音色，和其他账号拉开内容指纹；留空即跟随全局设置。</p>
          </div>
          <button type="button" className="source-script-dialog__close" aria-label="关闭账号配置" onClick={onClose}><X size={20} aria-hidden="true" /></button>
        </header>
        <div className="account-overrides-body">
          <p className="account-overrides-section">配音音色</p>
          <div className="account-overrides-grid">
            <label>
              AuraStudio 克隆音色 ID
              <input
                type="text"
                aria-label="账号 AuraStudio 音色 ID"
                value={auraVoice}
                onChange={(event) => setAuraVoice(event.target.value)}
                placeholder="留空用全局音色"
              />
            </label>
            <label>
              火山语音音色 ID
              <input
                type="text"
                aria-label="账号火山音色 ID"
                value={volcVoice}
                onChange={(event) => setVolcVoice(event.target.value)}
                placeholder="留空用全局音色"
              />
            </label>
          </div>
          <p className="account-overrides-section">从剪映草稿导入样式</p>
          <div className="account-overrides-import">
            {draftsView ? (
              <>
                <div className="account-overrides-import__row">
                  <select
                    aria-label="选择剪映草稿"
                    value={draftName}
                    onChange={(event) => { setDraftName(event.target.value); setExtraction(null); }}
                  >
                    {draftsView.drafts.map((item) => (
                      <option key={item.name} value={item.name}>
                        {item.name}{item.encrypted && !draftsView.decrypt_tool_available ? "（加密，缺解密工具）" : ""}
                      </option>
                    ))}
                  </select>
                  <button
                    type="button"
                    className="header-button"
                    disabled={!draftName || extracting}
                    onClick={() => void extractFromDraft()}
                  >
                    {extracting ? "读取中…" : "读取样式"}
                  </button>
                </div>
                {draftsView.decrypt_tip ? <p className="account-overrides-hint">{draftsView.decrypt_tip}</p> : null}
                {extraction ? (
                  <div className="account-overrides-preview" aria-label="草稿样式预览">
                    <ul>
                      <li>
                        字幕：{extraction.style.caption_font || "默认字体"} {extraction.style.caption_size ?? "—"} 号，
                        颜色 {extraction.style.caption_color || "—"}
                        {extraction.style.caption_border_hidden ? "，无描边" : extraction.style.caption_border_color ? `，描边 ${extraction.style.caption_border_color}` : ""}
                        {extraction.style.caption_bg_color ? `，底色 ${extraction.style.caption_bg_color}` : ""}
                        {extraction.style.keywords_hidden ? "，不标关键词" : `，关键词 ${extraction.style.keyword_color ?? ""} ${extraction.style.keyword_size ?? ""} 号`}
                      </li>
                      <li>
                        标题：{extraction.style.title_hidden ? "隐藏" : `${extraction.style.title_font || "默认字体"} ${extraction.style.title_size ?? "—"} 号，颜色 ${extraction.style.title_color || "—"}${extraction.style.title_bg_color ? `，底色 ${extraction.style.title_bg_color}` : ""}`}
                      </li>
                      <li>
                        副标题：{extraction.style.subtitle_hidden ? "隐藏" : `${extraction.style.subtitle_font || "默认字体"} ${extraction.style.subtitle_size ?? "—"} 号，颜色 ${extraction.style.subtitle_color || "—"}${extraction.style.subtitle_bg_color ? `，底色 ${extraction.style.subtitle_bg_color}` : ""}`}
                      </li>
                      {extraction.bgm_name ? (
                        <li>BGM：{extraction.bgm_name}{extraction.bgm_path ? "（剪映缓存里有文件）" : "（缓存里没有文件）"}</li>
                      ) : null}
                      {extraction.notes.map((note) => <li key={note} className="account-overrides-note">{note}</li>)}
                    </ul>
                    <div className="account-overrides-import__row">
                      <button type="button" className="header-button" onClick={applyExtraction}>
                        应用到下方样式
                      </button>
                      {extraction.bgm_path ? (
                        <button
                          type="button"
                          className="header-button"
                          disabled={importingBGM}
                          onClick={() => void importBGM()}
                        >
                          {importingBGM ? "收录中…" : "把这首 BGM 收进曲库并选用"}
                        </button>
                      ) : null}
                    </div>
                  </div>
                ) : null}
              </>
            ) : (
              <button type="button" className="header-button" onClick={() => void loadDrafts()}>
                读取本地剪映草稿列表
              </button>
            )}
            {draftsError ? <p className="account-overrides-error">{draftsError}</p> : null}
          </div>
          <p className="account-overrides-section">
            <label className="account-overrides-toggle">
              <input
                type="checkbox"
                aria-label="启用账号专属混剪样式"
                checked={styleEnabled}
                onChange={(event) => setStyleEnabled(event.target.checked)}
              />
              启用账号专属混剪样式
            </label>
          </p>
          {styleEnabled ? (
            <div className="account-overrides-grid">
              <label>
                字幕字体
                <select
                  aria-label="账号字幕字体"
                  value={style.caption_font}
                  onChange={(event) => patchStyle({ caption_font: event.target.value })}
                >
                  {fontOptions(style.caption_font).map((font) => (
                    <option key={font} value={font}>{font}</option>
                  ))}
                </select>
              </label>
              <label>
                字幕字号
                <input
                  type="number"
                  aria-label="账号字幕字号"
                  min={6}
                  max={40}
                  step={0.1}
                  value={style.caption_size}
                  onChange={(event) => patchStyle({ caption_size: Number(event.target.value) })}
                />
              </label>
              <label>
                字幕颜色
                <input
                  type="color"
                  aria-label="账号字幕颜色"
                  value={style.caption_color}
                  onChange={(event) => patchStyle({ caption_color: event.target.value })}
                />
              </label>
              <label>
                关键词颜色
                <input
                  type="color"
                  aria-label="账号关键词颜色"
                  value={style.keyword_color}
                  onChange={(event) => patchStyle({ keyword_color: event.target.value })}
                />
              </label>
              <label className="account-overrides-toggle">
                <input
                  type="checkbox"
                  aria-label="账号标注字幕关键词"
                  checked={!style.keywords_hidden}
                  onChange={(event) => patchStyle({ keywords_hidden: !event.target.checked })}
                />
                标注字幕关键词
              </label>
              <label className="account-overrides-toggle">
                <input
                  type="checkbox"
                  aria-label="账号字幕描边"
                  checked={!style.caption_border_hidden}
                  onChange={(event) => patchStyle({ caption_border_hidden: !event.target.checked })}
                />
                字幕描边
              </label>
              <label>
                字幕描边颜色（空=默认）
                <input
                  type="text"
                  aria-label="账号字幕描边颜色"
                  value={style.caption_border_color}
                  placeholder="#4A4238"
                  disabled={style.caption_border_hidden}
                  onChange={(event) => patchStyle({ caption_border_color: event.target.value })}
                />
              </label>
              <label>
                字幕底色（空=无底色）
                <input
                  type="text"
                  aria-label="账号字幕底色"
                  value={style.caption_bg_color}
                  placeholder="#FFDE00"
                  onChange={(event) => patchStyle({ caption_bg_color: event.target.value })}
                />
              </label>
              <label>
                字幕底色透明度（0 视为 1）
                <input
                  type="number"
                  aria-label="账号字幕底色透明度"
                  min={0}
                  max={1}
                  step={0.05}
                  value={style.caption_bg_alpha}
                  disabled={!style.caption_bg_color}
                  onChange={(event) => patchStyle({ caption_bg_alpha: Number(event.target.value) })}
                />
              </label>
              <label>
                主标题字体（空=默认）
                <select
                  aria-label="账号主标题字体"
                  value={style.title_font}
                  onChange={(event) => patchStyle({ title_font: event.target.value })}
                >
                  <option value="">默认</option>
                  {fontOptions(style.title_font).map((font) => (
                    <option key={font} value={font}>{font}</option>
                  ))}
                </select>
              </label>
              <label>
                主标题颜色
                <input
                  type="color"
                  aria-label="账号主标题颜色"
                  value={style.title_color}
                  onChange={(event) => patchStyle({ title_color: event.target.value })}
                />
              </label>
              <label>
                主标题底色（空=无）
                <input
                  type="text"
                  aria-label="账号主标题底色"
                  value={style.title_bg_color}
                  placeholder="#A74F59"
                  onChange={(event) => patchStyle({ title_bg_color: event.target.value })}
                />
              </label>
              <label>
                副标题颜色
                <input
                  type="color"
                  aria-label="账号副标题颜色"
                  value={style.subtitle_color}
                  onChange={(event) => patchStyle({ subtitle_color: event.target.value })}
                />
              </label>
              <label>
                副标题底色（空=无）
                <input
                  type="text"
                  aria-label="账号副标题底色"
                  value={style.subtitle_bg_color}
                  placeholder="#000000"
                  onChange={(event) => patchStyle({ subtitle_bg_color: event.target.value })}
                />
              </label>
              <label>
                BGM
                <select
                  aria-label="账号 BGM"
                  value={style.bgm_id}
                  onChange={(event) => patchStyle({ bgm_id: event.target.value })}
                >
                  <option value="builtin">内置验证曲目</option>
                  {bgmTracks.map((track) => (
                    <option key={track.id} value={track.id}>{track.name}</option>
                  ))}
                </select>
              </label>
              <label>
                BGM 音量（仅自选曲目生效，内置曲目音量固定）
                <input
                  type="number"
                  aria-label="账号 BGM 音量"
                  min={0.05}
                  max={1}
                  step={0.05}
                  value={style.bgm_volume}
                  disabled={style.bgm_id === "builtin"}
                  onChange={(event) => patchStyle({ bgm_volume: Number(event.target.value) })}
                />
              </label>
            </div>
          ) : (
            <p className="account-overrides-hint">未启用时该账号混剪跟随全局样式（设置 → 混剪样式）。</p>
          )}
        </div>
        <footer>
          <small>{error}</small>
          <div>
            <button type="button" className="source-script-dialog__cancel" onClick={onClose}>取消</button>
            <button
              type="button"
              className="source-script-dialog__save"
              onClick={() => void save()}
              disabled={busy}
              aria-busy={busy}
            >
              {busy ? "正在保存…" : "保存账号配置"}
            </button>
          </div>
        </footer>
      </section>
    </div>
  );
}
