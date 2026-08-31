import { useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import {
  Check,
  LogOut,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
  PenLine,
  Play,
  Plus,
  RotateCcw,
  Send,
  Settings,
  Sparkles,
  Star,
  Trash2,
  Workflow,
  X,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import {
  chatRemixLabAgent,
  clearRemixLabAgentHistory,
  confirmRemixLabAgentProposal,
  deleteRemixLabPrompt,
  fetchRemixLabActivePrompt,
  fetchRemixLabAgentHistory,
  fetchRemixLabAgentLast,
  fetchRemixLabAgentPrompts,
  fetchRemixLabAgentSettings,
  fetchRemixLabDefaults,
  fetchRemixLabExperiment,
  fetchRemixLabProductionByProject,
  fetchRemixLabPrompts,
  produceRemixLabRun,
  retryRemixLabRun,
  runRemixLabWorkflow,
  saveRemixLabAgentPrompts,
  saveRemixLabAgentSettings,
  saveRemixLabPresets,
  setRemixLabActivePrompt,
  upsertRemixLabPrompt,
  type RemixLabAgentHistoryTurn,
  type RemixLabAgentLast,
  type RemixLabAgentPrompts,
  type RemixLabAgentPromptsView,
  type RemixLabAgentProposal,
  type RemixLabAgentSettings,
  type RemixLabApi,
  type RemixLabCreateSlot,
  type RemixLabDefaults,
  type RemixLabExperiment,
  type RemixLabPrompt,
  type RemixLabRunView,
} from "./api";
import { RunWorkbench } from "./RunWorkbench";
import { RunFlow } from "./RunFlow";
import { WorkflowCanvas } from "./WorkflowCanvas";
import { AccountOverridesDialog } from "../accounts/AccountOverridesDialog";
import { stageLabel } from "../projects/stages";
import type { Account, MontageStyle, Project, Theme } from "../types";
import "./remix-lab.css";

type RemixLabPageProps = {
  api: RemixLabApi;
  experimentID?: string;
  onNavigate: (href: string) => void;
  onOpenSettings?: () => void;
  onLogout?: () => void;
  theme?: Theme;
  onThemeChange?: (theme: Theme) => void;
  selectedProjectID?: string;
  projectView?: ReactNode;
  globalMontageStyle?: MontageStyle;
};

type DraftSlot = {
  base_url: string;
  model: string;
  api_key: string;
  reasoning_effort: string;
  /** ""=单模型写手；"multi_agent"=三路情报agent+写手。 */
  pipeline: string;
  run_count: number;
  preset_index: number | null;
  keyConfigured: boolean;
};

type AgentTurn = {
  role: "user" | "assistant";
  text: string;
  proposals: RemixLabAgentProposal[];
};

const PROPOSAL_KINDS: Record<string, { label: string; Icon: LucideIcon }> = {
  upsert_prompt: { label: "改提示词", Icon: PenLine },
  delete_prompt: { label: "删提示词", Icon: Trash2 },
  start_experiment: { label: "开跑试验", Icon: Play },
  set_active_prompt: { label: "设为日产", Icon: Star },
  update_workflow: { label: "改工作流", Icon: Workflow },
};

const AGENT_SUGGESTIONS = ["按最新批注把提示词改一版", "给工作流加一个标题专家节点", "对照历史实验总结经验"];

const MAX_SLOTS = 4;
const DEFAULT_PROMPT_ID = "elder_stable";

const AGENT_PROMPT_FIELDS: Array<{
  key: keyof RemixLabAgentPrompts;
  label: string;
  hint: string;
}> = [
  { key: "hook_system", label: "钩子分析", hint: "情报组第1路：拆原文开头钩子和留人机制，产出复刻要点。" },
  { key: "facts_search_system", label: "事实核查（联网）", hint: "情报组第2路：核对原文数字，联网补带来源的新数据。" },
  { key: "facts_offline_system", label: "事实核查（离线备用）", hint: "没配搜索通道时的降级版：只盘点原文事实，不补新数据。" },
  { key: "ammo_system", label: "弹药库", hint: "情报组第3路：意象禁用清单、新意象候选、原创现场、换讲法。" },
  { key: "reviewer_system", label: "审稿终审", hint: "每篇成稿的规范终审，打回重做也用它。改这里就是改审稿标准。" },
];
function turnsFromAgentHistory(turns: RemixLabAgentHistoryTurn[]): AgentTurn[] {
  return turns
    .filter((turn) => turn.text.trim())
    .map((turn) => ({
      role: turn.role === "assistant" ? ("assistant" as const) : ("user" as const),
      text: turn.text,
      proposals: turn.proposals ?? [],
    }));
}

function isDroppedAgentConnection(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error);
  return /failed to fetch|network|aborted|timeout|load failed|disconnected/i.test(message);
}

function readCollapsed(key: string): boolean {
  try {
    return window.localStorage.getItem(key) === "1";
  } catch {
    return false;
  }
}

function writeCollapsed(key: string, value: boolean) {
  try {
    window.localStorage.setItem(key, value ? "1" : "0");
  } catch {
    // 忽略隐私模式等存储不可用的情况
  }
}

function clampRunCount(value: number): number {
  if (!Number.isFinite(value)) return 1;
  return Math.min(3, Math.max(1, Math.trunc(value)));
}

function slotsFromDefaults(defaults: RemixLabDefaults): DraftSlot[] {
  // 有预设时预设就是完整草稿（「完成」或开跑时存的），原样带回；
  // 没有预设才用全局二创默认值起一个槽。
  if (defaults.presets.length > 0) {
    return defaults.presets.slice(0, MAX_SLOTS).map((preset) => ({
      base_url: preset.base_url,
      model: preset.model,
      api_key: "",
      reasoning_effort: preset.reasoning_effort,
      pipeline: preset.pipeline || "",
      run_count: clampRunCount(preset.run_count || 1),
      preset_index: preset.preset_index,
      keyConfigured: preset.api_key_configured || defaults.remix_api_key_configured,
    }));
  }
  const slots: DraftSlot[] = [
    {
      base_url: defaults.remix_base_url,
      model: defaults.remix_model,
      api_key: "",
      reasoning_effort: defaults.remix_reasoning_effort,
      pipeline: "",
      run_count: 1,
      preset_index: null,
      keyConfigured: defaults.remix_api_key_configured,
    },
  ];
  return slots;
}

function formatHistoryTime(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function formatElapsed(seconds: number): string {
  const mins = Math.floor(Math.max(0, seconds) / 60);
  const secs = Math.max(0, seconds) % 60;
  return `${mins}:${String(secs).padStart(2, "0")}`;
}

function toCreateSlots(slots: DraftSlot[]): RemixLabCreateSlot[] {
  return slots.map((slot) => {
    const body: RemixLabCreateSlot = {
      base_url: slot.base_url,
      model: slot.model,
      api_key: slot.api_key,
      reasoning_effort: slot.reasoning_effort,
      run_count: clampRunCount(slot.run_count),
    };
    if (slot.pipeline) body.pipeline = slot.pipeline;
    if (slot.preset_index != null) body.preset_index = slot.preset_index;
    return body;
  });
}

export function RemixLabPage({
  api,
  experimentID,
  onNavigate,
  onOpenSettings,
  onLogout,
  theme,
  onThemeChange,
  selectedProjectID,
  projectView,
  globalMontageStyle,
}: RemixLabPageProps) {
  const [defaults, setDefaults] = useState<RemixLabDefaults | null>(null);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  const [accountID, setAccountID] = useState(() => {
    try {
      return window.localStorage.getItem("remix-lab:produce-account") ?? "";
    } catch {
      return "";
    }
  });
  const [accountFormOpen, setAccountFormOpen] = useState(false);
  const [newAccountName, setNewAccountName] = useState("");
  const [accountBackground, setAccountBackground] = useState<File | null>(null);
  const [configuringAccount, setConfiguringAccount] = useState<Account | null>(null);
  const [slots, setSlots] = useState<DraftSlot[]>([]);
  const [experiment, setExperiment] = useState<RemixLabExperiment | null>(null);
  const [message, setMessage] = useState("");
  const [detailRefresh, setDetailRefresh] = useState(0);
  const [prompts, setPrompts] = useState<RemixLabPrompt[]>([]);
  const [activePromptID, setActivePromptID] = useState(DEFAULT_PROMPT_ID);
  const [libraryOpen, setLibraryOpen] = useState(false);
  const [workflowRefresh, setWorkflowRefresh] = useState(0);
  const [historyCollapsed, setHistoryCollapsed] = useState(() => readCollapsed("remix-lab:history-collapsed"));
  const [agentCollapsed, setAgentCollapsed] = useState(() => readCollapsed("remix-lab:agent-collapsed"));
  const [editingPrompt, setEditingPrompt] = useState<Partial<RemixLabPrompt> | null>(null);
  const [slotConfigOpen, setSlotConfigOpen] = useState(false);
  const [savingSlots, setSavingSlots] = useState(false);
  const [agentPromptsView, setAgentPromptsView] = useState<RemixLabAgentPromptsView | null>(null);
  const [agentPromptsDraft, setAgentPromptsDraft] = useState<RemixLabAgentPrompts | null>(null);
  const [savingAgentPrompts, setSavingAgentPrompts] = useState(false);
  const [libraryTab, setLibraryTab] = useState<"writer" | "pipeline">("writer");
  const [packageRun, setPackageRun] = useState<RemixLabRunView | null>(null);
  const [flowRun, setFlowRun] = useState<{ id: string; label: string } | null>(null);
  const [agentSettings, setAgentSettings] = useState<RemixLabAgentSettings>({
    model: "",
    base_url: "",
    reasoning_effort: "",
    api_key_configured: false,
  });
  const [agentKey, setAgentKey] = useState("");
  const [agentMessage, setAgentMessage] = useState("");
  const [agentTurns, setAgentTurns] = useState<AgentTurn[]>([]);
  const [agentBusy, setAgentBusy] = useState(false);
  const [agentElapsed, setAgentElapsed] = useState(0);
  const agentLogRef = useRef<HTMLDivElement | null>(null);
  const agentInputRef = useRef<HTMLTextAreaElement | null>(null);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const nextDefaults = await fetchRemixLabDefaults(api);
        if (cancelled) return;
        setDefaults(nextDefaults);
        setSlots(slotsFromDefaults(nextDefaults));
      } catch (error) {
        if (!cancelled) setMessage(error instanceof Error ? error.message : "创作台加载失败。");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [api]);

  const reloadLibrary = async () => {
    const [nextPrompts, active] = await Promise.all([
      fetchRemixLabPrompts(api),
      fetchRemixLabActivePrompt(api),
    ]);
    setPrompts(nextPrompts);
    setActivePromptID(active.prompt?.id || DEFAULT_PROMPT_ID);
  };

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const [nextPrompts, active, nextAgent, chatHistory] = await Promise.all([
          fetchRemixLabPrompts(api),
          fetchRemixLabActivePrompt(api),
          fetchRemixLabAgentSettings(api),
          fetchRemixLabAgentHistory(api).catch(() => []),
        ]);
        if (cancelled) return;
        setPrompts(nextPrompts);
        setActivePromptID(active.prompt?.id || DEFAULT_PROMPT_ID);
        setAgentSettings(nextAgent);
        if (chatHistory.length > 0) {
          setAgentTurns(turnsFromAgentHistory(chatHistory));
        }
      } catch (error) {
        if (!cancelled) setMessage(error instanceof Error ? error.message : "创作台加载失败。");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [api]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const [accountRes, projectRes] = await Promise.all([api("/api/accounts"), api("/api/projects")]);
        if (cancelled) return;
        if (accountRes.ok) {
          const next = (await accountRes.json()) as Account[];
          setAccounts(next.filter((item) => item.status !== "inactive"));
        }
        if (projectRes.ok) {
          setProjects((await projectRes.json()) as Project[]);
        }
      } catch (error) {
        if (!cancelled) setMessage(error instanceof Error ? error.message : "项目列表读取失败。");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [api, selectedProjectID]);

  const pickAccount = (value: string) => {
    setAccountID(value);
    try {
      window.localStorage.setItem("remix-lab:produce-account", value);
    } catch {
      // 忽略存储不可用
    }
    if (selectedProjectID && value) {
      const open = projects.find((item) => item.id === selectedProjectID);
      if (open && open.account_id !== value) onNavigate("/");
    }
  };

  const createAccount = async (event: FormEvent) => {
    event.preventDefault();
    const name = newAccountName.trim();
    if (!name || !accountBackground) {
      setMessage("新建账号需要名称和一张背景图。");
      return;
    }
    try {
      const body = new FormData();
      body.append("name", name);
      body.append("background", accountBackground);
      const response = await api("/api/accounts", { method: "POST", body });
      if (!response.ok) throw new Error("账号创建失败。");
      const created = (await response.json()) as Account;
      setAccounts((current) => [...current, created]);
      pickAccount(created.id);
      setAccountFormOpen(false);
      setNewAccountName("");
      setAccountBackground(null);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "账号创建失败。");
    }
  };

  const visibleProjects = useMemo(
    () => (accountID ? projects.filter((project) => project.account_id === accountID) : projects),
    [projects, accountID],
  );

  const projectsByAccount = useMemo(() => {
    const nameOf = (id: string) => accounts.find((item) => item.id === id)?.name || "未分组账号";
    const groups = new Map<string, { accountID: string; name: string; items: Project[] }>();
    for (const project of visibleProjects) {
      const key = project.account_id || "unknown";
      const current = groups.get(key) ?? { accountID: key, name: nameOf(key), items: [] };
      current.items.push(project);
      groups.set(key, current);
    }
    return [...groups.values()];
  }, [visibleProjects, accounts]);

  const historySelectedID = useMemo(() => {
    if (selectedProjectID) return selectedProjectID;
    if (!experiment) return "";
    for (const run of experiment.runs) {
      if (run.production?.project_id) return run.production.project_id;
      if (run.adopted_project_id) return run.adopted_project_id;
    }
    return "";
  }, [selectedProjectID, experiment]);

  const removeProject = async (id: string) => {
    try {
      const response = await api(`/api/projects/${id}`, { method: "DELETE" });
      if (!response.ok) throw new Error("项目删除失败。");
      setProjects((current) => current.filter((item) => item.id !== id));
      if (selectedProjectID === id) onNavigate("/");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "项目删除失败。");
    }
  };

  useEffect(() => {
    setPackageRun(null);
  }, [experimentID]);

  useEffect(() => {
    if (!experimentID) {
      setExperiment(null);
      return;
    }
    let cancelled = false;
    let timer: number | undefined;

    const load = async () => {
      try {
        const detail = await fetchRemixLabExperiment(api, experimentID);
        if (cancelled) return;
        setExperiment(detail);
        if (detail.status === "running") {
          timer = window.setTimeout(() => {
            void load();
          }, 2000);
        }
      } catch (error) {
        if (!cancelled) setMessage(error instanceof Error ? error.message : "实验详情读取失败。");
      }
    };

    void load();
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [api, experimentID, detailRefresh]);

  const updateSlot = (index: number, patch: Partial<DraftSlot>) => {
    setSlots((current) =>
      current.map((slot, slotIndex) => (slotIndex === index ? { ...slot, ...patch } : slot)),
    );
  };

  const addSlot = () => {
    if (slots.length >= MAX_SLOTS) return;
    setSlots((current) => [
      ...current,
      {
        base_url: defaults?.remix_base_url || "",
        model: "",
        api_key: "",
        reasoning_effort: defaults?.remix_reasoning_effort || "",
        pipeline: "",
        run_count: 1,
        preset_index: null,
        keyConfigured: false,
      },
    ]);
  };

  const removeSlot = (index: number) => {
    setSlots((current) => (current.length <= 1 ? current : current.filter((_, slotIndex) => slotIndex !== index)));
  };

  // 提示条自动消失；新消息重置倒计时。
  useEffect(() => {
    if (!message) return;
    const timer = window.setTimeout(() => setMessage(""), 5000);
    return () => window.clearTimeout(timer);
  }, [message]);

  useEffect(() => {
    if (!agentBusy) {
      setAgentElapsed(0);
      return;
    }
    const started = Date.now();
    const timer = window.setInterval(() => {
      setAgentElapsed(Math.floor((Date.now() - started) / 1000));
    }, 250);
    return () => window.clearInterval(timer);
  }, [agentBusy]);

  useEffect(() => {
    if (!agentLogRef.current) return;
    agentLogRef.current.scrollTop = agentLogRef.current.scrollHeight;
  }, [agentBusy, agentElapsed, agentTurns]);

  useEffect(() => {
    if (!editingPrompt && !slotConfigOpen && !libraryOpen && !packageRun) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      if (editingPrompt) {
        setEditingPrompt(null);
        return;
      }
      if (packageRun) {
        setPackageRun(null);
        return;
      }
      if (libraryOpen) {
        setLibraryOpen(false);
        return;
      }
      setSlotConfigOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [editingPrompt, slotConfigOpen, libraryOpen, packageRun]);

  const openLibrary = async (tab: "writer" | "pipeline" = "writer") => {
    setLibraryTab(tab);
    setLibraryOpen(true);
    try {
      const view = await fetchRemixLabAgentPrompts(api);
      setAgentPromptsView(view);
      setAgentPromptsDraft({ ...view.prompts });
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "节点提示词读取失败。");
    }
  };

  const saveAgentPrompts = async () => {
    if (!agentPromptsDraft || savingAgentPrompts) return;
    setSavingAgentPrompts(true);
    try {
      const view = await saveRemixLabAgentPrompts(api, agentPromptsDraft);
      setAgentPromptsView(view);
      setAgentPromptsDraft({ ...view.prompts });
      setMessage("节点提示词已保存，下次开跑和打回立即生效。");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "节点提示词保存失败。");
    } finally {
      setSavingAgentPrompts(false);
    }
  };

  const openHistoryProject = async (projectID: string) => {
    try {
      const link = await fetchRemixLabProductionByProject(api, projectID);
      onNavigate(`/remix-lab/${link.experiment_id}`);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "这个项目没有关联工作流。");
    }
  };

  const openPackageEditor = async (runID: string, experimentIDForRun: string) => {
    try {
      const detail =
        experiment?.id === experimentIDForRun ? experiment : await fetchRemixLabExperiment(api, experimentIDForRun);
      const run = detail.runs.find((item) => item.id === runID);
      if (!run) {
        setMessage("还没有可改的定稿。");
        return;
      }
      if (detail.id !== experiment?.id) setExperiment(detail);
      setPackageRun(run);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "定稿读取失败。");
    }
  };

  // 「完成」即保存：槽位草稿直接落库为预设，刷新页面原样带回。
  const saveSlotConfig = async () => {
    if (savingSlots) return;
    if (slots.some((slot) => !slot.model.trim())) {
      setMessage("先在配置里填好模型。");
      return;
    }
    setSavingSlots(true);
    try {
      const saved = await saveRemixLabPresets(api, toCreateSlots(slots));
      setSlots(slotsFromDefaults(saved));
      setDefaults(saved);
      setSlotConfigOpen(false);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "模型配置保存失败。");
    } finally {
      setSavingSlots(false);
    }
  };

  // 工作流开跑：画布调用（带生产账号与全自动开关）。
  const startWorkflowRun = async (sourceText: string, runCount: number, accountID: string, auto: boolean) => {
    const created = await runRemixLabWorkflow(api, sourceText, runCount, accountID, auto);
    return created;
  };

  // 断点重试（详情页/弹窗用）：提交后刷新详情恢复轮询。
  const retryRunFromDetail = async (runID: string, nodeID: string) => {
    try {
      await retryRemixLabRun(api, runID, nodeID || undefined);
      setMessage(nodeID ? "已重试该节点，成功后自动续跑后面的环节。" : "已从断点重试，跑过的agent节点直接复用产物。");
      setFlowRun(null);
      setDetailRefresh((n) => n + 1);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "重试提交失败。");
    }
  };

  // 确认闸门放行 / 生产失败续跑（详情页/弹窗用）。
  const produceRunFromDetail = async (runID: string, accountID: string) => {
    try {
      await produceRemixLabRun(api, runID, accountID || undefined);
      setMessage("已放行：建项目 → 口播稿 → 配音 → 混剪，进度看工作流视图。");
      setFlowRun(null);
      setDetailRefresh((n) => n + 1);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "开始混剪失败。");
    }
  };

  const savePromptDraft = async () => {
    if (!editingPrompt) return;
    try {
      await upsertRemixLabPrompt(api, editingPrompt);
      setEditingPrompt(null);
      await reloadLibrary();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "提示词保存失败。");
    }
  };

  const removePrompt = async (id: string) => {
    try {
      await deleteRemixLabPrompt(api, id);
      await reloadLibrary();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "提示词删除失败。");
    }
  };

  const adoptPrompt = async (id: string) => {
    try {
      await setRemixLabActivePrompt(api, id);
      setActivePromptID(id);
      setMessage("已设为日产二创提示词，下次二创立即生效。");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "设为系统二创失败。");
    }
  };

  const saveAgent = async () => {
    try {
      const saved = await saveRemixLabAgentSettings(api, {
        model: agentSettings.model,
        base_url: agentSettings.base_url,
        reasoning_effort: agentSettings.reasoning_effort,
        api_key: agentKey,
      });
      setAgentSettings(saved);
      setAgentKey("");
      setMessage("智能体设置已保存。");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "智能体设置保存失败。");
    }
  };

  const submitAgent = async () => {
    const text = agentMessage.trim();
    if (!text || agentBusy) return;
    setAgentBusy(true);
    setMessage("");
    setAgentTurns((current) => [...current, { role: "user", text, proposals: [] }]);
    setAgentMessage("");
    try {
      const reply = await chatRemixLabAgent(api, text, experimentID);
      setAgentTurns((current) => [
        ...current,
        { role: "assistant", text: reply.reply, proposals: reply.proposals ?? [] },
      ]);
    } catch (error) {
      let recovered: RemixLabAgentLast | null = null;
      if (isDroppedAgentConnection(error)) {
        try {
          recovered = await fetchRemixLabAgentLast(api);
        } catch {
          recovered = null;
        }
      }
      if (recovered?.reply?.trim() && recovered.message.trim() === text) {
        setAgentTurns((current) => [
          ...current,
          { role: "assistant", text: recovered.reply ?? "", proposals: recovered.proposals ?? [] },
        ]);
        setMessage("刚才连接断了，已从后台找回上一轮回复。");
      } else {
        const detail = error instanceof Error ? error.message : "智能体请求失败。";
        setMessage(detail);
        setAgentTurns((current) => [...current, { role: "assistant", text: detail, proposals: [] }]);
      }
    } finally {
      setAgentBusy(false);
    }
  };

  const sendAgent = async (event: FormEvent) => {
    event.preventDefault();
    await submitAgent();
  };

  const resetAgentChat = async () => {
    if (agentBusy) return;
    try {
      await clearRemixLabAgentHistory(api);
      setAgentTurns([]);
      setMessage("已开新对话，智能体不再带旧上下文。");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "清空对话失败。");
    }
  };

  const applyAgentSuggestion = (text: string) => {
    setAgentMessage(text);
    agentInputRef.current?.focus();
  };

  const confirmProposal = async (proposalID: string) => {
    try {
      const result = await confirmRemixLabAgentProposal(api, proposalID);
      setAgentTurns((current) =>
        current.map((turn) => ({
          ...turn,
          proposals: turn.proposals.filter((item) => item.id !== proposalID),
        })),
      );
      await reloadLibrary();
      if (result.type === "start_experiment" && result.result?.id) {
        onNavigate(`/remix-lab/${result.result.id}`);
      }
      if (result.type === "set_active_prompt") {
        setMessage("已设为日产二创提示词，下次二创立即生效。");
      }
      if (result.type === "update_workflow") {
        setWorkflowRefresh((n) => n + 1);
        setMessage("工作流已按草案更新，画布已刷新，下次开跑生效。");
      }
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "草案确认失败。");
    }
  };

  return (
    <div className="remix-lab">
      {message ? (
        <div className="remix-lab-toast" role="status">
          <span>{message}</span>
          <button type="button" aria-label="关闭提示" onClick={() => setMessage("")}>
            <X size={14} strokeWidth={2.2} aria-hidden="true" />
          </button>
        </div>
      ) : null}
      <header>
        <div className="remix-lab-brand">
          <span className="remix-lab-mark" aria-hidden="true">
            <PenLine size={18} strokeWidth={2} />
          </span>
          <div>
            <span className="eyebrow">视频生产控制台</span>
            <h1>文案创作台</h1>
            <p>按账号切换最新工作流，左边是历史项目，定稿后直接进混剪。</p>
          </div>
          <nav className="remix-lab-account-switch" aria-label="账号">
            <label>
              当前账号
              <select
                aria-label="切换账号工作流"
                value={accountID}
                onChange={(event) => pickAccount(event.target.value)}
              >
                <option value="">全局默认工作流</option>
                {accounts.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                  </option>
                ))}
              </select>
            </label>
            <button
              type="button"
              className="header-button"
              disabled={!accountID}
              title={accountID ? "配置这个账号的专属样式和音色" : "先选中一个账号"}
              onClick={() => {
                const current = accounts.find((item) => item.id === accountID);
                if (current) setConfiguringAccount(current);
              }}
            >
              账号配置{accounts.find((item) => item.id === accountID)?.overrides ? " ·已定制" : ""}
            </button>
            <button
              type="button"
              className="header-button"
              aria-expanded={accountFormOpen}
              aria-controls="account-create-form"
              onClick={() => setAccountFormOpen((open) => !open)}
            >
              {accountFormOpen ? "收起账号管理" : "新增账号"}
            </button>
            {accountFormOpen ? (
              <form id="account-create-form" className="remix-lab-account-form" onSubmit={(event) => void createAccount(event)}>
                <label htmlFor="new-account-name">账号名称</label>
                <input
                  id="new-account-name"
                  value={newAccountName}
                  onChange={(event) => setNewAccountName(event.target.value)}
                  placeholder="添加账号名称"
                />
                <label className="background-pick">
                  {accountBackground ? "已选择背景图" : "选择固定背景图"}
                  <input
                    type="file"
                    accept="image/png,image/jpeg,image/webp"
                    onChange={(event) => setAccountBackground(event.target.files?.[0] || null)}
                  />
                </label>
                <button type="submit">添加账号</button>
              </form>
            ) : null}
          </nav>
        </div>
        <div className="remix-lab-header-actions">
          {theme && onThemeChange ? (
            <label className="theme-control">
              主题
              <select
                aria-label="选择界面主题"
                value={theme}
                onChange={(event) => onThemeChange(event.target.value as Theme)}
              >
                <option value="light">日间</option>
                <option value="dark">夜间</option>
              </select>
            </label>
          ) : null}
          {onOpenSettings ? (
            <button type="button" className="header-button remix-lab-icon-btn" onClick={() => void onOpenSettings()}>
              <Settings size={16} strokeWidth={2} />
              设置
            </button>
          ) : null}
          <button type="button" className="header-button" onClick={() => onNavigate("/image-projects")}>
            图文制作
          </button>
          {onLogout ? (
            <button type="button" className="header-button remix-lab-icon-btn" onClick={() => void onLogout()}>
              <LogOut size={16} strokeWidth={2} />
              退出
            </button>
          ) : null}
        </div>
      </header>
      <div
        className={[
          "remix-lab__body",
          historyCollapsed ? "remix-lab__body--history-collapsed" : "",
          agentCollapsed ? "remix-lab__body--agent-collapsed" : "",
          projectView && !experimentID ? "remix-lab__body--project" : "",
        ].join(" ")}
      >
        <aside className={historyCollapsed ? "remix-lab__history remix-lab__side--collapsed" : "remix-lab__history"}>
          <button
            type="button"
            className="header-button remix-lab-icon-btn remix-lab__rail-toggle"
            aria-label={historyCollapsed ? "展开历史" : "收起历史"}
            title={historyCollapsed ? "展开历史" : "收起历史"}
            onClick={() => {
              writeCollapsed("remix-lab:history-collapsed", !historyCollapsed);
              setHistoryCollapsed(!historyCollapsed);
            }}
          >
            {historyCollapsed ? <PanelLeftOpen size={15} strokeWidth={2} /> : <PanelLeftClose size={15} strokeWidth={2} />}
          </button>
          <div className="remix-lab__history-head">
            <h2>历史项目</h2>
            {selectedProjectID || experimentID ? (
              <button type="button" className="header-button" onClick={() => onNavigate("/")}>
                工作流
              </button>
            ) : null}
          </div>
          {visibleProjects.length === 0 ? (
            <p className="remix-lab__history-empty">
              {accountID ? "这个账号还没有项目。跑完工作流或从混剪节点建项目后会出现在这里。" : "还没有项目。中间贴原文开跑，或等混剪建好项目。"}
            </p>
          ) : null}
          {projectsByAccount.map((group) => (
            <div key={group.accountID} className="remix-lab__history-group">
              {!accountID && projectsByAccount.length > 1 ? (
                <p className="remix-lab__history-group-title">{group.name}</p>
              ) : null}
              <ul>
                {group.items.map((item) => (
                  <li key={item.id} className={item.id === historySelectedID ? "is-selected" : undefined}>
                    <button
                      type="button"
                      className={item.id === historySelectedID ? "remix-lab__history-item selected" : "remix-lab__history-item"}
                      onClick={() => void openHistoryProject(item.id)}
                    >
                      <strong>{item.title || item.id}</strong>
                      <span className="remix-lab__history-meta">
                        <span className="remix-lab-chip">{stageLabel(item.stage)}</span>
                        {item.updated_at ? (
                          <small className="remix-lab__history-time">{formatHistoryTime(item.updated_at)}</small>
                        ) : null}
                      </span>
                    </button>
                    <button
                      type="button"
                      className="header-button remix-lab-icon-btn remix-lab__history-delete"
                      aria-label={`删除项目 ${item.title || item.id}`}
                      onClick={() => void removeProject(item.id)}
                    >
                      <Trash2 size={14} strokeWidth={2} />
                      删除
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </aside>
        <main className="remix-lab__main">
          {projectView && !experimentID ? (
            <div className="remix-lab__project">{projectView}</div>
          ) : (
            <section className="remix-lab__designer" aria-label="工作流设计">
              <div className="wf-page-actions">
                <div className="remix-lab-prompts__head-actions">
                  <button type="button" className="header-button" onClick={() => setSlotConfigOpen(true)}>
                    模型配置
                  </button>
                  <button type="button" className="header-button" onClick={() => void openLibrary()}>
                    提示词库
                  </button>
                </div>
              </div>
              <WorkflowCanvas
                key={`${accountID || "global"}:${experimentID || "design"}`}
                api={api}
                prompts={prompts}
                onMessage={setMessage}
                refreshToken={workflowRefresh}
                accountID={accountID}
                onAccountIDChange={pickAccount}
                runWorkflow={startWorkflowRun}
                resumeExperiment={experimentID ? experiment : null}
                onEditPackage={(runID, expID) => void openPackageEditor(runID, expID)}
                onLeaveRun={experimentID ? () => onNavigate("/") : undefined}
                onEditAgentPrompts={() => void openLibrary("pipeline")}
              />
            </section>
          )}
        </main>
        <aside
          className={agentCollapsed ? "remix-lab__agent remix-lab__side--collapsed" : "remix-lab__agent"}
          aria-label="创作台智能体"
        >
          <button
            type="button"
            className="header-button remix-lab-icon-btn remix-lab__rail-toggle"
            aria-label={agentCollapsed ? "展开智能体" : "收起智能体"}
            title={agentCollapsed ? "展开智能体" : "收起智能体"}
            onClick={() => {
              writeCollapsed("remix-lab:agent-collapsed", !agentCollapsed);
              setAgentCollapsed(!agentCollapsed);
            }}
          >
            {agentCollapsed ? <PanelRightOpen size={15} strokeWidth={2} /> : <PanelRightClose size={15} strokeWidth={2} />}
          </button>
          <div className="remix-lab-agent-head">
            <span className="remix-lab-mark remix-lab-mark--sm" aria-hidden="true">
              <Sparkles size={16} strokeWidth={2} />
            </span>
            <div>
              <h2>智能体</h2>
              <p className="remix-lab-muted">只出草案，点确认才改库或开跑。</p>
            </div>
            <button
              type="button"
              className="remix-lab-agent-reset"
              onClick={() => void resetAgentChat()}
              disabled={agentBusy || agentTurns.length === 0}
            >
              <RotateCcw size={13} strokeWidth={2} aria-hidden="true" />
              新对话
            </button>
          </div>
          <details className="remix-lab-agent-settings">
            <summary>
              模型设置
              <span>{agentSettings.model || "未设置"}</span>
            </summary>
            <label>
              模型
              <input
                aria-label="智能体模型"
                value={agentSettings.model}
                onChange={(event) => setAgentSettings({ ...agentSettings, model: event.target.value })}
                required
              />
            </label>
            <label>
              Base URL
              <input
                value={agentSettings.base_url}
                placeholder="空则用二创服务地址"
                onChange={(event) => setAgentSettings({ ...agentSettings, base_url: event.target.value })}
              />
            </label>
            <label>
              思考强度
              <input
                value={agentSettings.reasoning_effort}
                onChange={(event) =>
                  setAgentSettings({ ...agentSettings, reasoning_effort: event.target.value })
                }
              />
            </label>
            <label>
              API Key
              <input
                type="password"
                aria-label="智能体 API Key"
                value={agentKey}
                placeholder={agentSettings.api_key_configured ? "已配置" : "空则用二创密钥"}
                onChange={(event) => setAgentKey(event.target.value)}
                autoComplete="off"
              />
            </label>
            <button type="button" className="header-button" onClick={() => void saveAgent()}>
              保存智能体设置
            </button>
          </details>
          <div className="remix-lab-agent-log" ref={agentLogRef}>
            {agentTurns.length === 0 && !agentBusy ? (
              <div className="remix-lab-agent-empty">
                <Sparkles size={18} strokeWidth={2} aria-hidden="true" />
                <p>它读得到工作流节点图、提示词库、历史成稿和你的批注；改节点、加节点、改提示词都先出草案等你确认。</p>
                <div className="remix-lab-agent-suggestions">
                  {AGENT_SUGGESTIONS.map((suggestion) => (
                    <button key={suggestion} type="button" onClick={() => applyAgentSuggestion(suggestion)}>
                      {suggestion}
                    </button>
                  ))}
                </div>
              </div>
            ) : null}
            {agentTurns.map((turn, index) => (
              <article key={`${turn.role}-${index}`} className={`remix-lab-agent-turn remix-lab-agent-turn--${turn.role}`}>
                <span className="remix-lab-agent-turn__who">{turn.role === "user" ? "你" : "草案"}</span>
                <p>{turn.text}</p>
                {turn.proposals.map((proposal) => {
                  const kind = PROPOSAL_KINDS[proposal.type] ?? { label: "草案", Icon: PenLine };
                  return (
                    <div key={proposal.id} className="remix-lab-proposal">
                      <span className="remix-lab-proposal__icon" aria-hidden="true">
                        <kind.Icon size={14} strokeWidth={2} />
                      </span>
                      <span className="remix-lab-proposal__body">
                        <small>{kind.label}</small>
                        <strong>{proposal.summary || proposal.type}</strong>
                      </span>
                      <button type="button" onClick={() => void confirmProposal(proposal.id)}>
                        <Check size={13} strokeWidth={2.4} aria-hidden="true" />
                        确认
                      </button>
                    </div>
                  );
                })}
              </article>
            ))}
            {agentBusy ? (
              <article
                className="remix-lab-agent-turn remix-lab-agent-turn--pending"
                role="status"
                aria-live="polite"
                aria-label={`正在思考 ${formatElapsed(agentElapsed)}`}
              >
                <span className="remix-lab-agent-turn__who">草案</span>
                <p>正在读历史实验并思考… {formatElapsed(agentElapsed)}</p>
                <div className="remix-lab-agent-progress" role="progressbar" aria-valuetext="进行中" />
                <p className="remix-lab-muted">思考模型经常要两三分钟。页面会一直等，最长十二分钟。</p>
              </article>
            ) : null}
          </div>
          <form className="remix-lab-agent-form" onSubmit={(event) => void sendAgent(event)}>
            <label>
              对智能体说
              <textarea
                aria-label="对智能体说"
                ref={agentInputRef}
                value={agentMessage}
                onChange={(event) => setAgentMessage(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing) return;
                  event.preventDefault();
                  void submitAgent();
                }}
                rows={3}
                placeholder="回车发送，Shift+回车换行。它记得这段对话的上下文。"
                disabled={agentBusy}
              />
            </label>
            <button
              type="submit"
              className="remix-lab-start remix-lab-agent-send"
              disabled={agentBusy || !agentMessage.trim()}
              aria-busy={agentBusy}
            >
              {agentBusy ? (
                `思考中 ${formatElapsed(agentElapsed)}`
              ) : (
                <>
                  <Send size={14} strokeWidth={2} aria-hidden="true" />
                  发送
                </>
              )}
            </button>
          </form>
        </aside>
      </div>
      {editingPrompt ? (
        <div
          className="modal-backdrop"
          onClick={() => setEditingPrompt(null)}
        >
          <div
            className="preview-modal remix-lab-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="remix-lab-prompt-dialog-title"
            onClick={(event) => event.stopPropagation()}
          >
            <div className="modal-head">
              <div>
                <span className="muted">提示词库</span>
                <h2 id="remix-lab-prompt-dialog-title">{editingPrompt.id ? "编辑提示词" : "新建提示词"}</h2>
              </div>
              <button
                type="button"
                className="close"
                aria-label="关闭提示词编辑"
                onClick={() => setEditingPrompt(null)}
              >
                <X size={20} aria-hidden="true" />
              </button>
            </div>
            <div className="remix-lab-prompt-editor">
              <label>
                名称
                <input
                  value={editingPrompt.name || ""}
                  onChange={(event) => setEditingPrompt({ ...editingPrompt, name: event.target.value })}
                />
              </label>
              <label>
                版本标注
                <input
                  value={editingPrompt.stamp || ""}
                  onChange={(event) => setEditingPrompt({ ...editingPrompt, stamp: event.target.value })}
                />
              </label>
              <label>
                系统提示词
                <textarea
                  rows={8}
                  value={editingPrompt.system || ""}
                  onChange={(event) => setEditingPrompt({ ...editingPrompt, system: event.target.value })}
                />
              </label>
              <label>
                用户提示词
                <textarea
                  rows={5}
                  value={editingPrompt.user || ""}
                  onChange={(event) => setEditingPrompt({ ...editingPrompt, user: event.target.value })}
                />
              </label>
              <div className="remix-lab__compose-actions remix-lab-modal__actions">
                <button type="button" className="header-button" onClick={() => setEditingPrompt(null)}>
                  取消
                </button>
                <button type="button" className="remix-lab-start" onClick={() => void savePromptDraft()}>
                  保存提示词
                </button>
              </div>
            </div>
          </div>
        </div>
      ) : null}
      {slotConfigOpen ? (
        <div className="modal-backdrop" onClick={() => setSlotConfigOpen(false)}>
          <div
            className="preview-modal remix-lab-modal remix-lab-modal--wide"
            role="dialog"
            aria-modal="true"
            aria-labelledby="remix-lab-slot-dialog-title"
            onClick={(event) => event.stopPropagation()}
          >
            <div className="modal-head">
              <div>
                <span className="muted">开跑时使用</span>
                <h2 id="remix-lab-slot-dialog-title">模型配置</h2>
              </div>
              <button
                type="button"
                className="close"
                aria-label="关闭模型配置"
                onClick={() => setSlotConfigOpen(false)}
              >
                <X size={20} aria-hidden="true" />
              </button>
            </div>
            <div className="remix-lab-slots">
              {slots.map((slot, index) => (
                <fieldset key={index} className="remix-lab-slot">
                  <legend>
                    模型槽 {index + 1}
                    <button
                      type="button"
                      className="header-button remix-lab-icon-btn"
                      disabled={slots.length <= 1}
                      onClick={() => removeSlot(index)}
                      aria-label={`删除模型槽 ${index + 1}`}
                    >
                      <Trash2 size={14} strokeWidth={2} />
                      删除
                    </button>
                  </legend>
                  <div className="remix-lab-slot__fields">
                    <label>
                      Base URL
                      <input
                        value={slot.base_url}
                        onChange={(event) => updateSlot(index, { base_url: event.target.value })}
                      />
                    </label>
                    <label>
                      模型
                      <input
                        value={slot.model}
                        onChange={(event) => updateSlot(index, { model: event.target.value })}
                      />
                    </label>
                    <label>
                      思考强度
                      <input
                        value={slot.reasoning_effort}
                        onChange={(event) => updateSlot(index, { reasoning_effort: event.target.value })}
                      />
                    </label>
                    <label>
                      管线
                      <select
                        aria-label="槽位管线"
                        value={slot.pipeline}
                        onChange={(event) => updateSlot(index, { pipeline: event.target.value })}
                      >
                        <option value="">写手 → 审稿agent终审</option>
                        <option value="multi_agent">情报组（钩子+事实搜索+弹药）→ 写手 → 审稿agent终审</option>
                      </select>
                      <small className="remix-lab-muted">所有成稿都会过审稿agent，只修违规处并留初稿对照。</small>
                    </label>
                    <label>
                      API Key
                      <input
                        type="password"
                        value={slot.api_key}
                        placeholder={slot.keyConfigured ? "已配置" : ""}
                        onChange={(event) => updateSlot(index, { api_key: event.target.value })}
                        autoComplete="off"
                      />
                    </label>
                    <label>
                      运行次数
                      <input
                        type="number"
                        min={1}
                        max={3}
                        value={slot.run_count}
                        onChange={(event) =>
                          updateSlot(index, { run_count: clampRunCount(Number(event.target.value)) })
                        }
                      />
                    </label>
                  </div>
                </fieldset>
              ))}
            </div>
            <div className="remix-lab-modal__actions">
              <button
                type="button"
                className="header-button remix-lab-icon-btn"
                disabled={slots.length >= MAX_SLOTS}
                onClick={addSlot}
              >
                <Plus size={16} strokeWidth={2} />
                添加模型槽
              </button>
              <button type="button" className="remix-lab-start" onClick={() => void saveSlotConfig()} disabled={savingSlots} aria-busy={savingSlots}>
                {savingSlots ? "正在保存…" : "保存并完成"}
              </button>
            </div>
          </div>
        </div>
      ) : null}
      {flowRun ? (
        <RunFlow
          api={api}
          runID={flowRun.id}
          runLabel={flowRun.label}
          onClose={() => setFlowRun(null)}
          onEditAgentPrompts={() => void openLibrary("pipeline")}
          onMessage={setMessage}
          onRetryNode={(nodeID) => void retryRunFromDetail(flowRun.id, nodeID)}
          onRetryProduce={() => void produceRunFromDetail(flowRun.id, "")}
        />
      ) : null}
      {libraryOpen ? (
        <div className="modal-backdrop" onClick={() => setLibraryOpen(false)}>
          <div
            className="preview-modal remix-lab-modal remix-lab-modal--wide"
            role="dialog"
            aria-modal="true"
            aria-labelledby="remix-lab-library-title"
            onClick={(event) => event.stopPropagation()}
          >
            <div className="modal-head">
              <div>
                <span className="muted">写手模板和节点管线都在这里改</span>
                <h2 id="remix-lab-library-title">提示词库</h2>
              </div>
              <button
                type="button"
                className="close"
                aria-label="关闭提示词库"
                onClick={() => setLibraryOpen(false)}
              >
                <X size={20} aria-hidden="true" />
              </button>
            </div>
            <div className="remix-lab-library-tabs" role="tablist" aria-label="提示词分类">
              <button
                type="button"
                role="tab"
                aria-selected={libraryTab === "writer"}
                className={libraryTab === "writer" ? "header-button is-active" : "header-button"}
                onClick={() => setLibraryTab("writer")}
              >
                写手提示词
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={libraryTab === "pipeline"}
                className={libraryTab === "pipeline" ? "header-button is-active" : "header-button"}
                onClick={() => setLibraryTab("pipeline")}
              >
                节点提示词
              </button>
            </div>
            {libraryTab === "writer" ? (
              <>
                <div className="remix-lab-library-toolbar">
                  <button
                    type="button"
                    className="header-button"
                    onClick={() => setEditingPrompt({ name: "", system: "", user: "", stamp: "", description: "" })}
                  >
                    新建提示词
                  </button>
                </div>
                <ul className="remix-lab-prompts__list">
                  {prompts.map((prompt) => {
                    const adopted = activePromptID === prompt.id;
                    return (
                      <li key={prompt.id}>
                        <div className="remix-lab-prompt-card">
                          <span className="remix-lab-prompt-card__body">
                            <span className="remix-lab-prompt-card__title">
                              <strong>{prompt.name}</strong>
                              {adopted ? <span className="remix-lab-chip remix-lab-chip--live">日产</span> : null}
                            </span>
                            {prompt.stamp ? <small>{prompt.stamp}</small> : null}
                            {prompt.description ? <p>{prompt.description}</p> : null}
                          </span>
                        </div>
                        <div className="remix-lab-prompts__actions">
                          <button type="button" className="header-button" onClick={() => setEditingPrompt(prompt)}>
                            编辑
                          </button>
                          <button type="button" className="header-button" onClick={() => void adoptPrompt(prompt.id)}>
                            设为系统二创
                          </button>
                          <button type="button" className="header-button" onClick={() => void removePrompt(prompt.id)}>
                            删除
                          </button>
                        </div>
                      </li>
                    );
                  })}
                </ul>
              </>
            ) : (
              <>
                <p className="remix-lab-muted remix-lab-agent-prompts__intro">
                  钩子、事实、弹药、审稿的系统提示词。改完保存后，下次开跑和打回立即生效。
                </p>
                <div className="remix-lab-agent-prompts">
                  {agentPromptsDraft
                    ? AGENT_PROMPT_FIELDS.map((field) => {
                        const overridden = agentPromptsView?.overridden?.[field.key] ?? false;
                        const isDirtyDefault =
                          agentPromptsView != null &&
                          agentPromptsDraft[field.key].trim() !== agentPromptsView.defaults[field.key].trim();
                        return (
                          <section key={field.key} className="remix-lab-agent-prompts__field">
                            <div className="remix-lab-agent-prompts__field-head">
                              <div>
                                <h3>
                                  {field.label}
                                  {overridden || isDirtyDefault ? (
                                    <span className="remix-lab-chip remix-lab-chip--review-live">自定义</span>
                                  ) : (
                                    <span className="remix-lab-chip">默认</span>
                                  )}
                                </h3>
                                <p className="remix-lab-muted">{field.hint}</p>
                              </div>
                              <button
                                type="button"
                                className="header-button"
                                disabled={!agentPromptsView || !isDirtyDefault}
                                onClick={() =>
                                  agentPromptsView &&
                                  setAgentPromptsDraft({
                                    ...agentPromptsDraft,
                                    [field.key]: agentPromptsView.defaults[field.key],
                                  })
                                }
                              >
                                恢复默认
                              </button>
                            </div>
                            <textarea
                              aria-label={`${field.label}提示词`}
                              rows={8}
                              value={agentPromptsDraft[field.key]}
                              onChange={(event) =>
                                setAgentPromptsDraft({ ...agentPromptsDraft, [field.key]: event.target.value })
                              }
                            />
                          </section>
                        );
                      })
                    : (
                        <p className="remix-lab-muted">正在读取节点提示词…</p>
                      )}
                </div>
                <div className="remix-lab-modal__actions">
                  <button
                    type="button"
                    className="remix-lab-start"
                    onClick={() => void saveAgentPrompts()}
                    disabled={savingAgentPrompts || !agentPromptsDraft}
                    aria-busy={savingAgentPrompts}
                  >
                    {savingAgentPrompts ? "正在保存…" : "保存节点提示词"}
                  </button>
                </div>
              </>
            )}
          </div>
        </div>
      ) : null}
      {packageRun ? (
        <div className="modal-backdrop" onClick={() => setPackageRun(null)}>
          <div
            className="preview-modal remix-lab-modal remix-lab-modal--wide"
            role="dialog"
            aria-modal="true"
            aria-label="改稿"
            onClick={(event) => event.stopPropagation()}
          >
            <div className="modal-head">
              <div>
                <span className="muted">工作流上确认后继续混剪</span>
                <h2>改稿</h2>
              </div>
              <button type="button" className="close" aria-label="关闭改稿" onClick={() => setPackageRun(null)}>
                <X size={20} aria-hidden="true" />
              </button>
            </div>
            <RunWorkbench
              api={api}
              run={packageRun}
              onMessage={setMessage}
              onChanged={() => setDetailRefresh((n) => n + 1)}
              onNavigate={(href) => {
                setPackageRun(null);
                onNavigate(href);
              }}
              onOpenFlow={() => {
                setFlowRun({ id: packageRun.id, label: `运行 ${packageRun.run_index}` });
                setPackageRun(null);
              }}
              onProduce={(accountID) => {
                void produceRunFromDetail(packageRun.id, accountID);
                setPackageRun(null);
              }}
            />
          </div>
        </div>
      ) : null}
      {configuringAccount ? (
        <AccountOverridesDialog
          account={configuringAccount}
          api={api}
          globalStyle={globalMontageStyle}
          onSaved={(updated) => {
            setAccounts((current) => current.map((item) => (item.id === updated.id ? updated : item)));
            setConfiguringAccount(null);
          }}
          onClose={() => setConfiguringAccount(null)}
        />
      ) : null}
    </div>
  );
}
