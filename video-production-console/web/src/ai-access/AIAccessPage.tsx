import { useCallback, useEffect, useState } from "react";
import { Bot, Check, Copy, Eye, EyeOff, KeyRound, Link2, Monitor, RefreshCw, ShieldCheck } from "lucide-react";
import "../shell/studio-page.css";
import "./ai-access.css";

type API = (path: string, init?: RequestInit) => Promise<Response>;
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

const capabilities = [
  { no: "01", title: "对标采集与监控", items: ["贴链接采集抖音、小红书、视频号", "按关键词搜同行爆款", "建监控任务，看飙升和高分视频", "读评论找痛点和机会"] },
  { no: "02", title: "拆解与复刻", items: ["拆解爆款的钩子、结构、镜头", "生成每个镜头的复刻提示词", "建复刻项目、批量生成视频", "拼接成片并做 AI 质检"] },
  { no: "03", title: "文案与混剪", items: ["按账号风格二创文案", "审稿、定稿、出发布包", "配音、字幕、出剪映草稿", "出封面、查任务进度"] },
  { no: "04", title: "商户与经营", items: ["建商户、记订单和成本", "上传授权素材", "单张出图、图文制作", "导出经营总览"] },
];

const examples = [
  "帮我在对标里搜「岚山海鲜」，把点赞最高的三条采集下来并拆解",
  "看看监控里今天有没有飙升的视频，有的话把评论洞察也做了",
  "用最新那份拆解建一个复刻项目，产品换成梭子蟹，每个镜头出两版",
  "把认知漫步账号昨天定稿的文案配音并出剪映草稿",
];

