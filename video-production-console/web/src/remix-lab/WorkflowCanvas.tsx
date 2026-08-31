import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  addEdge,
  Background,
  Controls,
  Handle,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  useReactFlow,
  type Connection,
  type Edge,
  type Node,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
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
  produceRemixLabRun,
  retryRemixLabRun,
  saveRemixLabWorkflow,
  type RemixLabApi,
  type RemixLabExperiment,
  type RemixLabProduction,
  type RemixLabPrompt,
  type RemixLabWorkflow,
  type RemixLabWorkflowNode,
  type RemixLabWorkflowProduction,
} from "./api";
import { RunFlowPanel } from "./RunFlow";

// 设计态画布：工作流是可编辑的资产——拖节点、连线、点节点在抽屉里改
// 模型和提示词，保存后下次开跑生效。骨干链（原文→写手→自检→审稿→定稿）
// 固定，agent 情报节点可以随意增删和改连线。

type WorkflowCanvasProps = {
  api: RemixLabApi;
  prompts: RemixLabPrompt[];
  onMessage: (text: string) => void;
  onEditAgentPrompts: () => void;
  runWorkflow: (source: string, runCount: number, accountID: string, auto: boolean) => Promise<RemixLabExperiment>;
  /** 工作流被外部改动（智能体提案确认）后父级递增，画布重新拉取。 */
  refreshToken: number;
  /** 当前账号：读写该账号最新一版工作流；空=全局默认。 */
  accountID?: string;
  onAccountIDChange?: (accountID: string) => void;
  /** 历史项目/实验详情：把已有运行摊回画布，继续确认和混剪。 */
  resumeExperiment?: RemixLabExperiment | null;
  onEditPackage?: (runID: string, experimentID: string) => void;
  onLeaveRun?: () => void;
};

type AccountOption = { id: string; name: string; status: string };

type LiveRunState = {
  experimentID: string;
  runs: Array<{ id: string; run_index: number }>;
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
  agent: { label: "AGENT", Icon: ScanSearch },
  writer: { label: "写手", Icon: PenLine },
  selfcheck: { label: "闸门", Icon: Gauge },
  reviewer: { label: "审稿", Icon: Stamp },
  output: { label: "产出", Icon: BookOpenText },
};

// 生产段（定稿之后）在设计画布上的节点定义：顺序固定，字幕关键词可删，
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
    default:
      return Clapperboard;
  }
}

type CanvasNodeData = { wfNode: RemixLabWorkflowNode };

type ProdNodeData = { spec: ProdNodeSpec; customPrompt: boolean };

function ProdDesignNode({ data, selected }: NodeProps & { data: ProdNodeData }) {
  const Icon = prodIcon(data.spec.id);
  return (
    <div
      className={[
        "run-flow-node",
        "run-flow-node--design",
        "run-flow-node--produce",
        selected ? "run-flow-node--selected" : "",
      ].join(" ")}
    >
      <Handle type="target" position={Position.Left} className="run-flow-handle" />
      <Handle type="target" position={Position.Bottom} id="from-final" className="run-flow-handle" />
      <div className="run-flow-node__head">
        <span className="run-flow-node__icon" aria-hidden="true">
          <Icon size={15} strokeWidth={2} />
        </span>
        <div className="run-flow-node__titles">
          <small>{data.spec.kindLabel}</small>
          <strong>{data.spec.title}</strong>
        </div>
      </div>
      <div className="run-flow-node__meta">
        {data.spec.promptKey ? <span>{data.customPrompt ? "自定义提示词" : "默认提示词"}</span> : null}
      </div>
      <Handle type="source" position={Position.Right} className="run-flow-handle" />
    </div>
  );
}

function DesignNode({ data, selected }: NodeProps & { data: CanvasNodeData }) {
  const wfNode = data.wfNode;
  const meta = NODE_TYPE_META[wfNode.type] ?? NODE_TYPE_META.agent;
  return (
    <div
      className={[
        "run-flow-node",
        "run-flow-node--design",
        selected ? "run-flow-node--selected" : "",
      ].join(" ")}
    >
      <Handle type="target" position={Position.Left} className="run-flow-handle" />
      <div className="run-flow-node__head">
        <span className="run-flow-node__icon" aria-hidden="true">
          <meta.Icon size={15} strokeWidth={2} />
        </span>
        <div className="run-flow-node__titles">
          <small>{meta.label}</small>
          <strong>{wfNode.title}</strong>
        </div>
      </div>
      <div className="run-flow-node__meta">
        {wfNode.config.model ? <span>{wfNode.config.model}</span> : null}
        {wfNode.config.channel === "search" ? <span>搜索通道</span> : null}
        {wfNode.type === "writer" ? <span>{wfNode.config.prompt_id ? `提示词 ${wfNode.config.prompt_id}` : "跟随日产提示词"}</span> : null}
        {wfNode.type === "agent" && wfNode.config.system_prompt ? (
          <span>{[...wfNode.config.system_prompt].length} 字提示词</span>
        ) : null}
        {wfNode.type === "output" ? <span>定稿后接下排混剪</span> : null}
      </div>
      <Handle type="source" position={Position.Right} className="run-flow-handle" />
      {wfNode.type === "output" ? (
        <Handle type="source" position={Position.Top} id="to-produce" className="run-flow-handle" />
      ) : null}
    </div>
  );
}

