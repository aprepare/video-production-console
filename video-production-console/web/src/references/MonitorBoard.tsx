import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ArrowUpRight, BellDot, CheckCheck, EyeOff, Flame, History, Pencil, Play, Plus, Radar, RefreshCw, Sparkles, Trash2, X } from "lucide-react";
import { formatCount, relativeDay, type ReferenceApi, type ReferencePost } from "./ReferencesPage";
import type { MonitorFeed, MonitorItem, MonitorRun, MonitorSnapshot, MonitorTask, SurgeMode } from "./monitorTypes";
import { PreviewPlayer, PreviewThumb, type PreviewTarget } from "./Preview";
import { useModelOptions } from "../ModelSelect";

type Message = { tone: "ok" | "error" | "info"; text: string };
type FeedFilter = "all" | "surge" | "high" | "unread";

async function readError(response: Response, fallback: string) {
  try {
    const body = await response.json();
    return typeof body?.message === "string" && body.message ? body.message : fallback;
  } catch {
    return fallback;
  }
}

const platformLabels = { douyin: "抖音", xhs: "小红书" } as const;
const intervalOptions = [30, 60, 120, 180, 360, 720, 1440];

export function intervalLabel(minutes: number) {
  if (minutes < 60) return `${minutes} 分钟`;
  if (minutes % 60 === 0) return minutes === 1440 ? "每天" : `${minutes / 60} 小时`;
  return `${(minutes / 60).toFixed(1)} 小时`;
}

export function timeAgo(iso: string, now = Date.now()) {
  if (!iso) return "";
  const diff = Math.round((now - Date.parse(iso)) / 60000);
  if (Number.isNaN(diff)) return "";
  if (diff < 1) return "刚刚";
  if (diff < 60) return `${diff} 分钟前`;
  if (diff < 1440) return `${Math.floor(diff / 60)} 小时前`;
  return `${Math.floor(diff / 1440)} 天前`;
}

function timeUntil(iso: string, now = Date.now()) {
  if (!iso) return "";
  const diff = Math.round((Date.parse(iso) - now) / 60000);
  if (Number.isNaN(diff)) return "";
  if (diff <= 0) return "即将运行";
  if (diff < 60) return `${diff} 分钟后`;
  return `${Math.floor(diff / 60)} 小时 ${diff % 60} 分后`;
}

const signed = (value: number) => (value > 0 ? `+${formatCount(value)}` : value < 0 ? `-${formatCount(-value)}` : "0");

/** 点赞走势小曲线：最近若干次快照。 */
export function Sparkline({ history }: { history: MonitorSnapshot[] }) {
  if (history.length < 2) return null;
  const values = history.map((point) => point.likes);
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = max - min || 1;
  const width = 84;
  const height = 26;
  const points = values.map((value, index) => `${((index / (values.length - 1)) * width).toFixed(1)},${(height - 2 - ((value - min) / span) * (height - 4)).toFixed(1)}`).join(" ");
  return <svg className="monitor-spark" viewBox={`0 0 ${width} ${height}`} width={width} height={height} role="img" aria-label={`点赞走势：${formatCount(values[0])} → ${formatCount(values[values.length - 1])}`}>
    <polyline points={points} fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round" />
  </svg>;
}

function scoreTone(score: number, threshold: number) {
  if (score >= threshold) return "high";
  if (score >= Math.max(0, threshold - 20)) return "mid";
  return "low";
}

type Draft = Pick<MonitorTask,
  "name" | "platform" | "keyword" | "interval_minutes" | "pages" | "enabled" | "model" |
  "score_enabled" | "score_threshold" | "score_focus" | "auto_collect" | "auto_transcribe" | "auto_analyze" | "auto_insight" |
  "surge_mode" | "surge_like_delta" | "surge_like_ratio" | "surge_comment_delta" | "surge_comment_ratio" | "surge_min_base">;

export const defaultDraft: Draft = {
  name: "", platform: "douyin", keyword: "", interval_minutes: 30, pages: 1, enabled: true, model: "",
  score_enabled: true, score_threshold: 70, score_focus: "", auto_collect: true, auto_transcribe: false, auto_analyze: false, auto_insight: false,
  surge_mode: "rule", surge_like_delta: 500, surge_like_ratio: 0.5, surge_comment_delta: 50, surge_comment_ratio: 0.5, surge_min_base: 20,
};

