import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import { RefreshCw, X } from "lucide-react";
import {
  fetchRemixLabPublished,
  saveRemixLabPublishedMetrics,
  type RemixLabApi,
  type RemixLabPublishedMetrics,
  type RemixLabPublishedScript,
} from "./api";
import type { Account } from "../types";

// 交付文案库：每条出过剪映草稿的成稿都自动进来；操作员手填播放/点赞/
// 出单，表格按转化率排一排，一眼看出哪种开头和课尾在赔本赚吆喝。

type Props = {
  api: RemixLabApi;
  accounts: Account[];
  onClose: () => void;
  onOpenRun: (experimentID: string) => void;
  onMessage: (text: string) => void;
};

type Draft = { views: string; likes: string; orders: string; notes: string };

function toDraft(m?: RemixLabPublishedMetrics | null): Draft {
  return {
    views: m?.views ? String(m.views) : "",
    likes: m?.likes ? String(m.likes) : "",
    orders: m?.orders ? String(m.orders) : "",
    notes: m?.notes ?? "",
  };
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** 每万播放出单数：把播放量差距很大的视频拉到同一尺度比。 */
function ordersPer10k(m?: RemixLabPublishedMetrics | null): string {
  if (!m || !m.views) return "—";
  return ((m.orders / m.views) * 10000).toFixed(2);
}

export function PublishedLibrary({ api, accounts, onClose, onOpenRun, onMessage }: Props) {
  const revisions = useRef(new Map<string, number>());
  const saveLock = useRef(false);
  const dirtyRuns = useRef(new Set<string>());
  const loadLock = useRef(false);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [items, setItems] = useState<RemixLabPublishedScript[]>([]);
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [expanded, setExpanded] = useState<string>("");
  const [saving, setSaving] = useState<string>("");
  const [accountFilter, setAccountFilter] = useState("");
  const [sortBy, setSortBy] = useState<"time" | "views" | "rate">("time");

  const load = async () => {
    if (loadLock.current) return;
    loadLock.current = true;
    setLoading(true);
    setLoadError("");
    try {
      const list = await fetchRemixLabPublished(api);
      setItems(list);
      setDrafts(current => Object.fromEntries(list.map(item => [item.run_id, dirtyRuns.current.has(item.run_id) ? current[item.run_id] : toDraft(item.metrics)])));
    } catch (error) {
      setLoadError(error instanceof Error ? error.message : "文案库读取失败。");
    } finally { loadLock.current = false; setLoading(false); }
  };

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api]);

  const accountName = (id: string) => accounts.find((a) => a.id === id)?.name || "未分组";

  const shown = useMemo(() => {
    const filtered = accountFilter ? items.filter((item) => item.account_id === accountFilter) : items;
    const rate = (m?: RemixLabPublishedMetrics | null) => (m && m.views ? m.orders / m.views : -1);
    return [...filtered].sort((a, b) => {
      if (sortBy === "views") return (b.metrics?.views ?? 0) - (a.metrics?.views ?? 0);
      if (sortBy === "rate") return rate(b.metrics) - rate(a.metrics);
      return b.produced_at.localeCompare(a.produced_at);
    });
  }, [items, accountFilter, sortBy]);

  const totals = useMemo(() => {
    let views = 0, likes = 0, orders = 0, filled = 0;
    for (const item of shown) {
      if (!item.metrics) continue;
      filled++;
      views += item.metrics.views;
      likes += item.metrics.likes;
      orders += item.metrics.orders;
    }
    return { views, likes, orders, filled };
  }, [shown]);

  const patchDraft = (runID: string, patch: Partial<Draft>) => {
    revisions.current.set(runID, (revisions.current.get(runID) || 0) + 1);
    dirtyRuns.current.add(runID);
    setDrafts((current) => ({ ...current, [runID]: { ...(current[runID] ?? toDraft()), ...patch } }));
  };

  const save = async (runID: string) => {
    if (saveLock.current) return;
    saveLock.current = true;
    const revision = revisions.current.get(runID);
    const draft = drafts[runID] ?? toDraft();
    setSaving(runID);
    try {
      const metrics: RemixLabPublishedMetrics = {
        views: Math.max(0, Math.trunc(Number(draft.views) || 0)),
        likes: Math.max(0, Math.trunc(Number(draft.likes) || 0)),
        orders: Math.max(0, Math.trunc(Number(draft.orders) || 0)),
        notes: draft.notes.trim(),
      };
      await saveRemixLabPublishedMetrics(api, runID, metrics);
      setItems((current) =>
        current.map((item) => (item.run_id === runID ? { ...item, metrics: { ...metrics, updated_at: new Date().toISOString() } } : item)),
      );
      if (revisions.current.get(runID) === revision) dirtyRuns.current.delete(runID);
      onMessage("成绩已保存。");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "成绩保存失败。");
    } finally {
      saveLock.current = false;
      setSaving("");
    }
  };

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div
        className="preview-modal remix-lab-modal remix-lab-modal--wide remix-lab-published"
        role="dialog"
        aria-modal="true"
        aria-labelledby="remix-lab-published-title"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="modal-head">
          <div>
            <span className="muted">草稿交付与发布复盘</span>
            <h2 id="remix-lab-published-title">交付文案库</h2>
          </div>
          <button type="button" className="close" aria-label="关闭文案库" onClick={onClose}>
            <X size={20} aria-hidden="true" />
          </button>
        </div>

        <div className="remix-lab-published__bar">
          <label>
            账号
            <select value={accountFilter} onChange={(event) => setAccountFilter(event.target.value)}>
              <option value="">全部</option>
              {accounts.map((a) => (
                <option key={a.id} value={a.id}>{a.name}</option>
              ))}
            </select>
          </label>
          <label>
            排序
            <select value={sortBy} onChange={(event) => setSortBy(event.target.value as typeof sortBy)}>
              <option value="time">最新在前</option>
              <option value="views">播放量</option>
              <option value="rate">万播出单</option>
            </select>
          </label>
          <span className="remix-lab-muted">
            {shown.length} 条 · 已填成绩 {totals.filled} 条 · 合计播放 {totals.views.toLocaleString()} · 点赞 {totals.likes.toLocaleString()} · 出单 {totals.orders}
            {totals.views ? ` · 万播出单 ${((totals.orders / totals.views) * 10000).toFixed(2)}` : ""}
          </span>
          <button type="button" className="header-button remix-lab-icon-btn" disabled={loading} onClick={() => void load()}>
            <RefreshCw size={13} strokeWidth={2} />
            刷新
          </button>
        </div>

        <div className="remix-lab-published__table">
          <table>
            <thead>
              <tr>
                <th>出稿时间</th>
                <th>账号</th>
                <th>标题</th>
                <th>模型</th>
                <th>播放</th>
                <th>点赞</th>
                <th>出单</th>
                <th>万播出单</th>
                <th>备注</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {loading ? (<tr><td colSpan={10} role="status">正在读取交付文案…</td></tr>) : loadError ? (<tr><td colSpan={10} role="alert">{loadError} <button type="button" onClick={() => void load()}>重试读取</button></td></tr>) : shown.length === 0 ? (
                <tr><td colSpan={10} className="remix-lab-published__empty">还没有出过草稿的成稿。混剪跑完后会自动出现在这里。</td></tr>
              ) : shown.map((item) => {
                const draft = drafts[item.run_id] ?? toDraft(item.metrics);
                const open = expanded === item.run_id;
                return (
                  <Fragment key={item.run_id}>
                    <tr className={item.published ? "" : "remix-lab-published__unpublished"}>
                      <td><small>{formatDate(item.produced_at)}</small><span className="remix-lab-chip">{item.published ? "已发布" : "待发布"}</span></td>
                      <td>{accountName(item.account_id)}</td>
                      <td className="remix-lab-published__title">
                        <button type="button" className="remix-lab-published__toggle" onClick={() => setExpanded(open ? "" : item.run_id)}>
                          {item.board_title || item.title}
                        </button>
                      </td>
                      <td><small>{item.model}</small></td>
                      <td><input type="number" min={0} aria-label="播放量" value={draft.views} onChange={(e) => patchDraft(item.run_id, { views: e.target.value })} /></td>
                      <td><input type="number" min={0} aria-label="点赞数" value={draft.likes} onChange={(e) => patchDraft(item.run_id, { likes: e.target.value })} /></td>
                      <td><input type="number" min={0} aria-label="出单数" value={draft.orders} onChange={(e) => patchDraft(item.run_id, { orders: e.target.value })} /></td>
                      <td>{ordersPer10k(item.metrics)}</td>
                      <td><input type="text" aria-label="备注" placeholder="为什么爆 / 为什么不出单" value={draft.notes} onChange={(e) => patchDraft(item.run_id, { notes: e.target.value })} /></td>
                      <td>
                        <button type="button" className="header-button" disabled={saving === item.run_id} onClick={() => void save(item.run_id)}>
                          {saving === item.run_id ? "保存中…" : "保存"}
                        </button>
                      </td>
                    </tr>
                    {open ? (
                      <tr className="remix-lab-published__detail">
                        <td colSpan={10}>
                          <div className="remix-lab-published__cols">
                            <div>
                              <b>开头</b>
                              <p>{item.opening}</p>
                            </div>
                            <div>
                              <b>课尾（最后 300 字）</b>
                              <p>{item.ending}</p>
                            </div>
                          </div>
                          <details>
                            <summary>全文（{item.script_runes} 字） · 提示词 {item.prompt_stamp}</summary>
                            <pre>{item.script}</pre>
                          </details>
                          <button type="button" className="header-button" onClick={() => onOpenRun(item.experiment_id)}>
                            打开这次运行
                          </button>
                        </td>
                      </tr>
                    ) : null}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
