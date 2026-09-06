import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { RoleModelFields } from "./RoleModelFields";
import { fetchRemixLabRerunOptions, rerunRemixLabRun, type RemixLabApi, type RemixLabRerunInput, type RemixLabRerunResult } from "./api";

export function RerunDialog({ api, runID, onClose, onCreated }: {
  api: RemixLabApi; runID: string; onClose: () => void; onCreated: (result: RemixLabRerunResult) => void;
}) {
  const [settings, setSettings] = useState<RemixLabRerunInput | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const lock = useRef(false);
  const dialog = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let cancelled = false;
    void fetchRemixLabRerunOptions(api, runID).then(value => {
      if (!cancelled) setSettings({...value,references:[]});
    }).catch(reason => { if (!cancelled) setError(reason instanceof Error ? reason.message : "配置读取失败。"); });
    return () => { cancelled = true; };
  }, [api, runID]);
  useEffect(() => { if (settings) dialog.current?.querySelector<HTMLInputElement>("input")?.focus(); }, [!!settings]);
  async function start() {
    if (!settings || lock.current) return;
    lock.current = true; setBusy(true); setError("");
    try { onCreated(await rerunRemixLabRun(api, runID, settings)); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "新一轮文案提交失败。"); }
    finally { lock.current = false; setBusy(false); }
  }
  return createPortal(
    <div className="modal-backdrop" onClick={() => { if (!busy) onClose(); }}>
      <div ref={dialog} className="preview-modal remix-lab-modal remix-lab-modal--wide" role="dialog" aria-modal="true" aria-labelledby="rerun-title"
        onClick={event => event.stopPropagation()} onKeyDown={event => {
          if (event.key === "Escape" && !busy) onClose();
          if (event.key === "Tab") {
            const items = dialog.current?.querySelectorAll<HTMLElement>("button:not(:disabled), input:not(:disabled), select:not(:disabled)");
            if (!items?.length) return;
            const first=items[0], last=items[items.length-1];
            if (event.shiftKey && document.activeElement===first) { event.preventDefault(); last.focus(); }
            if (!event.shiftKey && document.activeElement===last) { event.preventDefault(); first.focus(); }
          }
        }}>
        <div className="modal-head"><h2 id="rerun-title">重新生成文案</h2><button type="button" className="close" aria-label="关闭重跑配置" disabled={busy} onClick={onClose}>×</button></div>
        <p className="remix-lab-muted">沿用当前项目原文与账号最新版提示词，直接生成写手稿并审稿。模型选择仅用于新一轮，历史版本保留，完成后等待你确认。</p>
        {error ? <p role="alert">{error}</p> : null}
        {!settings && !error ? <p>正在读取模型配置…</p> : null}
        {settings ? <div className="remix-rerun-models">
          {([ ["planner", "二创策划"], ["writer", "写手"], ["reviewer", "审稿"] ] as const).map(([key, label]) => settings[key] ? (
            <fieldset key={key} disabled={busy} className="remix-rerun-model"><legend>{label}</legend>
              <RoleModelFields id={`rerun-${key}`} label={label} modelLabel={`重跑${label}模型`} value={settings[key]!}
                onChange={value => setSettings(current => current && ({...current, [key]: value}))} />
            </fieldset>
          ) : null)}
        </div> : null}
        <div className="remix-lab-modal__actions">
          <button type="button" className="header-button" disabled={busy} onClick={onClose}>取消</button>
          <button type="button" className="primary-button" disabled={busy || settings?.references?.some(value=>!value.model.trim()) || !settings?.writer.model.trim() || !settings?.reviewer.model.trim() || (!!settings?.planner && !settings.planner.model.trim())} onClick={() => void start()}>{busy ? "提交中…" : "开始新一轮"}</button>
        </div>
      </div>
    </div>, document.body,
  );
}