function draftFrom(task: MonitorTask): Draft {
  const { name, platform, keyword, interval_minutes, pages, enabled, model, score_enabled, score_threshold, score_focus, auto_collect, auto_transcribe, auto_analyze, auto_insight, surge_mode, surge_like_delta, surge_like_ratio, surge_comment_delta, surge_comment_ratio, surge_min_base } = task;
  return { name, platform, keyword, interval_minutes, pages, enabled, model, score_enabled, score_threshold, score_focus, auto_collect, auto_transcribe, auto_analyze, auto_insight, surge_mode, surge_like_delta, surge_like_ratio, surge_comment_delta, surge_comment_ratio, surge_min_base };
}

function TaskDialog({ initial, editing, onClose, onSubmit }: { initial: Draft; editing: boolean; onClose: () => void; onSubmit: (draft: Draft) => Promise<void> }) {
  const models = useModelOptions();
  const [draft, setDraft] = useState<Draft>(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    ref.current?.querySelector<HTMLInputElement>("input")?.focus();
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") onClose(); };
    window.addEventListener("keydown", onKey);
    return () => { window.removeEventListener("keydown", onKey); previous?.focus?.(); };
  }, [onClose]);
  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => setDraft((current) => ({ ...current, [key]: value }));
  const num = (key: keyof Draft, scale = 1) => (event: { target: { value: string } }) => set(key, (Number(event.target.value) / scale) as never);
  const modelList = draft.model && !models.includes(draft.model) ? [...models, draft.model] : models;

  const submit = async () => {
    if (!draft.keyword.trim()) { setError("请填写关键词。"); return; }
    setBusy(true);
    setError("");
    try {
      await onSubmit({ ...draft, keyword: draft.keyword.trim(), name: draft.name.trim() });
    } catch (err) {
      setError(err instanceof Error ? err.message : "保存失败。");
      setBusy(false);
    }
  };

  const showRule = draft.surge_mode !== "ai";
  return <div className="studio-dialog-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <div className="studio-dialog monitor-dialog" role="dialog" aria-modal="true" aria-label={editing ? "编辑监控" : "新建监控"} ref={ref}>
      <header><h2>{editing ? "编辑监控" : "新建监控"}</h2><button type="button" className="studio-button studio-button--quiet" aria-label="关闭" onClick={onClose}><X size={16} aria-hidden="true" /></button></header>
      <form className="monitor-form" onSubmit={(event) => { event.preventDefault(); void submit(); }}>
        <fieldset>
          <legend>搜什么</legend>
          <div className="studio-fields">
            <label className="studio-field">关键词<input value={draft.keyword} maxLength={40} onChange={(event) => set("keyword", event.target.value)} placeholder="例：岚山海鲜" /></label>
            <label className="studio-field">平台<select value={draft.platform} onChange={(event) => set("platform", event.target.value as Draft["platform"])}><option value="douyin">抖音</option><option value="xhs">小红书</option></select></label>
            <label className="studio-field">多久搜一次<select value={draft.interval_minutes} onChange={num("interval_minutes")}>{intervalOptions.map((value) => <option key={value} value={value}>{intervalLabel(value)}</option>)}</select></label>
            <label className="studio-field">每次搜几页<select value={draft.pages} onChange={num("pages")}><option value={1}>1 页（约 12–20 条）</option><option value={2}>2 页</option><option value={3}>3 页</option></select></label>
            <label className="studio-field wide">名称<input value={draft.name} maxLength={40} onChange={(event) => set("name", event.target.value)} placeholder="留空就用关键词" /><small>搜得太勤容易被平台风控，建议 30 分钟以上、每次 1 页。只在控制台开着时运行。</small></label>
          </div>
        </fieldset>

        <fieldset>
          <legend>AI 初筛</legend>
          <label className="references-check"><input type="checkbox" checked={draft.score_enabled} onChange={(event) => set("score_enabled", event.target.checked)} />给新出现的视频打分（看标题文案和封面，费用低）</label>
          {draft.score_enabled ? <div className="studio-fields">
            <label className="studio-field">打分模型<select value={draft.model} onChange={(event) => set("model", event.target.value)}><option value="">默认拆解模型</option>{modelList.map((model) => <option key={model} value={model}>{model}</option>)}</select></label>
            <label className="studio-field">高分线<span className="monitor-range"><input type="range" min={0} max={100} step={5} value={draft.score_threshold} onChange={num("score_threshold")} aria-label="高分线" /><output>{draft.score_threshold}</output></span></label>
            <label className="studio-field wide">打分标准（可选）<textarea rows={2} maxLength={300} value={draft.score_focus} onChange={(event) => set("score_focus", event.target.value)} placeholder="例：适合岚山海鲜餐馆模仿，优先真实到店、价格透明、老板出镜的" /></label>
          </div> : null}
        </fieldset>

        <fieldset>
          <legend>怎样算飙升</legend>
          <div className="studio-tabs monitor-mode" role="radiogroup" aria-label="飙升判定方式">
            {([["rule", "按规则"], ["ai", "交给 AI"], ["both", "规则 + AI"]] as Array<[SurgeMode, string]>).map(([value, label]) => <button key={value} type="button" role="radio" aria-checked={draft.surge_mode === value} aria-selected={draft.surge_mode === value} onClick={() => set("surge_mode", value)}>{label}</button>)}
          </div>
          <p className="studio-metrics">同一条视频再次被搜到时，和上一次比。{draft.surge_mode === "ai" ? "AI 会结合发布时长、作者体量判断是否异常。" : draft.surge_mode === "both" ? "命中规则的直接算飙升，其余有增长的交给 AI 判断。" : "满足下面任一条就算飙升，填 0 表示不用这一条。"}</p>
          {showRule ? <div className="monitor-rules">
            <span>点赞增加</span><input type="number" min={0} value={draft.surge_like_delta} onChange={num("surge_like_delta")} aria-label="点赞增量阈值" /><span>或增幅</span><span className="monitor-suffix"><input type="number" min={0} max={1000} value={Math.round(draft.surge_like_ratio * 100)} onChange={num("surge_like_ratio", 100)} aria-label="点赞增幅阈值" />%</span>
            <span>评论增加</span><input type="number" min={0} value={draft.surge_comment_delta} onChange={num("surge_comment_delta")} aria-label="评论增量阈值" /><span>或增幅</span><span className="monitor-suffix"><input type="number" min={0} max={1000} value={Math.round(draft.surge_comment_ratio * 100)} onChange={num("surge_comment_ratio", 100)} aria-label="评论增幅阈值" />%</span>
            <span>点赞基数至少</span><input type="number" min={0} value={draft.surge_min_base} onChange={num("surge_min_base")} aria-label="增幅起算基数" /><small className="monitor-rules__hint">低于它不算增幅，免得 1 涨到 3 也算 200%</small>
          </div> : null}
        </fieldset>

        <fieldset>
          <legend>自动处理</legend>
          <label className="references-check"><input type="checkbox" checked={draft.auto_collect} onChange={(event) => set("auto_collect", event.target.checked)} />高分或飙升的自动采进素材库（会下载原视频）</label>
          {draft.auto_collect ? <div className="monitor-sub">
            <label className="references-check"><input type="checkbox" checked={draft.auto_transcribe} onChange={(event) => set("auto_transcribe", event.target.checked)} />同时转写口播（通义听悟，占每日额度）</label>
            <label className="references-check"><input type="checkbox" checked={draft.auto_analyze} onChange={(event) => set("auto_analyze", event.target.checked)} />自动深度拆解（看画面和口播，较贵）</label>
            <label className="references-check"><input type="checkbox" checked={draft.auto_insight} onChange={(event) => set("auto_insight", event.target.checked)} />自动做评论洞察（找痛点和机会点）</label>
          </div> : null}
        </fieldset>
        {error ? <p className="studio-notice" data-tone="error" role="alert">{error}</p> : null}
        <footer>
          <label className="references-check monitor-enabled"><input type="checkbox" checked={draft.enabled} onChange={(event) => set("enabled", event.target.checked)} />启用</label>
          <span className="grow" />
          <button type="button" className="studio-button" onClick={onClose}>取消</button>
          <button type="submit" className="studio-button studio-button--primary" disabled={busy}>{busy ? "保存中…" : editing ? "保存" : "创建并立即搜一次"}</button>
        </footer>
      </form>
    </div>
  </div>;
}

