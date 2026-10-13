import { useCallback, useEffect, useState } from "react";
import { CheckCircle2, CircleAlert, CircleDashed, ClipboardCopy, KeyRound, Loader2, RefreshCw, ScanSearch, Settings2, TriangleAlert, Wrench } from "lucide-react";
import "../shell/studio-page.css";
import "./setup.css";

export type SetupApi = (path: string, init?: RequestInit) => Promise<Response>;

export type SetupItemStatus = "ok" | "warn" | "missing" | "error";
export type SetupItem = {
  id: string;
  title: string;
  required: boolean;
  status: SetupItemStatus;
  detail: string;
  fix: string;
};
export type SetupStatus = { ready: boolean; items: SetupItem[] };

type JianyingCandidate = {
  draft_root: string;
  install_dir: string;
  version: string;
  verified: boolean;
  custom_draft_path: string;
  draft_count: number;
  has_root_meta: boolean;
  source: string;
};
type JianyingDetection = { candidates: JianyingCandidate[]; running: boolean; configured_root: string; configured_matches: boolean };
type ActivationResult = { applied_public: string[]; applied_secrets: string[]; skipped: string[]; issued_to: string; expires_at: string; restart_required: boolean };

const statusMeta: Record<SetupItemStatus, { label: string; icon: typeof CheckCircle2 }> = {
  ok: { label: "就绪", icon: CheckCircle2 },
  warn: { label: "可用，有提醒", icon: TriangleAlert },
  missing: { label: "未配置", icon: CircleDashed },
  error: { label: "有问题", icon: CircleAlert },
};

async function readError(response: Response, fallback: string) {
  try {
    const body = await response.json();
    return typeof body?.message === "string" && body.message ? body.message : fallback;
  } catch {
    return fallback;
  }
}

type Props = {
  api: SetupApi;
  onOpenSettings: () => void;
  onStatusChange?: (status: SetupStatus) => void;
};

