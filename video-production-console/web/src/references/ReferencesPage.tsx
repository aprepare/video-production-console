import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ArrowUpRight, Heart, Link2, MessageCircle, RefreshCw, Search, Send, Trash2, Video, Wand2 } from "lucide-react";
import { ReferenceMedia, type Sentence } from "./ReferenceMedia";
import { ReferenceAnalysis } from "./ReferenceAnalysis";
import "../shell/studio-page.css";
import "./references.css";

export type ReferenceApi = (path: string, init?: RequestInit) => Promise<Response>;

type Platform = "douyin" | "xhs" | "channels";

export type PlatformPost = {
  platform: Platform;
  external_id: string;
  url: string;
  title: string;
  text: string;
  author: string;
  cover_url: string;
  video_url: string;
  duration_s: number;
  likes: number;
  comments: number;
  shares: number;
  collects: number;
  plays: number;
  created_at: number;
};

export type ReferencePost = {
  id: string;
  platform: Platform;
  external_id: string;
  url: string;
  title: string;
  caption: string;
  author: string;
  cover_url: string;
  duration_s: number;
  likes: number;
  comments: number;
  shares: number;
  collects: number;
  plays: number;
  keyword: string;
  merchant_id: string;
  transcript_status: "none" | "running" | "completed" | "failed";
  transcript: string;
  transcript_error: string;
  notes: string;
  created_at: string;
  media_status?: "none" | "downloading" | "ready" | "failed";
  media_size?: number;
  media_error?: string;
  has_video?: boolean;
  has_cover?: boolean;
  sentences?: Sentence[];
};

type GatewayStatus = { configured: boolean; reachable?: boolean; message?: string; apps: Array<{ id: string; name: string; ready: boolean; msg: string }> };

const platformLabels: Record<Platform, string> = { douyin: "抖音", xhs: "小红书", channels: "视频号" };
const transcriptLabels: Record<ReferencePost["transcript_status"], { label: string; status: string }> = {
  none: { label: "未转写", status: "draft" },
  running: { label: "转写中", status: "working" },
  completed: { label: "口播就绪", status: "ready" },
  failed: { label: "转写失败", status: "attention" },
};

export function formatCount(value: number) {
  if (!value) return "0";
  if (value >= 100000000) return `${(value / 100000000).toFixed(1)}亿`;
  if (value >= 10000) return `${(value / 10000).toFixed(1)}万`;
  return String(value);
}

/** One-shot hand-off read by WorkflowCanvas regardless of the selected account. */
export const PENDING_REMIX_SOURCE_KEY = "remix-lab:pending-source";

async function readError(response: Response, fallback: string) {
  try {
    const body = await response.json();
    return typeof body?.message === "string" && body.message ? body.message : fallback;
  } catch {
    return fallback;
  }
}

type Props = {
  api: ReferenceApi;
  onNavigate: (href: string) => void;
  onOpenSettings: () => void;
};

