import { useCallback, useEffect, useMemo, useState } from "react";
import { ClipboardCopy, Lightbulb, Loader2, MessageSquareQuote, Sparkles, Trash2 } from "lucide-react";
import type { ReferenceApi, ReferencePost } from "./ReferencesPage";
import type { CommentInsight, CommentInsightReport } from "./monitorTypes";
import { useModelOptions } from "../ModelSelect";

const statusLabels: Record<CommentInsight["status"], { label: string; tone: string }> = {
  queued: { label: "排队中", tone: "working" },
  running: { label: "归纳中", tone: "working" },
  completed: { label: "已完成", tone: "ready" },
  failed: { label: "失败", tone: "attention" },
};

async function readError(response: Response, fallback: string) {
  try {
    const body = await response.json();
    return typeof body?.message === "string" && body.message ? body.message : fallback;
  } catch {
    return fallback;
  }
}

const filled = <T,>(items?: T[]) => (items ?? []).filter(Boolean);

export function insightMarkdown(post: ReferencePost, report: CommentInsightReport) {
  const lines = [`# 评论洞察：${post.title || post.external_id}`, ""];
  const quotes = (items?: string[]) => filled(items).map((quote) => `  > ${quote}`);
  if (report.summary) lines.push(report.summary, "");
  if (filled(report.pain_points).length) lines.push("## 用户痛点", "", ...filled(report.pain_points).flatMap((item) => [`- **${item.point}**${item.mentions ? `（约 ${item.mentions} 人提到）` : ""}：${item.detail || ""}`, ...quotes(item.quotes)]), "");
  if (filled(report.questions).length) lines.push("## 反复被问的问题", "", ...filled(report.questions).flatMap((item) => [`- ${item.question}${item.mentions ? `（${item.mentions}）` : ""}`, ...quotes(item.quotes)]), "");
  if (filled(report.needs).length) lines.push("## 没被满足的需求", "", ...filled(report.needs).flatMap((item) => [`- ${item.need}`, ...quotes(item.quotes)]), "");
  if (filled(report.opportunities).length) lines.push("## 机会点", "", ...filled(report.opportunities).flatMap((item) => [`- **${item.title}**：${item.why || ""}`, `  - 怎么做：${item.action || ""}`, ...quotes(item.quotes)]), "");
  if (filled(report.content_ideas).length) lines.push("## 可拍的选题", "", ...filled(report.content_ideas).map((item) => `- **${item.title}**｜开头：${item.hook || ""}｜角度：${item.angle || ""}`), "");
  if (filled(report.objections).length) lines.push("## 顾虑", "", ...filled(report.objections).map((item) => `- ${item}`), "");
  return lines.join("\n");
}

function Quotes({ items }: { items?: string[] }) {
  const list = filled(items).slice(0, 3);
  if (!list.length) return null;
  return <ul className="insight-quotes">{list.map((quote, index) => <li key={index}><MessageSquareQuote size={12} aria-hidden="true" />{quote}</li>)}</ul>;
}

function Mentions({ count }: { count?: number }) {
  if (!count) return null;
  return <span className="insight-mentions" title="提到的大约人数">{count} 人提到</span>;
}

