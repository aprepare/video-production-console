import { useCallback, useEffect, useRef, useState } from "react";
import { Copy, Download, ImagePlus, RefreshCw, Sparkles } from "lucide-react";
import type { RemixLabApi } from "./api";

export type PublishingPackage = {
  titles?: string[];
  short_titles?: string[];
  descriptions?: string[];
  topics?: string[];
  cover_titles?: string[];
  cover_path?: string;
};

type CoverResult = { path: string; url: string; width: number; height: number; account: string };
type AICoverJob = { status: "none" | "running" | "done" | "failed"; titles?: [string, string]; started_at?: string; error?: string; cover?: CoverResult };

const AI_COVER_POLL_MS = 10_000;

function waitedMinutes(startedAt: string | undefined, now: number): number {
  const started = startedAt ? Date.parse(startedAt) : NaN;
  return Number.isNaN(started) ? 0 : Math.max(0, Math.floor((now - started) / 60_000));
}

/** 视频号短标题：一句话、无标点无空格、不超过十六个字。 */
export function shortTitleProblem(title: string): string {
  const length = [...title].length;
  if (/[\p{P}\p{S}\s]/u.test(title)) return "有标点或空格";
  if (length > 16) return `${length} 字，超了`;
  return "";
}

/** 封面两行大字：各一到十二个字，去掉标点。 */
export function cleanCoverLine(value: string): string {
  return value.replace(/[\p{P}\p{S}\s]/gu, "").slice(0, 12);
}

async function readError(response: Response, fallback: string) {
  try {
    const body = await response.json();
    return typeof body?.message === "string" && body.message ? body.message : fallback;
  } catch {
    return fallback;
  }
}

type Props = {
  api: RemixLabApi;
  runID: string;
  publishing: PublishingPackage | null;
  onCopy: (text: string, label: string) => void;
  onMessage: (message: string) => void;
};