/** AI 接入：让本机的 Claude Code、Codex 等通过 MCP 直接操作控制台。 */
export function AIAccessPage({ api }: { api: API }) {
  const [access, setAccess] = useState<MCPAccess | null>(null);
  const [error, setError] = useState("");
  const [shown, setShown] = useState(false);
  const [busy, setBusy] = useState(false);
  const [confirmRotate, setConfirmRotate] = useState(false);
  const [copied, setCopied] = useState("");

  const load = useCallback(async () => {
    try {
      const response = await api("/api/mcp/token");
      if (!response.ok) throw new Error(await readError(response, "AI 接入信息读取失败。"));
      setAccess(await response.json() as MCPAccess);
      setError("");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "AI 接入信息读取失败。");
    }
  }, [api]);
  useEffect(() => { void load(); }, [load]);

  const copy = async (text: string, key: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(key);
      window.setTimeout(() => setCopied((current) => (current === key ? "" : current)), 2000);
    } catch {
      setError("复制失败，请手动选中复制。");
    }
  };
  const rotate = async () => {
    setBusy(true);
    try {
      const response = await api("/api/mcp/token/rotate", { method: "POST" });
      if (!response.ok) throw new Error(await readError(response, "令牌重置失败。"));
      setAccess(await response.json() as MCPAccess);
      setConfirmRotate(false);
      setCopied("rotated");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "令牌重置失败。");
    } finally {
      setBusy(false);
    }
  };

  const CopyButton = ({ text, id, label, primary }: { text: string; id: string; label: string; primary?: boolean }) => <button type="button" className={primary ? "studio-button studio-button--primary" : "studio-button"} onClick={() => void copy(text, id)}>
    {copied === id ? <Check size={15} aria-hidden="true" /> : <Copy size={15} aria-hidden="true" />}{copied === id ? "已复制" : label}
  </button>;

  return <main className="studio-page ai-access-page" id="studio-content" tabIndex={-1}>
    <section className="aa-hero">
      <div className="aa-hero__text">
        <span className="aa-badge"><Bot size={13} aria-hidden="true" />MCP 服务 <em>本机一键接入</em></span>
        <h1>一键接入控制台 MCP，<br /><span>让 AI 帮你跑完整条流水线</span></h1>
        <p>接好以后，在 Claude Code、Codex 里用一句话就能采集对标、拆解爆款、批量复刻、混剪出草稿。只允许本机连接，花钱和删除的操作会先问你。</p>
        <div className="studio-row aa-hero__actions">
          {access ? <CopyButton text={access.claude} id="claude-hero" label="复制 Claude Code 命令" primary /> : null}
          {access ? <CopyButton text={access.codex} id="codex-hero" label="复制 Codex 配置" /> : null}
        </div>
        <ul className="aa-checks"><li><Check size={13} aria-hidden="true" />84 个工具</li><li><Check size={13} aria-hidden="true" />只在本机</li><li><Check size={13} aria-hidden="true" />花钱先确认</li></ul>
      </div>
      <div className="aa-chat" aria-hidden="true">
        <div className="aa-chat__bar"><span /><span /><span /><em>claude_code · jiying-console</em></div>
        <div className="aa-chat__ask">监控里「岚山海鲜」今天有没有飙升的视频？有的话拆解一下，给我复刻提示词。</div>
        <div className="aa-chat__answer">
          <div className="aa-chat__head"><strong>查询结果</strong><span>监控 · 今天</span></div>
          <p>有 1 条飙升：《在日照岚山怼一顿海鲜小炒》30 分钟点赞 +1.8 万。已拆解出 6 个镜头的复刻提示词。</p>
          <ul><li>镜头 1 · 0–2.4 秒 · 价格开场</li><li>镜头 2 · 2.4–6 秒 · 后厨爆炒特写</li></ul>
        </div>
      </div>
    </section>

    <section className="aa-what">
      <h2>MCP 是什么？</h2>
      <p>用 AI 干活，一句话就够。MCP 是 AI 和控制台之间的连接，接上以后，AI 就能替你操作这里的每个功能。</p>
      <div className="aa-what__cards">
        <article><span className="aa-icon"><Link2 size={18} aria-hidden="true" /></span><h3>MCP</h3><strong>连接 AI 和控制台的「数据线」</strong><p>接上后，AI 才能替你采集、拆解、复刻、混剪。</p></article>
        <article><span className="aa-icon"><Monitor size={18} aria-hidden="true" /></span><h3>客户端</h3><strong>你平时用的 AI 软件</strong><p>例如 Claude Code、Codex，在本机运行。</p></article>
        <article><span className="aa-icon"><KeyRound size={18} aria-hidden="true" /></span><h3>令牌</h3><strong>打开控制台的专属钥匙</strong><p>只给你自己用；泄露了就点「重置令牌」，旧的立即失效。</p></article>
      </div>
      <div className="aa-tip"><div><strong>接好以后，使用就和聊天一样简单</strong><span>你负责提问，AI 通过 MCP 操作控制台，把结果整理好告诉你。</span></div><p>例如直接问：帮我把点赞最高的三条海鲜视频采集下来并拆解。</p></div>
    </section>

    <section className="aa-connect" aria-labelledby="aa-connect-title">
      <h2 id="aa-connect-title">三步接入</h2>
      {error ? <p className="studio-notice" data-tone="error" role="alert">{error}</p> : null}
      {access ? <ol className="aa-steps">
        <li>
          <span className="aa-step-no">1</span>
          <div><h3>复制接入配置</h3><p>用 Claude Code 就复制命令，在终端运行一次；用 Codex 就复制配置，粘贴到 <code>~/.codex/config.toml</code>。</p>
            <div className="studio-row"><CopyButton text={access.claude} id="claude" label="Claude Code 命令" /><CopyButton text={access.codex} id="codex" label="Codex 配置" /><CopyButton text={access.json} id="json" label="通用 JSON 配置" /></div>
          </div>
        </li>
        <li>
          <span className="aa-step-no">2</span>
          <div><h3>确认地址和令牌</h3>
            <dl className="aa-facts">
              <div><dt>接入地址</dt><dd><code>{access.endpoint}</code></dd></div>
              <div><dt>令牌</dt><dd><code>{shown ? access.token : masked(access.token)}</code>
                <button type="button" className="studio-button studio-button--quiet" aria-label={shown ? "隐藏令牌" : "显示令牌"} onClick={() => setShown((value) => !value)}>{shown ? <EyeOff size={14} aria-hidden="true" /> : <Eye size={14} aria-hidden="true" />}</button>
              </dd></div>
            </dl>
          </div>
        </li>
        <li>
          <span className="aa-step-no">3</span>
          <div><h3>重启 AI 客户端，开始提问</h3><p>重启后在对话里说「用极影控制台……」，AI 会自动调用这里的工具。</p></div>
        </li>
      </ol> : !error ? <p className="studio-empty" role="status">正在读取接入信息…</p> : null}
      {access ? <div className="aa-rotate">
        <ShieldCheck size={16} aria-hidden="true" />
        <span>只允许本机连接。花钱（生成视频、出图、模型调用）和删除的操作，AI 会先问你。</span>
        <span className="grow" />
        {copied === "rotated" ? <span className="aa-rotate__done">令牌已重置，已接入的 AI 要用新配置重新接入。</span> : null}
        {!confirmRotate
          ? <button type="button" className="studio-button studio-button--quiet" disabled={busy} onClick={() => setConfirmRotate(true)}><RefreshCw size={14} aria-hidden="true" />重置令牌</button>
          : <><span>旧令牌会立即失效，确定吗？</span><button type="button" className="studio-button studio-button--danger" disabled={busy} onClick={() => void rotate()}>{busy ? "正在重置…" : "确认重置"}</button><button type="button" className="studio-button studio-button--quiet" onClick={() => setConfirmRotate(false)}>取消</button></>}
      </div> : null}
    </section>

    <section className="aa-caps" aria-labelledby="aa-caps-title">
      <p className="studio-kicker">核心能力</p>
      <h2 id="aa-caps-title">整条流水线，<span>一句话调用</span></h2>
      <div className="aa-caps__grid">{capabilities.map((cap) => <article key={cap.no}>
        <header><span>{cap.no}</span><h3>{cap.title}</h3></header>
        <ul>{cap.items.map((item) => <li key={item}><Check size={13} aria-hidden="true" />{item}</li>)}</ul>
      </article>)}</div>
    </section>

    <section className="aa-examples" aria-labelledby="aa-examples-title">
      <h2 id="aa-examples-title">可以这样问</h2>
      <ul>{examples.map((example, index) => <li key={index}><span>{example}</span><button type="button" className="studio-button studio-button--quiet" aria-label={`复制示例：${example}`} onClick={() => void copy(example, `ex${index}`)}>{copied === `ex${index}` ? <Check size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}</button></li>)}</ul>
    </section>
  </main>;
}