/** 开箱向导：一张清单看完哪些能用、哪些要修，每项都给出修复入口。 */
export function SetupPage({ api, onOpenSettings, onStatusChange }: Props) {
  const [status, setStatus] = useState<SetupStatus | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [notice, setNotice] = useState<{ tone: "ok" | "error" | "info"; text: string } | null>(null);
  const [restartNeeded, setRestartNeeded] = useState(false);

  const load = useCallback(async () => {
    setState("loading");
    try {
      const response = await api("/api/setup/status");
      if (!response.ok) throw new Error(await readError(response, "配置体检失败。"));
      const next = (await response.json()) as SetupStatus;
      setStatus(next);
      onStatusChange?.(next);
      setState("ready");
    } catch (error) {
      setNotice({ tone: "error", text: error instanceof Error ? error.message : "配置体检失败。" });
      setState("error");
    }
  }, [api, onStatusChange]);
  useEffect(() => { void load(); }, [load]);

  const restart = async () => {
    try {
      const response = await api("/api/system/restart", { method: "POST" });
      if (!response.ok) throw new Error(await readError(response, "重启失败，请手动重启控制台。"));
      setNotice({ tone: "info", text: "控制台正在重启，十几秒后刷新页面。" });
      window.setTimeout(() => window.location.reload(), 15000);
    } catch (error) {
      setNotice({ tone: "error", text: error instanceof Error ? error.message : "重启失败。" });
    }
  };

  const required = status?.items.filter((item) => item.required) ?? [];
  const optional = status?.items.filter((item) => !item.required) ?? [];
  const readyCount = required.filter((item) => item.status === "ok").length;

  return <main className="studio-page setup-page" id="studio-content" tabIndex={-1}>
    <header className="studio-page__head">
      <div>
        <p className="studio-kicker">系统 / 开箱向导</p>
        <h1>配置体检</h1>
        <p>粘贴服务商发给你的配置码，模型、接口和密钥一次配好；剪映、ffmpeg 等本机工具自动检测。下面每一项都告诉你哪里不对、怎么修。</p>
      </div>
      <button type="button" className="studio-button" onClick={() => void load()} disabled={state === "loading"}><RefreshCw size={15} aria-hidden="true" />{state === "loading" ? "检测中…" : "重新检测"}</button>
    </header>

    {notice ? <div className="studio-notice" data-tone={notice.tone === "info" ? undefined : notice.tone} role={notice.tone === "error" ? "alert" : "status"}>{notice.text}</div> : null}
    {restartNeeded ? <div className="studio-notice" role="status">部分设置要重启控制台才生效。<button type="button" className="studio-button studio-button--quiet" onClick={() => void restart()}>立即重启</button></div> : null}

    {status ? <section className={`setup-summary${status.ready ? " setup-summary--ready" : ""}`} aria-live="polite">
      {status.ready ? <CheckCircle2 size={28} strokeWidth={1.6} aria-hidden="true" /> : <Wrench size={28} strokeWidth={1.6} aria-hidden="true" />}
      <div>
        <strong>{status.ready ? "全部就绪，可以开始做内容了" : `还差 ${required.length - readyCount} 项必需配置`}</strong>
        <span>必需 {readyCount}/{required.length} 项就绪{optional.length ? ` · 可选 ${optional.filter((item) => item.status === "ok").length}/${optional.length} 项` : ""}</span>
      </div>
    </section> : null}

    <div className="studio-grid studio-grid--split setup-layout">
      <div>
        <ActivationPanel api={api} onDone={(result) => {
          setNotice({ tone: "ok", text: `配置码已生效：写入 ${result.applied_public.length} 项设置、${result.applied_secrets.length} 个密钥${result.skipped.length ? `，跳过 ${result.skipped.length} 项当前版本不支持的设置` : ""}。` });
          if (result.restart_required) setRestartNeeded(true);
          void load();
        }} onError={(text) => setNotice({ tone: "error", text })} />
        <JianyingPanel api={api} onApplied={(restart) => {
          setNotice({ tone: "ok", text: "剪映草稿目录已设置。" });
          if (restart) setRestartNeeded(true);
          void load();
        }} onError={(text) => setNotice({ tone: "error", text })} />
      </div>

      <section className="studio-panel" aria-labelledby="setup-checklist-title">
        <header><div><h2 id="setup-checklist-title">检查清单</h2><p>必需项全部就绪后，二创、配音、混剪和剪映交付才能完整跑通。</p></div></header>
        {state === "loading" && !status ? <p className="studio-empty" role="status"><Loader2 size={18} className="setup-spin" aria-hidden="true" /> 正在检测，最多几秒…</p> : null}
        {status ? <>
          <h3 className="setup-group">必需</h3>
          <ul className="setup-items">{required.map((item) => <SetupRow key={item.id} item={item} onOpenSettings={onOpenSettings} />)}</ul>
          {optional.length ? <>
            <h3 className="setup-group">可选</h3>
            <ul className="setup-items">{optional.map((item) => <SetupRow key={item.id} item={item} onOpenSettings={onOpenSettings} />)}</ul>
          </> : null}
        </> : null}
      </section>
    </div>

    <ExportCodePanel api={api} onError={(text) => setNotice({ tone: "error", text })} />
  </main>;
}

function SetupRow({ item, onOpenSettings }: { item: SetupItem; onOpenSettings: () => void }) {
  const meta = statusMeta[item.status] ?? statusMeta.error;
  const Icon = meta.icon;
  const fixLabel = item.fix === "activation_code" ? "用配置码修复" : item.fix === "jianying_detect" ? "自动检测剪映" : item.fix.startsWith("settings") ? "打开设置" : "";
  const fix = () => {
    if (item.fix === "activation_code") document.getElementById("setup-activation-code")?.focus();
    else if (item.fix === "jianying_detect") document.getElementById("setup-jianying-detect")?.click();
    else if (item.fix.startsWith("settings")) onOpenSettings();
  };
  return <li className="setup-item" data-status={item.status}>
    <Icon size={18} aria-hidden="true" className="setup-item__icon" />
    <div className="setup-item__body">
      <strong>{item.title}<span className="studio-status" data-status={item.status === "ok" ? "ready" : item.status === "warn" ? "draft" : "attention"}>{meta.label}</span></strong>
      {item.detail ? <p>{item.detail}</p> : null}
    </div>
    {item.status !== "ok" && fixLabel ? <button type="button" className="studio-button studio-button--quiet" onClick={fix}>{fixLabel}</button> : null}
  </li>;
}

