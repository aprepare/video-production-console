import { useEffect, useState } from "react";
import type { Account, AccountOverrides, BgmLibrary, BgmTrack, MontageStyle } from "../types";
import { montageFonts, normalizeStyleColor, withMontageStyleDefaults } from "../settings/montageStyle";
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
          <button type="button" className="source-script-dialog__close" aria-label="关闭账号配置" onClick={onClose}>×</button>
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
                  {montageFonts.map((font) => (
                    <option key={font} value={font}>{font}</option>
                  ))}
                </select>
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
                副标题颜色
                <input
                  type="color"
                  aria-label="账号副标题颜色"
                  value={style.subtitle_color}
                  onChange={(event) => patchStyle({ subtitle_color: event.target.value })}
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
