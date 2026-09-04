import { useEffect, useMemo, useRef, useState } from "react";
import {
  Background,
  Controls,
  Handle,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { BookOpenText, Clapperboard, FileText, Gauge, PenLine, ScanSearch, Stamp, X } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import {
  fetchRemixLabRunStages,
  redoRemixLabProduceStep,
  saveRemixLabRunPackage,
  type RemixLabApi,
  type RemixLabPackageInput,
  type RemixLabProduction,
  type RemixLabReviewRecord,
  type RemixLabRunStage,
  type RemixLabRunStagesView,
} from "./api";
import { ReviewCompare, ReviewIssueList, resolveReviewVersions, reviewVerdictLabel, reviewVerdictTone } from "./ReviewCompare";

// n8n 式运行工作流：节点=管线阶段，点节点在右侧看该步实际输入输出。
// RunFlowPanel 是可复用面板：live=true 时每 2.5 秒轮询刷新（详情页内嵌，
// 跑的过程中节点逐个亮起）；RunFlow 是完成后的全量检视弹窗。

type RunFlowPanelProps = {
  api: RemixLabApi;
  runID: string;
  /** true=运行中实时刷新；missing 状态显示为「等待中」。 */
  live?: boolean;
  onEditAgentPrompts: () => void;
  onMessage: (text: string) => void;
  /** 提供后，失败节点的检视器出现「重试并续跑」按钮（agent节点传ID，其余整体重试）。
   *  model 非空时先换模型再重试（agent 节点改快照，其余改槽位主模型）。 */
  onRetryNode?: (nodeID: string, model?: string) => void;
  /** run 状态变化回调（live 轮询时告诉宿主跑完了没）。 */
  onStatusChange?: (status: string) => void;
  /** 生产段状态回调（宿主渲染确认闸门按钮/进度）。 */
  onProductionChange?: (production: RemixLabProduction | null) => void;
  /** 生产节点失败时的续跑回调。 */
  onRetryProduce?: () => void;
  /** 确认二创后开始混剪。 */
  onConfirmProduce?: () => void;
};

type RunFlowProps = {
  api: RemixLabApi;
  runID: string;
  runLabel: string;
  onClose: () => void;
  onEditAgentPrompts: () => void;
  onMessage: (text: string) => void;
  onRetryNode?: (nodeID: string, model?: string) => void;
  onRetryProduce?: () => void;
  onConfirmProduce?: () => void;
};

const STAGE_ICONS: Record<string, LucideIcon> = {
  source: FileText,
  hook: ScanSearch,
  facts: BookOpenText,
  ammo: PenLine,
  writer: PenLine,
  selfcheck: Gauge,
  review: Stamp,
  final: FileText,
  "produce-gate": Gauge,
  "produce-project": BookOpenText,
  "produce-spoken": FileText,
  "produce-narration": FileText,
  "produce-montage": Clapperboard,
  "produce-publish": Stamp,
};

const KIND_ICONS: Record<string, LucideIcon> = {
  input: FileText,
  agent: ScanSearch,
  gate: Gauge,
  output: BookOpenText,
  produce: Clapperboard,
};

const KIND_LABEL: Record<string, string> = {
  input: "输入",
  agent: "AGENT",
  gate: "闸门",
  output: "产出",
  produce: "混剪",
};

function statusLabel(status: string, live: boolean): string {
  switch (status) {
    case "ok":
      return "成功";
    case "failed":
      return "失败";
    case "skipped":
      return "跳过";
    case "running":
      return "进行中";
    case "waiting":
      return "等待确认";
    case "missing":
      return live ? "等待中" : "未开始";
    default:
      return status;
  }
}

// 固定管线（旧运行没有坐标）的内置布局。
function stagePosition(id: string, multiAgent: boolean): { x: number; y: number } {
  if (multiAgent) {
    const grid: Record<string, [number, number]> = {
      source: [0, 190],
      hook: [300, 10],
      facts: [300, 190],
      ammo: [300, 370],
      writer: [600, 190],
      selfcheck: [880, 190],
      review: [1160, 190],
      final: [1440, 190],
    };
    const [x, y] = grid[id] ?? [0, 0];
    return { x, y };
  }
  const line: Record<string, number> = { source: 0, writer: 300, selfcheck: 600, review: 880, final: 1160 };
  return { x: line[id] ?? 0, y: 140 };
}

type StageNodeData = { stage: RemixLabRunStage; live: boolean };

function StageNode({ data, selected }: NodeProps & { data: StageNodeData }) {
  const stage = data.stage;
  const Icon = STAGE_ICONS[stage.id] ?? KIND_ICONS[stage.kind] ?? FileText;
  const pendingLive = data.live && stage.status === "missing";
  const pulsing = pendingLive || stage.status === "running" || stage.status === "waiting";
  return (
    <div
      className={[
        "run-flow-node",
        `run-flow-node--${stage.status}`,
        pendingLive ? "run-flow-node--pending" : "",
        selected ? "run-flow-node--selected" : "",
      ].join(" ")}
    >
      <Handle type="target" position={Position.Left} className="run-flow-handle" />
      <div className="run-flow-node__head">
        <span className="run-flow-node__icon" aria-hidden="true">
          <Icon size={15} strokeWidth={2} />
        </span>
        <div className="run-flow-node__titles">
          <small>{KIND_LABEL[stage.kind] ?? stage.kind}</small>
          <strong>{stage.title}</strong>
        </div>
        <span
          className={`run-flow-dot run-flow-dot--${stage.status}${pulsing ? " run-flow-dot--pulse" : ""}`}
          title={statusLabel(stage.status, data.live)}
        />
      </div>
      <div className="run-flow-node__meta">
        {stage.model ? <span>{shortModel(stage.model)}</span> : null}
        {stage.ms ? <span>{(stage.ms / 1000).toFixed(1)}s</span> : null}
        <span>{statusLabel(stage.status, data.live)}</span>
      </div>
      <Handle type="source" position={Position.Right} className="run-flow-handle" />
    </div>
  );
}

function shortModel(model: string): string {
  const base = model.trim().split("/").pop() || model;
  return base.length > 20 ? base.slice(0, 18) + "…" : base;
}

function prettyOutput(raw: string | undefined): string {
  const text = (raw ?? "").trim();
  if (!text) return "";
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

const nodeTypes = { stage: StageNode };

/** 审稿节点抽屉：结论 + 逐条 issue + 审稿前/审稿后全字段对照（只读，采用去改稿工作台）。 */
function ReviewStageSection({ output, draftV1 }: { output: string; draftV1: string }) {
  const review = useMemo(() => {
    try {
      const parsed = JSON.parse(output) as RemixLabReviewRecord;
      return parsed && typeof parsed === "object" && parsed.verdict ? parsed : null;
    } catch {
      return null;
    }
  }, [output]);
  const versions = useMemo(() => resolveReviewVersions(review, draftV1), [review, draftV1]);
  if (!review) {
    return (
      <section>
        <h4>输出</h4>
        <pre className="run-flow-inspector__code">{prettyOutput(output)}</pre>
      </section>
    );
  }
  return (
    <>
      <section className="run-flow-review">
        <h4>审稿结论</h4>
        <p className="run-flow-review__verdict">
          <span className={`remix-lab-chip remix-lab-chip--review-${reviewVerdictTone(review.verdict)}`}>
            {reviewVerdictLabel(review.verdict)}
            {review.round > 1 ? ` · 第${review.round}轮` : ""}
          </span>
          {review.summary ? <span>{review.summary}</span> : null}
        </p>
        {review.error ? <p className="remix-lab-run__error">{review.error}</p> : null}
        {review.annotations ? (
          <p className="run-flow-inspector__meta">操作员批注：{review.annotations}</p>
        ) : null}
        <ReviewIssueList review={review} />
      </section>
      {versions.before ? (
        <section>
          <h4>审稿前 / 审稿后（正文 · 短标题 · 视频描述 · 话题）</h4>
          <ReviewCompare before={versions.before} after={versions.after} stacked />
          <p className="remix-lab-muted run-flow-review__hint">要采用哪一版，去「改稿」工作台在对应字段点「用这版」。</p>
        </section>
      ) : null}
      <details className="run-flow-publish__raw">
        <summary>原始 review.json</summary>
        <pre className="run-flow-inspector__code">{prettyOutput(output)}</pre>
      </details>
    </>
  );
}

/** 发布节点抽屉：复制发布文案（描述/短标题/话题），确认已发布。 */
function ProducePublishSection({
  stage,
  projectID,
  projectStage,
  busy,
  onCopy,
  onPublish,
}: {
  stage: RemixLabRunStage;
  projectID: string;
  projectStage: string;
  busy: boolean;
  onCopy: (text: string, label: string) => void;
  onPublish: (projectID: string) => void;
}) {
  const publishing = (stage.extra?.publishing ?? null) as {
    titles?: string[];
    short_titles?: string[];
    descriptions?: string[];
    topics?: string[];
  } | null;
  const published = projectStage === "published" || stage.extra?.published === true;
  const descriptions = publishing?.descriptions ?? [];
  return (
    <section className="run-flow-publish">
      {publishing?.short_titles?.length ? (
        <>
          <h4>短标题（点击复制，第1条板面主标题、第2条副标题）</h4>
          <div className="run-flow-publish__chips">
            {publishing.short_titles.map((title, index) => (
              <button key={index} type="button" className="header-button" onClick={() => onCopy(title, "短标题")}>
                {title}
              </button>
            ))}
          </div>
        </>
      ) : null}
      {descriptions.length ? (
        <>
          <h4>视频描述（已带话题，选一条复制）</h4>
          {descriptions.map((item, index) => (
            <div key={index} className="run-flow-publish__desc">
              <pre className="run-flow-inspector__code run-flow-inspector__code--prose">{item}</pre>
              <button type="button" className="header-button" onClick={() => onCopy(item, "视频描述")}>
                复制描述 {index + 1}
              </button>
            </div>
          ))}
        </>
      ) : null}
      {!publishing ? <p className="remix-lab-muted">这次运行没有发布包文案；正文在定稿节点查看。</p> : null}
      <div className="run-flow-inspector__actions">
        {published ? (
          <p className="remix-lab-muted">项目已标记为已发布。</p>
        ) : projectID && stage.status === "waiting" ? (
          <button type="button" className="remix-lab-start" disabled={busy} onClick={() => onPublish(projectID)}>
            {busy ? "提交中…" : "确认已发布"}
          </button>
        ) : (
          <p className="remix-lab-muted">剪映草稿出来后，在这里复制发布文案并确认发布。</p>
        )}
      </div>
    </section>
  );
}

/** 定稿编辑草稿：列表字段按「每行一条」编辑。 */
type FinalDraftForm = {
  script: string;
  titles: string;
  shortTitles: string;
  descriptions: string;
  topics: string;
  cta: string;
};

function splitLines(value: string): string[] {
  return value
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
}

/** 定稿节点抽屉：把发布包 JSON 摊开成能直接读、直接复制的排版；可就地编辑
 * 后保存（混剪用改过的版本）；解析不了再回落原始 JSON。 */
function FinalDraftSection({
  output,
  onCopy,
  onSave,
  saving,
}: {
  output: string;
  onCopy: (text: string, label: string) => void;
  onSave?: (pkg: RemixLabPackageInput) => Promise<boolean>;
  saving?: boolean;
}) {
  const [form, setForm] = useState<FinalDraftForm | null>(null);
  const pkg = useMemo(() => {
    try {
      const parsed = JSON.parse(output) as {
        continuous_script?: string;
        titles?: string[];
        short_titles?: string[];
        descriptions?: string[];
        topics?: string[];
        cta?: string;
      };
      if (parsed && typeof parsed === "object" && typeof parsed.continuous_script === "string" && parsed.continuous_script.trim()) {
        return parsed;
      }
    } catch {
      // 不是发布包结构：按原始输出展示。
    }
    return null;
  }, [output]);
  if (!pkg) {
    return (
      <section>
        <h4>输出</h4>
        <pre className="run-flow-inspector__code">{prettyOutput(output)}</pre>
      </section>
    );
  }
  const script = (pkg.continuous_script ?? "").trim();
  if (form) {
    const patch = (key: keyof FinalDraftForm) => (event: { target: { value: string } }) =>
      setForm((current) => (current ? { ...current, [key]: event.target.value } : current));
    return (
      <section className="run-flow-publish">
        <h4>编辑定稿正文</h4>
        <textarea
          className="run-flow-inspector__editor"
          aria-label="编辑定稿正文"
          rows={18}
          value={form.script}
          onChange={patch("script")}
        />
        <h4>短标题（每行一条，第1条板面主标题、第2条副标题）</h4>
        <textarea className="run-flow-inspector__editor" aria-label="编辑短标题" rows={3} value={form.shortTitles} onChange={patch("shortTitles")} />
        <h4>候选标题（每行一条）</h4>
        <textarea className="run-flow-inspector__editor" aria-label="编辑候选标题" rows={5} value={form.titles} onChange={patch("titles")} />
        <h4>视频描述（每行一条）</h4>
        <textarea className="run-flow-inspector__editor" aria-label="编辑视频描述" rows={4} value={form.descriptions} onChange={patch("descriptions")} />
        <h4>话题（每行一个，带#）</h4>
        <textarea className="run-flow-inspector__editor" aria-label="编辑话题" rows={3} value={form.topics} onChange={patch("topics")} />
        <h4>转化段（CTA）</h4>
        <textarea className="run-flow-inspector__editor" aria-label="编辑转化段" rows={3} value={form.cta} onChange={patch("cta")} />
        <p className="remix-lab-muted">保存后还没开始的混剪直接用新稿；已经出过口播/草稿的，需在对应生产节点重做才会生效。</p>
        <div className="run-flow-inspector__actions">
          <button
            type="button"
            className="remix-lab-start"
            disabled={Boolean(saving)}
            onClick={() => {
              void (async () => {
                const ok = await onSave?.({
                  continuous_script: form.script.trim(),
                  titles: splitLines(form.titles),
                  short_titles: splitLines(form.shortTitles),
                  descriptions: splitLines(form.descriptions),
                  topics: splitLines(form.topics),
                  cta: form.cta.trim(),
                });
                if (ok) setForm(null);
              })();
            }}
          >
            {saving ? "保存中…" : "保存定稿"}
          </button>
          <button type="button" className="header-button" onClick={() => setForm(null)}>
            取消
          </button>
        </div>
      </section>
    );
  }
  return (
    <section className="run-flow-publish">
      <h4>口播正文（{script.length} 字）</h4>
      <pre className="run-flow-inspector__code run-flow-inspector__code--prose">{script}</pre>
      <div className="run-flow-inspector__actions">
        <button type="button" className="header-button" onClick={() => onCopy(script, "口播正文")}>
          复制正文
        </button>
        {onSave ? (
          <button
            type="button"
            className="header-button"
            onClick={() =>
              setForm({
                script,
                titles: (pkg.titles ?? []).join("\n"),
                shortTitles: (pkg.short_titles ?? []).join("\n"),
                descriptions: (pkg.descriptions ?? []).join("\n"),
                topics: (pkg.topics ?? []).join("\n"),
                cta: (pkg.cta ?? "").trim(),
              })
            }
          >
            编辑定稿
          </button>
        ) : null}
      </div>
      {pkg.short_titles?.length ? (
        <>
          <h4>短标题（点击复制，第1条板面主标题、第2条副标题）</h4>
          <div className="run-flow-publish__chips">
            {pkg.short_titles.map((title, index) => (
              <button key={index} type="button" className="header-button" onClick={() => onCopy(title, "短标题")}>
                {title}
              </button>
            ))}
          </div>
        </>
      ) : null}
      {pkg.descriptions?.length ? (
        <>
          <h4>视频描述（已带话题，选一条复制）</h4>
          {pkg.descriptions.map((description, index) => (
            <div key={index} className="run-flow-publish__desc">
              <pre className="run-flow-inspector__code run-flow-inspector__code--prose">{description}</pre>
              <button type="button" className="header-button" onClick={() => onCopy(description, "视频描述")}>
                复制描述 {index + 1}
              </button>
            </div>
          ))}
        </>
      ) : null}
      {pkg.cta ? (
        <>
          <h4>转化段（CTA）</h4>
          <pre className="run-flow-inspector__code run-flow-inspector__code--prose">{pkg.cta}</pre>
        </>
      ) : null}
      <details className="run-flow-publish__raw">
        <summary>原始 JSON</summary>
        <pre className="run-flow-inspector__code">{prettyOutput(output)}</pre>
      </details>
    </section>
  );
}

/** 生产节点的实际产物（任务状态+资产内容），选中时按需拉取。 */
type ProduceDetail = {
  key: string;
  taskStatus?: string;
  taskError?: string;
  assetText?: string;
  note?: string;
  /** 配音音频资产（试听/下载）。 */
  narrationAssetID?: string;
  /** 已登记的剪映草稿资产（导出视频/打开目录）。 */
  draftAssetID?: string;
  draftReady?: boolean;
  /** 项目当前阶段（发布节点判断是否已发布）。 */
  projectStage?: string;
};

export function RunFlowPanel({ api, runID, live = false, onEditAgentPrompts, onMessage, onRetryNode, onStatusChange, onProductionChange, onRetryProduce, onConfirmProduce }: RunFlowPanelProps) {
  const [view, setView] = useState<RemixLabRunStagesView | null>(null);
  const [selectedID, setSelectedID] = useState<string>("");
  // 换模型重试：失败节点检视器里的可选输入，切换选中节点时清空。
  const [retryModel, setRetryModel] = useState("");
  const [produceDetail, setProduceDetail] = useState<ProduceDetail | null>(null);
  // 节点抽屉里的操作状态：动作互斥、动作后强制刷新、生产运行时持续轮询。
  const [busyAction, setBusyAction] = useState("");
  const [refreshTick, setRefreshTick] = useState(0);
  const [productionActive, setProductionActive] = useState(false);
  const [keywordsDraft, setKeywordsDraft] = useState<string | null>(null);
  const [spokenDraft, setSpokenDraft] = useState<string | null>(null);
  const [exportingDraft, setExportingDraft] = useState("");
  const autoSelected = useRef(false);
  const lastStatus = useRef("");

  useEffect(() => {
    setRetryModel("");
  }, [selectedID]);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const next = await fetchRemixLabRunStages(api, runID);
        if (cancelled) return;
        setView(next);
        setProductionActive((next.production?.status ?? "") === "running");
        if (next.status !== lastStatus.current) {
          lastStatus.current = next.status;
          onStatusChange?.(next.status);
        }
        onProductionChange?.(next.production ?? null);
        // 非实时模式（完成后检视）默认选中第一个出问题的节点，没有就选定稿。
        if (!live && !autoSelected.current) {
          autoSelected.current = true;
          const firstBad = next.stages.find((stage) => stage.status === "failed");
          setSelectedID(firstBad?.id ?? "final");
        }
        // 实时模式跑失败时自动选中断点，方便直接点重试。
        if (live && next.status === "failed" && !autoSelected.current) {
          autoSelected.current = true;
          const firstBad = next.stages.find((stage) => stage.status === "failed");
          if (firstBad) setSelectedID(firstBad.id);
        }
      } catch (error) {
        if (!cancelled) onMessage(error instanceof Error ? error.message : "工作流分解读取失败。");
      }
    };
    void load();
    // 历史视图里点了重做/续跑后生产会重新 running：也要跟着轮询到跑完。
    if (!live && !productionActive) {
      return () => {
        cancelled = true;
      };
    }
    const timer = window.setInterval(() => void load(), 2500);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [api, runID, live, productionActive, refreshTick, onMessage, onStatusChange, onProductionChange]);

  const multiAgent = view?.pipeline === "multi_agent";
  const hasPositions = (view?.stages ?? []).some((stage) => (stage.x ?? 0) !== 0 || (stage.y ?? 0) !== 0);
  const nodes: Node[] = useMemo(
    () =>
      (view?.stages ?? []).map((stage) => ({
        id: stage.id,
        type: "stage",
        position: hasPositions
          ? { x: stage.x ?? 0, y: stage.y ?? 0 }
          : stagePosition(stage.id, Boolean(multiAgent)),
        data: { stage, live },
        selected: stage.id === selectedID,
      })),
    [view, multiAgent, selectedID, hasPositions, live],
  );
  const edges: Edge[] = useMemo(
    () =>
      (view?.edges ?? []).map(([from, to]) => ({
        id: `${from}-${to}`,
        source: from,
        target: to,
        animated: live,
        style: { strokeWidth: 1.6 },
      })),
    [view, live],
  );

  const selected = view?.stages.find((stage) => stage.id === selectedID) ?? null;
  const checkEvents = (selected?.extra?.events as Array<Record<string, unknown>> | undefined) ?? [];

  // 生产节点选中时按需拉实况：任务状态 + 该步实际产物（口播稿/字幕关键词/SRT），
  // 以及项目阶段、配音音频与剪映草稿资产（试听/导出/发布按钮据此渲染）。
  const produceTaskID = selected?.kind === "produce" ? String(selected.extra?.task_id ?? "") : "";
  const produceProjectID = selected?.kind === "produce" ? String(selected.extra?.project_id ?? "") : "";
  const produceAssetType = selected?.kind === "produce" ? String(selected.extra?.asset_type ?? "") : "";
  const produceStatus = selected?.kind === "produce" ? selected.status : "";
  const produceStep = selected?.kind === "produce" ? String(selected.extra?.production_step ?? "") : "";
  useEffect(() => {
    setKeywordsDraft(null);
    setSpokenDraft(null);
  }, [selectedID]);
  useEffect(() => {
    if (!selectedID || (!produceTaskID && !produceProjectID)) {
      setProduceDetail(null);
      return;
    }
    const key = [selectedID, produceTaskID, produceProjectID, produceAssetType, produceStatus, refreshTick].join("|");
    let cancelled = false;
    void (async () => {
      const next: ProduceDetail = { key };
      try {
        if (produceTaskID) {
          const response = await api(`/api/tasks/${produceTaskID}`);
          if (response.ok) {
            const task = (await response.json()) as { status?: string; error_message?: string };
            next.taskStatus = task.status ?? "";
            next.taskError = task.error_message ?? "";
          }
        }
        if (produceProjectID) {
          const response = await api(`/api/projects/${produceProjectID}`);
          if (response.ok) {
            const detail = (await response.json()) as {
              project?: { stage?: string };
              assets?: Record<string, { id?: string; state?: string }>;
            };
            next.projectStage = detail.project?.stage ?? "";
            const narration = detail.assets?.narration;
            if (narration?.id && narration.state === "ready") next.narrationAssetID = narration.id;
            const draft = detail.assets?.mix_draft;
            if (draft?.id) {
              next.draftAssetID = draft.id;
              next.draftReady = draft.state === "ready";
            }
            if (produceAssetType) {
              const asset = detail.assets?.[produceAssetType];
              if (asset?.id) {
                const content = await api(`/api/assets/${asset.id}/content`);
                if (content.ok) {
                  const text = await content.text();
                  next.assetText = text.length > 20000 ? `${text.slice(0, 20000)}\n…（内容过长已截断）` : text;
                }
              } else {
                next.note = "该步的产物还没生成。";
              }
            }
          }
        }
      } catch {
        next.note = "实况详情拉取失败。";
      }
      if (!cancelled) setProduceDetail(next);
    })();
    return () => {
      cancelled = true;
    };
  }, [api, selectedID, produceTaskID, produceProjectID, produceAssetType, produceStatus, refreshTick]);

  // 剪映导出：启动后每 3 秒查一次结果。
  useEffect(() => {
    if (!exportingDraft) return;
    const timer = window.setInterval(() => {
      void (async () => {
        try {
          const response = await api(`/api/assets/${exportingDraft}/export-video`);
          if (!response.ok) return;
          const status = (await response.json()) as { status?: string; output_path?: string; message?: string };
          if (status.status === "done") {
            setExportingDraft("");
            onMessage(`视频已导出：${status.output_path || "完成"}`);
          } else if (status.status === "failed") {
            setExportingDraft("");
            onMessage(`视频导出失败：${status.message || "未知原因"}`);
          }
        } catch {
          // 瞬时失败继续轮询
        }
      })();
    }, 3000);
    return () => window.clearInterval(timer);
  }, [api, exportingDraft, onMessage]);

  const production = view?.production ?? null;
  const productionSettled = production?.status === "completed" || production?.status === "failed";
  const bumpRefresh = () => setRefreshTick((tick) => tick + 1);

  const copyText = async (text: string, label: string) => {
    try {
      await navigator.clipboard.writeText(text);
      onMessage(`${label}已复制。`);
    } catch {
      onMessage("复制失败，请手动选中文本复制。");
    }
  };

  // 保存操作员手改的定稿：还没确认混剪时，后面整条生产链直接用新稿。
  const savePackage = async (pkg: RemixLabPackageInput): Promise<boolean> => {
    if (busyAction) return false;
    setBusyAction("package");
    try {
      await saveRemixLabRunPackage(api, runID, pkg);
      onMessage("定稿已保存，混剪将使用你改过的版本。");
      setRefreshTick((tick) => tick + 1);
      return true;
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "定稿保存失败。");
      return false;
    } finally {
      setBusyAction("");
    }
  };

  const redoStep = async (step: string, label: string) => {
    if (busyAction) return;
    setBusyAction(`redo-${step}`);
    try {
      await redoRemixLabProduceStep(api, runID, step);
      onMessage(`已重跑「${label}」，进度看生产节点。`);
      bumpRefresh();
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "重做这一步失败。");
    } finally {
      setBusyAction("");
    }
  };

  const cancelProduceTask = async (taskID: string) => {
    if (busyAction) return;
    setBusyAction("cancel");
    try {
      const response = await api(`/api/tasks/${taskID}/cancel`, { method: "POST" });
      onMessage(response.ok ? "已请求停止任务；停下后这一步会标失败，可重试续跑。" : "停止任务失败。");
      bumpRefresh();
    } catch {
      onMessage("停止任务失败。");
    } finally {
      setBusyAction("");
    }
  };

  const saveSpoken = async (projectID: string, content: string) => {
    if (!content.trim()) {
      onMessage("口播稿不能是空的。");
      return;
    }
    setBusyAction("spoken");
    try {
      const body = new FormData();
      body.set("file", new File([content], "spoken_script.txt", { type: "text/plain;charset=utf-8" }));
      const response = await api(`/api/projects/${projectID}/assets/spoken_script`, { method: "POST", body });
      if (!response.ok) throw new Error("口播稿保存失败。");
      onMessage("口播稿已保存；已有配音和草稿不会自动更新，需要时重做配音或重出草稿。");
      setSpokenDraft(null);
      bumpRefresh();
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "口播稿保存失败。");
    } finally {
      setBusyAction("");
    }
  };

  const saveKeywords = async (projectID: string, content: string) => {
    try {
      JSON.parse(content);
    } catch {
      onMessage("关键词必须是合法 JSON，先检查格式再保存。");
      return;
    }
    setBusyAction("keywords");
    try {
      const body = new FormData();
      body.set("file", new File([content], "caption_keywords.json", { type: "application/json" }));
      const response = await api(`/api/projects/${projectID}/assets/caption_keywords`, { method: "POST", body });
      if (!response.ok) throw new Error("关键词保存失败。");
      onMessage("关键词已保存；已有草稿不会自动更新，需要时重出草稿。");
      setKeywordsDraft(null);
      bumpRefresh();
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "关键词保存失败。");
    } finally {
      setBusyAction("");
    }
  };

  const startExport = async (assetID: string) => {
    if (exportingDraft) return;
    const response = await api(`/api/assets/${assetID}/export-video`, { method: "POST" });
    if (!response.ok) {
      onMessage(
        response.status === 403
          ? "只能从控制台所在电脑导出视频。"
          : response.status === 409
            ? "已有导出任务在进行中，请等它结束。"
            : "导出启动失败，请确认剪映已打开并停在首页。",
      );
      return;
    }
    onMessage("已开始控制剪映导出视频，期间请不要操作鼠标键盘。");
    setExportingDraft(assetID);
  };

  const openDraftDirectory = async (assetID: string) => {
    setBusyAction("open-dir");
    try {
      const response = await api(`/api/assets/${assetID}/open-directory`, { method: "POST" });
      onMessage(
        response.ok
          ? "已在电脑上打开剪映草稿目录。"
          : response.status === 403
            ? "只能从控制台所在电脑打开目录。"
            : "未能打开剪映目录，请确认目录仍然存在。",
      );
    } catch {
      onMessage("打开剪映目录失败，请稍后重试。");
    } finally {
      setBusyAction("");
    }
  };

  const publishProject = async (projectID: string) => {
    setBusyAction("publish");
    try {
      const response = await api(`/api/projects/${projectID}/publish`, { method: "POST" });
      if (!response.ok) throw new Error("发布状态更新失败，请稍后重试。");
      onMessage("已标记为已发布。");
      bumpRefresh();
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "发布状态更新失败，请稍后重试。");
    } finally {
      setBusyAction("");
    }
  };

  return (
    <div className="run-flow-body">
      <div className="run-flow-canvas">
        {view ? (
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            onNodeClick={(_, node) => setSelectedID(node.id)}
            fitView
            fitViewOptions={{ padding: 0.18 }}
            minZoom={0.3}
            maxZoom={1.6}
            nodesConnectable={false}
            deleteKeyCode={null}
          >
            <Background gap={22} size={1.4} />
            <Controls showInteractive={false} />
          </ReactFlow>
        ) : (
          <p className="remix-lab-muted run-flow-loading">正在读取工作流分解…</p>
        )}
      </div>
      {selected ? (
        <aside
          className={
            selected.prompt_key === "reviewer_system" && selected.output
              ? "run-flow-inspector run-flow-inspector--wide"
              : "run-flow-inspector"
          }
          aria-label={`${selected.title}详情`}
        >
          <header>
            <h3>{selected.title}</h3>
            <span className={`remix-lab-seal remix-lab-seal--${selected.status === "ok" ? "ok" : selected.status === "failed" ? "danger" : "idle"}`}>
              {statusLabel(selected.status, live)}
            </span>
          </header>
          {selected.model ? (
            <p className="run-flow-inspector__meta">
              模型 {selected.model}
              {selected.ms ? ` · 耗时 ${(selected.ms / 1000).toFixed(1)}s` : ""}
            </p>
          ) : null}
          {selected.error ? <p className="remix-lab-run__error">{selected.error}</p> : null}

          {selected.kind === "produce" || selected.id === "produce-gate" ? (
            <>
              {typeof selected.extra?.desc === "string" && selected.extra.desc ? (
                <p className="remix-lab-muted">{selected.extra.desc}</p>
              ) : null}
              {produceDetail?.taskStatus ? (
                <p className="run-flow-inspector__meta">
                  任务状态 {produceDetail.taskStatus}
                  {produceDetail.taskError ? ` · ${produceDetail.taskError}` : ""}
                </p>
              ) : null}
              {produceDetail?.note ? <p className="remix-lab-muted">{produceDetail.note}</p> : null}
              {selected.id === "produce-publish" ? (
                <ProducePublishSection
                  stage={selected}
                  projectID={produceProjectID}
                  projectStage={produceDetail?.projectStage ?? ""}
                  busy={busyAction === "publish"}
                  onCopy={(text, label) => void copyText(text, label)}
                  onPublish={(projectID) => void publishProject(projectID)}
                />
              ) : null}
              {produceDetail?.assetText ? (
                <section>
                  <h4>实际产物</h4>
                  {keywordsDraft !== null && produceStep === "captions" ? (
                    <>
                      <textarea
                        className="run-flow-inspector__editor"
                        aria-label="编辑字幕关键词"
                        rows={14}
                        value={keywordsDraft}
                        onChange={(event) => setKeywordsDraft(event.target.value)}
                      />
                      <div className="run-flow-inspector__actions">
                        <button
                          type="button"
                          className="remix-lab-start"
                          disabled={busyAction === "keywords"}
                          onClick={() => void saveKeywords(produceProjectID, keywordsDraft)}
                        >
                          {busyAction === "keywords" ? "保存中…" : "保存关键词"}
                        </button>
                        <button type="button" className="header-button" onClick={() => setKeywordsDraft(null)}>
                          取消
                        </button>
                      </div>
                    </>
                  ) : spokenDraft !== null && produceStep === "spoken" ? (
                    <>
                      <p className="remix-lab-muted">每行一句，配音和字幕按行切。</p>
                      <textarea
                        className="run-flow-inspector__editor"
                        aria-label="编辑口播稿"
                        rows={18}
                        value={spokenDraft}
                        onChange={(event) => setSpokenDraft(event.target.value)}
                      />
                      <div className="run-flow-inspector__actions">
                        <button
                          type="button"
                          className="remix-lab-start"
                          disabled={busyAction === "spoken"}
                          onClick={() => void saveSpoken(produceProjectID, spokenDraft)}
                        >
                          {busyAction === "spoken" ? "保存中…" : "保存口播稿"}
                        </button>
                        <button type="button" className="header-button" onClick={() => setSpokenDraft(null)}>
                          取消
                        </button>
                      </div>
                    </>
                  ) : (
                    <pre className="run-flow-inspector__code">{prettyOutput(produceDetail.assetText)}</pre>
                  )}
                </section>
              ) : null}
              <div className="run-flow-inspector__actions">
              {typeof selected.extra?.script === "string" && selected.extra.script ? (
                <section>
                  <h4>二创定稿</h4>
                  <pre className="run-flow-inspector__code">{prettyOutput(String(selected.extra.script))}</pre>
                </section>
              ) : null}
              {onRetryProduce && selected.status === "failed" ? (
                <button type="button" className="remix-lab-start" onClick={onRetryProduce}>
                  重试生产并续跑（已完成的步骤不重做）
                </button>
              ) : null}
              {selected.id === "produce-gate" && selected.status === "waiting" ? (
                onConfirmProduce ? (
                  <button type="button" className="remix-lab-start" onClick={onConfirmProduce}>
                    确认开始混剪
                  </button>
                ) : (
                  <p className="remix-lab-muted">确认二创后，混剪会在这排节点上往下跑。</p>
                )
              ) : null}
              {produceStep === "spoken" && produceDetail?.assetText && spokenDraft === null && produceProjectID ? (
                <button
                  type="button"
                  className="header-button"
                  onClick={() => setSpokenDraft(produceDetail.assetText ?? "")}
                >
                  编辑口播稿
                </button>
              ) : null}
              {produceStep === "captions" && produceDetail?.assetText && keywordsDraft === null && produceProjectID ? (
                <button
                  type="button"
                  className="header-button"
                  onClick={() => setKeywordsDraft(produceDetail.assetText ?? "")}
                >
                  编辑关键词
                </button>
              ) : null}
              {productionSettled && produceStep === "spoken" ? (
                <button
                  type="button"
                  className="header-button"
                  disabled={busyAction !== ""}
                  onClick={() => void redoStep("spoken", "口播稿")}
                >
                  重做口播稿（连带关键词、配音、草稿）
                </button>
              ) : null}
              {productionSettled && produceStep === "captions" ? (
                <button
                  type="button"
                  className="header-button"
                  disabled={busyAction !== ""}
                  onClick={() => void redoStep("captions", "字幕关键词")}
                >
                  重标关键词（随后重出草稿）
                </button>
              ) : null}
              {produceStep === "narration" && produceDetail?.narrationAssetID ? (
                <button
                  type="button"
                  className="header-button"
                  onClick={() => window.open(`/api/assets/${produceDetail.narrationAssetID}/content`, "_blank")}
                >
                  试听 / 下载配音
                </button>
              ) : null}
              {productionSettled && produceStep === "narration" ? (
                <button
                  type="button"
                  className="header-button"
                  disabled={busyAction !== ""}
                  onClick={() => void redoStep("narration", "配音")}
                >
                  重新配音（随后重出草稿）
                </button>
              ) : null}
              {produceStep === "montage" && produceDetail?.draftAssetID && produceDetail.draftReady ? (
                <>
                  <button
                    type="button"
                    className="remix-lab-start"
                    disabled={Boolean(exportingDraft)}
                    onClick={() => void startExport(produceDetail.draftAssetID ?? "")}
                  >
                    {exportingDraft ? "正在导出…" : "导出视频"}
                  </button>
                  <button
                    type="button"
                    className="header-button"
                    disabled={busyAction === "open-dir"}
                    onClick={() => void openDraftDirectory(produceDetail.draftAssetID ?? "")}
                  >
                    在电脑上打开剪映目录
                  </button>
                </>
              ) : null}
              {productionSettled && produceStep === "montage" ? (
                <button
                  type="button"
                  className="header-button"
                  disabled={busyAction !== ""}
                  onClick={() => void redoStep("montage", "混剪草稿")}
                >
                  重出一份剪映草稿（旧的保留）
                </button>
              ) : null}
              {produceTaskID && (produceDetail?.taskStatus === "running" || produceDetail?.taskStatus === "queued") ? (
                <button
                  type="button"
                  className="header-button"
                  disabled={busyAction === "cancel"}
                  onClick={() => void cancelProduceTask(produceTaskID)}
                >
                  停止当前任务
                </button>
              ) : null}
              </div>
            </>
          ) : onRetryNode && selected.status === "failed" ? (
            <div className="run-flow-inspector__actions run-flow-inspector__retry">
              <label className="run-flow-retry-model">
                换个模型重试（可空，留空沿用{selected.model ? ` ${selected.model}` : "原模型"}）
                <input
                  aria-label="重试使用的模型"
                  value={retryModel}
                  placeholder={selected.model || "模型 ID，例如 claude-opus-4-6"}
                  onChange={(event) => setRetryModel(event.target.value)}
                />
              </label>
              <button
                type="button"
                className="remix-lab-start"
                onClick={() => onRetryNode(selected.kind === "agent" ? selected.id : "", retryModel.trim() || undefined)}
              >
                {selected.kind === "agent" ? "重试此节点并续跑" : "从断点重试（已跑完的agent不重跑）"}
                {retryModel.trim() ? " · 换用新模型" : ""}
              </button>
            </div>
          ) : null}

          {selected.prompt_key ? (
            <div className="run-flow-inspector__actions">
              {selected.prompt_key === "writer" ? (
                <p className="remix-lab-muted">写手提示词在提示词库编辑（本次运行用的是「{String(selected.extra?.prompt_name ?? "")}」）。</p>
              ) : (
                <button type="button" className="header-button" onClick={onEditAgentPrompts}>
                  编辑这路Agent提示词
                </button>
              )}
            </div>
          ) : null}

          {selected.kind === "gate" && selected.extra?.limits != null ? (
            <section>
              <h4>本次生效阈值</h4>
              <p className="run-flow-inspector__meta">
                {(() => {
                  const limits = selected.extra.limits as Record<string, unknown>;
                  return `连抄 ≤${String(limits.overlap_max_pct)}%（硬上限 ${String(limits.overlap_hard_pct)}%）· 篇幅 ≥${String(limits.len_min_ratio)}倍（硬下限 ${String(limits.len_hard_ratio)}）· 最多返工 ${String(limits.max_rounds)} 轮`;
                })()}
              </p>
              <p className="remix-lab-muted">在设计画布点机械自检节点可以按篇调整这些阈值。</p>
            </section>
          ) : null}
          {selected.kind === "gate" && checkEvents.length > 0 ? (
            <section>
              <h4>自检轮次</h4>
              <ul className="run-flow-inspector__events">
                {checkEvents.map((event, index) => (
                  <li key={index}>
                    第{String(event.round ?? index)}轮 · {String(event.verdict ?? "")}
                    {event.overlap_pct != null ? ` · 连抄${String(event.overlap_pct)}%` : ""}
                    {event.len_ratio != null ? ` · 篇幅${String(event.len_ratio)}倍` : ""}
                  </li>
                ))}
              </ul>
            </section>
          ) : null}

          {selected.kind === "output" && selected.output ? (
            <FinalDraftSection
              output={selected.output}
              onCopy={(text, label) => void copyText(text, label)}
              onSave={savePackage}
              saving={busyAction === "package"}
            />
          ) : selected.prompt_key === "reviewer_system" && selected.output ? (
            <ReviewStageSection
              output={selected.output}
              draftV1={typeof selected.extra?.draft_v1 === "string" ? selected.extra.draft_v1 : ""}
            />
          ) : selected.output ? (
            <section>
              <h4>输出</h4>
              <pre className="run-flow-inspector__code">{prettyOutput(selected.output)}</pre>
            </section>
          ) : null}
          {selected.system_prompt ? (
            <section>
              <h4>
                {selected.kind === "produce"
                  ? "任务提示词（设计画布的生产节点可改）"
                  : `系统提示词${selected.prompt_key === "writer" ? "（本次运行实际发送）" : ""}`}
              </h4>
              <pre className="run-flow-inspector__code">{selected.system_prompt}</pre>
            </section>
          ) : null}
          {selected.user_prompt ? (
            <section>
              <h4>用户消息{selected.prompt_key === "writer" ? "（含注入的情报包）" : "（模板）"}</h4>
              <pre className="run-flow-inspector__code">{selected.user_prompt}</pre>
            </section>
          ) : null}
        </aside>
      ) : null}
    </div>
  );
}

export function RunFlow({ api, runID, runLabel, onClose, onEditAgentPrompts, onMessage, onRetryNode, onRetryProduce, onConfirmProduce }: RunFlowProps) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div
        className="preview-modal remix-lab-modal run-flow-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="run-flow-title"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="modal-head">
          <div>
            <span className="muted">工作流视图 · 每一步的真实输入输出</span>
            <h2 id="run-flow-title">{runLabel}</h2>
          </div>
          <button type="button" className="close" aria-label="关闭工作流视图" onClick={onClose}>
            <X size={20} aria-hidden="true" />
          </button>
        </div>
        <RunFlowPanel
          api={api}
          runID={runID}
          onEditAgentPrompts={onEditAgentPrompts}
          onMessage={onMessage}
          onRetryNode={onRetryNode}
          onRetryProduce={onRetryProduce}
          onConfirmProduce={onConfirmProduce}
        />
      </div>
    </div>
  );
}