function ActivationPanel({ api, onDone, onError }: { api: SetupApi; onDone: (result: ActivationResult) => void; onError: (text: string) => void }) {
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async () => {
    const value = code.trim();
    if (!value || busy) return;
    setBusy(true);
    try {
      const response = await api("/api/setup/activate", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ code: value }) });
      if (!response.ok) throw new Error(await readError(response, "配置码无效。"));
      setCode("");
      onDone(await response.json());
    } catch (error) {
      onError(error instanceof Error ? error.message : "配置码无效。");
    } finally {
      setBusy(false);
    }
  };
  return <section className="studio-panel" aria-labelledby="setup-activation-title">
    <header><div><h2 id="setup-activation-title"><KeyRound size={16} aria-hidden="true" /> 粘贴配置码</h2><p>服务商发给你的一串以 JYC1 开头的文字。模型、接口和密钥都在里面，不用一项项填。</p></div></header>
    <form className="setup-form" onSubmit={(event) => { event.preventDefault(); void submit(); }}>
      <label className="studio-field"><span className="visually-hidden">配置码</span>
        <textarea id="setup-activation-code" rows={3} value={code} spellCheck={false} autoComplete="off" onChange={(event) => setCode(event.target.value)} placeholder="JYC1.xxxxxxxx…" />
      </label>
      <div className="studio-row">
        <small className="studio-metrics">配置码只在本机解开，密钥加密保存，不会显示出来。</small>
        <span className="grow" />
        <button type="submit" className="studio-button studio-button--primary" disabled={busy || !code.trim().startsWith("JYC1.")}>{busy ? "正在应用…" : "应用配置码"}</button>
      </div>
    </form>
  </section>;
}

function JianyingPanel({ api, onApplied, onError }: { api: SetupApi; onApplied: (restartRequired: boolean) => void; onError: (text: string) => void }) {
  const [detection, setDetection] = useState<JianyingDetection | null>(null);
  const [busy, setBusy] = useState<"" | "detect" | "apply">("");
  const detect = async () => {
    setBusy("detect");
    try {
      const response = await api("/api/jianying-drafts/detect");
      if (!response.ok) throw new Error(await readError(response, "剪映检测失败。"));
      setDetection(await response.json());
    } catch (error) {
      onError(error instanceof Error ? error.message : "剪映检测失败。");
    } finally {
      setBusy("");
    }
  };
  const apply = async (root: string) => {
    setBusy("apply");
    try {
      const response = await api("/api/jianying-drafts/detect/apply", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ draft_root: root }) });
      if (!response.ok) throw new Error(await readError(response, "设置剪映目录失败。"));
      const body = await response.json();
      onApplied(Boolean(body?.restart_required));
      await detect();
    } catch (error) {
      onError(error instanceof Error ? error.message : "设置剪映目录失败。");
    } finally {
      setBusy("");
    }
  };
  return <section className="studio-panel" aria-labelledby="setup-jianying-title">
    <header>
      <div><h2 id="setup-jianying-title"><ScanSearch size={16} aria-hidden="true" /> 剪映</h2><p>自动找出本机剪映的安装位置、版本和草稿目录，不用自己去翻文件夹。</p></div>
      <button id="setup-jianying-detect" type="button" className="studio-button" disabled={Boolean(busy)} onClick={() => void detect()}>{busy === "detect" ? "检测中…" : "自动检测"}</button>
    </header>
    {detection ? detection.candidates.length ? <ul className="setup-candidates">
      {detection.candidates.map((candidate) => {
        const current = detection.configured_root && candidate.draft_root.toLowerCase() === detection.configured_root.toLowerCase();
        return <li key={candidate.draft_root} data-current={current ? "true" : undefined}>
          <div>
            <strong>剪映 {candidate.version || "版本未知"}<span className="studio-status" data-status={candidate.verified ? "ready" : "draft"}>{candidate.verified ? "已验证版本" : "未验证版本"}</span>{current ? <span className="studio-status" data-status="ready">当前使用</span> : null}</strong>
            <span className="studio-metrics"><span>{candidate.has_root_meta ? `${candidate.draft_count >= 0 ? candidate.draft_count : "?"} 个草稿` : "还没有草稿注册文件"}</span>{candidate.install_dir ? <span>安装在 {candidate.install_dir}</span> : null}</span>
            <code className="setup-path" title={candidate.draft_root}>{candidate.draft_root}</code>
            {!candidate.has_root_meta ? <small>打开剪映新建并关闭一个空草稿后再检测。</small> : null}
            {!candidate.verified ? <small>这个版本还没验证过，草稿可能需要人工检查；建议使用已验证的 11.1 版本。</small> : null}
          </div>
          {!current ? <button type="button" className="studio-button studio-button--primary" disabled={Boolean(busy) || !candidate.has_root_meta} onClick={() => void apply(candidate.draft_root)}>{busy === "apply" ? "正在设置…" : "用这个"}</button> : null}
        </li>;
      })}
    </ul> : <p className="studio-empty">没找到剪映。请先安装剪映专业版并打开一次，再点「自动检测」。</p> : <p className="studio-metrics">点「自动检测」查找本机剪映。</p>}
    {detection?.running ? <p className="studio-notice" role="status">剪映正在运行。生成草稿前最好先关掉剪映，避免它覆盖草稿列表。</p> : null}
  </section>;
}