function RunHistory({ api, task }: { api: ReferenceApi; task: MonitorTask }) {
  const [runs, setRuns] = useState<MonitorRun[] | null>(null);
  useEffect(() => {
    let alive = true;
    void (async () => {
      try {
        const response = await api(`/api/monitors/${task.id}/runs?limit=10`);
        if (response.ok && alive) setRuns(await response.json());
      } catch { if (alive) setRuns([]); }
    })();
    return () => { alive = false; };
  }, [api, task.id, task.last_run_at]);
  if (!runs) return <p className="studio-metrics" role="status">读取中…</p>;
  if (!runs.length) return <p className="studio-metrics">还没运行过。</p>;
  return <ol className="monitor-runs">{runs.map((run) => <li key={run.id}>
    <span className="studio-status" data-status={run.status === "completed" ? "ready" : run.status === "running" ? "working" : "attention"}>{run.status === "completed" ? "完成" : run.status === "running" ? "运行中" : "失败"}</span>
    <time>{timeAgo(run.started_at)}</time>
    {run.status === "failed" ? <span className="monitor-runs__error">{run.error}</span> : <span className="studio-metrics"><span>搜到 {run.found}</span><span>新 {run.new_count}</span><span>飙升 {run.surge_count}</span><span>采集 {run.collected_count}</span></span>}
  </li>)}</ol>;
}

