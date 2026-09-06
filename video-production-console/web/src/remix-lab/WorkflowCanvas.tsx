import { FixedFlowSteps, type FixedFlowStep } from "./FixedFlowSteps";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Edge, Node } from "@xyflow/react";
import {
  BookOpenText,
  Clapperboard,
  FileText,
  FolderPlus,
  Gauge,
  Maximize2,
  Minimize2,
  PenLine,
  Play,
  Redo2,
  ScanSearch,
  Stamp,
  Trash2,
  Undo2,
  Volume2,
  X,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import {
  fetchRemixLabWorkflow,
  importRemixLabDraft,
  produceRemixLabRun,
  retryRemixLabRun,
  saveRemixLabWorkflow,
  type RemixLabApi,
  type RemixLabDefaults,
  type RemixLabExperiment,
  type RemixLabProduction,
  type RemixLabPrompt,
  type RemixLabWorkflow,
  type RemixLabWorkflowNode,
  type RemixLabWorkflowProduction,
} from "./api";
import { RunFlowPanel } from "./RunFlow";
import { ModelMultiSelect } from "../ModelSelect";
import { FastModeButton } from "./FastModeButton";
import { RerunDialog } from "./RerunDialog";
import { RoleModelsDialog } from "./RoleModelsDialog";
import { WorkflowPromptEditor } from "./WorkflowPromptEditor";

type WorkflowCanvasProps = {
  modelDefaults?: RemixLabDefaults | null;
  onOpenConnections?: () => void;
  defaultServiceTier?: string;
  api: RemixLabApi;
  prompts: RemixLabPrompt[];
  onMessage: (text: string) => void;
  onEditAgentPrompts: () => void;
  runWorkflow: (source: string, runCount: number, accountID: string, auto: boolean, models: string[]) => Promise<RemixLabExperiment>;
  /** 工作流被外部改动（智能体提案确认）后父级递增，步骤列表重新拉取。 */
  refreshToken: number;
  /** 当前账号：读写该账号最新一版工作流；空=全局默认。 */
  accountID?: string;
  onAccountIDChange?: (accountID: string) => void;
  /** 历史项目/实验详情：把已有运行打开运行步骤，继续确认和混剪。 */
  resumeExperiment?: RemixLabExperiment | null;
  onEditPackage?: (runID: string, experimentID: string) => void;
  onLeaveRun?: () => void;
  onRerunCreated?: (experiment: RemixLabExperiment) => void;
};

type AccountOption = { id: string; name: string; status: string };

// 运行页签文字：多模型对比时带上模型名，否则只有「运行 N」区分不开。
function liveRunTabs(exp: RemixLabExperiment): LiveRunState["runs"] {
  const modelBySlot = new Map(exp.slots.map((slot) => [slot.id, slot.model]));
  const slotByID = new Map(exp.slots.map((slot) => [slot.id, slot]));
  const multiModel = new Set(exp.runs.map((run) => run.slot_id)).size > 1;
  return exp.runs.map((run) => ({
    id: run.id,
    run_index: run.run_index,
    label: slotByID.get(run.slot_id)?.label?.startsWith("重跑")
      ? `第 ${(slotByID.get(run.slot_id)?.sort_index ?? 0) + 1} 轮 · ${modelBySlot.get(run.slot_id)}`
      : multiModel
      ? `${modelBySlot.get(run.slot_id) || "模型"}${exp.runs.filter((r) => r.slot_id === run.slot_id).length > 1 ? ` · ${run.run_index}` : ""}`
      : `运行 ${run.run_index}`,
  }));
}

type LiveRunState = {
  experimentID: string;
  runs: Array<{ id: string; run_index: number; label: string }>;
  activeRunID: string;
};

const RUN_STATUS_LABEL: Record<string, string> = {
  queued: "排队中",
  running: "生成中",
  completed: "已完成",
  failed: "失败",
};

const PRODUCE_STEP_LABEL: Record<string, string> = {
  project: "建项目导入",
  spoken: "口播稿",
  captions: "字幕关键词",
  narration: "配音",
  montage: "混剪草稿",
  done: "完成",
};

const NODE_TYPE_META: Record<string, { label: string; Icon: LucideIcon }> = {
  input: { label: "输入", Icon: FileText },
  agent: { label: "前置情报", Icon: ScanSearch },
  writer: { label: "写手", Icon: PenLine },
  selfcheck: { label: "规则检查", Icon: Gauge },
  reviewer: { label: "审稿", Icon: Stamp },
  output: { label: "产出", Icon: BookOpenText },
};

// 定稿之后的固定制作步骤；字幕关键词可关闭，
// 口播/字幕/混剪三步的任务提示词可改（存进工作流的 production 配置）。
const PRODUCTION_DEFAULT_PROMPTS: Record<string, string> = {
  "produce-spoken": "按一句一行、每行不超过九个字，把当前连续文案切成口播稿。",
  "produce-captions": "为口播稿每一行挑出值得放大强调的警示词和数字。",
  "produce-montage": "执行风景混剪，产出可编辑的剪映草稿。",
};

const EFFORT_OPTIONS = ["", "low", "medium", "high", "xhigh"] as const;

function normalizeProduction(raw?: RemixLabWorkflowProduction): RemixLabWorkflowProduction {
  if (!raw) return { captions_disabled: true };
  return { ...raw };
}

function optionalNumberValue(value: number | undefined): string {
  return value === undefined || Number.isNaN(value) ? "" : String(value);
}

function firstLineTitle(text: string): string {
  const line = text.trim().split(/\r?\n/, 1)[0]?.trim() ?? "";
  const runes = [...line];
  if (runes.length === 0) return "手工定稿";
  return runes.length > 24 ? runes.slice(0, 24).join("") : line;
}

type ProdPromptKey = "spoken_prompt" | "captions_prompt" | "montage_prompt";

type ProdNodeSpec = { id: string; title: string; kindLabel: string; promptKey?: ProdPromptKey };

function productionNodeSpecs(captionsDisabled: boolean): ProdNodeSpec[] {
  const specs: ProdNodeSpec[] = [
    { id: "produce-gate", title: "确认二创", kindLabel: "混剪" },
    { id: "produce-project", title: "建项目", kindLabel: "混剪" },
    { id: "produce-spoken", title: "口播稿", kindLabel: "混剪", promptKey: "spoken_prompt" },
  ];
  if (!captionsDisabled) {
    specs.push({ id: "produce-captions", title: "字幕关键词", kindLabel: "混剪", promptKey: "captions_prompt" });
  }
  specs.push(
    { id: "produce-narration", title: "配音", kindLabel: "混剪" },
    { id: "produce-montage", title: "混剪草稿", kindLabel: "混剪", promptKey: "montage_prompt" },
    { id: "produce-publish", title: "发布", kindLabel: "混剪" },
  );
  return specs;
}

function prodIcon(id: string): LucideIcon {
  switch (id) {
    case "produce-gate":
      return Gauge;
    case "produce-project":
      return FolderPlus;
    case "produce-spoken":
      return FileText;
    case "produce-narration":
      return Volume2;
    case "produce-publish":
      return Stamp;
    default:
      return Clapperboard;
  }
}

type CanvasNodeData = { wfNode: RemixLabWorkflowNode };

type CanvasSnap = {
  nodes: Node[];
  edges: Edge[];
  prod: RemixLabWorkflowProduction;
};

function cloneSnap(nodes: Node[], edges: Edge[], prod: RemixLabWorkflowProduction): CanvasSnap {
  return structuredClone({ nodes, edges, prod });
}

function toRfNodes(workflow: RemixLabWorkflow): Node[] {
  return workflow.nodes.map((wfNode) => ({
    id: wfNode.id,
    type: "wfNode",
    position: { x: wfNode.x, y: wfNode.y },
    data: { wfNode },
  }));
}

function toRfEdges(workflow: RemixLabWorkflow): Edge[] {
  return workflow.edges.map(([from, to]) => ({
    id: `${from}->${to}`,
    source: from,
    target: to,
    style: { strokeWidth: 1.6 },
  }));
}

export function WorkflowCanvas({ api, prompts, onMessage, runWorkflow, refreshToken, accountID: accountIDProp, onAccountIDChange, resumeExperiment, onEditPackage, onLeaveRun, onRerunCreated, modelDefaults, onOpenConnections, defaultServiceTier = "" }: WorkflowCanvasProps) {
  const [meta, setMeta] = useState<{ version: number; name: string }>({ version: 1, name: "默认二创工作流" });
  const [rfNodes, setRfNodes] = useState<Node[]>([]);
  const [rfEdges, setRfEdges] = useState<Edge[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [prodCfg, setProdCfg] = useState<RemixLabWorkflowProduction>({});
  const [editorialRules, setEditorialRules] = useState<string | undefined>(undefined);
  const [selectedID, setSelectedID] = useState("");
  const sourceKey = `remix-lab:source:${accountIDProp || "default"}`;
  const [source, setSourceValue] = useState(() => {
    try { return window.sessionStorage.getItem(sourceKey) || ""; } catch { return ""; }
  });
  const setSource = (value: string) => {
    setSourceValue(value);
    try { window.sessionStorage.setItem(sourceKey, value); } catch { /* Storage may be unavailable. */ }
  };
  useEffect(() => {
    try { setSourceValue(window.sessionStorage.getItem(sourceKey) || ""); } catch { setSourceValue(""); }
  }, [sourceKey]);
  const [draftScript, setDraftScript] = useState("");
  const [draftBoardTitle, setDraftBoardTitle] = useState("");
  const [draftSubtitle, setDraftSubtitle] = useState("");
  const [importing, setImporting] = useState(false);
  const [runCount, setRunCount] = useState(1);
  // 对比开跑：勾选多个模型时每个模型各出一稿并行跑；空 = 用写手节点/默认档。
  const [compareModels, setCompareModels] = useState<string[]>([]);
  const [starting, setStarting] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const [liveRun, setLiveRun] = useState<LiveRunState | null>(null);
  const [rerunID, setRerunID] = useState("");
  const [modelWorkflow, setModelWorkflow] = useState<RemixLabWorkflow | null>(null);
  const [promptEditorOpen, setPromptEditorOpen] = useState(false);
  const [runStatus, setRunStatus] = useState("queued");
  const [production, setProduction] = useState<RemixLabProduction | null>(null);
  const [accounts, setAccounts] = useState<AccountOption[]>([]);
  const [accountID, setAccountID] = useState(() => {
    try {
      return window.localStorage.getItem("remix-lab:produce-account") ?? "";
    } catch {
      return "";
    }
  });
  const [autoProduce, setAutoProduce] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [histTick, setHistTick] = useState(0);
  const nodesRef = useRef<Node[]>([]);
  const edgesRef = useRef<Edge[]>([]);
  const prodRef = useRef<RemixLabWorkflowProduction>({});
  const editorialRulesRef = useRef<string | undefined>(undefined);
  const historyRef = useRef<CanvasSnap[]>([]);
  const futureRef = useRef<CanvasSnap[]>([]);
  const lastHistAt = useRef(0);
  const savingRef = useRef(false);
  const pendingSave = useRef(false);
  const skipHist = useRef(false);

  // 生产账号列表（开跑栏与确认闸门共用）。
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const response = await api("/api/accounts");
        if (!response.ok) return;
        const all = (await response.json()) as AccountOption[];
        if (cancelled) return;
        setAccounts(all.filter((account) => account.status !== "inactive"));
      } catch {
        // 账号列表拉不到不拦页面：开跑时再提示
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [api]);

  const pickAccount = (value: string) => {
    setAccountID(value);
    try {
      window.localStorage.setItem("remix-lab:produce-account", value);
    } catch {
      // 忽略存储不可用
    }
    if (!value) setAutoProduce(false);
    onAccountIDChange?.(value);
  };

  useEffect(() => {
    if (accountIDProp === undefined) return;
    setAccountID(accountIDProp);
  }, [accountIDProp]);

  // 全屏时 Esc 退出（优先于节点抽屉）。
  useEffect(() => {
    if (!fullscreen) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setFullscreen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [fullscreen]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const workflow = await fetchRemixLabWorkflow(api, accountIDProp);
        if (cancelled) return;
        setMeta({ version: workflow.version, name: workflow.name });
        setRfNodes(toRfNodes(workflow));
        setRfEdges(toRfEdges(workflow));
        setProdCfg(normalizeProduction(workflow.production));
        setEditorialRules(workflow.editorial_rules);
        setSelectedID(workflow.nodes.find((node) => node.type === "input")?.id || workflow.nodes[0]?.id || "");
        setLoaded(true);
        setDirty(false);
        historyRef.current = [];
        futureRef.current = [];
        lastHistAt.current = 0;
        setHistTick((tick) => tick + 1);
      } catch (error) {
        if (!cancelled) onMessage(error instanceof Error ? error.message : "工作流读取失败。");
      }
    })();
    return () => {
      cancelled = true;
    };
    // refreshToken 变化（智能体确认了 update_workflow）时重新拉取。
  }, [api, refreshToken, accountIDProp, setRfNodes, setRfEdges, onMessage]);

  useEffect(() => {
    if (!resumeExperiment?.runs.length) return;
    const runs = liveRunTabs(resumeExperiment);
    let savedRunID = "";
    try { savedRunID = window.sessionStorage.getItem(`remix-lab:active-run:${resumeExperiment.id}`) || ""; } catch { /* optional preference */ }
    const keep = liveRun?.experimentID === resumeExperiment.id && runs.some((run) => run.id === liveRun.activeRunID)
      ? liveRun.activeRunID
      : runs.find(run => run.id === savedRunID)?.id || runs[0].id;
    const active = resumeExperiment.runs.find((run) => run.id === keep) ?? resumeExperiment.runs[0];
    setLiveRun({ experimentID: resumeExperiment.id, runs, activeRunID: keep });
    setSourceValue(resumeExperiment.source_text);
    setRunStatus(active.status);
    setProduction(active.production ?? null);
    // liveRun 只用来保住当前选中的 run，不放进依赖以免轮询把选中冲掉。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resumeExperiment]);

  useEffect(() => {
    if (!liveRun) return;
    try { window.sessionStorage.setItem(`remix-lab:active-run:${liveRun.experimentID}`, liveRun.activeRunID); } catch { /* optional preference */ }
  }, [liveRun?.experimentID, liveRun?.activeRunID]);

  // 闸门上选的混剪账号只决定这一稿进哪个号，不切换页面的账号/工作流。
  // 默认跟这一稿自己的账号（运行记录里的）或页面当前账号；都没有就留空让人选。
  // 每换一条 run 都重新取默认——以前沿用上一稿的选择，结果 A 号写的稿被放进了 B 号。
  const [gateAccountID, setGateAccountID] = useState("");
  const gateDefaultKey = useRef("");
  useEffect(() => {
    if (production?.status !== "waiting_confirm") {
      gateDefaultKey.current = "";
      setGateAccountID("");
      return;
    }
    const key = `${liveRun?.activeRunID ?? ""}|${production.account_id}|${accountID}`;
    if (gateDefaultKey.current === key) return;
    gateDefaultKey.current = key;
    setGateAccountID(production.account_id || accountID || "");
  }, [production?.status, production?.account_id, accountID, liveRun?.activeRunID]);
  const gateAccountMismatch =
    production?.status === "waiting_confirm" && !!production.account_id && !!gateAccountID && gateAccountID !== production.account_id;

  nodesRef.current = rfNodes;
  edgesRef.current = rfEdges;
  prodRef.current = prodCfg;
  editorialRulesRef.current = editorialRules;

  const beginHistory = useCallback((force = false) => {
    if (skipHist.current) return;
    const now = Date.now();
    if (!force && historyRef.current.length > 0 && now - lastHistAt.current < 700) return;
    historyRef.current = [
      ...historyRef.current.slice(-49),
      cloneSnap(nodesRef.current, edgesRef.current, prodRef.current),
    ];
    futureRef.current = [];
    lastHistAt.current = now;
    setHistTick((tick) => tick + 1);
  }, []);

  const applySnap = useCallback((snap: CanvasSnap) => {
    skipHist.current = true;
    setRfNodes(snap.nodes);
    setRfEdges(snap.edges);
    setProdCfg(snap.prod);
    setDirty(true);
    window.setTimeout(() => {
      skipHist.current = false;
    }, 0);
  }, [setRfNodes, setRfEdges]);

  const undo = useCallback(() => {
    const prev = historyRef.current.pop();
    if (!prev) return;
    futureRef.current.push(cloneSnap(nodesRef.current, edgesRef.current, prodRef.current));
    lastHistAt.current = 0;
    setHistTick((tick) => tick + 1);
    applySnap(prev);
  }, [applySnap]);

  const redo = useCallback(() => {
    const next = futureRef.current.pop();
    if (!next) return;
    historyRef.current.push(cloneSnap(nodesRef.current, edgesRef.current, prodRef.current));
    lastHistAt.current = 0;
    setHistTick((tick) => tick + 1);
    applySnap(next);
  }, [applySnap]);

  // 写手节点当前配的模型，给对比多选做「不勾时用什么」的提示。
  const writerNodeModel = useMemo(() => {
    const writer = rfNodes.find((node) => (node.data as CanvasNodeData).wfNode?.type === "writer");
    return ((writer?.data as CanvasNodeData | undefined)?.wfNode.config.model ?? "").trim();
  }, [rfNodes]);

  const selectedNode = useMemo(() => {
    const found = rfNodes.find((node) => node.id === selectedID);
    return found ? (found.data as CanvasNodeData).wfNode : null;
  }, [rfNodes, selectedID]);

  const prodSpecs = useMemo(
    () => productionNodeSpecs(Boolean(prodCfg.captions_disabled)),
    [prodCfg.captions_disabled],
  );
  // 固定展示顺序不改变保存的工作流、依赖关系或旧版位置数据。
  const steps = useMemo<FixedFlowStep[]>(() => {
    const order: Record<string, number> = { input: 0, agent: 1, writer: 2, selfcheck: 3, reviewer: 4, output: 5 };
    const create = rfNodes.map((node) => (node.data as CanvasNodeData).wfNode)
      .sort((a, b) => (order[a.type] ?? 1) - (order[b.type] ?? 1))
      .map((node): FixedFlowStep => ({
        id: node.id, title: node.title, group: "create",
        icon: NODE_TYPE_META[node.type]?.Icon || ScanSearch,
        subtitle: String(node.config.model || (node.type === "input" ? "粘贴原文或导入定稿" : NODE_TYPE_META[node.type]?.label || "步骤配置")),
      }));
    return [...create, ...prodSpecs.map((spec): FixedFlowStep => ({
      id: spec.id, title: spec.title, group: "produce", icon: prodIcon(spec.id),
      subtitle: spec.promptKey ? "提示词与执行设置" : spec.id === "produce-gate" ? "检查文案后开始" : spec.id === "produce-publish" ? "导出后确认发布" : "制作设置",
    }))];
  }, [rfNodes, prodSpecs]);

  const selectedProdSpec = useMemo(
    () => prodSpecs.find((spec) => spec.id === selectedID) ?? null,
    [prodSpecs, selectedID],
  );

  const patchProd = useCallback((patch: Partial<RemixLabWorkflowProduction>) => {
    beginHistory();
    setProdCfg((current) => {
      const next: RemixLabWorkflowProduction = { ...current, ...patch };
      (Object.keys(patch) as Array<keyof RemixLabWorkflowProduction>).forEach((key) => {
        if (patch[key] === undefined || patch[key] === "") {
          delete next[key];
        }
      });
      return next;
    });
    setDirty(true);
  }, [beginHistory]);

  const patchNode = useCallback(
    (id: string, patch: (wfNode: RemixLabWorkflowNode) => RemixLabWorkflowNode) => {
      beginHistory();
      setRfNodes((current) =>
        current.map((node) =>
          node.id === id
            ? { ...node, data: { wfNode: patch((node.data as CanvasNodeData).wfNode) } }
            : node,
        ),
      );
      setDirty(true);
    },
    [beginHistory, setRfNodes],
  );

  const removeNode = (id: string) => {
    const target = rfNodes.find((node) => node.id === id);
    if (!target) return;
    if ((target.data as CanvasNodeData).wfNode.type !== "agent") {
      onMessage("骨干节点不能删，只能删 agent 情报节点。");
      return;
    }
    beginHistory(true);
    setRfNodes((current) => current.filter((node) => node.id !== id));
    setRfEdges((current) => current.filter((edge) => edge.source !== id && edge.target !== id));
    if (selectedID === id) setSelectedID("");
    setDirty(true);
  };

  const buildWorkflow = (): RemixLabWorkflow => ({
    version: meta.version,
    name: meta.name,
    editorial_rules: editorialRulesRef.current,
    nodes: nodesRef.current.map((node) => ({
      ...(node.data as CanvasNodeData).wfNode,
      x: Math.round(node.position.x),
      y: Math.round(node.position.y),
    })),
    edges: edgesRef.current.map((edge) => [edge.source, edge.target] as [string, string]),
    production: prodRef.current,
  });

  const saveRef = useRef<() => Promise<boolean>>(async () => false);
  const save = async (): Promise<boolean> => {
    if (savingRef.current) {
      pendingSave.current = true;
      return false;
    }
    savingRef.current = true;
    try {
      const saved = await saveRemixLabWorkflow(api, buildWorkflow(), accountIDProp || accountID);
      setMeta({ version: saved.version, name: saved.name });
      setDirty(false);
      return true;
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "工作流保存失败。");
      return false;
    } finally {
      savingRef.current = false;
      if (pendingSave.current) {
        pendingSave.current = false;
        void saveRef.current();
      }
    }
  };
  saveRef.current = save;

  const openModels = async () => {
    if (savingRef.current) { onMessage("正在保存工作流，请稍后打开模型配置。"); return; }
    if (dirty && !await save()) return;
    pendingConfig.current = null;
    setModelWorkflow(buildWorkflow());
  };

  const openPromptEditor = async () => {
    if (savingRef.current) {
      onMessage("正在保存工作流，请稍后编辑二创提示词。");
      return;
    }
    if (dirty && !await save()) return;
    pendingConfig.current = null;
    setPromptEditorOpen(true);
  };

  const saveModels = async (workflow: RemixLabWorkflow) => {
    const saved = await saveRemixLabWorkflow(api, workflow, accountIDProp ?? accountID);
    const nextNodes = toRfNodes(saved);
    nodesRef.current = nextNodes;
    setRfNodes(nextNodes);
    setRfEdges(toRfEdges(saved));
    setProdCfg(normalizeProduction(saved.production));
    setEditorialRules(saved.editorial_rules);
    edgesRef.current = toRfEdges(saved);
    prodRef.current = normalizeProduction(saved.production);
    editorialRulesRef.current = saved.editorial_rules;
    setMeta({version:saved.version, name:saved.name});
    pendingConfig.current = null;
    setDirty(false);
    onMessage("模型配置已保存，下次新建或重新生成时使用。");
  };

  const start = async () => {
    if (starting) return;
    if (!source.trim()) {
      onMessage("先在对标原文步骤粘贴完整文案。");
      return;
    }
    if (dirty) {
      const ok = await save();
      if (!ok) return;
    }
    setStarting(true);
    try {
      const created = await runWorkflow(source.trim(), runCount, accountID, autoProduce, compareModels);
      // 原地切到运行视图，实时显示每个步骤的进度。
      setLiveRun({
        experimentID: created.id,
        runs: liveRunTabs(created),
        activeRunID: created.runs[0]?.id ?? "",
      });
      setRunStatus("queued");
      setProduction(null);
      setSelectedID("");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "工作流开跑失败。");
    } finally {
      setStarting(false);
    }
  };

  const importDraft = async () => {
    if (importing) return;
    const script = draftScript.trim();
    if (!script) {
      onMessage("请填写定稿正文。");
      return;
    }
    if (dirty) {
      const ok = await save();
      if (!ok) return;
    }
    setImporting(true);
    try {
      const board = draftBoardTitle.trim() || firstLineTitle(script);
      const shortTitles = [board, draftSubtitle.trim()].filter(Boolean);
      const created = await importRemixLabDraft(api, {
        continuous_script: script,
        titles: [],
        short_titles: shortTitles,
        descriptions: [],
        topics: [],
        cta: "",
        account_id: accountID,
      });
      setLiveRun({
        experimentID: created.id,
        runs: liveRunTabs(created),
        activeRunID: created.runs[0]?.id ?? "",
      });
      setRunStatus("completed");
      setProduction(created.runs[0]?.production ?? null);
      setSelectedID("");
      onMessage("已导入定稿，确认后开始混剪。");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "定稿导入失败。");
    } finally {
      setImporting(false);
    }
  };

  const pendingConfig = useRef<{ workflow: ReturnType<typeof buildWorkflow>; owner: string } | null>(null);
  useEffect(() => {
    if (!dirty || !loaded || liveRun) return;
    const snapshot = { workflow: buildWorkflow(), owner: accountIDProp || accountID };
    pendingConfig.current = snapshot;
    const timer = window.setTimeout(() => {
      void saveRef.current().then(ok => {
        if (ok && pendingConfig.current === snapshot) pendingConfig.current = null;
      });
    }, 500);
    return () => window.clearTimeout(timer);
  }, [dirty, rfNodes, rfEdges, prodCfg, loaded, liveRun]);

  useEffect(() => () => {
    const pending = pendingConfig.current;
    pendingConfig.current = null;
    if (pending) void saveRemixLabWorkflow(api, pending.workflow, pending.owner).catch(error => {
      onMessage(error instanceof Error ? error.message : "工作流保存失败。");
    });
  }, [api, accountIDProp]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (!(event.ctrlKey || event.metaKey) || event.altKey) return;
      const target = event.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.tagName === "SELECT" ||
          target.isContentEditable)
      ) {
        return;
      }
      if (event.key === "z" && !event.shiftKey) {
        event.preventDefault();
        undo();
      } else if (event.key === "y" || (event.key === "z" && event.shiftKey)) {
        event.preventDefault();
        redo();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [undo, redo]);

  // 确认闸门放行 / 生产失败续跑。
  const produceLiveRun = async () => {
    if (!liveRun || confirming) return;
    const chosen = gateAccountID || production?.account_id || accountID || "";
    if (!chosen) {
      onMessage("先在底部选好要进哪个账号的混剪。");
      return;
    }
    setConfirming(true);
    try {
      await produceRemixLabRun(api, liveRun.activeRunID, chosen);
      setProduction((current) => (current ? { ...current, status: "running" } : current));
      onMessage("已放行：建项目 → 口播稿 → 配音 → 混剪，在步骤列表查看进度。");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "开始混剪失败。");
    } finally {
      setConfirming(false);
    }
  };

  // 断点重试：agent 节点传ID（作废重跑），其余整体重试（缓存复用）。
  // model 非空时先换模型再重试，同实验后续重试沿用新模型。
  const retryLock = useRef(false);
  const retryLiveRun = async (nodeID: string, model?: string) => {
    if (!liveRun || retryLock.current) return;
    retryLock.current = true;
    try {
      await retryRemixLabRun(api, liveRun.activeRunID, nodeID || undefined, model);
      setRunStatus("running");
      onMessage(
        model
          ? `已换用 ${model} 重试，后续重试与返工也用它。`
          : nodeID
            ? "已重试该节点，成功后自动续跑后面的环节。"
            : "已从断点重试，跑过的agent节点直接复用产物。",
      );
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "重试提交失败。");
    } finally { retryLock.current = false; }
  };

  return (
    <div className={fullscreen ? "wf-designer wf-designer--fixed wf-designer--fullscreen" : "wf-designer wf-designer--fixed"}>
      <div className="wf-designer__toolbar">
        <div className="wf-designer__title">
          <strong>{meta.name}</strong>
        </div>
        <div className="wf-designer__actions">
          {loaded ? (
            <button type="button" className="header-button" onClick={() => void openPromptEditor()}>
              编辑二创提示词
            </button>
          ) : null}
          {loaded ? <button type="button" className="header-button" onClick={() => void openModels()}>模型配置</button> : null}
          {!liveRun ? (
            <>
              <button
                type="button"
                className="header-button remix-lab-icon-btn"
                disabled={histTick >= 0 && historyRef.current.length === 0}
                onClick={undo}
                aria-label="撤销"
                title="撤销（Ctrl+Z）"
              >
                <Undo2 size={14} strokeWidth={2} />
                撤销
              </button>
              <button
                type="button"
                className="header-button remix-lab-icon-btn"
                disabled={histTick >= 0 && futureRef.current.length === 0}
                onClick={redo}
                aria-label="重做"
                title="重做（Ctrl+Y）"
              >
                <Redo2 size={14} strokeWidth={2} />
                重做
              </button>
            </>
          ) : null}
          <button
            type="button"
            className="header-button remix-lab-icon-btn"
            onClick={() => setFullscreen((current) => !current)}
          >
            {fullscreen ? <Minimize2 size={14} strokeWidth={2} /> : <Maximize2 size={14} strokeWidth={2} />}
            {fullscreen ? "退出全屏" : "全屏"}
          </button>
        </div>
      </div>

      {liveRun ? (
        <div className="wf-designer__live">
          <div className="wf-designer__live-head">
            <span
              className={`remix-lab-seal remix-lab-seal--${
                production?.status === "failed" || runStatus === "failed"
                  ? "danger"
                  : production?.status === "completed"
                    ? "ok"
                    : production?.status === "running"
                      ? "live"
                      : runStatus === "completed"
                        ? "ok"
                        : "live"
              }`}
            >
              {production?.status === "running"
                ? `混剪中 · ${PRODUCE_STEP_LABEL[production.step] ?? production.step}`
                : production?.status === "completed"
                  ? "剪映草稿已生成"
                  : production?.status === "failed"
                    ? "生产失败"
                    : (RUN_STATUS_LABEL[runStatus] ?? runStatus)}
            </span>
            {liveRun.runs.length > 1
              ? liveRun.runs.map((run) => (
                  <button
                    key={run.id}
                    type="button"
                    className={
                      run.id === liveRun.activeRunID
                        ? "remix-lab-slot-chip remix-lab-slot-chip--active"
                        : "remix-lab-slot-chip"
                    }
                    onClick={() => {
                      setLiveRun({ ...liveRun, activeRunID: run.id });
                      setRunStatus("queued");
                      setProduction(null);
                    }}
                  >
                    {run.label}
                  </button>
                ))
              : null}
            <span className="remix-lab-muted">
              {production?.status === "waiting_confirm"
                ? "二创定稿已出：先改稿或直接确认，混剪进度在制作步骤中显示。"
                : production?.status === "failed"
                  ? "点失败的混剪步骤查看原因，可重试续跑。"
                  : production?.status === "completed"
                    ? "剪映草稿已生成：混剪步骤可导出视频，发布步骤可复制文案、确认发布。"
                    : runStatus === "failed"
                      ? "点失败的步骤查看原因，在详情中重试并续跑。"
                      : runStatus === "completed"
                        ? "二创跑完了：点确认二创看文案，确认后开始混剪。"
                        : "步骤状态实时更新，点击即可查看该步的输出。"}
            </span>
            <div className="wf-designer__live-actions">
              <button type="button" className="header-button" disabled={runStatus === "queued" || runStatus === "running"}
                onClick={() => setRerunID(liveRun.activeRunID)}>重新生成文案</button>
              {production?.status === "waiting_confirm" ? (
                <>
                  <label className="wf-live-account">
                    混剪账号
                    <select
                      aria-label="混剪账号"
                      value={gateAccountID}
                      onChange={(event) => setGateAccountID(event.target.value)}
                    >
                      {accounts.length === 0 ? <option value="">读取账号…</option> : <option value="">选一个账号</option>}
                      {accounts.map((item) => (
                        <option key={item.id} value={item.id}>
                          {item.name}
                        </option>
                      ))}
                    </select>
                  </label>
                  {gateAccountMismatch ? (
                    <span className="remix-lab-muted wf-live-account__warn">
                      这稿是按「{accounts.find((item) => item.id === production?.account_id)?.name ?? "另一个账号"}」写的，确认要进别的号？
                    </span>
                  ) : null}
                  <button
                    type="button"
                    className="remix-lab-start"
                    disabled={confirming}
                    onClick={() => produceLiveRun()}
                  >
                    {confirming ? "放行中…" : "确认开始混剪"}
                  </button>
                </>
              ) : null}
              {production?.status === "failed" ? (
                <button type="button" className="remix-lab-start" disabled={confirming} onClick={() => produceLiveRun()}>
                  重试生产
                </button>
              ) : null}
              {onEditPackage && (runStatus === "completed" || runStatus === "failed" || production?.status === "waiting_confirm") ? (
                <button
                  type="button"
                  className="header-button"
                  onClick={() => onEditPackage(liveRun.activeRunID, liveRun.experimentID)}
                >
                  改稿
                </button>
              ) : null}
              {production?.project_id ? (
                <button
                  type="button"
                  className="header-button"
                  title="低频操作（上传替换素材、删项目等）仍在旧项目页"
                  onClick={() => window.open(`/projects/${production.project_id}`, "_blank")}
                >
                  打开项目页
                </button>
              ) : null}
              <button
                type="button"
                className="header-button"
                onClick={() => {
                  if (onLeaveRun) onLeaveRun();
                  else setLiveRun(null);
                }}
              >
                返回编辑
              </button>
            </div>
          </div>
          <div className="wf-designer__live-panel">
            <RunFlowPanel
              key={liveRun.activeRunID}
              api={api}
              runID={liveRun.activeRunID}
              live={(runStatus !== "completed" && runStatus !== "failed") || production?.status === "running"}
              onEditAgentPrompts={() => void openPromptEditor()}
              onMessage={onMessage}
              onRetryNode={(nodeID, model) => retryLiveRun(nodeID, model)}
              onStatusChange={setRunStatus}
              onProductionChange={setProduction}
              onRetryProduce={() => produceLiveRun()}
              onConfirmProduce={() => produceLiveRun()}
            />
          </div>
        </div>
      ) : null}

      {modelWorkflow ? <RoleModelsDialog workflow={modelWorkflow} defaults={modelDefaults}
        ownerLabel={accounts.find(account => account.id === (accountIDProp ?? accountID))?.name || "当前工作流"}
        onClose={() => setModelWorkflow(null)} onSave={saveModels}
        onConnections={onOpenConnections ? () => { setModelWorkflow(null); onOpenConnections(); } : undefined} /> : null}
      {promptEditorOpen ? (
        <WorkflowPromptEditor
          api={api}
          accountID={accountIDProp ?? accountID}
          ownerLabel={accounts.find(account => account.id === (accountIDProp ?? accountID))?.name || "当前工作流"}
          onClose={() => setPromptEditorOpen(false)}
          onSaved={(result) => {
            const saved = result.workflow;
            const nextNodes = toRfNodes(saved);
            const nextEdges = toRfEdges(saved);
            const nextProd = normalizeProduction(saved.production);
            nodesRef.current = nextNodes;
            edgesRef.current = nextEdges;
            prodRef.current = nextProd;
            editorialRulesRef.current = saved.editorial_rules;
            setRfNodes(nextNodes);
            setRfEdges(nextEdges);
            setProdCfg(nextProd);
            setEditorialRules(saved.editorial_rules);
            setMeta({ version: saved.version, name: saved.name });
            setSelectedID((current) => saved.nodes.some((node) => node.id === current) ? current : "");
            setDirty(false);
            pendingConfig.current = null;
            historyRef.current = [];
            futureRef.current = [];
            setHistTick((tick) => tick + 1);
            setPromptEditorOpen(false);
            onMessage("二创提示词已保存，后续新跑和重新二创使用新配置。");
          }}
        />
      ) : null}
      {rerunID ? <RerunDialog key={rerunID} api={api} runID={rerunID} onClose={() => setRerunID("")} onCreated={result => {
        setRerunID("");
        setLiveRun({experimentID: result.experiment.id, runs: liveRunTabs(result.experiment), activeRunID: result.run_id});
        setRunStatus("queued"); setProduction(null);
        onRerunCreated?.(result.experiment);
        onMessage("新一轮文案已开始，旧稿保留在当前项目的运行页签中。");
      }} /> : null}
      {!liveRun ? (
      <>
      <div className="wf-designer__body fixed-flow-workspace fixed-flow-workspace--setup">
        {loaded ? <FixedFlowSteps steps={steps} selectedID={selectedID} onSelect={setSelectedID} /> : <p className="remix-lab-muted run-flow-loading">正在读取工作流…</p>}
        {!selectedNode && !selectedProdSpec ? <div className="fixed-flow-empty"><strong>选择一个步骤</strong><span>查看设置、输入内容或准备开始制作。</span></div> : null}
        {selectedNode ? (
          <aside className="run-flow-inspector wf-designer__drawer fixed-flow-details" aria-label={`${selectedNode.title}配置`}>
            <header>
              <h3>{selectedNode.title}</h3>
              <button
                type="button"
                className="header-button remix-lab-icon-btn"
                aria-label="关闭节点配置"
                onClick={() => setSelectedID("")}
              >
                <X size={13} strokeWidth={2} />
              </button>
            </header>
            {selectedNode.type !== "input" ? (
              <p className="run-flow-inspector__meta">
                {NODE_TYPE_META[selectedNode.type]?.label ?? selectedNode.type}
              </p>
            ) : null}

            {selectedNode.type === "agent" || selectedNode.type === "reviewer" ? (
              <>
                <label className="wf-field">
                  步骤名称
                  <input
                    aria-label="节点名"
                    value={selectedNode.title}
                    onChange={(event) =>
                      patchNode(selectedNode.id, (wfNode) => ({ ...wfNode, title: event.target.value }))
                    }
                  />
                </label>
                <label className="wf-field">
                  模型（空=默认模型档）
                  <input
                    aria-label="节点模型"
                    value={selectedNode.config.model ?? ""}
                    onChange={(event) =>
                      patchNode(selectedNode.id, (wfNode) => ({
                        ...wfNode,
                        config: { ...wfNode.config, model: event.target.value },
                      }))
                    }
                  />
                </label>
                <label className="wf-field">
                  思考强度
                  <input
                    aria-label="节点思考强度"
                    value={selectedNode.config.reasoning_effort ?? ""}
                    onChange={(event) =>
                      patchNode(selectedNode.id, (wfNode) => ({
                        ...wfNode,
                        config: { ...wfNode.config, reasoning_effort: event.target.value },
                      }))
                    }
                  />
                </label>
                <FastModeButton
                  label={`${selectedNode.title} Fast 加速模式`}
                  value={selectedNode.config.service_tier}
                  onChange={(service_tier) => patchNode(selectedNode.id, (node) => ({
                    ...node, config: { ...node.config, service_tier },
                  }))}
                />
                {selectedNode.type === "agent" ? (
                  <label className="wf-field">
                    通道
                    <select
                      aria-label="节点通道"
                      value={selectedNode.config.channel ?? ""}
                      onChange={(event) =>
                        patchNode(selectedNode.id, (wfNode) => ({
                          ...wfNode,
                          config: { ...wfNode.config, channel: event.target.value },
                        }))
                      }
                    >
                      <option value="">写手通道（默认）</option>
                      <option value="search">搜索通道（设置页 Grok，联网）</option>
                    </select>
                  </label>
                ) : null}
                <label className="wf-field">
                  系统提示词
                  <textarea
                    aria-label="节点系统提示词"
                    rows={10}
                    value={selectedNode.config.system_prompt ?? ""}
                    onChange={(event) =>
                      patchNode(selectedNode.id, (wfNode) => ({
                        ...wfNode,
                        config: { ...wfNode.config, system_prompt: event.target.value },
                      }))
                    }
                  />
                </label>
                {selectedNode.type === "agent" ? (
                  <>
                    <label className="wf-field">
                      用户消息模板（{"{{source}}"} = 原文，{"{{node:ID}}"} = 上游输出）
                      <textarea
                        aria-label="节点用户模板"
                        rows={4}
                        value={selectedNode.config.user_template ?? ""}
                        onChange={(event) =>
                          patchNode(selectedNode.id, (wfNode) => ({
                            ...wfNode,
                            config: { ...wfNode.config, user_template: event.target.value },
                          }))
                        }
                      />
                    </label>
                    <label className="wf-field">
                      注入写手时的小节标题
                      <input
                        aria-label="注入标题"
                        value={selectedNode.config.inject_title ?? ""}
                        placeholder="空=用节点名"
                        onChange={(event) =>
                          patchNode(selectedNode.id, (wfNode) => ({
                            ...wfNode,
                            config: { ...wfNode.config, inject_title: event.target.value },
                          }))
                        }
                      />
                    </label>
                    <label className="wf-field">
                      注入时的使用纪律（可空）
                      <textarea
                        aria-label="注入纪律"
                        rows={2}
                        value={selectedNode.config.inject_rule ?? ""}
                        onChange={(event) =>
                          patchNode(selectedNode.id, (wfNode) => ({
                            ...wfNode,
                            config: { ...wfNode.config, inject_rule: event.target.value },
                          }))
                        }
                      />
                    </label>
                    <button
                      type="button"
                      className="header-button remix-lab-icon-btn wf-field__danger"
                      onClick={() => removeNode(selectedNode.id)}
                    >
                      <Trash2 size={13} strokeWidth={2} />
                      删除这个节点
                    </button>
                  </>
                ) : null}
              </>
            ) : null}

            {selectedNode.type === "writer" ? (
              <>
                <label className="wf-field">
                  写手提示词（提示词库）
                  <select
                    aria-label="写手提示词"
                    value={selectedNode.config.prompt_id ?? ""}
                    onChange={(event) =>
                      patchNode(selectedNode.id, (wfNode) => ({
                        ...wfNode,
                        config: { ...wfNode.config, prompt_id: event.target.value },
                      }))
                    }
                  >
                    <option value="">跟随日产提示词</option>
                    {prompts.map((prompt) => (
                      <option key={prompt.id} value={prompt.id}>
                        {prompt.name}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="wf-field">
                  模型（空=默认模型档）
                  <input
                    aria-label="写手模型"
                    value={selectedNode.config.model ?? ""}
                    onChange={(event) =>
                      patchNode(selectedNode.id, (wfNode) => ({
                        ...wfNode,
                        config: { ...wfNode.config, model: event.target.value },
                      }))
                    }
                  />
                </label>
                <label className="wf-field">
                  思考强度
                  <select
                    aria-label="写手思考强度"
                    value={selectedNode.config.reasoning_effort ?? ""}
                    onChange={(event) =>
                      patchNode(selectedNode.id, (wfNode) => ({
                        ...wfNode,
                        config: { ...wfNode.config, reasoning_effort: event.target.value },
                      }))
                    }
                  >
                    {EFFORT_OPTIONS.map((option) => (
                      <option key={option || "default"} value={option}>
                        {option || "跟随默认模型档"}
                      </option>
                    ))}
                  </select>
                </label>
                <p className="remix-lab-muted">
                  写手直接阅读原文，成稿交给审稿模型；篇幅与最终取舍由你审查。这里的模型设置只用于写手。
                </p>
                <FastModeButton
                  label="写手 Fast 加速模式"
                  value={selectedNode.config.service_tier}
                  inheritedValue={defaultServiceTier}
                  onChange={(service_tier) => patchNode(selectedNode.id, (node) => ({
                    ...node, config: { ...node.config, service_tier },
                  }))}
                />
              </>
            ) : null}

            {selectedNode.type === "input" ? (
              <>
                <label className="wf-field">
                  对标原文
                  <textarea
                    aria-label="对标原文"
                    rows={12}
                    value={source}
                    onChange={(event) => setSource(event.target.value)}
                    placeholder="粘贴完整对标口播，点开始二创按当前工作流执行"
                  />
                </label>
                <p className="remix-lab-muted">{source.trim() ? `${[...source.trim()].length} 字` : "还没填"}</p>
                <div className="wf-designer__drawer-run">
                  {accountIDProp === undefined ? (
                    <label className="wf-field">
                      生产账号
                      <select
                        aria-label="生产账号"
                        value={accountID}
                        onChange={(event) => pickAccount(event.target.value)}
                      >
                        <option value="">跑完再选</option>
                        {accounts.map((account) => (
                          <option key={account.id} value={account.id}>
                            {account.name}
                          </option>
                        ))}
                      </select>
                    </label>
                  ) : null}
                  <div className="wf-field wf-designer__compare">
                    <span>对比写手模型（勾选多个模型，每个模型各出一稿；审稿沿用已有设置）</span>
                    <ModelMultiSelect
                      value={compareModels}
                      onChange={(next) => {
                        setCompareModels(next);
                        if (next.length > 1) setAutoProduce(false);
                      }}
                      inheritedLabel={writerNodeModel || "写手节点默认"}
                    />
                  </div>
                  <div className="wf-designer__run-controls">
                    <label>
                      运行次数
                      <input
                        aria-label="运行次数"
                        type="number"
                        min={1}
                        max={3}
                        value={runCount}
                        onChange={(event) => {
                          const next = Math.min(3, Math.max(1, Math.trunc(Number(event.target.value) || 1)));
                          setRunCount(next);
                          if (next !== 1) setAutoProduce(false);
                        }}
                      />
                    </label>
                    <label className="wf-designer__auto" title="定稿不等确认，直接建项目→口播→配音→混剪，一步到剪映草稿">
                      <input
                        aria-label="全自动到剪映草稿"
                        type="checkbox"
                        checked={autoProduce}
                        disabled={!accountID || runCount !== 1 || compareModels.length > 1}
                        onChange={(event) => setAutoProduce(event.target.checked)}
                      />
                      全自动到草稿
                    </label>
                    <button
                      type="button"
                      className="remix-lab-start remix-lab-icon-btn"
                      disabled={starting || !source.trim()}
                      onClick={() => void start()}
                    >
                      <Play size={14} strokeWidth={2} />
                      {starting ? "开跑中…" : "开始二创"}
                    </button>
                  </div>
                </div>
              </>
            ) : null}
            {selectedNode.type === "output" ? (
              <>
                <p className="remix-lab-muted">
                  已有成稿可直接贴进来，跳过前面的二创；导入后停在确认闸门，再接下排混剪。
                </p>
                <label className="wf-field">
                  定稿正文
                  <textarea
                    aria-label="定稿正文"
                    rows={12}
                    value={draftScript}
                    onChange={(event) => setDraftScript(event.target.value)}
                    placeholder="把讨论好的二创文案整篇贴进来"
                  />
                </label>
                <p className="remix-lab-muted">
                  {draftScript.trim() ? `${[...draftScript.trim()].length} 字` : "还没填"}
                </p>
                <label className="wf-field">
                  板标题（空则用正文第一句）
                  <input
                    aria-label="板标题"
                    value={draftBoardTitle}
                    onChange={(event) => setDraftBoardTitle(event.target.value)}
                    placeholder="混剪板面主标题"
                  />
                </label>
                <label className="wf-field">
                  副标题
                  <input
                    aria-label="副标题"
                    value={draftSubtitle}
                    onChange={(event) => setDraftSubtitle(event.target.value)}
                  />
                </label>
                <div className="wf-designer__drawer-run">
                  <button
                    type="button"
                    className="remix-lab-start remix-lab-icon-btn"
                    disabled={importing || !draftScript.trim()}
                    onClick={() => void importDraft()}
                  >
                    {importing ? "导入中…" : "作为定稿导入"}
                  </button>
                </div>
              </>
            ) : null}
          </aside>
        ) : null}

        {selectedProdSpec ? (
          <aside className="run-flow-inspector wf-designer__drawer fixed-flow-details" aria-label={`${selectedProdSpec.title}配置`}>
            <header>
              <h3>{selectedProdSpec.title}</h3>
              <button
                type="button"
                className="header-button remix-lab-icon-btn"
                aria-label="关闭节点配置"
                onClick={() => setSelectedID("")}
              >
                <X size={13} strokeWidth={2} />
              </button>
            </header>
            {selectedProdSpec.id === "produce-spoken" || selectedProdSpec.id === "produce-montage" ? (
              <div className="wf-field-grid">
                <label className="wf-field">
                  模型（空=默认）
                  <input
                    aria-label={`${selectedProdSpec.title}模型`}
                    value={
                      (selectedProdSpec.id === "produce-spoken" ? prodCfg.spoken_model : prodCfg.montage_model) ?? ""
                    }
                    placeholder="跟随默认模型档"
                    onChange={(event) =>
                      patchProd(
                        selectedProdSpec.id === "produce-spoken"
                          ? { spoken_model: event.target.value }
                          : { montage_model: event.target.value },
                      )
                    }
                  />
                </label>
                <label className="wf-field">
                  思考强度
                  <select
                    aria-label={`${selectedProdSpec.title}思考强度`}
                    value={
                      (selectedProdSpec.id === "produce-spoken" ? prodCfg.spoken_effort : prodCfg.montage_effort) ?? ""
                    }
                    onChange={(event) =>
                      patchProd(
                        selectedProdSpec.id === "produce-spoken"
                          ? { spoken_effort: event.target.value }
                          : { montage_effort: event.target.value },
                      )
                    }
                  >
                    {EFFORT_OPTIONS.map((option) => (
                      <option key={option || "default"} value={option}>
                        {option || "跟随默认"}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
            ) : null}
            {selectedProdSpec.id === "produce-narration" ? (
              <>
                <label className="wf-field">
                  克隆音色 ID（空=账号/全局）
                  <input
                    aria-label="配音音色"
                    value={prodCfg.narration_voice_id ?? ""}
                    placeholder="moss_audio_…"
                    onChange={(event) => patchProd({ narration_voice_id: event.target.value })}
                  />
                </label>
                <div className="wf-field-grid">
                  <label className="wf-field">
                    配音模型
                    <input
                      aria-label="配音模型"
                      value={prodCfg.narration_model ?? ""}
                      placeholder="跟随设置"
                      onChange={(event) => patchProd({ narration_model: event.target.value })}
                    />
                  </label>
                  <label className="wf-field">
                    情绪
                    <input
                      aria-label="配音情绪"
                      value={prodCfg.narration_emotion ?? ""}
                      placeholder="跟随设置"
                      onChange={(event) => patchProd({ narration_emotion: event.target.value })}
                    />
                  </label>
                </div>
                <div className="wf-field-grid wf-field-grid--3">
                  <label className="wf-field">
                    语速
                    <input
                      aria-label="配音语速"
                      type="number"
                      min={0.5}
                      max={2}
                      step={0.01}
                      value={optionalNumberValue(prodCfg.narration_speed)}
                      placeholder="跟随设置"
                      onChange={(event) =>
                        patchProd({
                          narration_speed: event.target.value === "" ? undefined : Number(event.target.value),
                        })
                      }
                    />
                  </label>
                  <label className="wf-field">
                    音量
                    <input
                      aria-label="配音音量"
                      type="number"
                      min={0}
                      max={10}
                      step={0.01}
                      value={optionalNumberValue(prodCfg.narration_volume)}
                      placeholder="跟随设置"
                      onChange={(event) =>
                        patchProd({
                          narration_volume: event.target.value === "" ? undefined : Number(event.target.value),
                        })
                      }
                    />
                  </label>
                  <label className="wf-field">
                    音调
                    <input
                      aria-label="配音音调"
                      type="number"
                      min={-12}
                      max={12}
                      step={1}
                      value={optionalNumberValue(prodCfg.narration_pitch)}
                      placeholder="跟随设置"
                      onChange={(event) =>
                        patchProd({
                          narration_pitch: event.target.value === "" ? undefined : Math.trunc(Number(event.target.value)),
                        })
                      }
                    />
                  </label>
                </div>
              </>
            ) : null}
            {selectedProdSpec.promptKey ? (
              <label className="wf-field">
                任务提示词（空=用默认）
                <textarea
                  aria-label={`${selectedProdSpec.title}任务提示词`}
                  rows={5}
                  value={prodCfg[selectedProdSpec.promptKey] ?? ""}
                  placeholder={PRODUCTION_DEFAULT_PROMPTS[selectedProdSpec.id]}
                  onChange={(event) =>
                    patchProd({ [selectedProdSpec.promptKey as string]: event.target.value })
                  }
                />
              </label>
            ) : null}
            {selectedProdSpec.id === "produce-captions" ? (
              <button
                type="button"
                className="header-button remix-lab-icon-btn wf-field__danger"
                onClick={() => {
                  patchProd({ captions_disabled: true });
                  setSelectedID("");
                  onMessage("字幕关键词节点已删除：生产时跳过这一步，混剪回落本地词表。");
                }}
              >
                <Trash2 size={13} strokeWidth={2} />
                删除这个节点
              </button>
            ) : null}
            {selectedProdSpec.id === "produce-gate" ? (
              <p className="remix-lab-muted">二创定稿出来后，在这一步确认文案并开始混剪。对标原文勾选「全自动到草稿」时会跳过这步。</p>
            ) : null}
            {selectedProdSpec.id === "produce-project" ? (
              <p className="remix-lab-muted">确认后自动在所选账号下建项目并写入定稿，进度和结果都在当前页面查看。</p>
            ) : null}
            {selectedProdSpec.id === "produce-publish" ? (
              <p className="remix-lab-muted">剪映草稿出来后，在这一步复制视频描述和短标题，视频发出后点确认已发布。</p>
            ) : null}
          </aside>
        ) : null}
      </div>
      </>
      ) : null}
    </div>
  );
}