/** 发布包：短标题、简介、封面大字与封面图，按视频号规则展示，点哪条复制哪条。 */
export function PublishPackage({ api, runID, publishing, onCopy, onMessage }: Props) {
  const initial = publishing?.cover_titles ?? [];
  const [line1, setLine1] = useState(initial[0] ?? publishing?.short_titles?.[0] ?? "");
  const [line2, setLine2] = useState(initial[1] ?? "");
  const [cover, setCover] = useState<CoverResult | null>(publishing?.cover_path ? { path: publishing.cover_path, url: `/api/remix-lab/runs/${runID}/cover`, width: 0, height: 0, account: "" } : null);
  const [busy, setBusy] = useState(false);
  const [version, setVersion] = useState(0);
  const [aiJob, setAIJob] = useState<AICoverJob | null>(null);
  const [now, setNow] = useState(() => Date.now());
  // 只有本页看着它从出图中变成完成时才换上新图，避免进页面时用旧的 AI 图盖掉后来的本地出图。
  const watchingRef = useRef(false);

  const loadAIJob = useCallback(async () => {
    try {
      const response = await api(`/api/remix-lab/runs/${runID}/ai-cover`);
      if (!response.ok) return;
      const job = (await response.json()) as AICoverJob;
      if (job.status === "done" && job.cover && watchingRef.current) {
        setCover(job.cover);
        setVersion((value) => value + 1);
        onMessage("AI 封面出好了，已放进封面图文件夹；请看一眼字有没有写错。");
      }
      watchingRef.current = job.status === "running";
      setAIJob(job);
      setNow(Date.now());
    } catch {
      // 查进度失败不打断发布操作，下一轮再查。
    }
  }, [api, runID, onMessage]);

  useEffect(() => { void loadAIJob(); }, [loadAIJob]);
  const aiRunning = aiJob?.status === "running";
  useEffect(() => {
    if (!aiRunning) return;
    const timer = window.setInterval(() => void loadAIJob(), AI_COVER_POLL_MS);
    return () => window.clearInterval(timer);
  }, [aiRunning, loadAIJob]);

  if (!publishing) return <p className="remix-lab-muted">这次运行没有发布包文案；正文在定稿节点查看。</p>;
  const shortTitles = publishing.short_titles ?? [];
  const descriptions = publishing.descriptions ?? [];

  const coverLines = (): [string, string] | null => {
    const first = cleanCoverLine(line1);
    const second = cleanCoverLine(line2);
    if (!first || !second) {
      onMessage("封面两行大字都要填，每行一到十二个字，不带标点。");
      return null;
    }
    return [first, second];
  };

  const generateAI = async () => {
    const lines = coverLines();
    if (!lines) return;
    try {
      const response = await api(`/api/remix-lab/runs/${runID}/ai-cover`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ line1: lines[0], line2: lines[1] }),
      });
      if (!response.ok) throw new Error(await readError(response, "AI 出图提交失败。"));
      setAIJob(await response.json());
      watchingRef.current = true;
      setNow(Date.now());
      onMessage("已交给 AI 出图。平台常要排队，可以先去做别的，出好会自动放进来。");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "AI 出图提交失败。");
    }
  };

  const generate = async () => {
    const lines = coverLines();
    if (!lines) return;
    const [first, second] = lines;
    setBusy(true);
    try {
      const response = await api(`/api/remix-lab/runs/${runID}/cover`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ line1: first, line2: second }),
      });
      if (!response.ok) throw new Error(await readError(response, "封面生成失败。"));
      setCover(await response.json());
      setVersion((value) => value + 1);
      onMessage("封面已生成，并保存到封面图文件夹。");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "封面生成失败。");
    } finally {
      setBusy(false);
    }
  };

  return <div className="publish-package">
    {shortTitles.length ? <section>
      <h4>短标题<small>视频号标题栏用，一句话、不带标点、十六个字以内，点一条复制</small></h4>
      <ul className="publish-package__titles">
        {shortTitles.map((title, index) => {
          const problem = shortTitleProblem(title);
          return <li key={index}>
            <button type="button" className="publish-package__copy" onClick={() => onCopy(title, "短标题")}>
              <span>{title}</span>
              <small data-warn={problem ? "true" : undefined}>{problem || `${[...title].length} 字`}</small>
              <Copy size={14} aria-hidden="true" />
            </button>
          </li>;
        })}
      </ul>
    </section> : null}

    {descriptions.length ? <section>
      <h4>简介<small>一个短词加几个话题，点一条复制</small></h4>
      <ul className="publish-package__titles">
        {descriptions.map((item, index) => <li key={index}>
          <button type="button" className="publish-package__copy" onClick={() => onCopy(item, "简介")}>
            <span>{item}</span>
            <Copy size={14} aria-hidden="true" />
          </button>
        </li>)}
      </ul>
    </section> : null}

    <section className="publish-package__cover">
      <h4>封面<small>两行大字，按这个账号的封面模板出图</small></h4>
      <div className="publish-package__lines">
        <label>第一行<input value={line1} maxLength={16} onChange={(event) => setLine1(event.target.value)} placeholder="例：钱的新去处" /></label>
        <label>第二行<input value={line2} maxLength={16} onChange={(event) => setLine2(event.target.value)} placeholder="例：看懂这一步不吃亏" /></label>
        <button type="button" className="remix-lab-start" disabled={busy} onClick={() => void generate()}>
          {cover ? <RefreshCw size={15} aria-hidden="true" /> : <ImagePlus size={15} aria-hidden="true" />}
          {busy ? "正在出图…" : cover ? "重新生成" : "生成封面"}
        </button>
      </div>
      <div className="publish-package__ai">
        <button type="button" className="header-button" disabled={aiRunning} onClick={() => void generateAI()}>
          <Sparkles size={14} aria-hidden="true" />
          {aiRunning ? "AI 出图中…" : "AI 出图"}
        </button>
        <small>
          {aiRunning
            ? `「${aiJob?.titles?.join(" / ") ?? ""}」排队或出图中，已等 ${waitedMinutes(aiJob?.started_at, now)} 分钟；可以先离开，出好自动放进来。`
            : aiJob?.status === "failed"
              ? <span data-warn="true">{aiJob.error || "AI 出图失败。"}</span>
              : "按账号参考封面整张重画，每张约 30 币；平台常排队十几分钟，出好后看一眼字。生成封面是本地出图，免费、立刻出。"}
        </small>
      </div>
      {cover ? <figure className="publish-package__preview">
        <img src={`${cover.url}?v=${version}`} alt={`封面：${cleanCoverLine(line1)} ${cleanCoverLine(line2)}`} />
        <figcaption>
          <span title={cover.path}>已保存：{cover.path}</span>
          <a className="header-button" href={`${cover.url}?v=${version}`} download><Download size={14} aria-hidden="true" />下载</a>
        </figcaption>
      </figure> : null}
    </section>
  </div>;
}