function TaskCard({ api, task, active, busy, onSelect, onRun, onToggle, onEdit, onDelete }: {
  api: ReferenceApi; task: MonitorTask; active: boolean; busy: boolean;
  onSelect: () => void; onRun: () => void; onToggle: () => void; onEdit: () => void; onDelete: () => void;
}) {
  const [showRuns, setShowRuns] = useState(false);
  const state = task.running ? { label: "搜索中", tone: "working" } : !task.enabled ? { label: "已暂停", tone: "draft" } : task.last_status === "failed" ? { label: "上次失败", tone: "attention" } : { label: "监控中", tone: "ready" };
  return <li className="monitor-task" aria-current={active ? "true" : undefined}>
    <button type="button" className="monitor-task__main" onClick={onSelect}>
      <span className="monitor-task__top">
        <span className="reference-platform" data-platform={task.platform}>{platformLabels[task.platform]}</span>
        <span className="studio-status" data-status={state.tone}>{state.label}</span>
        {task.surge_count ? <span className="monitor-pill" data-tone="surge"><Flame size={11} aria-hidden="true" />{task.surge_count}</span> : null}
        {task.unread_count ? <span className="monitor-pill" data-tone="unread">{task.unread_count} 未读</span> : null}
      </span>
      <strong>{task.name || task.keyword}</strong>
      <span className="studio-metrics">
        <span>每 {intervalLabel(task.interval_minutes)}</span>
        <span>{task.item_count} 条</span>
        {task.last_run_at ? <span>上次 {timeAgo(task.last_run_at)}</span> : null}
        {task.enabled && !task.running && task.next_run_at ? <span>下次 {timeUntil(task.next_run_at)}</span> : null}
      </span>
    </button>
    {task.last_status === "failed" && task.last_error ? <p className="monitor-task__error" role="alert">{task.last_error}</p> : null}
    <div className="monitor-task__actions">
      <button type="button" className="studio-button studio-button--quiet" disabled={busy || task.running} onClick={onRun} aria-label={`立即搜一次：${task.name || task.keyword}`}><Play size={14} aria-hidden="true" />立即搜</button>
      <button type="button" className="studio-button studio-button--quiet" disabled={busy} onClick={onToggle}>{task.enabled ? "暂停" : "启用"}</button>
      <button type="button" className="studio-button studio-button--quiet" aria-expanded={showRuns} onClick={() => setShowRuns((value) => !value)} aria-label="运行记录"><History size={14} aria-hidden="true" /></button>
      <span className="grow" />
      <button type="button" className="studio-button studio-button--quiet" onClick={onEdit} aria-label={`编辑：${task.name || task.keyword}`}><Pencil size={14} aria-hidden="true" /></button>
      <button type="button" className="studio-button studio-button--quiet studio-button--danger" disabled={busy} onClick={onDelete} aria-label={`删除：${task.name || task.keyword}`}><Trash2 size={14} aria-hidden="true" /></button>
    </div>
    {showRuns ? <RunHistory api={api} task={task} /> : null}
  </li>;
}

