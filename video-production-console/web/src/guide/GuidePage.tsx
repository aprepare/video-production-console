import { useCallback, useEffect, useState } from "react";
import { ArrowRight, CheckCircle2, Circle, ClipboardList, Clapperboard, PenLine, Radar, RefreshCw, ShieldCheck, Store, Truck } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import "../shell/studio-page.css";
import "./guide.css";

export type GuideApi = (path: string, init?: RequestInit) => Promise<Response>;

/** What the console already has; each step's "done" is derived from real data, not a checkbox. */
export type GuideProgress = {
  setupReady: boolean | null;
  merchants: number;
  orders: number;
  references: number;
  transcribed: number;
  experiments: number;
  produced: number;
  delivered: number;
};

type Step = {
  id: string;
  icon: LucideIcon;
  title: string;
  why: string;
  how: string[];
  result: string;
  href: string;
  action: string;
  done: (progress: GuideProgress) => boolean;
  status: (progress: GuideProgress) => string;
};

const steps: Step[] = [
  {
    id: "setup",
    icon: ShieldCheck,
    title: "检查配置",
    why: "写稿、转写、配音和剪映都要先连上，有一项没好后面就会卡住。",
    how: [
      "打开「配置体检」，看「必需」里有没有红色或灰色的项。",
      "服务商给了配置码就粘贴进去，模型和密钥一次配好。",
      "点剪映旁边的「自动检测」，选检测到的那一个。",
    ],
    result: "页面顶部显示「全部就绪，可以开始做内容了」。",
    href: "/setup",
    action: "去配置体检",
    done: (p) => p.setupReady === true,
    status: (p) => p.setupReady === null ? "检测中…" : p.setupReady ? "必需项全部就绪" : "还有必需项没好",
  },
  {
    id: "merchant",
    icon: Store,
    title: "登记商户和订单",
    why: "知道给谁做、做多少条、收多少钱，后面的成本和交付才有地方记。",
    how: [
      "在「商户与订单」点「新增商户」，填店名、品类和「卖点（真实、可核验）」。",
      "进入商户详情，在「授权素材」点「上传授权素材」，传店里的实拍视频和图片，写明来源和授权范围。",
      "点「新建订单」：填套餐、条数、合同额和交付日，签了就把状态改成「已签约」。",
    ],
    result: "经营总览里能看到这家商户和订单。",
    href: "/merchants",
    action: "去商户与订单",
    done: (p) => p.merchants > 0 && p.orders > 0,
    status: (p) => `${p.merchants} 个商户 · ${p.orders} 个订单`,
  },
  {
    id: "references",
    icon: Radar,
    title: "找爆款当参考",
    why: "先看同行什么内容火、怎么拍怎么说的，再照着结构做，比凭空写省力。",
    how: [
      "在「对标采集」粘贴视频号或抖音的分享链接，或者按关键词搜。",
      "采集后原视频自动存到本机，口播自动转成文字（约半分钟到一分钟）。",
      "想知道它怎么拍的，点「拆解分析」，会给出拍摄清单和口播骨架。",
    ],
    result: "素材库里有一条标着「口播就绪」的内容。",
    href: "/references",
    action: "去对标采集",
    done: (p) => p.transcribed > 0,
    status: (p) => `${p.references} 条对标 · ${p.transcribed} 条口播就绪`,
  },
  {
    id: "remix",
    icon: PenLine,
    title: "二创写文案",
    why: "把爆款的结构和节奏换成这家店的内容，写出能直接念的新口播。",
    how: [
      "在对标详情点「送进二创」，口播会自动带到「文案与混剪」。",
      "上方「当前账号」选一个账号，点「风格与简报」：选写法（开店选「本地门店探店」），填卖点、给谁看、想让人做什么。",
      "点「开始二创」，等写手和审稿跑完；点「确认二创」看稿、改稿，满意就保存定稿。",
    ],
    result: "历史里出现一条「二创完成」的内容。",
    href: "/",
    action: "去文案与混剪",
    done: (p) => p.experiments > 0,
    status: (p) => `${p.experiments} 条二创`,
  },
  {
    id: "produce",
    icon: Clapperboard,
    title: "配音并生成剪映草稿",
    why: "定稿后系统自动切口播、配音、配字幕、挑镜头，直接在剪映里出草稿。",
    how: [
      "看完稿点「确认开始混剪」，系统依次做口播稿、配音、混剪。",
      "做完在项目页看分镜预览：每个镜头为什么选、有没有重复素材。",
      "打开剪映，在草稿列表里找到同名草稿，微调后导出。",
    ],
    result: "剪映草稿列表里出现这条视频的草稿。",
    href: "/",
    action: "去文案与混剪",
    done: (p) => p.produced > 0,
    status: (p) => `${p.produced} 条已进入混剪`,
  },
  {
    id: "deliver",
    icon: Truck,
    title: "交付并记账",
    why: "知道这一单花了多少时间和钱、赚了多少，才能定价和决定续不续。",
    how: [
      "打开订单，在「归单项目」里选做好的项目点「归单」，交给商户后点它旁边的「标记已交付」。",
      "在「成本流水」点「记一笔」，记人工分钟、接口费用和返工。",
      "收到钱就在订单「编辑」里填「已收」，系统自动算出这一单的贡献（已收减已记成本）。",
    ],
    result: "订单的「交付进度」走满，把状态改成「已交付」，能看到这一单的贡献。",
    href: "/merchants",
    action: "去商户与订单",
    done: (p) => p.delivered > 0,
    status: (p) => `${p.delivered} 个订单已交付`,
  },
];

