import { useEffect, useState } from "react";
import { ModelSelect } from "../ModelSelect";
import { reasoningEfforts } from "../taskModel";
import type { ReasoningEffort } from "../taskModel";
import type { Settings } from "../types";

type Props = {
  api: (path: string, init?: RequestInit) => Promise<Response>;
  settings: Settings | null;
  onSaved: (settings: Settings) => void;
};

export function ImageTextConnectionSettings({ api, settings, onSaved }: Props) {
  const [baseURL, setBaseURL] = useState("");
  const [model, setModel] = useState("");
  const [reasoningEffort, setReasoningEffort] = useState<ReasoningEffort | "">("");
  const [apiKey, setAPIKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");

  const restart = async () => {
    if (busy) return;
    setBusy(true);
    try {
      const response = await api("/api/system/restart", { method: "POST" });
      if (!response.ok) {
        setMessage("重启请求失败，已保存的设置仍保留。请稍后重试。");
        return;
      }
      setMessage("控制台正在重启，页面稍后自动刷新…");
      const deadline = Date.now() + 60_000;
      await new Promise(resolve => setTimeout(resolve, 2000));
      while (Date.now() < deadline) {
        try {
          const health = await fetch("/api/health", { cache: "no-store" });
          if (health.ok) { window.location.reload(); return; }
        } catch { /* The process is still restarting. */ }
        await new Promise(resolve => setTimeout(resolve, 1000));
      }
      setMessage("重启等待超时，请刷新页面检查连接。");
    } catch {
      setMessage("重启请求失败，已保存的设置仍保留。请稍后重试。");
    } finally { setBusy(false); }
  };

  useEffect(() => {
    if (!settings) return;
    setBaseURL(settings.public.image_text_base_url || "");
    setModel(settings.public.image_text_model || "");
    setReasoningEffort(settings.public.image_text_reasoning_effort || "");
    setAPIKey("");
  }, [settings]);

  const save = async () => {
    if (!settings || busy) return;
    setBusy(true);
    setMessage("");
    try {
      const secrets = apiKey.trim() ? { image_text_api_key: apiKey.trim() } : {};
      const response = await api("/api/settings", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          public: {
            image_text_base_url: baseURL.trim(),
            image_text_model: model.trim(),
            image_text_reasoning_effort: reasoningEffort,
          },
          secrets,
        }),
      });
      if (!response.ok) {
        let detail = "文本模型连接保存失败，请检查地址、模型和密钥。";
        try {
          const payload = (await response.json()) as { message?: string };
          if (payload.message?.trim()) detail = payload.message.trim();
        } catch {
          // Keep the actionable fallback when the server did not return JSON.
        }
        setMessage(detail);
        return;
      }
      const next = await response.json() as Settings;
      onSaved(next);
      setAPIKey("");
      setMessage(next.restart_required ? "文本模型连接已保存。重启后生效。" : "文本模型连接已保存并生效。");
    } catch {
      setMessage("文本模型连接保存失败，请稍后重试。");
    } finally {
      setBusy(false);
    }
  };

  return (
    <details className="image-text-connection">
      <summary>
        <span>文本模型连接</span>
        <small>{settings?.secrets.image_text_api_key?.configured ? "密钥已配置" : "密钥未配置"}</small>
      </summary>
      <div className="image-text-connection__body">
        <p>图文分段、提示词和项目名称使用这套 OpenAI 兼容连接。单个项目仍可在下方覆盖模型。</p>
        <div className="image-text-connection__grid">
          <label>
            图文文本模型地址
            <input aria-label="图文文本模型地址" value={baseURL} placeholder="https://example.com/v1" onChange={(event) => setBaseURL(event.target.value)} />
          </label>
          <label>
            图文默认文本模型
            <ModelSelect aria-label="图文默认文本模型" value={model} emptyLabel="不设置" onChange={setModel} />
          </label>
          <label>
            图文文本思考强度
            <select aria-label="图文文本思考强度" value={reasoningEffort} onChange={(event) => setReasoningEffort(event.target.value as ReasoningEffort | "")}>
              <option value="">不设置</option>
              {reasoningEfforts.map((effort) => <option key={effort} value={effort}>{effort}</option>)}
            </select>
          </label>
          <label>
            图文文本模型 API Key
            <small>{settings?.secrets.image_text_api_key?.configured ? "已配置，留空保持不变" : "未配置"}</small>
            <input type="password" aria-label="图文文本模型 API Key" value={apiKey} placeholder="留空保持不变" onChange={(event) => setAPIKey(event.target.value)} />
          </label>
        </div>
        <div className="image-text-connection__actions">
          <button type="button" disabled={busy || !settings} onClick={() => void save()}>{busy ? "正在保存…" : "保存文本模型连接"}</button>
          {settings?.restart_required ? <button type="button" disabled={busy} onClick={() => void restart()}>重启使设置生效</button> : null}
          {message ? <span role="status">{message}</span> : null}
        </div>
      </div>
    </details>
  );
}