function FeedCard({ item, threshold, busy, onPreview, onCollect, onOpen, onDismiss, onRescore, onRead }: {
  item: MonitorItem; threshold: number; busy: boolean;
  onPreview: () => void; onCollect: (transcribe: boolean) => void; onOpen: () => void; onDismiss: () => void; onRescore: () => void; onRead: () => void;
}) {
  const collected = Boolean(item.reference_id);
  return <li className="monitor-item" data-surge={item.surge ? "true" : undefined} data-unread={item.read ? undefined : "true"} onMouseEnter={item.read ? undefined : onRead} onFocus={item.read ? undefined : onRead}>
    <PreviewThumb platform={item.platform} coverURL={item.cover_url} videoURL={item.video_url} durationS={item.duration_s} title={item.title} onPlay={onPreview} />
    <div className="monitor-item__body">
      <div className="monitor-item__flags">
        {item.surge ? <span className="monitor-pill" data-tone="surge"><Flame size={11} aria-hidden="true" />飙升</span> : null}
        {item.seen_count === 1 ? <span className="monitor-pill" data-tone="new">新发现</span> : null}
        {!item.read ? <span className="monitor-dot" aria-label="未读" /> : null}
        <span className="studio-metrics"><span>{item.task_name}</span><span>{platformLabels[item.platform]}</span>{item.posted_at ? <span>发布 {relativeDay(item.posted_at)}</span> : null}<span>首次见到 {timeAgo(item.first_seen_at)}</span></span>
      </div>
      <strong className="monitor-item__title">{item.title || item.caption.slice(0, 40) || "（无标题）"}</strong>
      {item.surge && item.surge_reason ? <p className="monitor-item__surge"><Flame size={13} aria-hidden="true" />{item.surge_reason}</p> : null}
      <div className="monitor-item__stats">
        <span className="studio-metrics"><span>{item.author || "未知作者"}</span><span>赞 {formatCount(item.likes)}{item.like_delta ? <em data-up={item.like_delta > 0 ? "true" : undefined}> {signed(item.like_delta)}</em> : null}</span><span>评 {formatCount(item.comments)}{item.comment_delta ? <em data-up={item.comment_delta > 0 ? "true" : undefined}> {signed(item.comment_delta)}</em> : null}</span>{item.collects ? <span>藏 {formatCount(item.collects)}</span> : null}{item.like_rate_per_hour >= 1 ? <span>{formatCount(Math.round(item.like_rate_per_hour))} 赞/小时</span> : null}</span>
        <Sparkline history={item.history} />
      </div>
      {item.score_status === "scored" ? <div className="monitor-score" data-tone={scoreTone(item.score, threshold)}>
        <span className="monitor-score__num" aria-label={`AI 评分 ${item.score}`}>{item.score}</span>
        <div>
          <p>{item.score_reason}</p>
          {item.score_tags.length ? <span className="monitor-tags">{item.score_tags.map((tag) => <span key={tag}>{tag}</span>)}</span> : null}
        </div>
      </div> : null}
      {item.score_status === "pending" ? <p className="studio-metrics" role="status">AI 打分中…</p> : null}
      {item.score_status === "failed" ? <p className="monitor-item__error">打分失败：{item.score_error || "模型没有返回结果"}</p> : null}
      <div className="monitor-item__actions">
        {collected
          ? <button type="button" className="studio-button" onClick={onOpen}><ArrowUpRight size={14} aria-hidden="true" />打开素材</button>
          : <>
            <button type="button" className="studio-button" disabled={busy} onClick={() => onCollect(false)}>采集</button>
            <button type="button" className="studio-button" disabled={busy || !item.video_url} onClick={() => onCollect(true)}>采集并转写</button>
          </>}
        <button type="button" className="studio-button studio-button--quiet" disabled={busy} onClick={onRescore}><Sparkles size={14} aria-hidden="true" />{item.score_status === "scored" ? "重新打分" : "AI 打分"}</button>
        <span className="grow" />
        {item.url ? <a className="studio-button studio-button--quiet" href={item.url} target="_blank" rel="noreferrer" aria-label="原帖"><ArrowUpRight size={14} aria-hidden="true" /></a> : null}
        <button type="button" className="studio-button studio-button--quiet" disabled={busy} onClick={onDismiss} aria-label="不再显示"><EyeOff size={14} aria-hidden="true" /></button>
      </div>
    </div>
  </li>;
}

