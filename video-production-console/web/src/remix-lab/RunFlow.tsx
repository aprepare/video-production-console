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
  type RemixLabApi,
  type RemixLabProduction,
  type RemixLabRunStage,
  type RemixLabRunStagesView,
} from "./api";

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
  /** 提供后，失败节点的检视器出现「重试并续跑」按钮（agent节点传ID，其余整体重试）。 */
  onRetryNode?: (nodeID: string) => void;
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
  onRetryNode?: (nodeID: string) => void;
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

/** 生产节点的实际产物（任务状态+资产内容），选中时按需拉取。 */
type ProduceDetail = {
  key: string;
  taskStatus?: string;
  taskError?: string;
  assetText?: string;
  note?: string;
};

export function RunFlowPanel({ api, runID, live = false, onEditAgentPrompts, onMessage, onRetryNode, onStatusChange, onProductionChange, onRetryProduce, onConfirmProduce }: RunFlowPanelProps) {
  const [view, setView] = useState<RemixLabRunStagesView | null>(null);
  const [selectedID, setSelectedID] = useState<string>("");
  const [produceDetail, setProduceDetail] = useState<ProduceDetail | null>(null);
  const autoSelected = useRef(false);
  const lastStatus = useRef("");

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const next = await fetchRemixLabRunStages(api, runID);
        if (cancelled) return;
        setView(next);
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
    if (!live) {
      return () => {
        cancelled = true;
      };
    }
    const timer = window.setInterval(() => void load(), 2500);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [api, runID, live, onMessage, onStatusChange, onProductionChange]);

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

  // 生产节点选中时按需拉实况：任务状态 + 该步实际产物（口播稿/字幕关键词/SRT）。
  const produceTaskID = selected?.kind === "produce" ? String(selected.extra?.task_id ?? "") : "";
  const produceProjectID = selected?.kind === "produce" ? String(selected.extra?.project_id ?? "") : "";
  const produceAssetType = selected?.kind === "produce" ? String(selected.extra?.asset_type ?? "") : "";
  const produceStatus = selected?.kind === "produce" ? selected.status : "";
  useEffect(() => {
    if (!selectedID || (!produceTaskID && !(produceProjectID && produceAssetType))) {
      setProduceDetail(null);
      return;
    }
    const key = [selectedID, produceTaskID, produceProjectID, produceAssetType, produceStatus].join("|");
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
        if (produceProjectID && produceAssetType) {
          const response = await api(`/api/projects/${produceProjectID}`);
          if (response.ok) {
            const detail = (await response.json()) as {
              assets?: Record<string, { id?: string; state?: string }>;
            };
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
      } catch {
        next.note = "实况详情拉取失败。";
      }
      if (!cancelled) setProduceDetail(next);
    })();
    return () => {
      cancelled = true;
    };
  }, [api, selectedID, produceTaskID, produceProjectID, produceAssetType, produceStatus]);

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
        <aside className="run-flow-inspector" aria-label={`${selected.title}详情`}>
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
              {produceDetail?.assetText ? (
                <section>
                  <h4>实际产物</h4>
                  <pre className="run-flow-inspector__code">{prettyOutput(produceDetail.assetText)}</pre>
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
              </div>
            </>
          ) : onRetryNode && selected.status === "failed" ? (
            <div className="run-flow-inspector__actions">
              <button
                type="button"
                className="remix-lab-start"
                onClick={() => onRetryNode(selected.kind === "agent" ? selected.id : "")}
              >
                {selected.kind === "agent" ? "重试此节点并续跑" : "从断点重试（已跑完的agent不重跑）"}
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

          {selected.output ? (
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
          {selected.id === "review" && typeof selected.extra?.draft_v1 === "string" && selected.extra.draft_v1 ? (
            <section>
              <h4>进审初稿（draft_v1）</h4>
              <pre className="run-flow-inspector__code">{prettyOutput(selected.extra.draft_v1 as string)}</pre>
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