export function InsightReport({ report }: { report: CommentInsightReport }) {
  const sentiment = report.sentiment ?? {};
  const total = (sentiment.positive ?? 0) + (sentiment.neutral ?? 0) + (sentiment.negative ?? 0);
  return <div className="insight-report">
    {report.summary ? <p className="analysis-report__summary">{report.summary}</p> : null}
    {total > 0 ? <div className="insight-sentiment" aria-label={`情绪：正面 ${sentiment.positive ?? 0}%，中性 ${sentiment.neutral ?? 0}%，负面 ${sentiment.negative ?? 0}%`}>
      <div className="insight-sentiment__bar">
        <span data-tone="positive" style={{ flexGrow: sentiment.positive ?? 0 }} />
        <span data-tone="neutral" style={{ flexGrow: sentiment.neutral ?? 0 }} />
        <span data-tone="negative" style={{ flexGrow: sentiment.negative ?? 0 }} />
      </div>
      <span className="studio-metrics"><span>正面 {sentiment.positive ?? 0}%</span><span>中性 {sentiment.neutral ?? 0}%</span><span>负面 {sentiment.negative ?? 0}%</span></span>
    </div> : null}

    {filled(report.opportunities).length ? <section className="analysis-card insight-card--opportunity">
      <h3><Lightbulb size={15} aria-hidden="true" />机会点</h3>
      <ol className="insight-list">{filled(report.opportunities).map((item, index) => <li key={index}>
        <strong>{item.title}</strong>
        {item.why ? <p>{item.why}</p> : null}
        {item.action ? <p className="insight-action"><span>怎么做</span>{item.action}</p> : null}
        <Quotes items={item.quotes} />
      </li>)}</ol>
    </section> : null}

    <div className="analysis-grid">
      {filled(report.pain_points).length ? <section className="analysis-card">
        <h3>用户痛点</h3>
        <ul className="insight-list">{filled(report.pain_points).map((item, index) => <li key={index}>
          <strong>{item.point}</strong><Mentions count={item.mentions} />
          {item.detail ? <p>{item.detail}</p> : null}
          <Quotes items={item.quotes} />
        </li>)}</ul>
      </section> : null}
      {filled(report.questions).length ? <section className="analysis-card">
        <h3>反复被问</h3>
        <ul className="insight-list">{filled(report.questions).map((item, index) => <li key={index}>
          <strong>{item.question}</strong><Mentions count={item.mentions} />
          <Quotes items={item.quotes} />
        </li>)}</ul>
      </section> : null}
    </div>

    {filled(report.needs).length ? <section className="analysis-card">
      <h3>没被满足的需求</h3>
      <ul className="insight-list">{filled(report.needs).map((item, index) => <li key={index}><strong>{item.need}</strong><Quotes items={item.quotes} /></li>)}</ul>
    </section> : null}

    {filled(report.content_ideas).length ? <section className="analysis-card analysis-card--replicate">
      <h3><Sparkles size={15} aria-hidden="true" />可以拍的选题</h3>
      <ul className="insight-ideas">{filled(report.content_ideas).map((item, index) => <li key={index}>
        <strong>{item.title}</strong>
        {item.hook ? <p><span>开头</span>{item.hook}</p> : null}
        {item.angle ? <p><span>角度</span>{item.angle}</p> : null}
      </li>)}</ul>
    </section> : null}

    {filled(report.objections).length || filled(report.keywords).length ? <div className="analysis-grid">
      {filled(report.objections).length ? <section className="analysis-card"><h3>阻碍下单/到店的顾虑</h3><ul>{filled(report.objections).map((item, index) => <li key={index}>{item}</li>)}</ul></section> : null}
      {filled(report.keywords).length ? <section className="analysis-card"><h3>高频词</h3><p className="insight-keywords">{filled(report.keywords).map((item, index) => <span key={index}>{item.word}<small>{item.count}</small></span>)}</p></section> : null}
    </div> : null}
  </div>;
}