export function ReferencesPage({ api, onNavigate, onOpenSettings }: Props) {
  const [status, setStatus] = useState<GatewayStatus | null>(null);
  const [posts, setPosts] = useState<ReferencePost[]>([]);
  const [listState, setListState] = useState<"loading" | "ready" | "error">("loading");
  const [message, setMessage] = useState<{ tone: "ok" | "error" | "info"; text: string } | null>(null);
  const [selectedID, setSelectedID] = useState("");
  const [query, setQuery] = useState("");
  const [tab, setTab] = useState<"link" | "search">("link");

  const loadList = useCallback(async () => {
    try {
      const response = await api("/api/references?limit=300");
      if (!response.ok) throw new Error();
      setPosts(await response.json());
      setListState("ready");
    } catch {
      setListState("error");
    }
  }, [api]);

  useEffect(() => {
    void loadList();
    void (async () => {
      try {
        const response = await api("/api/references/status");
        if (response.ok) setStatus(await response.json());
      } catch { /* status is advisory */ }
    })();
  }, [api, loadList]);

  // Keep polling while any transcription or download is running.
  const running = posts.some((post) => post.transcript_status === "running" || post.media_status === "downloading");
  useEffect(() => {
    if (!running) return;
    const timer = window.setInterval(() => void loadList(), 6000);
    return () => window.clearInterval(timer);
  }, [running, loadList]);

  const upsertLocal = (post: ReferencePost) => {
    setPosts((current) => [post, ...current.filter((item) => item.id !== post.id)]);
  };

  const filtered = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase();
    if (!needle) return posts;
    return posts.filter((post) => [post.title, post.caption, post.author, post.transcript, post.keyword].some((value) => value.toLocaleLowerCase().includes(needle)));
  }, [posts, query]);
  const selected = posts.find((post) => post.id === selectedID) || null;

  const notConfigured = status && !status.configured;

  return <main className="studio-page references-page" id="studio-content" tabIndex={-1}>
    <header className="studio-page__head">
      <div>
        <p className="studio-kicker">创作工作室 / 对标采集</p>
        <h1>对标采集</h1>
        <p>粘贴抖音、小红书、视频号链接或按关键词搜索。原视频自动存到本机，可播放、下载、拆解；口播转写完成后一键送进二创。</p>
      </div>
      <GatewayBadges status={status} />
    </header>

    {notConfigured ? <div className="studio-notice" role="status">
      平台接口还没配置。<button type="button" className="studio-button studio-button--quiet" onClick={onOpenSettings}>打开设置 → 平台接口</button>
    </div> : null}
    {message ? <div className="studio-notice" data-tone={message.tone === "info" ? undefined : message.tone} role={message.tone === "error" ? "alert" : "status"}>{message.text}</div> : null}

    <div className="studio-grid studio-grid--split references-layout">
      <div>
        <section className="studio-panel" aria-labelledby="reference-collect-title">
          <header>
            <div><h2 id="reference-collect-title">采集</h2><p>采集时默认同时提交通义听悟转写，约 20–60 秒出结果。</p></div>
            <div className="studio-tabs" role="tablist" aria-label="采集方式">
              <button type="button" role="tab" aria-selected={tab === "link"} onClick={() => setTab("link")}>粘贴链接</button>
              <button type="button" role="tab" aria-selected={tab === "search"} onClick={() => setTab("search")}>关键词搜索</button>
            </div>
          </header>
          {tab === "link"
            ? <LinkCollector api={api} disabled={Boolean(notConfigured)} onCollected={(post) => { upsertLocal(post); setSelectedID(post.id); setMessage({ tone: "ok", text: post.transcript_status === "running" ? "已采集，口播正在转写。" : "已采集。" }); }} onError={(text) => setMessage({ tone: "error", text })} />
            : <SearchCollector api={api} disabled={Boolean(notConfigured)} onCollected={(collected) => { collected.forEach(upsertLocal); setMessage({ tone: "ok", text: `已采集 ${collected.length} 条，口播转写在后台进行。` }); }} onError={(text) => setMessage({ tone: "error", text })} />}
        </section>

        <section className="studio-panel" aria-labelledby="reference-library-title">
          <header>
            <div><h2 id="reference-library-title">对标素材库 <span className="references-count">{posts.length}</span></h2><p>按采集时间排序；点一条查看口播并送进二创。</p></div>
            <button type="button" className="studio-button studio-button--quiet" aria-label="刷新对标列表" onClick={() => void loadList()}><RefreshCw size={15} aria-hidden="true" /></button>
          </header>
          <label className="studio-search references-filter"><Search size={16} aria-hidden="true" /><input type="search" aria-label="搜索对标素材" placeholder="搜索标题、作者、口播" value={query} onChange={(event) => setQuery(event.target.value)} /></label>
          {listState === "loading" ? <p role="status" className="studio-empty">正在读取…</p> : null}
          {listState === "error" ? <div role="alert" className="studio-notice" data-tone="error">对标列表读取失败。<button type="button" className="studio-button studio-button--quiet" onClick={() => void loadList()}>重试</button></div> : null}
          {listState === "ready" && filtered.length === 0 ? <div className="studio-empty"><strong>{posts.length ? "没有匹配的对标" : "还没有对标素材"}</strong><span>{posts.length ? "换个关键词试试。" : "粘贴一条分享链接，或在关键词搜索里勾选爆款采集。"}</span></div> : null}
          <ul className="studio-list references-list">
            {filtered.map((post) => <li key={post.id}>
              <button type="button" className="reference-item" aria-current={post.id === selectedID ? "true" : undefined} onClick={() => setSelectedID(post.id)}>
                <span className="reference-item__top"><span className="reference-platform" data-platform={post.platform}>{platformLabels[post.platform]}{post.media_status === "ready" ? <Video size={11} aria-label="原视频已下载" /> : null}</span><span className="studio-status" data-status={transcriptLabels[post.transcript_status].status}>{transcriptLabels[post.transcript_status].label}</span></span>
                <strong>{post.title || post.caption.slice(0, 40) || "（无标题）"}</strong>
                <span className="studio-metrics"><span>{post.author || "未知作者"}</span><span><Heart size={11} aria-hidden="true" /> {formatCount(post.likes)}</span><span><MessageCircle size={11} aria-hidden="true" /> {formatCount(post.comments)}</span>{post.duration_s ? <span>{Math.round(post.duration_s)}s</span> : null}</span>
              </button>
            </li>)}
          </ul>
        </section>
      </div>

      <section className="studio-panel reference-detail" aria-label="对标详情">
        {selected
          ? <ReferenceDetail key={selected.id} api={api} post={selected} onChange={upsertLocal} onDeleted={(id) => { setPosts((current) => current.filter((item) => item.id !== id)); setSelectedID(""); }} onNavigate={onNavigate} onMessage={setMessage} />
          : <div className="studio-empty"><Wand2 size={28} strokeWidth={1.5} aria-hidden="true" /><strong>选择一条对标</strong><span>可以看原视频、按句跳转口播、做拆解分析，再把口播送进二创。</span></div>}
      </section>
    </div>
  </main>;
}