async function readJSON<T>(api: GuideApi, path: string): Promise<T | null> {
  try {
    const response = await api(path);
    return response.ok ? (await response.json()) as T : null;
  } catch {
    return null;
  }
}

/** Reads the counts each step needs; a failed call just leaves that step at zero. */
export async function loadGuideProgress(api: GuideApi): Promise<GuideProgress> {
  const [setup, overview, references, experiments, projects] = await Promise.all([
    readJSON<{ ready: boolean }>(api, "/api/setup/status"),
    readJSON<{ merchants_by_status?: Record<string, number>; orders_by_status?: Record<string, number> }>(api, "/api/business/overview"),
    readJSON<Array<{ transcript_status?: string }>>(api, "/api/references?limit=300"),
    readJSON<Array<{ production_status?: string; project_id?: string }>>(api, "/api/remix-lab/experiments"),
    readJSON<Array<{ stage?: string }>>(api, "/api/projects"),
  ]);
  const sum = (counts?: Record<string, number>) => Object.values(counts ?? {}).reduce((total, value) => total + value, 0);
  const orders = overview?.orders_by_status ?? {};
  const producedFromRemix = (experiments ?? []).filter((item) => item.project_id || item.production_status).length;
  return {
    setupReady: setup ? Boolean(setup.ready) : false,
    merchants: sum(overview?.merchants_by_status),
    orders: sum(orders),
    references: references?.length ?? 0,
    transcribed: (references ?? []).filter((item) => item.transcript_status === "completed").length,
    experiments: experiments?.length ?? 0,
    produced: Math.max(producedFromRemix, (projects ?? []).length),
    delivered: (orders.delivered ?? 0) + (orders.closed ?? 0),
  };
}

type Props = {
  api: GuideApi;
  onNavigate: (href: string) => void;
};

