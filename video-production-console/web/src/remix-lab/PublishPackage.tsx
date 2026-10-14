import { useState } from "react";
import { Copy, Download, ImagePlus, RefreshCw } from "lucide-react";
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

  if (!publishing) return <p className="remix-lab-muted">这次运行没有发布包文案；正文在定稿节点查看。</p>;
  const shortTitles = publishing.short_titles ?? [];
  const descriptions = publishing.descriptions ?? [];

  const generate = async () => {
    const first = cleanCoverLine(line1);
    const second = cleanCoverLine(line2);
    if (!first || !second) {
      onMessage("封面两行大字都要填，每行一到十二个字，不带标点。");
      return;
    }
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