/** 评论洞察：拉评论交给模型，归纳痛点、问题和机会点，每条都带原评论作证据。 */
export function CommentInsightPanel({ api, post, onMessage }: { api: ReferenceApi; post: ReferencePost; onMessage: (message: { tone: "ok" | "error" | "info"; text: string }) => void }) {
  const models = useModelOptions();
  const [records, setRecords] = useState<CommentInsight[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [detail, setDetail] = useState<CommentInsight | null>(null);
  const [model, setModel] = useState("");
  const [busy, setBusy] = useState<"" | "start" | "delete">("");

  const loadList = useCallback(async () => {
    try {
      const response = await api(`/api/comment-insights?reference_id=${encodeURIComponent(post.id)}`);
      if (!response.ok) return;
      const items = (await response.json()) as CommentInsight[];
      setRecords(items);
      setSelectedID((current) => current && items.some((item) => item.id === current) ? current : items[0]?.id ?? "");
    } catch { /* advisory */ }
  }, [api, post.id]);
  useEffect(() => { void loadList(); }, [loadList]);

  const loadDetail = useCallback(async (id: string) => {
    if (!id) { setDetail(null); return; }
    try {
      const response = await api(`/api/comment-insights/${id}`);
      if (response.ok) setDetail(await response.json());
    } catch { /* keep the previous view */ }
  }, [api]);
  useEffect(() => { void loadDetail(selectedID); }, [selectedID, loadDetail]);

  const pending = records.some((record) => record.status === "queued" || record.status === "running");
  useEffect(() => {
    if (!pending) return;
    const timer = window.setInterval(() => { void loadList(); if (selectedID) void loadDetail(selectedID); }, 4000);
    return () => window.clearInterval(timer);
  }, [pending, loadList, loadDetail, selectedID]);

  const start = async () => {
    setBusy("start");
    try {
      const response = await api("/api/comment-insights", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ reference_id: post.id, model }) });
      if (!response.ok) throw new Error(await readError(response, "提交失败。"));
      const record = (await response.json()) as CommentInsight;
      setRecords((current) => [record, ...current.filter((item) => item.id !== record.id)]);
      setSelectedID(record.id);
      onMessage({ tone: "info", text: post.platform === "xhs" ? "正在拉全部评论（含楼中楼），再交给模型归纳，一般 1–3 分钟。" : "正在拉评论并交给模型归纳，一般 1–2 分钟。" });
    } catch (error) {
      onMessage({ tone: "error", text: error instanceof Error ? error.message : "提交失败。" });
    } finally {
      setBusy("");
    }
  };
  const remove = async (id: string) => {
    if (!window.confirm("删除这份评论洞察？")) return;
    setBusy("delete");
    try {
      const response = await api(`/api/comment-insights/${id}`, { method: "DELETE" });
      if (!response.ok) throw new Error(await readError(response, "删除失败。"));
      setRecords((current) => current.filter((item) => item.id !== id));
      setSelectedID("");
    } catch (error) {
      onMessage({ tone: "error", text: error instanceof Error ? error.message : "删除失败。" });
    } finally {
      setBusy("");
    }
  };

  const report = useMemo(() => detail?.status === "completed" ? detail.report ?? {} : null, [detail]);
  const markdown = useMemo(() => report ? insightMarkdown(post, report) : "", [post, report]);
  const copy = async () => {
    try { await navigator.clipboard.writeText(markdown); onMessage({ tone: "ok", text: "评论洞察已复制。" }); }
    catch { onMessage({ tone: "error", text: "复制失败。" }); }
  };

  return <div className="reference-analysis">
    <div className="reference-analysis__form">
      <p className="studio-metrics">把评论区交给模型：用户在抱怨什么、反复问什么、还缺什么，能做成什么机会。每个结论都附原评论，可以自己核对。</p>
      <div className="studio-row">
        <select aria-label="洞察模型" value={model} onChange={(event) => setModel(event.target.value)} className="reference-analysis__model">
          <option value="">默认拆解模型</option>
          {models.map((option) => <option key={option} value={option}>{option}</option>)}
        </select>
        <span className="grow" />
        <button type="button" className="studio-button studio-button--primary" disabled={pending || Boolean(busy)} onClick={() => void start()}>
          <Lightbulb size={15} aria-hidden="true" />{busy === "start" ? "正在提交…" : pending ? "归纳中…" : records.length ? "重新分析" : "分析评论"}
        </button>
      </div>
    </div>

    {records.length > 1 ? <div className="studio-row reference-analysis__history" role="group" aria-label="历史洞察">
      {records.map((record) => <button key={record.id} type="button" className="studio-button studio-button--quiet" aria-pressed={record.id === selectedID} onClick={() => setSelectedID(record.id)}>
        {new Date(record.created_at).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })} · {statusLabels[record.status].label}
      </button>)}
    </div> : null}

    {!records.length ? <div className="studio-empty"><MessageSquareQuote size={26} strokeWidth={1.5} aria-hidden="true" /><strong>还没有评论洞察</strong><span>{post.comments ? `这条有 ${post.comments} 条评论。` : ""}评论越多结论越可靠，少于 5 条没法归纳。</span></div> : null}

    {detail && (detail.status === "queued" || detail.status === "running") ? <div className="reference-media__placeholder" role="status"><Loader2 size={18} className="reference-spin" aria-hidden="true" /><span>{detail.status === "queued" ? "排队中…" : `正在归纳${detail.comment_count ? ` ${detail.comment_count} 条评论` : ""}…`}</span></div> : null}
    {detail?.status === "failed" ? <p className="studio-notice" data-tone="error" role="alert">{detail.error || "评论洞察失败。"}</p> : null}

    {report && detail ? <article className="analysis-report">
      <header className="studio-row">
        <span className="studio-status" data-status="ready">基于 {detail.comment_count} 条评论</span>
        <span className="studio-metrics">{detail.model || "默认模型"}</span>
        <span className="grow" />
        <button type="button" className="studio-button studio-button--quiet" onClick={() => void copy()}><ClipboardCopy size={15} aria-hidden="true" />复制</button>
        <button type="button" className="studio-button studio-button--quiet studio-button--danger" aria-label="删除这份洞察" disabled={Boolean(busy)} onClick={() => void remove(detail.id)}><Trash2 size={15} aria-hidden="true" /></button>
      </header>
      <InsightReport report={report} />
    </article> : null}
  </div>;
}