function GatewayBadges({ status }: { status: GatewayStatus | null }) {
  if (!status || !status.configured) return null;
  if (status.reachable === false) return <span className="studio-status" data-status="attention" title={status.message}>平台接口不可达</span>;
  return <div className="references-gateway" aria-label="平台接口状态">
    {status.apps.map((app) => <span key={app.id} className="studio-status" data-status={app.ready ? "ready" : "attention"} title={app.msg}>{app.id === "tingwu" ? "听悟" : platformLabels[app.id as Platform] || app.name || app.id}</span>)}
  </div>;
}

function LinkCollector({ api, disabled, onCollected, onError }: { api: ReferenceApi; disabled: boolean; onCollected: (post: ReferencePost) => void; onError: (text: string) => void }) {
  const [link, setLink] = useState("");
  const [transcribe, setTranscribe] = useState(true);
  const [busy, setBusy] = useState(false);
  const submit = async () => {
    if (!link.trim() || busy) return;
    setBusy(true);
    try {
      const response = await api("/api/references", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ link: link.trim(), transcribe }) });
      if (!response.ok) throw new Error(await readError(response, "采集失败。"));
      onCollected(await response.json());
      setLink("");
    } catch (error) {
      onError(error instanceof Error ? error.message : "采集失败。");
    } finally {
      setBusy(false);
    }
  };
  return <form className="references-form" onSubmit={(event) => { event.preventDefault(); void submit(); }}>
    <label className="studio-field">分享链接或整段分享文字
      <textarea rows={3} value={link} onChange={(event) => setLink(event.target.value)} placeholder="例：7.94 复制打开抖音，看看【海吃王】… https://v.douyin.com/xxxx/" />
      <small>抖音请用 App「分享 → 复制链接」的 v.douyin.com 短链；视频号用 weixin.qq.com/sph 分享链。</small>
    </label>
    <div className="studio-row">
      <label className="references-check"><input type="checkbox" checked={transcribe} onChange={(event) => setTranscribe(event.target.checked)} /> 同时转写口播</label>
      <span className="grow" />
      <button type="submit" className="studio-button studio-button--primary" disabled={disabled || busy || !link.trim()}><Link2 size={15} aria-hidden="true" />{busy ? "正在解析…" : "采集"}</button>
    </div>
  </form>;
}