/** 监控：关键词定时搜，AI 打分，盯住数据飙升的视频，按优先级排给你看。 */
export function MonitorBoard({ api, disabled, onMessage, onOpenReference }: { api: ReferenceApi; disabled: boolean; onMessage: (message: Message) => void; onOpenReference: (post: ReferencePost) => void }) {
  const [tasks, setTasks] = useState<MonitorTask[] | null>(null);
  const [taskID, setTaskID] = useState("");
  const [filter, setFilter] = useState<FeedFilter>("all");
  const [feed, setFeed] = useState<MonitorFeed | null>(null);
  const [feedError, setFeedError] = useState(false);
  const [dialog, setDialog] = useState<{ task?: MonitorTask } | null>(null);
  const [busyID, setBusyID] = useState("");
  const [preview, setPreview] = useState<MonitorItem | null>(null);

  const loadTasks = useCallback(async () => {
    try {
      const response = await api("/api/monitors");
      if (!response.ok) throw new Error();
      setTasks(await response.json());
    } catch {
      setTasks((current) => current ?? []);
    }
  }, [api]);
  const loadFeed = useCallback(async () => {
    try {
      const params = new URLSearchParams({ filter, limit: "100" });
      if (taskID) params.set("task_id", taskID);
      const response = await api(`/api/monitors/feed?${params}`);
      if (!response.ok) throw new Error();
      setFeed(await response.json());
      setFeedError(false);
    } catch {
      setFeedError(true);
    }
  }, [api, filter, taskID]);

  useEffect(() => { void loadTasks(); }, [loadTasks]);
  useEffect(() => { void loadFeed(); }, [loadFeed]);

  const anyRunning = Boolean(tasks?.some((task) => task.running)) || Boolean(feed?.items.some((item) => item.score_status === "pending"));
  useEffect(() => {
    const timer = window.setInterval(() => { void loadTasks(); void loadFeed(); }, anyRunning ? 5000 : 60000);
    return () => window.clearInterval(timer);
  }, [anyRunning, loadTasks, loadFeed]);

  const thresholds = useMemo(() => new Map((tasks ?? []).map((task) => [task.id, task.score_threshold])), [tasks]);
  const replaceTask = (task: MonitorTask) => setTasks((current) => (current ?? []).some((item) => item.id === task.id) ? (current ?? []).map((item) => item.id === task.id ? task : item) : [task, ...(current ?? [])]);
  const replaceItem = (item: MonitorItem) => setFeed((current) => current ? { ...current, items: current.items.map((entry) => entry.id === item.id ? item : entry) } : current);

  const send = async <T,>(path: string, init: RequestInit, fallback: string): Promise<T | null> => {
    const response = await api(path, init);
    if (!response.ok) throw new Error(await readError(response, fallback));
    return response.status === 204 ? null : (await response.json()) as T;
  };
  const json = (method: string, body: unknown): RequestInit => ({ method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  const guarded = async (id: string, work: () => Promise<void>) => {
    setBusyID(id);
    try { await work(); } catch (error) { onMessage({ tone: "error", text: error instanceof Error ? error.message : "操作失败。" }); } finally { setBusyID(""); }
  };

  const saveTask = async (draft: Draft) => {
    const editing = dialog?.task;
    const task = editing
      ? await send<MonitorTask>(`/api/monitors/${editing.id}`, json("PATCH", draft), "保存失败。")
      : await send<MonitorTask>("/api/monitors", json("POST", draft), "创建失败。");
    if (task) { replaceTask(task); if (!editing) setTaskID(task.id); }
    setDialog(null);
    onMessage({ tone: "ok", text: editing ? "监控已保存。" : `已创建「${task?.name || draft.keyword}」，正在搜第一轮。` });
    void loadFeed();
  };
  const runTask = (task: MonitorTask) => guarded(task.id, async () => {
    const updated = await send<MonitorTask>(`/api/monitors/${task.id}/run`, { method: "POST" }, "启动失败。");
    if (updated) replaceTask(updated);
    onMessage({ tone: "info", text: `正在搜「${task.name || task.keyword}」，完成后自动刷新。` });
  });
  const toggleTask = (task: MonitorTask) => guarded(task.id, async () => {
    const updated = await send<MonitorTask>(`/api/monitors/${task.id}`, json("PATCH", { enabled: !task.enabled }), "操作失败。");
    if (updated) replaceTask(updated);
  });
  const deleteTask = (task: MonitorTask) => {
    if (!window.confirm(`删除监控「${task.name || task.keyword}」？它搜到的记录和数据快照会一起删掉，已采进素材库的不受影响。`)) return;
    void guarded(task.id, async () => {
      await send(`/api/monitors/${task.id}`, { method: "DELETE" }, "删除失败。");
      setTasks((current) => (current ?? []).filter((item) => item.id !== task.id));
      if (taskID === task.id) setTaskID("");
      void loadFeed();
    });
  };

  const markRead = (item: MonitorItem) => {
    replaceItem({ ...item, read: true });
    void api(`/api/monitors/items/${item.id}`, json("PATCH", { read: true })).then(() => undefined, () => undefined);
  };
  const readAll = () => guarded("read-all", async () => {
    await send("/api/monitors/items/read-all", json("POST", { task_id: taskID }), "操作失败。");
    void loadFeed();
    void loadTasks();
  });
  const dismiss = (item: MonitorItem) => guarded(item.id, async () => {
    await send(`/api/monitors/items/${item.id}`, json("PATCH", { dismissed: true }), "操作失败。");
    setFeed((current) => current ? { ...current, items: current.items.filter((entry) => entry.id !== item.id) } : current);
  });
  const collect = (item: MonitorItem, transcribe: boolean) => guarded(item.id, async () => {
    const post = await send<ReferencePost>(`/api/monitors/items/${item.id}/collect`, json("POST", { transcribe }), "采集失败。");
    if (post) {
      replaceItem({ ...item, reference_id: post.id, read: true });
      onMessage({ tone: "ok", text: transcribe ? "已采进素材库，口播正在转写。" : "已采进素材库，原视频在后台下载。" });
    }
  });
  const rescore = (item: MonitorItem) => guarded(item.id, async () => {
    replaceItem({ ...item, score_status: "pending" });
    try {
      const updated = await send<MonitorItem>(`/api/monitors/items/${item.id}/score`, { method: "POST" }, "打分失败。");
      if (updated) replaceItem(updated);
    } catch (error) {
      replaceItem(item);
      throw error;
    }
  });
  const open = (item: MonitorItem) => guarded(item.id, async () => {
    const post = await send<ReferencePost>(`/api/references/${item.reference_id}`, { method: "GET" }, "素材读取失败，可能已被删除。");
    if (post) onOpenReference(post);
  });

  const counts = feed?.counts ?? { all: 0, surge: 0, high: 0, unread: 0 };
  const filters: Array<[FeedFilter, string, number]> = [["all", "全部", counts.all], ["surge", "飙升", counts.surge], ["high", "高分", counts.high], ["unread", "未读", counts.unread]];
  const previewTarget: PreviewTarget | null = preview ? { platform: preview.platform, title: preview.title, author: preview.author, url: preview.url, coverURL: preview.cover_url, videoURL: preview.video_url } : null;

  return <div className="studio-grid monitor-layout">
    <section className="studio-panel monitor-tasks" aria-labelledby="monitor-tasks-title">
      <header>
        <div><h2 id="monitor-tasks-title">监控任务</h2><p>关键词定时自动搜，只在控制台开着时运行。</p></div>
        <button type="button" className="studio-button studio-button--primary" disabled={disabled} onClick={() => setDialog({})}><Plus size={15} aria-hidden="true" />新建</button>
      </header>
      {tasks === null ? <p className="studio-empty" role="status">正在读取…</p> : null}
      {tasks?.length === 0 ? <div className="studio-empty">
        <Radar size={26} strokeWidth={1.5} aria-hidden="true" />
        <strong>还没有监控</strong>
        <span>比如「岚山海鲜」每 30 分钟搜一次：新视频 AI 打分，点赞暴涨的标成飙升，排在最前面。</span>
        <button type="button" className="studio-button studio-button--primary" disabled={disabled} onClick={() => setDialog({})}><Plus size={15} aria-hidden="true" />新建监控</button>
      </div> : null}
      {tasks?.length ? <>
        <button type="button" className="monitor-all" aria-current={taskID === "" ? "true" : undefined} onClick={() => setTaskID("")}>全部任务</button>
        <ul className="studio-list monitor-task-list">{tasks.map((task) => <TaskCard key={task.id} api={api} task={task} active={task.id === taskID} busy={busyID === task.id}
          onSelect={() => setTaskID(task.id === taskID ? "" : task.id)} onRun={() => void runTask(task)} onToggle={() => void toggleTask(task)} onEdit={() => setDialog({ task })} onDelete={() => deleteTask(task)} />)}</ul>
      </> : null}
    </section>

    <section className="studio-panel monitor-feed" aria-labelledby="monitor-feed-title">
      <header>
        <div><h2 id="monitor-feed-title">发现</h2><p>飙升的排最前，其次是高分新视频。鼠标移过即标为已读。</p></div>
        <div className="studio-row">
          <button type="button" className="studio-button studio-button--quiet" disabled={!counts.unread || busyID === "read-all"} onClick={() => void readAll()}><CheckCheck size={15} aria-hidden="true" />全部已读</button>
          <button type="button" className="studio-button studio-button--quiet" aria-label="刷新发现" onClick={() => { void loadFeed(); void loadTasks(); }}><RefreshCw size={15} aria-hidden="true" /></button>
        </div>
      </header>
      <div className="studio-tabs monitor-filters" role="tablist" aria-label="筛选">
        {filters.map(([value, label, count]) => <button key={value} type="button" role="tab" aria-selected={filter === value} onClick={() => setFilter(value)}>
          {value === "surge" ? <Flame size={13} aria-hidden="true" /> : value === "unread" ? <BellDot size={13} aria-hidden="true" /> : null}{label}<span className="monitor-count">{count}</span>
        </button>)}
      </div>
      {feedError ? <div role="alert" className="studio-notice" data-tone="error">发现列表读取失败。<button type="button" className="studio-button studio-button--quiet" onClick={() => void loadFeed()}>重试</button></div> : null}
      {feed && feed.items.length === 0 ? <div className="studio-empty">
        <strong>{filter === "surge" ? "暂时没有飙升的视频" : filter === "high" ? "还没有达到高分线的视频" : filter === "unread" ? "都看过了" : tasks?.length ? "还没有搜到内容" : "建一个监控就会出现在这里"}</strong>
        <span>{filter === "surge" ? "同一条视频至少要被搜到两次才能比较涨幅。" : tasks?.some((task) => task.running) ? "正在搜索，稍等片刻。" : ""}</span>
      </div> : null}
      {feed?.items.length ? <ul className="studio-list monitor-items">{feed.items.map((item) => <FeedCard key={item.id} item={item} threshold={thresholds.get(item.task_id) ?? 70} busy={busyID === item.id}
        onPreview={() => { setPreview(item); if (!item.read) markRead(item); }} onCollect={(transcribe) => void collect(item, transcribe)} onOpen={() => void open(item)}
        onDismiss={() => void dismiss(item)} onRescore={() => void rescore(item)} onRead={() => markRead(item)} />)}</ul> : null}
    </section>

    {dialog ? <TaskDialog initial={dialog.task ? draftFrom(dialog.task) : defaultDraft} editing={Boolean(dialog.task)} onClose={() => setDialog(null)} onSubmit={saveTask} /> : null}
    {previewTarget && preview ? <PreviewPlayer target={previewTarget} onClose={() => setPreview(null)} actions={preview.reference_id
      ? <button type="button" className="studio-button" onClick={() => { void open(preview); setPreview(null); }}>打开素材</button>
      : <>
        <button type="button" className="studio-button" disabled={busyID === preview.id} onClick={() => { void collect(preview, false); setPreview(null); }}>采集</button>
        <button type="button" className="studio-button studio-button--primary" disabled={busyID === preview.id} onClick={() => { void collect(preview, true); setPreview(null); }}>采集并转写</button>
      </>} /> : null}
  </div>;
}