const nodeTypes = { wfNode: DesignNode, prodNode: ProdDesignNode };

function FitViewAfterLayout({ token }: { token: string }) {
  const { fitView } = useReactFlow();
  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    const timer = window.setTimeout(() => {
      if (!cancelled) void fitView({ padding: 0.22, duration: 0 });
    }, 80);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [fitView, token]);
  return null;
}

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

export function WorkflowCanvas({ api, prompts, onMessage, onEditAgentPrompts, runWorkflow, refreshToken, accountID: accountIDProp, onAccountIDChange, resumeExperiment, onEditPackage, onLeaveRun }: WorkflowCanvasProps) {
  const [meta, setMeta] = useState<{ version: number; name: string }>({ version: 1, name: "默认二创工作流" });
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState<Node>([]);
  const [rfEdges, setRfEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [loaded, setLoaded] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [prodCfg, setProdCfg] = useState<RemixLabWorkflowProduction>({});
  const [selectedID, setSelectedID] = useState("");
  const [source, setSource] = useState("");
  const [runCount, setRunCount] = useState(1);
  const [starting, setStarting] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const [liveRun, setLiveRun] = useState<LiveRunState | null>(null);
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
  const historyRef = useRef<CanvasSnap[]>([]);
  const futureRef = useRef<CanvasSnap[]>([]);
  const lastHistAt = useRef(0);
  const draggingHist = useRef(false);
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
        // 账号列表拉不到不拦画布：开跑时再提示
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
    const runs = resumeExperiment.runs.map((run) => ({ id: run.id, run_index: run.run_index }));
    const keep = liveRun?.experimentID === resumeExperiment.id && runs.some((run) => run.id === liveRun.activeRunID)
      ? liveRun.activeRunID
      : runs[0].id;
    const active = resumeExperiment.runs.find((run) => run.id === keep) ?? resumeExperiment.runs[0];
    setLiveRun({ experimentID: resumeExperiment.id, runs, activeRunID: keep });
    setSource(resumeExperiment.source_text);
    setRunStatus(active.status);
    if (active.production) setProduction(active.production);
    // liveRun 只用来保住当前选中的 run，不放进依赖以免轮询把选中冲掉。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resumeExperiment]);

  useEffect(() => {
    if (production?.status !== "waiting_confirm" || accountID || accounts.length === 0) return;
    const next = accounts[0].id;
    setAccountID(next);
    try {
      window.localStorage.setItem("remix-lab:produce-account", next);
    } catch {
      // 忽略存储不可用
    }
    onAccountIDChange?.(next);
  }, [production?.status, accountID, accounts, onAccountIDChange]);

  nodesRef.current = rfNodes;
  edgesRef.current = rfEdges;
  prodRef.current = prodCfg;

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

  const selectedNode = useMemo(() => {
    const found = rfNodes.find((node) => node.id === selectedID);
    return found ? (found.data as CanvasNodeData).wfNode : null;
  }, [rfNodes, selectedID]);

  // 生产段伪节点：单独排在二创图上方，避免接在定稿右侧把整图拉出视口。
  // 顺序固定、不可拖拽、不入库为 workflow.nodes（保存时只落 production 配置）。
  const prodSpecs = useMemo(
    () => productionNodeSpecs(Boolean(prodCfg.captions_disabled)),
    [prodCfg.captions_disabled],
  );
  const prodNodes: Node[] = useMemo(() => {
    const design = rfNodes.filter((node) => (node.data as CanvasNodeData).wfNode?.type);
    const final = design.find((node) => (node.data as CanvasNodeData).wfNode?.type === "output");
    if (!final || design.length === 0) return [];
    const minX = Math.min(...design.map((node) => node.position.x));
    const maxY = Math.max(...design.map((node) => node.position.y));
    return prodSpecs.map((spec, index) => ({
      id: spec.id,
      type: "prodNode",
      position: { x: minX + 230 * index, y: maxY + 240 },
      data: {
        spec,
        customPrompt: Boolean(spec.promptKey && String(prodCfg[spec.promptKey] ?? "").trim()),
      },
      draggable: false,
      connectable: false,
      selected: selectedID === spec.id,
    }));
  }, [rfNodes, prodSpecs, prodCfg, selectedID]);
  const prodEdges: Edge[] = useMemo(() => {
    const final = rfNodes.find((node) => (node.data as CanvasNodeData).wfNode?.type === "output");
    if (!final || prodSpecs.length === 0) return [];
    const edges: Edge[] = [
      {
        id: "produce-edge-from-final",
        source: final.id,
        sourceHandle: "to-produce",
        target: prodSpecs[0].id,
        targetHandle: "from-final",
        style: { strokeWidth: 1.6, strokeDasharray: "6 4" },
      },
    ];
    for (let i = 0; i + 1 < prodSpecs.length; i++) {
      edges.push({
        id: `produce-edge-${i}`,
        source: prodSpecs[i].id,
        target: prodSpecs[i + 1].id,
        style: { strokeWidth: 1.6, strokeDasharray: "6 4" },
      });
    }
    return edges;
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

  const onConnect = useCallback(
    (connection: Connection) => {
      if (!connection.source || !connection.target || connection.source === connection.target) return;
      if (connection.source.startsWith("produce-") || connection.target.startsWith("produce-")) {
        onMessage("生产链（确认闸门→建项目→口播→配音→混剪）顺序固定，不能改连线；字幕关键词节点可在抽屉里删除。");
        return;
      }
      const sourceNode = rfNodes.find((node) => node.id === connection.source);
      const targetNode = rfNodes.find((node) => node.id === connection.target);
      if (!sourceNode || !targetNode) return;
      const sourceType = (sourceNode.data as CanvasNodeData).wfNode.type;
      const targetType = (targetNode.data as CanvasNodeData).wfNode.type;
      const allowed =
        (sourceType === "input" || sourceType === "agent") &&
        (targetType === "agent" || targetType === "writer");
      if (!allowed) {
        onMessage("只能把原文/agent 连到 agent 或写手；骨干链（写手→自检→审稿→定稿）是固定的。");
        return;
      }
      beginHistory(true);
      setRfEdges((current) => addEdge({ ...connection, id: `${connection.source}->${connection.target}`, style: { strokeWidth: 1.6 } }, current));
      setDirty(true);
    },
    [beginHistory, rfNodes, setRfEdges, onMessage],
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

  const start = async () => {
    if (starting) return;
    if (!source.trim()) {
      onMessage("先把对标原文贴进原文节点。");
      return;
    }
    if (dirty) {
      const ok = await save();
      if (!ok) return;
    }
    setStarting(true);
    try {
      const created = await runWorkflow(source.trim(), runCount, accountID, autoProduce);
      // 不跳页：画布原地切到运行视图，节点逐个亮起。
      setLiveRun({
        experimentID: created.id,
        runs: created.runs.map((run) => ({ id: run.id, run_index: run.run_index })),
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

  useEffect(() => {
    if (!dirty || !loaded || liveRun) return;
    const timer = window.setTimeout(() => {
      void saveRef.current();
    }, 500);
    return () => window.clearTimeout(timer);
  }, [dirty, rfNodes, rfEdges, prodCfg, loaded, liveRun]);

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
    const chosen = accountID || production?.account_id || "";
    if (!chosen) {
      onMessage("先在底部选好要进哪个账号的混剪。");
      return;
    }
    setConfirming(true);
    try {
      await produceRemixLabRun(api, liveRun.activeRunID, chosen);
      setProduction((current) => (current ? { ...current, status: "running" } : current));
      onMessage("已放行：建项目 → 口播稿 → 配音 → 混剪，全程在图上看进度。");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "开始混剪失败。");
    } finally {
      setConfirming(false);
    }
  };

  // 断点重试：agent 节点传ID（作废重跑），其余整体重试（缓存复用）。
  const retryLiveRun = async (nodeID: string) => {
    if (!liveRun) return;
    try {
      await retryRemixLabRun(api, liveRun.activeRunID, nodeID || undefined);
      setRunStatus("running");
      onMessage(nodeID ? "已重试该节点，成功后自动续跑后面的环节。" : "已从断点重试，跑过的agent节点直接复用产物。");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "重试提交失败。");
    }
  };

  return (
    <ReactFlowProvider>
    <div className={fullscreen ? "wf-designer wf-designer--fullscreen" : "wf-designer"}>
      <div className="wf-designer__toolbar">
        <div className="wf-designer__title">
          <strong>{meta.name}</strong>
        </div>
        <div className="wf-designer__actions">
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
                    运行 {run.run_index}
                  </button>
                ))
              : null}
            <span className="remix-lab-muted">
              {production?.status === "waiting_confirm"
                ? "二创定稿已出：先改稿或直接确认，混剪在下排节点推进。"
                : production?.status === "failed"
                  ? "点红色混剪节点看原因，可重试续跑。"
                  : production?.status === "completed"
                    ? "剪映草稿已生成，点混剪节点查看产物。"
                    : runStatus === "failed"
                      ? "点红色节点看原因，检视器里可以重试并续跑。"
                      : runStatus === "completed"
                        ? "二创跑完了：点确认二创看文案，确认后开始混剪。"
                        : "节点跑完一个亮一个，点节点看它的实时输出。"}
            </span>
            <div className="wf-designer__live-actions">
              {production?.status === "waiting_confirm" ? (
                <>
                  <label className="wf-live-account">
                    混剪账号
                    <select
                      aria-label="混剪账号"
                      value={accountID}
                      onChange={(event) => pickAccount(event.target.value)}
                    >
                      {accounts.length === 0 ? <option value="">读取账号…</option> : null}
                      {accounts.map((item) => (
                        <option key={item.id} value={item.id}>
                          {item.name}
                        </option>
                      ))}
                    </select>
                  </label>
                  <button
                    type="button"
                    className="remix-lab-start"
                    disabled={confirming}
                    onClick={() => void produceLiveRun()}
                  >
                    {confirming ? "放行中…" : "确认开始混剪"}
                  </button>
                </>
              ) : null}
              {production?.status === "failed" ? (
                <button type="button" className="remix-lab-start" disabled={confirming} onClick={() => void produceLiveRun()}>
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
              onEditAgentPrompts={onEditAgentPrompts}
              onMessage={onMessage}
              onRetryNode={(nodeID) => void retryLiveRun(nodeID)}
              onStatusChange={setRunStatus}
              onProductionChange={setProduction}
              onRetryProduce={() => void produceLiveRun()}
              onConfirmProduce={() => void produceLiveRun()}
            />
          </div>
        </div>
      ) : null}

      {!liveRun ? (
      <>
      <div className="wf-designer__body">
        <div className="wf-designer__canvas">
          {loaded ? (
            <ReactFlow
              nodes={[...rfNodes, ...prodNodes]}
              edges={[...rfEdges, ...prodEdges]}
              nodeTypes={nodeTypes}
              onNodesChange={(changes) => {
                const designChanges = changes.filter(
                  (change) => !("id" in change && String(change.id).startsWith("produce-")),
                );
                if (designChanges.length === 0) return;
                if (
                  !draggingHist.current &&
                  designChanges.some((change) => change.type === "position" && change.dragging === true)
                ) {
                  beginHistory(true);
                  draggingHist.current = true;
                }
                onNodesChange(designChanges);
                if (designChanges.some((change) => change.type === "position" && change.dragging === false)) {
                  draggingHist.current = false;
                  setDirty(true);
                }
              }}
              onEdgesChange={(changes) => {
                if (
                  changes.some(
                    (change) => change.type === "remove" && !change.id.startsWith("produce-edge-"),
                  )
                ) {
                  beginHistory(true);
                  setDirty(true);
                }
                onEdgesChange(changes);
              }}
              onConnect={onConnect}
              onNodeClick={(_, node) => setSelectedID(node.id)}
              onPaneClick={() => setSelectedID("")}
              fitView
              fitViewOptions={{ padding: 0.22 }}
              minZoom={0.25}
              onlyRenderVisibleElements={false}
              maxZoom={1.6}
              deleteKeyCode={["Delete", "Backspace"]}
              onBeforeDelete={async ({ nodes, edges }) => {
                // 键盘删除只允许删设计边；节点走抽屉里的删除按钮（防误删骨干），
                // 生产链的虚线边不可删。
                const keptEdges = edges.filter((edge) => !edge.id.startsWith("produce-edge-"));
                if (nodes.length > 0) return { nodes: [], edges: keptEdges };
                return { nodes, edges: keptEdges };
              }}
            >
              <Background gap={22} size={1.4} />
              <Controls showInteractive={false} />
              <FitViewAfterLayout
                token={loaded ? `${rfNodes.map((node) => node.id).join(",")}|${prodNodes.map((node) => node.id).join(",")}` : ""}
              />
            </ReactFlow>
          ) : (
            <p className="remix-lab-muted run-flow-loading">正在读取工作流…</p>
          )}
        </div>

        {selectedNode ? (
          <aside className="run-flow-inspector wf-designer__drawer" aria-label={`${selectedNode.title}配置`}>
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
                  节点名
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
                <p className="remix-lab-muted">
                  连进写手的 agent 输出会按图拼成【情报包】注入上下文；机械自检（10字连抄+篇幅）内置在写手环节。
                </p>
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
                        disabled={!accountID || runCount !== 1}
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
            {selectedNode.type === "selfcheck" ? (
              <>
                <p className="remix-lab-muted">
                  出稿闸门：与原文连续10字重合、篇幅比例超标时自动回炉写手返工；返工用尽仍超硬性线则判失败。留空（0）用默认值。
                </p>
                <label className="wf-field">
                  连抄触发上限 %（默认20，超过就返工）
                  <input
                    aria-label="连抄触发上限"
                    type="number"
                    min={0}
                    max={60}
                    value={selectedNode.config.overlap_max_pct ?? 0}
                    onChange={(event) =>
                      patchNode(selectedNode.id, (wfNode) => ({
                        ...wfNode,
                        config: { ...wfNode.config, overlap_max_pct: Math.max(0, Math.trunc(Number(event.target.value) || 0)) },
                      }))
                    }
                  />
                </label>
                <label className="wf-field">
                  连抄硬上限 %（默认30，返工后仍超直接判失败）
                  <input
                    aria-label="连抄硬上限"
                    type="number"
                    min={0}
                    max={80}
                    value={selectedNode.config.overlap_hard_pct ?? 0}
                    onChange={(event) =>
                      patchNode(selectedNode.id, (wfNode) => ({
                        ...wfNode,
                        config: { ...wfNode.config, overlap_hard_pct: Math.max(0, Math.trunc(Number(event.target.value) || 0)) },
                      }))
                    }
                  />
                </label>
                <label className="wf-field">
                  篇幅下限倍数（默认0.8，短于原文这个比例就补写）
                  <input
                    aria-label="篇幅下限"
                    type="number"
                    step={0.05}
                    min={0}
                    max={1.5}
                    value={selectedNode.config.len_min_ratio ?? 0}
                    onChange={(event) =>
                      patchNode(selectedNode.id, (wfNode) => ({
                        ...wfNode,
                        config: { ...wfNode.config, len_min_ratio: Math.max(0, Number(event.target.value) || 0) },
                      }))
                    }
                  />
                </label>
                <label className="wf-field">
                  篇幅硬下限倍数（默认0.65，返工后仍短判失败）
                  <input
                    aria-label="篇幅硬下限"
                    type="number"
                    step={0.05}
                    min={0}
                    max={1.5}
                    value={selectedNode.config.len_hard_ratio ?? 0}
                    onChange={(event) =>
                      patchNode(selectedNode.id, (wfNode) => ({
                        ...wfNode,
                        config: { ...wfNode.config, len_hard_ratio: Math.max(0, Number(event.target.value) || 0) },
                      }))
                    }
                  />
                </label>
                <label className="wf-field">
                  最多返工轮数（默认2，可填1-4）
                  <input
                    aria-label="返工轮数"
                    type="number"
                    min={0}
                    max={4}
                    value={selectedNode.config.max_rounds ?? 0}
                    onChange={(event) =>
                      patchNode(selectedNode.id, (wfNode) => ({
                        ...wfNode,
                        config: { ...wfNode.config, max_rounds: Math.max(0, Math.trunc(Number(event.target.value) || 0)) },
                      }))
                    }
                  />
                </label>
                <p className="remix-lab-muted">连抄的度量口径（连续10个内容字与原文一致计一处）固定不可改。</p>
              </>
            ) : null}
            {selectedNode.type === "output" ? (
              <p className="remix-lab-muted">定稿后接下排混剪：确认二创 → 建项目 → 口播 → 配音 → 剪映草稿。</p>
            ) : null}
          </aside>
        ) : null}

        {selectedProdSpec ? (
          <aside className="run-flow-inspector wf-designer__drawer" aria-label={`${selectedProdSpec.title}配置`}>
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
              <p className="remix-lab-muted">二创定稿出来后，在这个节点确认文案并开始混剪。对标原文勾选「全自动到草稿」时会跳过这步。</p>
            ) : null}
            {selectedProdSpec.id === "produce-project" ? (
              <p className="remix-lab-muted">确认后自动在所选账号下建项目并写入定稿，不用离开这张图。</p>
            ) : null}
          </aside>
        ) : null}
      </div>
      </>
      ) : null}
    </div>
    </ReactFlowProvider>
  );
}