function SearchCollector({ api, disabled, onCollected, onError }: { api: ReferenceApi; disabled: boolean; onCollected: (posts: ReferencePost[]) => void; onError: (text: string) => void }) {
  const [platform, setPlatform] = useState<"douyin" | "xhs">("douyin");
  const [keyword, setKeyword] = useState("");
  const [results, setResults] = useState<PlatformPost[]>([]);
  const [paging, setPaging] = useState<{ cursor: string; search_id: string; session_id?: string; has_more: boolean } | null>(null);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState<"" | "search" | "more" | "collect">("");
  const lastKeyword = useRef("");

  const search = async (more = false) => {
    const term = more ? lastKeyword.current : keyword.trim();
    if (!term) return;
    setBusy(more ? "more" : "search");
    try {
      const params = new URLSearchParams({ platform, keyword: term });
      if (more && paging) {
        params.set("cursor", paging.cursor);
        params.set("search_id", paging.search_id);
        if (paging.session_id) params.set("session_id", paging.session_id);
      }
      const response = await api(`/api/references/search?${params}`);
      if (!response.ok) throw new Error(await readError(response, "搜索失败。"));
      const page = await response.json();
      lastKeyword.current = term;
      setResults((current) => (more ? [...current, ...page.items] : page.items));
      setPaging({ cursor: page.cursor, search_id: page.search_id, session_id: page.session_id, has_more: page.has_more });
      if (!more) setPicked(new Set());
    } catch (error) {
      onError(error instanceof Error ? error.message : "搜索失败。");
    } finally {
      setBusy("");
    }
  };

  const sorted = useMemo(() => [...results].sort((a, b) => b.likes - a.likes), [results]);
  const toggle = (id: string) => setPicked((current) => {
    const next = new Set(current);
    if (next.has(id)) next.delete(id); else next.add(id);
    return next;
  });

  const collect = async () => {
    const chosen = sorted.filter((post) => picked.has(post.external_id));
    if (!chosen.length) return;
    setBusy("collect");
    const collected: ReferencePost[] = [];
    try {
      for (const post of chosen) {
        const response = await api("/api/references", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ post, keyword: lastKeyword.current, transcribe: Boolean(post.video_url) }) });
        if (!response.ok) throw new Error(await readError(response, "采集失败。"));
        collected.push(await response.json());
      }
      setPicked(new Set());
    } catch (error) {
      onError(error instanceof Error ? error.message : "采集失败。");
    } finally {
      if (collected.length) onCollected(collected);
      setBusy("");
    }
  };

  return <div className="references-search">
    <form className="studio-row" onSubmit={(event) => { event.preventDefault(); void search(); }}>
      <select aria-label="搜索平台" value={platform} onChange={(event) => setPlatform(event.target.value as "douyin" | "xhs")} className="references-platform-select">
        <option value="douyin">抖音</option><option value="xhs">小红书</option>
      </select>
      <label className="studio-search grow"><Search size={16} aria-hidden="true" /><input type="search" aria-label="搜索关键词" placeholder="例：岚山海鲜、日照绿茶、煎饼果子" value={keyword} onChange={(event) => setKeyword(event.target.value)} /></label>
      <button type="submit" className="studio-button studio-button--primary" disabled={disabled || Boolean(busy) || !keyword.trim()}>{busy === "search" ? "搜索中…" : "搜索"}</button>
    </form>
    {sorted.length ? <>
      <div className="studio-row references-search__bar">
        <span className="studio-metrics">按点赞排序 · 已选 {picked.size} 条</span>
        <span className="grow" />
        <button type="button" className="studio-button" disabled={!picked.size || Boolean(busy)} onClick={() => void collect()}>{busy === "collect" ? "正在采集…" : `采集并转写（${picked.size}）`}</button>
      </div>
      <ul className="studio-list references-results">
        {sorted.map((post) => <li key={post.external_id}>
          <label className="reference-result">
            <input type="checkbox" checked={picked.has(post.external_id)} onChange={() => toggle(post.external_id)} aria-label={`选择 ${post.title || post.external_id}`} />
            <span className="reference-result__body">
              <strong>{post.title || "（无标题）"}</strong>
              <span className="studio-metrics"><span>{post.author}</span><span><Heart size={11} aria-hidden="true" /> {formatCount(post.likes)}</span><span><MessageCircle size={11} aria-hidden="true" /> {formatCount(post.comments)}</span>{post.duration_s ? <span>{Math.round(post.duration_s)}s</span> : <span>图文</span>}</span>
            </span>
            {post.url ? <a href={post.url} target="_blank" rel="noreferrer" aria-label="在平台打开" className="reference-result__open" onClick={(event) => event.stopPropagation()}><ArrowUpRight size={15} aria-hidden="true" /></a> : null}
          </label>
        </li>)}
      </ul>
      {paging?.has_more ? <button type="button" className="studio-button references-more" disabled={Boolean(busy)} onClick={() => void search(true)}>{busy === "more" ? "加载中…" : "加载更多"}</button> : null}
    </> : null}
  </div>;
}

