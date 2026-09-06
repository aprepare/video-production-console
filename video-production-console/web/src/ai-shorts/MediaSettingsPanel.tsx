import { useEffect, useState } from "react";
import type { Api } from "./api";

type Endpoint = { base_url: string; has_api_key: boolean };
type Settings = { image: Endpoint; video: Endpoint; image_concurrency: number; video_concurrency: number };
const empty = (): Endpoint => ({ base_url: "", has_api_key: false });

export function MediaSettingsPanel({ api, disabled }: { api: Api; disabled?: boolean }) {
  const [settings, setSettings] = useState<Settings>({ image: empty(), video: empty(), image_concurrency:20, video_concurrency:6 });
  const [saved, setSaved] = useState<Settings>({ image: empty(), video: empty(), image_concurrency:20, video_concurrency:6 });
  const [keys, setKeys] = useState({ image: "", video: "" });
  const [clear, setClear] = useState({ image: false, video: false });
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState("");
  useEffect(() => {
    let cancelled = false;
    void api("/api/ai-shorts/media-settings").then(async response => {
      if (!response.ok) throw new Error("接口配置读取失败，请确认已启动新版程序。");
      const value = await response.json() as Settings;
      if (!value.image || !value.video) throw new Error("接口配置格式不正确，请刷新新版页面。");
      if (!cancelled) { setSettings(value); setSaved(value); setLoaded(true); }
    }).catch(error => { if (!cancelled) setMessage(error instanceof Error ? error.message : "接口配置读取失败"); });
    return () => { cancelled = true; };
  }, [api]);
  const save = async () => {
    setSaving(true); setMessage("");
    try {
      const body = { ...Object.fromEntries((["image", "video"] as const).map(kind => [kind, { base_url: settings[kind].base_url.trim(), api_key: keys[kind], clear_api_key: clear[kind] }])), image_concurrency:settings.image_concurrency, video_concurrency:settings.video_concurrency };
      const response = await api("/api/ai-shorts/media-settings", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
      const value = await response.json();
      if (!response.ok) throw new Error(value.message || "保存接口失败");
      setSettings(value); setSaved(value); setKeys({ image: "", video: "" }); setClear({ image: false, video: false });
      setMessage("接口已保存，后续生成使用新配置。");
    } catch (error) { setMessage(error instanceof Error ? error.message : "保存接口失败"); }
    finally { setSaving(false); }
  };
  return <section className="ai-shorts__card">
    <h2>图片／视频接口</h2>
    <p className="ai-shorts__muted">所有AI短片共用；图片和视频可使用不同服务商。模型名称在项目中单独选择。</p>
    <fieldset disabled={disabled || saving || !loaded} style={{ border: 0, padding: 0, minWidth: 0 }}>
      {(["image", "video"] as const).map(kind => {
        const label = kind === "image" ? "图片" : "视频";
        const changed = settings[kind].base_url.trim().replace(/\/+$/, "") !== saved[kind].base_url;
        return <div key={kind}>
          <label>{label}接口 Base URL<input aria-label={`${label}接口URL`} value={settings[kind].base_url} placeholder="留空沿用二创接口，如 https://example.com/v1" onChange={e => setSettings({ ...settings, [kind]: { ...settings[kind], base_url: e.target.value } })} /></label>
          <label>{label} API Key<input aria-label={`${label}API Key`} type="password" autoComplete="new-password" value={keys[kind]} placeholder={settings[kind].has_api_key && !changed && !clear[kind] ? "已配置，留空保留" : "填写此接口对应的密钥"} onChange={e => setKeys({ ...keys, [kind]: e.target.value })} /></label>
          <label>{label}并发<input aria-label={`${label}并发`} type="number" min={1} max={64} step={1} value={settings[`${kind}_concurrency`]} onChange={e => setSettings({ ...settings, [`${kind}_concurrency`]:Number(e.target.value) })} /></label>
          {settings[kind].has_api_key && <label className="ai-shorts__toggle"><input type="checkbox" checked={clear[kind]} onChange={e => setClear({ ...clear, [kind]: e.target.checked })} />清除{label}已存密钥</label>}
        </div>;
      })}
      <p className="ai-shorts__muted">并发可填1～64，下一轮生成生效。不同镜头可同时生图、生视频；同一镜先出图再出视频。填写Base URL，不加生成路径。换地址会清除该接口旧密钥；密钥加密保存。当前支持兼容图片接口和Grok视频任务接口，服务商需支持相同协议。</p>
      <button type="button" className="header-button" onClick={save}>{saving ? "保存中…" : "保存图片／视频接口"}</button>
    </fieldset>
    {message && <p className="ai-shorts__muted">{message}</p>}
  </section>;
}
