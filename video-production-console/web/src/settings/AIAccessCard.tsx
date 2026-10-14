import { useCallback, useEffect, useState } from "react";
import { Bot, Copy, Eye, EyeOff, RefreshCw } from "lucide-react";

type Api = (path: string, init?: RequestInit) => Promise<Response>;

type MCPAccess = { token: string; endpoint: string; claude: string; codex: string; json: string };

async function readError(response: Response, fallback: string) {
  try {
    const body = await response.json();
    return typeof body?.message === "string" && body.message ? body.message : fallback;
  } catch {
    return fallback;
  }
}

function masked(token: string) {
  return token.length > 10 ? `${token.slice(0, 6)}••••••••${token.slice(-4)}` : "••••••••";
}

/** 本机 AI（Claude Code、Codex 等）通过 MCP 操作控制台：显示令牌、复制接入配置、重置令牌。 */
export function AIAccessCard({ api }: { api: Api }) {
  const [access, setAccess] = useState<MCPAccess | null>(null);
  const [error, setError] = useState("");
  const [shown, setShown] = useState(false);
  const [busy, setBusy] = useState(false);
  const [confirmRotate, setConfirmRotate] = useState(false);
  const [note, setNote] = useState("");

  const load = useCallback(async () => {
    try {
      const response = await api("/api/mcp/token");
      if (!response.ok) throw new Error(await readError(response, "AI 接入信息读取失败。"));
      setAccess(await response.json());
      setError("");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "AI 接入信息读取失败。");
    }
  }, [api]);
  useEffect(() => { void load(); }, [load]);

  const copy = async (text: string, label: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setNote(`已复制${label}。`);
    } catch {
      setNote("复制失败，请手动选中复制。");
    }
  };

  const rotate = async () => {
    setBusy(true);
    try {
      const response = await api("/api/mcp/token/rotate", { method: "POST" });
      if (!response.ok) throw new Error(await readError(response, "令牌重置失败。"));
      setAccess(await response.json());
      setConfirmRotate(false);
      setNote("令牌已重置，旧令牌立即失效；已接入的 AI 要用新配置重新接入。");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "令牌重置失败。");
    } finally {
      setBusy(false);
    }
  };

  return <section className="settings-field settings-field--wide jianying-health ai-access" aria-label="AI 接入">
    <span className="jianying-health__title"><Bot size={15} aria-hidden="true" /> AI 接入（MCP）</span>
    <p className="ai-access__lead">让本机的 Claude Code、Codex 等 AI 直接操作控制台：建账号、采集对标、二创、改提示词、混剪、出封面、记账。只允许本机连接，花钱和删除的操作 AI 会先问你。</p>
    {error ? <p className="settings-feedback settings-feedback--error" role="alert">{error}</p> : null}
    {access ? <>
      <dl className="jianying-health__facts">
        <div><dt>接入地址</dt><dd><code>{access.endpoint}</code></dd></div>
        <div><dt>令牌</dt><dd>
          <code>{shown ? access.token : masked(access.token)}</code>
          <button type="button" className="settings-inline-button" aria-label={shown ? "隐藏令牌" : "显示令牌"} onClick={() => setShown((value) => !value)}>
            {shown ? <EyeOff size={13} aria-hidden="true" /> : <Eye size={13} aria-hidden="true" />}
          </button>
        </dd></div>
      </dl>
      <div className="ai-access__actions">
        <button type="button" className="settings-inline-button" onClick={() => void copy(access.claude, " Claude Code 接入命令（在终端运行）")}><Copy size={13} aria-hidden="true" />复制 Claude Code 命令</button>
        <button type="button" className="settings-inline-button" onClick={() => void copy(access.codex, " Codex 配置（粘贴到 ~/.codex/config.toml）")}><Copy size={13} aria-hidden="true" />复制 Codex 配置</button>
        <button type="button" className="settings-inline-button" onClick={() => void copy(access.json, "通用 JSON 配置")}><Copy size={13} aria-hidden="true" />复制 JSON 配置</button>
        {!confirmRotate
          ? <button type="button" className="settings-inline-button" disabled={busy} onClick={() => setConfirmRotate(true)}><RefreshCw size={13} aria-hidden="true" />重置令牌</button>
          : <>
            <span>旧令牌会立即失效，确定吗？</span>
            <button type="button" className="settings-inline-button settings-inline-button--danger" disabled={busy} onClick={() => void rotate()}>{busy ? "正在重置…" : "确认重置"}</button>
            <button type="button" className="settings-inline-button" onClick={() => setConfirmRotate(false)}>取消</button>
          </>}
      </div>
    </> : null}
    {note ? <p className="settings-feedback" role="status">{note}</p> : null}
  </section>;
}