function ReferenceDetail({ api, post, onChange, onDeleted, onNavigate, onMessage }: {
  api: ReferenceApi;
  post: ReferencePost;
  onChange: (post: ReferencePost) => void;
  onDeleted: (id: string) => void;
  onNavigate: (href: string) => void;
  onMessage: (message: { tone: "ok" | "error" | "info"; text: string }) => void;
}) {
  const [transcript, setTranscript] = useState(post.transcript);
  const [notes, setNotes] = useState(post.notes);
  const [busy, setBusy] = useState<"" | "save" | "transcribe" | "delete" | "comments">("");
  const [comments, setComments] = useState<Array<{ author: string; text: string; likes: number }> | null>(null);
  const [view, setView] = useState<"source" | "analysis" | "comments">("source");
  const seekRef = useRef<((seconds: number) => void) | null>(null);
  useEffect(() => { setTranscript(post.transcript); }, [post.transcript]);
  const dirty = transcript.trim() !== post.transcript.trim() || notes.trim() !== post.notes.trim();

  const save = async () => {
    setBusy("save");
    try {
      const response = await api(`/api/references/${post.id}`, { method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ transcript, notes }) });
      if (!response.ok) throw new Error(await readError(response, "保存失败。"));
      onChange(await response.json());
      onMessage({ tone: "ok", text: "已保存。" });
    } catch (error) {
      onMessage({ tone: "error", text: error instanceof Error ? error.message : "保存失败。" });
    } finally {
      setBusy("");
    }
  };
  const transcribe = async () => {
    setBusy("transcribe");
    try {
      const response = await api(`/api/references/${post.id}/transcribe`, { method: "POST" });
      if (!response.ok) throw new Error(await readError(response, "转写提交失败。"));
      onChange(await response.json());
    } catch (error) {
      onMessage({ tone: "error", text: error instanceof Error ? error.message : "转写提交失败。" });
    } finally {
      setBusy("");
    }
  };
  const remove = async () => {
    if (!window.confirm("从对标素材库删除这一条？")) return;
    setBusy("delete");
    try {
      const response = await api(`/api/references/${post.id}`, { method: "DELETE" });
      if (!response.ok) throw new Error(await readError(response, "删除失败。"));
      onDeleted(post.id);
    } catch (error) {
      onMessage({ tone: "error", text: error instanceof Error ? error.message : "删除失败。" });
      setBusy("");
    }
  };
  const loadComments = async () => {
    setBusy("comments");
    try {
      const response = await api(`/api/references/${post.id}/comments?limit=30`);
      if (!response.ok) throw new Error(await readError(response, "评论读取失败。"));
      const list = await response.json();
      setComments([...list].sort((a, b) => b.likes - a.likes));
    } catch (error) {
      onMessage({ tone: "error", text: error instanceof Error ? error.message : "评论读取失败。" });
    } finally {
      setBusy("");
    }
  };
  const sendToRemix = () => {
    const text = transcript.trim();
    if (!text) return;
    try { window.sessionStorage.setItem(PENDING_REMIX_SOURCE_KEY, text); } catch { /* storage unavailable */ }
    onNavigate("/");
  };

  const label = transcriptLabels[post.transcript_status];
  const seek = (seconds: number) => {
    setView("source");
    // The player mounts with the source tab; wait a frame before seeking.
    window.requestAnimationFrame(() => seekRef.current?.(seconds));
  };
  return <>
    <header>
      <div>
        <p className="studio-kicker">{platformLabels[post.platform]} · {post.author || "未知作者"}</p>
        <h2>{post.title || "（无标题）"}</h2>
        <span className="studio-metrics"><span>点赞 {formatCount(post.likes)}</span><span>评论 {formatCount(post.comments)}</span><span>转发 {formatCount(post.shares)}</span><span>收藏 {formatCount(post.collects)}</span>{post.plays ? <span>播放 {formatCount(post.plays)}</span> : null}</span>
      </div>
      <div className="studio-row">
        {post.url ? <a className="studio-button studio-button--quiet" href={post.url} target="_blank" rel="noreferrer"><ArrowUpRight size={15} aria-hidden="true" />原帖</a> : null}
        <button type="button" className="studio-button studio-button--quiet studio-button--danger" disabled={Boolean(busy)} onClick={() => void remove()} aria-label="删除这条对标"><Trash2 size={15} aria-hidden="true" /></button>
      </div>
    </header>

    <div className="studio-tabs reference-views" role="tablist" aria-label="对标详情视图">
      <button type="button" role="tab" aria-selected={view === "source"} onClick={() => setView("source")}>原片与口播</button>
      <button type="button" role="tab" aria-selected={view === "analysis"} onClick={() => setView("analysis")}>拆解分析</button>
      {post.platform !== "channels" ? <button type="button" role="tab" aria-selected={view === "comments"} onClick={() => { setView("comments"); if (!comments && busy !== "comments") void loadComments(); }}>热评</button> : null}
    </div>

    {view === "source" ? <>
      <ReferenceMedia api={api} post={post} onChange={onChange} onMessage={onMessage} seekRef={seekRef} />

      {post.caption ? <details className="reference-caption"><summary>分享文案（不是口播）</summary><p>{post.caption}</p></details> : null}

      <div className="reference-transcript">
        <div className="studio-row">
          <strong>口播原文</strong>
          <span className="studio-status" data-status={label.status}>{label.label}</span>
          <span className="grow" />
          {post.transcript_status !== "running" && post.has_video !== false ? <button type="button" className="studio-button studio-button--quiet" disabled={Boolean(busy)} onClick={() => void transcribe()}>{post.transcript_status === "completed" ? "重新转写" : busy === "transcribe" ? "提交中…" : "转写口播"}</button> : null}
        </div>
        {post.transcript_status === "failed" ? <p className="studio-notice" data-tone="error" role="alert">{post.transcript_error || "转写失败。"}</p> : null}
        {post.transcript_status === "running" ? <p className="studio-notice" role="status">通义听悟正在转写，完成后自动出现，不用刷新。</p> : null}
        <label className="studio-field">
          <span className="visually-hidden">口播原文</span>
          <textarea rows={8} value={transcript} onChange={(event) => setTranscript(event.target.value)} placeholder={post.transcript_status === "running" ? "转写中…" : "转写完成后这里是口播全文；也可以手动粘贴或校对。"} aria-label="口播原文" />
        </label>
        <span className="studio-metrics">{transcript.trim().length} 字</span>
      </div>

      <label className="studio-field">备注<textarea rows={2} value={notes} onChange={(event) => setNotes(event.target.value)} placeholder="为什么值得对标：开头钩子、选题角度、评论区痛点…" /></label>

      <div className="studio-row reference-actions">
        <button type="button" className="studio-button" disabled={!dirty || Boolean(busy)} onClick={() => void save()}>{busy === "save" ? "正在保存…" : "保存修改"}</button>
        <span className="grow" />
        <button type="button" className="studio-button studio-button--primary" disabled={!transcript.trim()} onClick={sendToRemix}><Send size={15} aria-hidden="true" />送进二创</button>
      </div>
    </> : null}

    {view === "analysis" ? <ReferenceAnalysis api={api} post={post} onSeek={seek} onMessage={onMessage} /> : null}

    {view === "comments" ? <section className="reference-comments" aria-label="热评">
      <div className="studio-row">
        <h3>热评{comments ? ` ${comments.length}` : ""}</h3>
        <span className="grow" />
        <button type="button" className="studio-button studio-button--quiet" disabled={Boolean(busy)} onClick={() => void loadComments()}><MessageCircle size={15} aria-hidden="true" />{busy === "comments" ? "读取中…" : "刷新"}</button>
      </div>
      {busy === "comments" && !comments ? <p className="studio-empty" role="status">正在读取…</p> : null}
      {comments ? comments.length ? <ol className="studio-list">{comments.map((comment, index) => <li key={index}><span className="studio-metrics"><span>{comment.author}</span><span><Heart size={11} aria-hidden="true" /> {formatCount(comment.likes)}</span></span><p>{comment.text}</p></li>)}</ol> : <p className="studio-empty">没有读取到评论。</p> : null}
    </section> : null}
  </>;
}