function ExportCodePanel({ api, onError }: { api: SetupApi; onError: (text: string) => void }) {
  const [open, setOpen] = useState(false);
  const [issuedTo, setIssuedTo] = useState("");
  const [days, setDays] = useState(0);
  const [includeSecrets, setIncludeSecrets] = useState(true);
  const [result, setResult] = useState<{ code: string; public_keys: string[]; secret_keys: string[] } | null>(null);
  const [busy, setBusy] = useState(false);
  const generate = async () => {
    setBusy(true);
    try {
      const response = await api("/api/setup/export-code", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ issued_to: issuedTo.trim(), expires_days: days, include_secrets: includeSecrets }) });
      if (!response.ok) throw new Error(await readError(response, "生成配置码失败。"));
      setResult(await response.json());
    } catch (error) {
      onError(error instanceof Error ? error.message : "生成配置码失败。");
    } finally {
      setBusy(false);
    }
  };
  const copy = async () => {
    if (!result) return;
    try { await navigator.clipboard.writeText(result.code); } catch { /* the textarea is selectable as a fallback */ }
  };
  return <details className="studio-panel setup-export" open={open} onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary><Settings2 size={15} aria-hidden="true" /> 服务商工具：把本机配置打包成配置码</summary>
    <p className="studio-metrics">把这台电脑上的模型、接口和密钥打包成一个码发给客户；客户粘贴后就能用同样的服务。路径类设置（剪映、素材库、数据目录）不会打包，客户那边自动检测。</p>
    <div className="studio-fields">
      <label className="studio-field">发给谁<input value={issuedTo} onChange={(event) => setIssuedTo(event.target.value)} placeholder="例：岚山海鲜·王老板" /></label>
      <label className="studio-field">有效期<select value={days} onChange={(event) => setDays(Number(event.target.value))}><option value={0}>长期有效</option><option value={7}>7 天</option><option value={30}>30 天</option><option value={90}>90 天</option><option value={365}>1 年</option></select></label>
    </div>
    <label className="setup-check"><input type="checkbox" checked={includeSecrets} onChange={(event) => setIncludeSecrets(event.target.checked)} /> 包含密钥（不勾选时客户需要自己填密钥）</label>
    <div className="studio-row">
      <span className="grow" />
      <button type="button" className="studio-button studio-button--primary" disabled={busy} onClick={() => void generate()}>{busy ? "正在生成…" : "生成配置码"}</button>
    </div>
    {result ? <div className="setup-export__result">
      <textarea readOnly rows={4} value={result.code} aria-label="生成的配置码" onFocus={(event) => event.currentTarget.select()} />
      <div className="studio-row">
        <span className="studio-metrics"><span>{result.public_keys.length} 项设置</span><span>{result.secret_keys.length} 个密钥</span></span>
        <span className="grow" />
        <button type="button" className="studio-button" onClick={() => void copy()}><ClipboardCopy size={15} aria-hidden="true" />复制</button>
      </div>
      <p className="studio-notice" role="status">配置码里有密钥，等于把服务账号交给对方，只发给付费客户，不要发到群里。</p>
    </div> : null}
  </details>;
}