/** 新手引导：按真实工作流排六步，每步写清为什么、怎么点、做完是什么样，并按已有数据打勾。 */
export function GuidePage({ api, onNavigate }: Props) {
  const [progress, setProgress] = useState<GuideProgress | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    setProgress(await loadGuideProgress(api));
    setLoading(false);
  }, [api]);
  useEffect(() => { void load(); }, [load]);

  const current = progress ? steps.findIndex((step) => !step.done(progress)) : -1;
  const doneCount = progress ? steps.filter((step) => step.done(progress)).length : 0;

  return <main className="studio-page guide-page" id="studio-content" tabIndex={-1}>
    <header className="studio-page__head">
      <div>
        <p className="studio-kicker">开始使用 / 新手引导</p>
        <h1>一条视频是怎么做出来的</h1>
        <p>整个系统就是一条线：<strong>找爆款 → 写文案 → 配音出草稿 → 交付记账</strong>。按下面六步走一遍，每一步做完会自动打勾。</p>
      </div>
      <button type="button" className="studio-button" disabled={loading} onClick={() => void load()}><RefreshCw size={15} aria-hidden="true" />{loading ? "检查中…" : "刷新进度"}</button>
    </header>

    <div className="guide-map" aria-label="工作区分工">
      <span><ShieldCheck size={15} aria-hidden="true" />配置体检<small>连上服务</small></span>
      <ArrowRight size={14} aria-hidden="true" />
      <span><Store size={15} aria-hidden="true" />商户与订单<small>给谁做</small></span>
      <ArrowRight size={14} aria-hidden="true" />
      <span><Radar size={15} aria-hidden="true" />对标采集<small>参考什么</small></span>
      <ArrowRight size={14} aria-hidden="true" />
      <span><PenLine size={15} aria-hidden="true" />文案与混剪<small>写稿、配音、出草稿</small></span>
      <ArrowRight size={14} aria-hidden="true" />
      <span><ClipboardList size={15} aria-hidden="true" />商户与订单<small>交付记账</small></span>
    </div>

    <p className="guide-count" aria-live="polite">{progress ? doneCount === steps.length ? "六步都走通了，之后每接一单就按这个顺序来。" : `已完成 ${doneCount}/${steps.length} 步` : "正在读取进度…"}</p>

    <ol className="guide-steps">
      {steps.map((step, index) => {
        const done = progress ? step.done(progress) : false;
        const isCurrent = index === current;
        const Icon = step.icon;
        return <li key={step.id} className="guide-step" data-state={done ? "done" : isCurrent ? "current" : "todo"} aria-current={isCurrent ? "step" : undefined}>
          <div className="guide-step__marker" aria-hidden="true">{done ? <CheckCircle2 size={22} /> : <Circle size={22} />}<span>{index + 1}</span></div>
          <div className="guide-step__body">
            <h2><Icon size={17} aria-hidden="true" />{step.title}{done ? <span className="studio-status" data-status="ready">已完成</span> : isCurrent ? <span className="studio-status" data-status="working">下一步</span> : null}</h2>
            <p className="guide-step__why">{step.why}</p>
            <ol className="guide-step__how">{step.how.map((line) => <li key={line}>{line}</li>)}</ol>
            <p className="guide-step__result"><strong>做完的样子</strong>{step.result}</p>
            <div className="studio-row">
              {progress ? <span className="studio-metrics">{step.status(progress)}</span> : null}
              <span className="grow" />
              <button type="button" className={isCurrent ? "studio-button studio-button--primary" : "studio-button"} onClick={() => onNavigate(step.href)}>{step.action}<ArrowRight size={15} aria-hidden="true" /></button>
            </div>
          </div>
        </li>;
      })}
    </ol>

    <section className="studio-panel guide-faq" aria-labelledby="guide-faq-title">
      <header><div><h2 id="guide-faq-title">常见疑问</h2></div></header>
      <dl>
        <dt>自己用的和发给商户的是一个软件吗？</dt>
        <dd>是同一个。区别只在数据和配置：商户那边是空的，粘贴配置码就能用你的服务，剪映自动检测。</dd>
        <dt>财经账号的写法会被改掉吗？</dt>
        <dd>不会。老账号继续用原来的提示词；只有在「风格与简报」里换了写法的账号才会变。</dd>
        <dt>为什么有的对标没有视频？</dt>
        <dd>小红书接口只给图文，没有视频地址；抖音解析失败一般是服务器上的登录信息过期，联系服务商刷新。</dd>
        <dt>哪些操作会花钱？</dt>
        <dd>写稿、转写、拆解、配音按用量计费；AIGC 重塑的「图生视频」最贵，提交前会再弹窗确认。</dd>
      </dl>
    </section>
  </main>;
}
