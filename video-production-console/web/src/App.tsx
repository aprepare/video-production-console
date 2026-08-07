import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent } from "react";
import { ArrowLeft } from "lucide-react";
import "./App.css";
import "./idea.css";
import { parseLocation } from "./project-workbench/routes";
import type { ActiveWorkflow } from "./project-workbench/types";

type Account = { id: string; name: string; status?: string };
type Project = {
  id: string;
  account_id: string;
  title: string;
  stage: string;
  updated_at?: string;
  missing_assets?: string[];
};
type Asset = {
  id: string;
  type: string;
  filename: string;
  mime_type: string;
  size: number;
  version: number;
  status?: string;
  created_at: string;
};
type TaskMessage = {
  id: string;
  role: string;
  content: string;
  question_schema?: string;
  created_at: string;
};
type TaskEvent = {
  id?: string;
  sequence?: number;
  kind?: string;
  level?: string;
  display_text?: string;
  raw_json?: string;
  created_at?: string;
  // The task API currently serializes persisted events with Go field names.
  Kind?: string;
  Level?: string;
  DisplayText?: string;
  RawJSON?: string;
};
type SemanticEvent = {
  id?: string;
  sequence?: number;
  kind?: string;
  phase?: string;
  level?: string;
  title?: string;
  detail?: string;
  created_at?: string;
  ID?: string;
  Sequence?: number;
  Kind?: string;
  Phase?: string;
  Level?: string;
  Title?: string;
  Detail?: string;
  CreatedAt?: string;
};
type ReasoningEffort = "low" | "medium" | "high" | "xhigh" | "max" | "ultra";
type TaskModelOverride = { model: string; reasoningEffort: ReasoningEffort | "" };
type Task = {
  id: string;
  project_id?: string;
  type: string;
  skill_name: string;
  status: string;
  action?: string;
  completion_phase?: string;
  prompt_snapshot?: string;
  result_summary?: string;
  error_message?: string;
  created_at: string;
  model?: string;
  reasoning_effort?: ReasoningEffort;
  messages?: TaskMessage[];
  events?: TaskEvent[];
  semantic_events?: SemanticEvent[];
  montage?: MontageResult;
  publishing_package?: PublishingPackage;
};
type PublishingPackage = {
  titles?: string[];
  top_titles?: Array<{ rank: number; title: string; reason: string }>;
  short_titles?: string[];
  descriptions?: string[];
  description?: string;
  topics?: string[];
  cta?: string;
};
type MontageResult = {
  phase: string;
  workspace?: { id: string; filename: string; mime_type: string; size: number; created_at: string };
  registration_attempts?: Array<{
    id: string;
    attempt: number;
    state: string;
    registered_path?: string;
    receipt_path?: string;
    error_code?: string;
    error_message?: string;
    started_at: string;
    finished_at?: string;
  }>;
  registered_asset?: { id: string; filename: string; path: string; sha256: string; created_at: string };
  can_retry_registration: boolean;
};
type RuntimeStatus = { Limit: number; Running: number; Queued: number };
type ProjectDetail = {
  project: Project;
  assets: Record<string, Asset>;
  asset_history?: Record<string, Asset[]>;
  background_reference?: Asset | null;
  topic_context?: IdeaCandidate | null;
  missing_assets?: string[];
  active_workflow?: ActiveWorkflow | null;
};
type PublicSettings = {
  listen_addr: string;
  data_root: string;
  max_codex_concurrency: number;
  baokuan_base_url: string;
  baokuan_mcp_executable: string;
  obsidian_vault: string;
  topic_cards_dir: string;
  grok_base_url: string;
  grok_model: string;
  codex_binary_path: string;
  media_index_path: string;
  media_root: string;
  jianying_root: string;
  machine_profile_path: string;
  app_server_enabled: boolean;
  codex_workspace_roots: string[];
  codex_history_limit: number;
  codex_default_model: string;
  codex_default_reasoning_effort: ReasoningEffort;
};
type Settings = {
  public: PublicSettings;
  configured_public?: PublicSettings;
  active_public?: PublicSettings;
  restart_required?: boolean;
  settings_version: number;
  secrets: Record<string, { configured: boolean; masked: string }>;
};
type IdeaMessage = {
  id: string;
  role: string;
  content: string;
  task_id?: string;
  createdAt?: string;
  created_at?: string;
};
type IdeaCandidate = {
  id: string;
  title: string;
  summary?: string;
  mother_theme?: string;
  family_conflict?: string;
  anomaly_framing?: string;
  narrative_entry?: string;
  source_refs?: string[];
  fragment_refs?: string[];
  score?: number;
  selected?: boolean;
};
type IdeaSession = {
  id: string;
  accountID?: string;
  account_id?: string;
  title: string;
  status: string;
  messages?: IdeaMessage[];
  candidates?: IdeaCandidate[];
};
type IdeaSessionDetail = {
  session: IdeaSession;
  messages?: IdeaMessage[];
  candidates?: IdeaCandidate[];
};
type ChatMessage = {
  id: string;
  role: string;
  kind: string;
  content: string;
  delivery_status: string;
  turn_id?: string;
  created_at: string;
};
type ChatSession = {
  id: string;
  title: string;
  kind: string;
  source?: "console" | "desktop";
  status: string;
  model?: string;
  reasoning_effort?: string;
  skill_names?: string[];
  updated_at: string;
};
type ChatDetail = { session: ChatSession; messages: ChatMessage[] };
type HistoryThread = {
  id: string;
  title: string;
  preview: string;
  source: "desktop" | "cli" | "task";
  model?: string;
  reasoning_effort?: string;
  active: boolean;
  recency: string;
};
type DirectoryManifest = {
  asset_id: string;
  registered_path: string;
  entries: Array<{ path: string; kind: string; size: number }>;
};

const historySourceLabels: Record<HistoryThread["source"], string> = {
  desktop: "桌面版",
  cli: "CLI",
  task: "控制台任务",
};
const montagePhaseLabels: Record<string, string> = {
  agent_running: "正在生成明文草稿",
  plaintext: "明文草稿已生成",
  registering: "正在登记到剪映",
  failed: "登记失败",
  interrupted: "登记已中断",
  registered: "已登记为正式资产",
};

function isTechnicalChatMessage(message: ChatMessage) {
  return ["event", "tool", "technical", "protocol"].includes(message.kind);
}

function isAbortError(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}

function writeTaskQuery(taskID: string, mode: "push" | "replace" = "replace") {
  const url = new URL(window.location.href);
  if (taskID) url.searchParams.set("task", taskID);
  else url.searchParams.delete("task");
  if (mode === "push") window.history.pushState({}, "", url.toString());
  else window.history.replaceState({}, "", url.toString());
}

const liveTaskStatuses = new Set([
  "queued",
  "running",
  "awaiting_input",
  "resuming",
  "waiting_input",
]);

function latestRegistrationState(montage: MontageResult) {
  return [...(montage.registration_attempts || [])]
    .sort((left, right) => right.attempt - left.attempt)[0]?.state?.toLowerCase();
}

function derivedMontagePhase(task: Task) {
  if (!task.montage) return task.completion_phase || "agent_running";
  const attemptState = latestRegistrationState(task.montage);
  if (attemptState === "failed" || attemptState === "interrupted") return attemptState;
  if (attemptState === "succeeded") return "registered";
  if (attemptState === "queued" || attemptState === "running") return "registering";
  const completion = task.completion_phase || task.montage.phase;
  if (completion === "plaintext_ready") return "plaintext";
  if (["agent_running", "registering", "registered"].includes(completion)) return completion;
  return completion || "agent_running";
}

function montageHeadline(montage: MontageResult, phase: string) {
  switch (phase) {
    case "agent_running":
      return "Codex 正在生成可登记的明文草稿";
    case "registered":
      return "草稿已登记，可以在剪映中继续编辑";
    case "registering":
      return "明文草稿已完成，正在登记剪映";
    case "failed":
      return "草稿已生成，但登记剪映失败";
    case "interrupted":
      return "草稿已保留，登记过程被中断";
    case "plaintext":
      return "明文草稿已完成，等待登记剪映";
    default:
      return montage.can_retry_registration
        ? "草稿已生成，可以只重试剪映登记"
        : "正在生成可登记的明文草稿";
  }
}

const stages = [
  "topic",
  "script",
  "assets",
  "mixing",
  "review",
  "ready",
  "published",
];
const assetLabels: Record<string, string> = {
  source_script: "爆款原文",
  topic_card: "正式选题卡",
  continuous_script: "连续文案",
  spoken_script: "口播稿",
  narration: "配音",
  subtitle_srt: "SRT 字幕",
  account_background: "账号固定背景图",
  audio: "配音",
  subtitle: "SRT 字幕",
  mix_draft: "混剪草稿",
  final_video: "成片",
};
const uploadAssetLabels: Record<string, string> = {
  continuous_script: "连续文案",
  spoken_script: "口播稿",
  audio: "配音",
  subtitle: "SRT 字幕",
  mix_draft: "混剪草稿",
  final_video: "成片",
};
const statusLabels: Record<string, string> = {
  queued: "排队中",
  running: "运行中",
  awaiting_input: "等待回复",
  resuming: "恢复中",
  completed: "已完成",
  failed: "失败",
  canceled: "已取消",
  interrupted: "已中断",
  waiting_input: "等待回复",
  cancelled: "已取消",
};
const cancellableTaskStatuses = new Set([
  "queued",
  "running",
  "resuming",
  "awaiting_input",
  "waiting_input",
]);
const taskActionLabels: Record<string, string> = {
  "topic.brainstorm": "选题分析",
  "topic.commit": "保存选题卡",
  "topic.deepen": "深化选题",
  "remix.standard": "二创文案",
  "remix.enhanced": "增强二创文案",
  "remix.from_topic_card": "根据选题写文案",
  "remix.spoken_format": "口播断句",
  "remix.review": "文案检查",
  "montage.plan": "混剪方案",
  "montage.execute": "混剪草稿",
};
const textAssets = new Set([
  "source_script",
  "topic_card",
  "continuous_script",
  "spoken_script",
  "subtitle",
  "subtitle_srt",
]);
const reasoningEfforts: ReasoningEffort[] = [
  "low",
  "medium",
  "high",
  "xhigh",
  "max",
  "ultra",
];
type PublicStringSettingKey = {
  [Key in keyof PublicSettings]: PublicSettings[Key] extends string ? Key : never;
}[keyof PublicSettings];
const settingFields: Array<[PublicStringSettingKey, string, string]> = [
  ["baokuan_base_url", "爆款库地址", "http://127.0.0.1:2022"],
  ["obsidian_vault", "Obsidian Vault", "本地 Vault 目录"],
  ["topic_cards_dir", "选题卡目录", "Vault 内选题卡目录"],
  ["grok_base_url", "Grok 服务地址", "OpenAI 兼容 API 地址"],
  ["grok_model", "Grok 模型", "模型名称"],
  ["codex_binary_path", "Codex CLI 路径", "codex 可执行文件路径"],
  ["media_index_path", "素材索引", "媒体索引文件"],
  ["media_root", "媒体素材目录", "本地媒体根目录"],
  ["jianying_root", "剪映草稿目录", "剪映草稿根目录"],
  ["machine_profile_path", "混剪机器配置", "machine profile JSON 文件"],
];
const restartFieldLabels: Partial<Record<keyof PublicSettings, string>> = {
  listen_addr: "监听地址",
  data_root: "数据目录",
  baokuan_base_url: "爆款库地址",
  baokuan_mcp_executable: "爆款库连接程序",
  obsidian_vault: "Obsidian 目录",
  topic_cards_dir: "选题卡目录",
  grok_base_url: "Grok 服务地址",
  grok_model: "Grok 模型",
  codex_binary_path: "Codex 程序路径",
  media_index_path: "素材索引",
  media_root: "媒体素材目录",
  jianying_root: "剪映草稿目录",
  machine_profile_path: "混剪机器配置",
  app_server_enabled: "实时 Codex 对话服务",
  codex_workspace_roots: "Codex 工作目录白名单",
  codex_default_model: "默认模型",
  codex_default_reasoning_effort: "默认推理强度",
};

function taskTitle(task: Task) {
  return taskActionLabels[task.action || ""] || task.skill_name || task.type || "Codex 任务";
}

function taskMessageContent(content: string) {
  const value = content.trim();
  if (value === "Plaintext montage draft completed and validated.") {
    return "明文混剪草稿已生成并校验完成，等待登记到剪映。";
  }
  if (!value.startsWith("{")) return content;
  try {
    const envelope = JSON.parse(value) as Record<string, unknown>;
    if (envelope.schema_version !== "2.0" || typeof envelope.action !== "string") return content;
    const action = envelope.action;
    const status = envelope.status;
    if (status === "failed") return "任务没有完成，请查看下方失败原因。";
    if (status === "awaiting_input") return "Codex 需要你补充信息，请在下方回复。";
    if (action === "montage.execute") return "明文混剪草稿已生成并校验完成，尚未登记到剪映。";
    if (action === "topic.brainstorm") return "候选选题已生成，可以选择一个继续深化。";
    if (action === "topic.commit" || action === "topic.deepen") return "选题卡已处理完成，可在项目素材中查看。";
    if (action.startsWith("remix.")) return "文案结果已生成，可在项目素材中查看。";
    return typeof envelope.summary === "string" && envelope.summary.trim()
      ? envelope.summary
      : "任务已处理完成。";
  } catch {
    return content;
  }
}

function taskEventProgress(event: TaskEvent) {
  const kind = event.kind || event.Kind || "";
  const displayText = event.display_text || event.DisplayText || "";
  const raw = event.raw_json || event.RawJSON || "";
  if (displayText) return taskMessageContent(displayText);
  if (/baokuan_search_materials/i.test(raw)) return "正在检索爆款库素材";
  if (/baokuan_list_snippets/i.test(raw)) return "正在筛选可借鉴的爆款片段";
  if (
    /thread\.started|turn\.started/i.test(kind) ||
    /thread\.started|turn\.started/i.test(raw)
  )
    return "Codex 已启动，正在分析选题";
  if (/turn\.completed/i.test(kind) || /turn\.completed/i.test(raw))
    return "正在整理候选选题";
  if (/error|failed/i.test(kind) || /error|failed/i.test(raw))
    return "任务遇到问题，正在等待处理";
  if (/item\.started/i.test(kind) || /item\.started/i.test(raw))
    return "正在分析素材与选题方向";
  return "正在推进选题分析";
}

function taskProgressStatus(task: Task) {
  if (task.status === "failed")
    return task.error_message || "任务失败，请查看任务详情";
  if (task.status === "awaiting_input" || task.status === "waiting_input")
    return "Codex 正在等待你的回复";
  if (task.status === "completed") {
    if (task.action === "topic.brainstorm") return "候选选题已生成";
    if (task.action === "montage.execute") return "混剪草稿已处理完成";
    return "任务已完成";
  }
  return statusLabels[task.status] || task.status;
}

function normalizeSemanticEvents(events: SemanticEvent[]) {
  return events.map((event) => ({
    id: event.id || event.ID || "",
    sequence: event.sequence ?? event.Sequence ?? 0,
    kind: event.kind || event.Kind || "",
    phase: event.phase || event.Phase || "",
    level: event.level || event.Level || "",
    title: (event.title || event.Title || "").trim(),
    detail: event.detail || event.Detail || "",
    created_at: event.created_at || event.CreatedAt || "",
  }));
}

function taskTimeline(task: Task): string[] {
  const semantic = normalizeSemanticEvents(task.semantic_events || [])
    .filter((event) => event.title)
    .sort((left, right) => left.sequence - right.sequence);
  const messages = semantic.length
    ? semantic.map((event) => event.title)
    : (task.events || []).map(taskEventProgress);
  const unique = messages.filter(
    (message, index) => message && messages.indexOf(message) === index,
  );
  if (task.status === "failed" && task.error_message) {
    const withoutGenericFailure = unique.filter(
      (message) => message !== "任务遇到问题，正在等待处理",
    );
    if (!withoutGenericFailure.includes(task.error_message)) {
      withoutGenericFailure.push(task.error_message);
    }
    return withoutGenericFailure.slice(-4);
  }
  return unique.slice(-4);
}

function taskQuestions(task: Task): string[] {
  const message = [...(task.messages || [])]
    .reverse()
    .find((item) => item.role === "assistant" && item.question_schema);
  if (!message?.question_schema) return [];
  try {
    const parsed = JSON.parse(message.question_schema) as Array<
      string | { text?: string }
    >;
    return parsed
      .map((question) =>
        typeof question === "string" ? question : question.text || "",
      )
      .filter(Boolean);
  } catch {
    return [];
  }
}

function TaskModelFields({
  value,
  onChange,
  defaults,
  labelPrefix = "",
}: {
  value: TaskModelOverride;
  onChange: (value: TaskModelOverride) => void;
  defaults?: PublicSettings;
  labelPrefix?: string;
}) {
  const actualModel = value.model.trim() || defaults?.codex_default_model || "Codex 默认模型";
  const actualEffort =
    value.reasoningEffort || defaults?.codex_default_reasoning_effort || "Codex 默认强度";
  return (
    <details className="task-model-fields">
      <summary>模型与推理强度（可选）</summary>
      <div className="task-model-grid">
        <label>
          模型
          <input
            aria-label={`${labelPrefix}临时模型`}
            value={value.model}
            placeholder="继承默认模型"
            onChange={(event) => onChange({ ...value, model: event.target.value })}
          />
        </label>
        <label>
          推理强度
          <select
            aria-label={`${labelPrefix}临时推理强度`}
            value={value.reasoningEffort}
            onChange={(event) =>
              onChange({
                ...value,
                reasoningEffort: event.target.value as ReasoningEffort | "",
              })
            }
          >
            <option value="">继承默认强度</option>
            {reasoningEfforts.map((effort) => (
              <option key={effort} value={effort}>
                {effort}
              </option>
            ))}
          </select>
        </label>
      </div>
      <p>实际将使用：{actualModel} · {actualEffort}</p>
    </details>
  );
}

function App() {
  const [csrf, setCsrf] = useState("");
  const [authenticated, setAuthenticated] = useState<boolean | null>(null);
  const [password, setPassword] = useState("");
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  const [account, setAccount] = useState("");
  const [loading, setLoading] = useState(true);
  const [newAccount, setNewAccount] = useState("");
  const [accountBackground, setAccountBackground] = useState<File | null>(null);
  const [accountBackgroundReplacement, setAccountBackgroundReplacement] =
    useState<File | null>(null);
  const [newProject, setNewProject] = useState("");
  const [message, setMessage] = useState("");
  const [selected, setSelected] = useState<Project | null>(null);
  const [detail, setDetail] = useState<ProjectDetail | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState("");
  const [assetType, setAssetType] = useState("continuous_script");
  const [assetFile, setAssetFile] = useState<File | null>(null);
  const [preview, setPreview] = useState<{
    asset: Asset;
    text?: string;
  } | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settings, setSettings] = useState<Settings | null>(null);
  const [settingsDraft, setSettingsDraft] = useState<PublicSettings | null>(
    null,
  );
  const [secretDraft, setSecretDraft] = useState({
    grok_api_key: "",
    pexels_api_key: "",
  });
  const [ideaOpen, setIdeaOpen] = useState(false);
  const [ideaSession, setIdeaSession] = useState<IdeaSession | null>(null);
  const [ideaSessions, setIdeaSessions] = useState<IdeaSession[]>([]);
  const [ideaDraft, setIdeaDraft] = useState(false);
  const [ideaTask, setIdeaTask] = useState<Task | null>(null);
  const [ideaInput, setIdeaInput] = useState("");
  const [ideaCreatingProject, setIdeaCreatingProject] = useState("");
  const [ideaRefreshRevision, setIdeaRefreshRevision] = useState(0);
  const [projectTaskModel, setProjectTaskModel] = useState<TaskModelOverride>({
    model: "",
    reasoningEffort: "",
  });
  const [ideaTaskModel, setIdeaTaskModel] = useState<TaskModelOverride>({
    model: "",
    reasoningEffort: "",
  });
  const [taskOpen, setTaskOpen] = useState<Task | null>(null);
  const [taskAnswerInput, setTaskAnswerInput] = useState("");
  const [runtime, setRuntime] = useState<RuntimeStatus | null>(null);
  const [chatOpen, setChatOpen] = useState(false);
  const [chatSessions, setChatSessions] = useState<ChatSession[]>([]);
  const [chatCreating, setChatCreating] = useState(false);
  const [chatCreationSource, setChatCreationSource] = useState<"console" | "desktop">("console");
  const [chatDetail, setChatDetail] = useState<ChatDetail | null>(null);
  const [chatInput, setChatInput] = useState("");
  const [chatSending, setChatSending] = useState(false);
  const [chatRefreshRevision, setChatRefreshRevision] = useState(0);
  const [historyThreads, setHistoryThreads] = useState<HistoryThread[]>([]);
  const [historySource, setHistorySource] = useState("");
  const [directoryManifest, setDirectoryManifest] = useState<DirectoryManifest | null>(null);
  const [directoryManifestStatus, setDirectoryManifestStatus] = useState("");
  const [openingDirectory, setOpeningDirectory] = useState(false);
  const [urlRevision, setURLRevision] = useState(0);
  const handledURLRevisionRef = useRef(0);
  const selectedIDRef = useRef("");
  const detailGenerationRef = useRef(0);
  const detailAbortRef = useRef<AbortController | null>(null);
  const detailInFlightRef = useRef(false);
  const detailQueuedRef = useRef<Project | null>(null);
  const detailRefreshTimerRef = useRef<number | null>(null);
  const taskCacheRef = useRef(new Map<string, Task>());
  const taskOpenIDRef = useRef("");
  const chatSessionIDRef = useRef("");
  const chatGenerationRef = useRef(0);
  const chatAbortRef = useRef<AbortController | null>(null);
  const ideaSessionIDRef = useRef("");
  const ideaGenerationRef = useRef(0);
  const ideaAbortRef = useRef<AbortController | null>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const dialogWasOpenRef = useRef(false);
  const activeDialogRef = useRef<HTMLElement | null>(null);
  const nestedDialogFocusRef = useRef<HTMLElement[]>([]);
  const taskRestoreAbortRef = useRef<AbortController | null>(null);
  const activeTasks = useMemo(
    () =>
      tasks.filter((task) => liveTaskStatuses.has(task.status)),
    [tasks],
  );
  const publishingPackage = useMemo(
    () =>
      [...tasks]
        .filter((task) => task.status === "completed" && task.publishing_package)
        .sort(
          (left, right) =>
            new Date(right.created_at).getTime() - new Date(left.created_at).getTime(),
        )[0]?.publishing_package,
    [tasks],
  );
  const publishingShortTitles = useMemo(() => {
    if (!publishingPackage) return [];
    if (publishingPackage.short_titles?.length) return publishingPackage.short_titles;
    const recommended = publishingPackage.top_titles?.map((item) => item.title) || [];
    return recommended.length ? recommended : (publishingPackage.titles || []).slice(0, 5);
  }, [publishingPackage]);
  const publishingDescriptions = useMemo(() => {
    if (!publishingPackage) return [];
    const descriptions = publishingPackage.descriptions?.length
      ? publishingPackage.descriptions
      : publishingPackage.description
        ? [publishingPackage.description]
        : [];
    const topicLine = (publishingPackage.topics || []).join(" ");
    return descriptions.map((description) =>
      description.includes("#") || !topicLine
        ? description
        : `${description}\n${topicLine}`,
    );
  }, [publishingPackage]);
  const activeIdeaSessionID = ideaDraft ? undefined : ideaSession?.id;
  const activeTaskIDs = useMemo(
    () => activeTasks.map((task) => task.id).sort().join(","),
    [activeTasks],
  );
  const restartChangedFields = useMemo(() => {
    if (!settings?.restart_required || !settings.active_public) return [];
    const configured = settings.configured_public || settings.public;
    return (Object.keys(configured) as Array<keyof PublicSettings>)
      .filter(
        (key) =>
          JSON.stringify(configured[key]) !== JSON.stringify(settings.active_public?.[key]),
      )
      .map((key) => restartFieldLabels[key] || String(key));
  }, [settings]);

  const api = useCallback(
    async (path: string, init: RequestInit = {}) => {
      const method = (init.method || "GET").toUpperCase();
      const headers = new Headers(init.headers);
      if (!["GET", "HEAD", "OPTIONS"].includes(method) && csrf)
        headers.set("X-CSRF-Token", csrf);
      const response = await fetch(path, {
        ...init,
        headers,
        credentials: "same-origin",
      });
      if (response.status === 401) {
        setAuthenticated(false);
        setCsrf("");
      }
      return response;
    },
    [csrf],
  );

  const copyPublishingText = async (text: string, label: string) => {
    try {
      if (navigator.clipboard?.writeText) {
        try {
          await navigator.clipboard.writeText(text);
          setMessage(`${label}已复制，可以直接粘贴到视频号。`);
          return;
        } catch {
          // HTTP LAN access may expose Clipboard API but reject writes.
        }
      }
      const textarea = document.createElement("textarea");
      textarea.value = text;
      textarea.style.position = "fixed";
      textarea.style.opacity = "0";
      document.body.appendChild(textarea);
      textarea.focus();
      textarea.select();
      const copied = document.execCommand("copy");
      textarea.remove();
      if (!copied) throw new Error("copy command was rejected");
      setMessage(`${label}已复制，可以直接粘贴到视频号。`);
    } catch {
      setMessage(`${label}复制失败，请长按文字手动复制。`);
    }
  };

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [a, p, s] = await Promise.all([
        api("/api/accounts"),
        api("/api/projects"),
        api("/api/settings"),
      ]);
      if (!a.ok || !p.ok) throw new Error("读取控制台数据失败");
      setAccounts(await a.json());
      setProjects(await p.json());
      if (s.ok) setSettings((await s.json()) as Settings);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "控制台服务尚未连接");
    } finally {
      setLoading(false);
    }
  }, [api]);

  const loadDetail = useCallback(
    async (project: Project) => {
      if (detailInFlightRef.current && selectedIDRef.current === project.id) {
        detailQueuedRef.current = project;
        return;
      }
      detailAbortRef.current?.abort();
      const controller = new AbortController();
      detailAbortRef.current = controller;
      detailInFlightRef.current = true;
      const generation = ++detailGenerationRef.current;
      const projectID = project.id;
      setDetailError("");
      setDetailLoading(true);
      try {
        const [p, t] = await Promise.all([
          api(`/api/projects/${projectID}`, { signal: controller.signal }),
          api(`/api/tasks?project_id=${projectID}`, { signal: controller.signal }),
        ]);
        if (!p.ok || !t.ok) throw new Error("读取项目详情失败");
        const projectDetail = (await p.json()) as ProjectDetail;
        const listed = (await t.json()) as Task[];
        const recentIDs = new Set(
          [...listed]
            .sort(
              (left, right) =>
                new Date(right.created_at).getTime() - new Date(left.created_at).getTime(),
            )
            .slice(0, 6)
            .map((task) => task.id),
        );
        const fullTasks = await Promise.all(
          listed.map(async (task) => {
            const shouldHydrate =
              liveTaskStatuses.has(task.status) ||
              recentIDs.has(task.id) ||
              Boolean(task.action?.startsWith("remix.")) ||
              taskOpenIDRef.current === task.id;
            if (!shouldHydrate) return taskCacheRef.current.get(task.id) || task;
            const cached = taskCacheRef.current.get(task.id);
            const cacheIsStatic =
              task.action !== "montage.execute" ||
              (cached ? derivedMontagePhase(cached) === "registered" : false);
            if (
              cached &&
              cacheIsStatic &&
              !liveTaskStatuses.has(task.status) &&
              taskOpenIDRef.current !== task.id
            )
              return cached;
            const [taskResponse, progressResponse, resultResponse] = await Promise.all([
              api(`/api/tasks/${task.id}`, { signal: controller.signal }),
              api(`/api/tasks/${task.id}/semantic-events?limit=20`, { signal: controller.signal }),
              task.action === "montage.execute" || !liveTaskStatuses.has(task.status)
                ? api(`/api/tasks/${task.id}/result`, { signal: controller.signal })
                : Promise.resolve(null),
            ]);
            const full = taskResponse?.ok
              ? ((await taskResponse.json()) as Task)
              : { ...task };
            if (progressResponse?.ok) {
              const progress = (await progressResponse.json()) as {
                events?: SemanticEvent[];
              };
              full.semantic_events = normalizeSemanticEvents(progress.events || []);
            }
            if (resultResponse?.ok) Object.assign(full, await resultResponse.json());
            taskCacheRef.current.set(task.id, full);
            return full;
          }),
        );
        if (
          controller.signal.aborted ||
          generation !== detailGenerationRef.current ||
          selectedIDRef.current !== projectID ||
          projectDetail.project.id !== projectID
        ) return;
        setDetail(projectDetail);
        setDetailError("");
        setTasks(fullTasks);
      } catch (error) {
        if (!isAbortError(error) && selectedIDRef.current === projectID) {
          setDetailError("项目详情暂时无法读取");
          setMessage("项目刷新暂时中断，将在下次活动时重试。");
        }
      } finally {
        if (generation === detailGenerationRef.current) {
          detailInFlightRef.current = false;
          setDetailLoading(false);
          const queued = detailQueuedRef.current;
          detailQueuedRef.current = null;
          if (queued && selectedIDRef.current === queued.id)
            window.setTimeout(() => void loadDetail(queued), 0);
        }
      }
    },
    [api],
  );

  const scheduleDetailRefresh = useCallback(
    (project: Project) => {
      if (detailRefreshTimerRef.current !== null)
        window.clearTimeout(detailRefreshTimerRef.current);
      detailRefreshTimerRef.current = window.setTimeout(() => {
        detailRefreshTimerRef.current = null;
        if (selectedIDRef.current === project.id) void loadDetail(project);
      }, 300);
    },
    [loadDetail],
  );

  useEffect(() => {
    void (async () => {
      try {
        const response = await fetch("/api/auth/me", {
          credentials: "same-origin",
        });
        if (!response.ok) {
          setAuthenticated(false);
          return;
        }
        const me = (await response.json()) as { csrfToken?: string };
        setCsrf(me.csrfToken || "");
        setAuthenticated(true);
      } catch {
        setAuthenticated(false);
      }
    })();
  }, []);
  useEffect(() => {
    if (authenticated) void load();
  }, [authenticated, load]);
  useEffect(() => {
    if (!ideaOpen || !activeIdeaSessionID) return;
    const sessionID = activeIdeaSessionID;
    ideaSessionIDRef.current = sessionID;
    const generation = ++ideaGenerationRef.current;
    ideaAbortRef.current?.abort();
    const controller = new AbortController();
    ideaAbortRef.current = controller;
    let timer: number | undefined;
    let stopped = false;
    const refresh = async () => {
      try {
        const response = await api(`/api/ideas/${sessionID}`, { signal: controller.signal });
        if (!response.ok) return;
        const next = (await response.json()) as IdeaSessionDetail;
        if (
          stopped ||
          generation !== ideaGenerationRef.current ||
          ideaSessionIDRef.current !== sessionID ||
          next.session.id !== sessionID
        ) return;
        const messages = next.messages || [];
        setIdeaSession({ ...next.session, messages, candidates: next.candidates || [] });
        const taskID = [...messages].reverse().find((item) => item.task_id)?.task_id;
        if (!taskID) {
          setIdeaTask(null);
          return;
        }
        const taskResponse = await api(`/api/tasks/${taskID}`, { signal: controller.signal });
        if (
          taskResponse.ok &&
          !stopped &&
          generation === ideaGenerationRef.current &&
          ideaSessionIDRef.current === sessionID
        ) setIdeaTask((await taskResponse.json()) as Task);
      } catch (error) {
        if (!isAbortError(error)) {
          // Keep the last stable conversation visible; the next poll retries.
        }
      } finally {
        if (!stopped && !controller.signal.aborted)
          timer = window.setTimeout(() => void refresh(), 3500);
      }
    };
    void refresh();
    return () => {
      stopped = true;
      controller.abort();
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [api, ideaOpen, activeIdeaSessionID, ideaRefreshRevision]);
  useEffect(() => {
    if (!authenticated) return;
    let stopped = false;
    let timer: number | undefined;
    let controller: AbortController | null = null;
    const refresh = async () => {
      controller = new AbortController();
      try {
        const response = await api("/api/runtime", { signal: controller.signal });
        if (response.ok && !stopped) setRuntime((await response.json()) as RuntimeStatus);
      } catch (error) {
        if (!isAbortError(error)) {
          // Runtime badge keeps its last stable value while offline.
        }
      } finally {
        if (!stopped) timer = window.setTimeout(() => void refresh(), 7000);
      }
    };
    void refresh();
    return () => {
      stopped = true;
      controller?.abort();
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [authenticated, api]);
  useEffect(() => {
    if (!chatOpen || !chatDetail?.session.id) return;
    const sessionID = chatDetail.session.id;
    chatSessionIDRef.current = sessionID;
    const generation = ++chatGenerationRef.current;
    chatAbortRef.current?.abort();
    const controller = new AbortController();
    chatAbortRef.current = controller;
    let stopped = false;
    let timer: number | undefined;
    const refresh = async () => {
      try {
        const response = await api(`/api/chat/sessions/${sessionID}`, { signal: controller.signal });
        if (!response.ok) return;
        const next = (await response.json()) as ChatDetail;
        if (
          !stopped &&
          generation === chatGenerationRef.current &&
          chatSessionIDRef.current === sessionID &&
          next.session.id === sessionID
        ) setChatDetail(next);
      } catch (error) {
        if (!isAbortError(error)) {
          // Keep the last stable messages; the next poll retries.
        }
      } finally {
        if (!stopped && !controller.signal.aborted)
          timer = window.setTimeout(() => void refresh(), 2500);
      }
    };
    void refresh();
    return () => {
      stopped = true;
      controller.abort();
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [api, chatOpen, chatDetail?.session.id, chatRefreshRevision]);
  useEffect(() => {
    if (!selected || (!activeTaskIDs && !taskOpen)) return;
    const timer = window.setInterval(
      () => scheduleDetailRefresh(selected),
      taskOpen ? 5000 : 8000,
    );
    return () => window.clearInterval(timer);
  }, [selected, activeTaskIDs, taskOpen, scheduleDetailRefresh]);
  useEffect(() => {
    if (!selected || !activeTaskIDs) return;
    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    const sockets = activeTaskIDs.split(",").map((taskID) => {
      const socket = new WebSocket(
        `${protocol}//${location.host}/api/tasks/${taskID}/events?after=0`,
      );
      socket.onmessage = () => scheduleDetailRefresh(selected);
      return socket;
    });
    return () => sockets.forEach((socket) => socket.close());
  }, [selected, activeTaskIDs, scheduleDetailRefresh]);
  useEffect(() => {
    taskOpenIDRef.current = taskOpen?.id || "";
    if (!taskOpen) return;
    const latest = tasks.find((task) => task.id === taskOpen.id);
    if (!latest) {
      if (!tasks.length) return;
      setTaskOpen(null);
      writeTaskQuery("", "replace");
      return;
    }
    if (latest !== taskOpen) setTaskOpen(latest);
  }, [tasks, taskOpen]);
  useEffect(() => {
    setTaskAnswerInput("");
  }, [taskOpen?.id]);
  useEffect(() => {
    const assetID = taskOpen?.montage?.registered_asset?.id;
    setDirectoryManifest(null);
    setDirectoryManifestStatus("");
    if (!assetID) return;
    const controller = new AbortController();
    setDirectoryManifestStatus("正在读取剪映目录清单…");
    void (async () => {
      try {
        const response = await api(`/api/assets/${assetID}/directory-manifest`, {
          signal: controller.signal,
        });
        if (!response.ok) {
          if (!controller.signal.aborted)
            setDirectoryManifestStatus(
              response.status === 413
                ? "目录文件较多，暂时无法在控制台完整列出。"
                : "剪映目录清单暂时不可用，请在电脑上确认目录仍然存在。",
            );
          return;
        }
        const next = (await response.json()) as DirectoryManifest;
        if (!controller.signal.aborted && next.asset_id === assetID) {
          setDirectoryManifest(next);
          setDirectoryManifestStatus("");
        }
      } catch (error) {
        if (!isAbortError(error))
          setDirectoryManifestStatus("读取剪映目录清单失败，稍后打开任务时会重试。");
      }
    })();
    return () => controller.abort();
  }, [api, taskOpen?.montage?.registered_asset?.id]);
  useEffect(() => {
    const onPopState = () => setURLRevision((value) => value + 1);
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);
  useEffect(() => {
    if (!authenticated || loading) return;
    const route = parseLocation(window.location.pathname);
    const taskID = new URL(window.location.href).searchParams.get("task") || "";
    if (route.view === "projects") {
      if (!["/", "/projects", "/projects/"].includes(window.location.pathname)) {
        window.history.replaceState({}, "", `/projects${window.location.search}`);
      }
      if (
        !taskID &&
        selectedIDRef.current &&
        handledURLRevisionRef.current !== urlRevision
      ) {
        selectedIDRef.current = "";
        detailGenerationRef.current += 1;
        detailAbortRef.current?.abort();
        detailInFlightRef.current = false;
        detailQueuedRef.current = null;
        setSelected(null);
        setDetail(null);
        setDetailError("");
        setTasks([]);
      }
      handledURLRevisionRef.current = urlRevision;
      return;
    }
    const project = projects.find((item) => item.id === route.projectID);
    if (!project) {
      window.history.replaceState({}, "", `/projects${window.location.search}`);
      handledURLRevisionRef.current = urlRevision;
      return;
    }
    handledURLRevisionRef.current = urlRevision;
    if (selectedIDRef.current === project.id) return;
    selectedIDRef.current = project.id;
    detailQueuedRef.current = null;
    detailAbortRef.current?.abort();
    detailInFlightRef.current = false;
    setSelected(project);
    setDetail(null);
    setDetailError("");
    setTasks([]);
    void loadDetail(project);
  }, [authenticated, loadDetail, loading, projects, urlRevision]);
  useEffect(() => {
    if (!authenticated || !projects.length) return;
    const taskID = new URL(window.location.href).searchParams.get("task") || "";
    if (!taskID) {
      if (taskOpen) setTaskOpen(null);
      return;
    }
    if (taskOpen?.id === taskID) return;
    taskRestoreAbortRef.current?.abort();
    const controller = new AbortController();
    taskRestoreAbortRef.current = controller;
    void (async () => {
      try {
        const response = await api(`/api/tasks/${taskID}`, { signal: controller.signal });
        if (!response.ok) return;
        const restored = (await response.json()) as Task;
        if (controller.signal.aborted) return;
        const project = projects.find((item) => item.id === restored.project_id);
        if (project && selectedIDRef.current !== project.id) {
          detailAbortRef.current?.abort();
          detailInFlightRef.current = false;
          detailQueuedRef.current = null;
          selectedIDRef.current = project.id;
          setSelected(project);
          setDetail(null);
          setTasks([]);
          void loadDetail(project);
        }
        taskOpenIDRef.current = restored.id;
        setTaskOpen(restored);
      } catch (error) {
        if (!isAbortError(error)) setMessage("无法恢复链接中的任务详情。");
      }
    })();
    return () => controller.abort();
  }, [api, authenticated, loadDetail, projects, taskOpen, urlRevision]);
  useEffect(() => {
    const dialogs = Array.from(document.querySelectorAll<HTMLElement>('[role="dialog"]'));
    const active = dialogs[dialogs.length - 1];
    const dialogOpen = Boolean(
      selected || preview || settingsOpen || ideaOpen || chatOpen || taskOpen,
    );
    if (!dialogOpen) {
      if (dialogWasOpenRef.current) previousFocusRef.current?.focus();
      dialogWasOpenRef.current = false;
      activeDialogRef.current = null;
      nestedDialogFocusRef.current = [];
      return;
    }
    if (!dialogWasOpenRef.current)
      previousFocusRef.current = document.activeElement as HTMLElement | null;
    dialogWasOpenRef.current = true;
    const previousDialog = activeDialogRef.current;
    let nestedRestore: HTMLElement | null = null;
    if (previousDialog && previousDialog !== active) {
      if (previousDialog.isConnected)
        nestedDialogFocusRef.current.push(document.activeElement as HTMLElement);
      else nestedRestore = nestedDialogFocusRef.current.pop() || null;
    }
    activeDialogRef.current = active || null;
    const activeLayer =
      (active?.closest(".modal-backdrop, .drawer-backdrop") as HTMLElement | null) || active;
    if (activeLayer) {
      (activeLayer as HTMLElement & { inert: boolean }).inert = false;
      activeLayer.removeAttribute("aria-hidden");
    }
    if (active) {
      (active as HTMLElement & { inert: boolean }).inert = false;
      active.removeAttribute("aria-hidden");
    }
    const shell = document.querySelector<HTMLElement>(".shell");
    const inertRecords = Array.from(shell?.children || [])
      .filter((element) => element !== activeLayer)
      .map((element) => {
        const target = element as HTMLElement & { inert: boolean };
        const previous = { target, inert: target.inert, ariaHidden: target.getAttribute("aria-hidden") };
        target.inert = true;
        target.setAttribute("aria-hidden", "true");
        return previous;
      });
    dialogs.forEach((dialog) => {
      if (dialog !== active) {
        (dialog as HTMLElement & { inert: boolean }).inert = true;
        dialog.setAttribute("aria-hidden", "true");
      }
    });
    const focusable = () =>
      active
        ? Array.from(
            active.querySelectorAll<HTMLElement>(
              'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
            ),
          ).filter((element) => element.getClientRects().length > 0)
        : [];
    const focusTimer = window.setTimeout(() => {
      if (nestedRestore?.isConnected) nestedRestore.focus();
      else (focusable()[0] || active)?.focus();
    }, 0);
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Tab" && active) {
        const controls = focusable();
        if (!controls.length) {
          event.preventDefault();
          active.focus();
          return;
        }
        const first = controls[0];
        const last = controls[controls.length - 1];
        const current = document.activeElement;
        if (event.shiftKey && (current === first || !active.contains(current))) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && (current === last || !active.contains(current))) {
          event.preventDefault();
          first.focus();
        }
        return;
      }
      if (event.key !== "Escape") return;
      event.preventDefault();
      if (taskOpen) {
        setTaskOpen(null);
        writeTaskQuery("", "replace");
      } else if (chatOpen) setChatOpen(false);
      else if (ideaOpen) setIdeaOpen(false);
      else if (settingsOpen) setSettingsOpen(false);
      else if (preview) setPreview(null);
      else if (selected) {
        selectedIDRef.current = "";
        detailAbortRef.current?.abort();
        setSelected(null);
        setDetail(null);
        setTasks([]);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.clearTimeout(focusTimer);
      window.removeEventListener("keydown", onKeyDown);
      inertRecords.forEach(({ target, inert, ariaHidden }) => {
        target.inert = inert;
        if (ariaHidden === null) target.removeAttribute("aria-hidden");
        else target.setAttribute("aria-hidden", ariaHidden);
      });
      dialogs.forEach((dialog) => {
        if (dialog !== active) {
          (dialog as HTMLElement & { inert: boolean }).inert = false;
          dialog.removeAttribute("aria-hidden");
        }
      });
    };
  }, [chatOpen, ideaOpen, preview, selected, settingsOpen, taskOpen]);
  useEffect(() => () => {
    detailAbortRef.current?.abort();
    chatAbortRef.current?.abort();
    ideaAbortRef.current?.abort();
    taskRestoreAbortRef.current?.abort();
    if (detailRefreshTimerRef.current !== null)
      window.clearTimeout(detailRefreshTimerRef.current);
  }, []);

  const login = async (event: FormEvent) => {
    event.preventDefault();
    const response = await fetch("/api/auth/login", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password }),
    });
    if (!response.ok) {
      setMessage("口令不正确，或稍后再试。");
      return;
    }
    const result = (await response.json()) as { csrf_token?: string };
    setCsrf(result.csrf_token || "");
    setPassword("");
    setAuthenticated(true);
    setMessage("");
  };
  const logout = async () => {
    await api("/api/auth/logout", { method: "POST" });
    setAuthenticated(false);
    setCsrf("");
  };
  const visible = useMemo(
    () =>
      account
        ? projects.filter((project) => project.account_id === account)
        : projects,
    [projects, account],
  );
  const openTask = (task: Task) => {
    taskOpenIDRef.current = task.id;
    setTaskOpen(task);
    writeTaskQuery(task.id, "push");
    if (selected) scheduleDetailRefresh(selected);
  };
  const closeTask = () => {
    taskOpenIDRef.current = "";
    setTaskOpen(null);
    writeTaskQuery("", "replace");
  };
  const openProject = (project: Project) => {
    if (
      (selectedIDRef.current && selectedIDRef.current !== project.id) ||
      (taskOpen && taskOpen.project_id !== project.id)
    ) closeTask();
    selectedIDRef.current = project.id;
    detailQueuedRef.current = null;
    detailAbortRef.current?.abort();
    detailInFlightRef.current = false;
    setSelected(project);
    setDetail(null);
    setDetailError("");
    setTasks([]);
    window.history.pushState({}, "", `/projects/${project.id}`);
    void loadDetail(project);
  };
  const closeProject = () => {
    closeTask();
    selectedIDRef.current = "";
    detailGenerationRef.current += 1;
    detailAbortRef.current?.abort();
    detailInFlightRef.current = false;
    detailQueuedRef.current = null;
    if (detailRefreshTimerRef.current !== null)
      window.clearTimeout(detailRefreshTimerRef.current);
    setSelected(null);
    setDetail(null);
    setDetailError("");
    setTasks([]);
    window.history.pushState({}, "", "/projects");
  };
  const createAccount = async (event: FormEvent) => {
    event.preventDefault();
    if (!newAccount.trim() || !accountBackground) {
      setMessage("请输入账号名称并选择固定背景图。");
      return;
    }
    const body = new FormData();
    body.set("name", newAccount.trim());
    body.set("background", accountBackground);
    const response = await api("/api/accounts", { method: "POST", body });
    if (!response.ok) {
      setMessage("账号创建失败，请检查名称和背景图。");
      return;
    }
    setNewAccount("");
    setAccountBackground(null);
    await load();
  };
  const createProject = async (event: FormEvent) => {
    event.preventDefault();
    if (!newProject.trim() || !account) return;
    const response = await api("/api/projects", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ account_id: account, title: newProject.trim() }),
    });
    if (!response.ok) {
      setMessage("项目创建失败。");
      return;
    }
    setNewProject("");
    await load();
  };
  const ensureTopicCard = async () => {
    if (!selected) return;
    const project = selected;
    const response = await api(`/api/projects/${project.id}/topic-card`, {
      method: "POST",
    });
    if (!response.ok) {
      let reason = "无法为当前项目生成正式选题卡";
      try {
        const payload = (await response.json()) as { message?: string };
        if (payload.message) reason = payload.message;
      } catch {
        // Keep the concise fallback.
      }
      setMessage(`正式选题卡任务创建失败：${reason}`);
      return;
    }
    setMessage("已启动正式选题卡写入任务；完成后会显示在当前项目素材中，并同步到 Obsidian。");
    if (selectedIDRef.current === project.id) await loadDetail(project);
  };
  const startTask = async (type: string, prompt: string) => {
    if (type === "topic_select") {
      await openIdeaPlanner();
      return;
    }
    if (!selected) return;
    const project = selected;
    const projectDetail = detail;
    if (!projectDetail || projectDetail.project.id !== project.id) {
      setMessage("项目详情仍在刷新，请确认当前项目后再启动任务。");
      return;
    }
    if (type === "topic_deepen" && !projectDetail.assets?.topic_card) {
      await ensureTopicCard();
      return;
    }
    if (type === "remix" && projectDetail.assets?.continuous_script) {
      const currentScript = projectDetail.assets.continuous_script;
      const confirmed = window.confirm(
        `当前项目已有连续文案 v${currentScript.version}。\n\n再次二创会生成一个新版本并将它设为当前文案；旧版本仍会保留。\n\n确定继续吗？`,
      );
      if (!confirmed) return;
    }
    const response = await api(`/api/projects/${project.id}/tasks`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        account_id: project.account_id,
        type,
        prompt,
        ...(type === "remix" && projectDetail.assets?.topic_card
          ? { action: "remix.from_topic_card" }
          : {}),
        ...(projectTaskModel.model.trim()
          ? { model: projectTaskModel.model.trim() }
          : {}),
        ...(projectTaskModel.reasoningEffort
          ? { reasoning_effort: projectTaskModel.reasoningEffort }
          : {}),
      }),
    });
    if (!response.ok) {
      let reason = "请检查当前项目所需素材和配置";
      try {
        const payload = (await response.json()) as { message?: string };
        if (payload.message) reason = payload.message;
      } catch {
        // Keep the concise fallback.
      }
      setMessage(`Codex 任务创建失败：${reason}`);
    } else {
      setProjectTaskModel({ model: "", reasoningEffort: "" });
      if (selectedIDRef.current === project.id) await loadDetail(project);
    }
  };
  const deleteProject = async () => {
    if (!selected) return;
    if (
      !window.confirm(
        `确定删除项目“${selected.title}”吗？项目专属文案、配音、SRT、草稿、成片和任务记录都会一并删除，此操作无法恢复。`,
      )
    )
      return;
    const response = await api(`/api/projects/${selected.id}`, {
      method: "DELETE",
    });
    if (!response.ok) {
      let reason = "项目删除失败";
      try {
        const payload = (await response.json()) as { message?: string };
        if (payload.message) reason = payload.message;
      } catch {
        // Keep the concise fallback.
      }
      setMessage(reason);
      return;
    }
    const deletedTitle = selected.title;
    closeProject();
    setProjects((current) => current.filter((item) => item.id !== selected.id));
    setMessage(`项目“${deletedTitle}”已删除。`);
  };
  const answerTask = async (task: Task, providedAnswer?: string) => {
    const questions = taskQuestions(task);
    const prompt = questions.length
      ? `Codex 正在等待你回复：\n\n${questions.join("\n")}\n\n请输入回答：`
      : "请输入给 Codex 的回复";
    const answer = providedAnswer ?? window.prompt(prompt);
    if (!answer?.trim()) return;
    const response = await api(`/api/tasks/${task.id}/answer`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ answer: answer.trim() }),
    });
    if (!response.ok) setMessage("任务回复失败。");
    else {
      setTaskAnswerInput("");
      if (selected) await loadDetail(selected);
    }
  };
  const cancelTask = async (task: Task) => {
    if (
      !window.confirm(
        "确定停止这个 Codex 任务吗？\n\n停止后不会登记这次任务的产物，现有项目素材不会被覆盖。",
      )
    )
      return;
    const response = await api(`/api/tasks/${task.id}/cancel`, {
      method: "POST",
    });
    if (!response.ok) setMessage("任务停止失败。");
    else {
      setMessage("任务已停止；现有项目素材没有改动。");
      if (selected) await loadDetail(selected);
    }
  };
  const retryMontageRegistration = async (task: Task) => {
    const response = await api(`/api/tasks/${task.id}/retry-registration`, { method: "POST" });
    if (!response.ok) {
      setMessage(response.status === 409 ? "剪映草稿正在登记，无需重复操作。" : "剪映草稿登记重试失败。");
      return;
    }
    setMessage("已重新排队登记剪映草稿，不会重新运行 Codex。");
    if (selected) await loadDetail(selected);
  };
  const openRegisteredDirectory = async (assetID: string) => {
    if (openingDirectory) return;
    setOpeningDirectory(true);
    try {
      const response = await api(`/api/assets/${assetID}/open-directory`, { method: "POST" });
      setDirectoryManifestStatus(
        response.ok
          ? "已在这台电脑上打开剪映目录。"
          : response.status === 403
            ? "只能从控制台所在电脑打开目录。"
            : "未能打开剪映目录，请确认目录仍然存在。",
      );
    } catch {
      setDirectoryManifestStatus("打开剪映目录失败，请稍后重试。");
    } finally {
      setOpeningDirectory(false);
    }
  };
  const uploadAsset = async (event: FormEvent) => {
    event.preventDefault();
    if (!selected || !assetFile) return;
    const body = new FormData();
    body.set("file", assetFile);
    const response = await api(
      `/api/projects/${selected.id}/assets/${assetType}`,
      { method: "POST", body },
    );
    if (!response.ok) setMessage("素材上传失败，请检查文件格式。");
    else {
      setAssetFile(null);
      await loadDetail(selected);
    }
  };
  const replaceAccountBackground = async (event: FormEvent) => {
    event.preventDefault();
    if (!selected || !accountBackgroundReplacement) return;
    const body = new FormData();
    body.set("background", accountBackgroundReplacement);
    const response = await api(
      `/api/accounts/${selected.account_id}/background`,
      { method: "POST", body },
    );
    if (!response.ok) {
      setMessage("固定背景图上传失败，请选择 PNG、JPEG 或 WebP 图片。");
      return;
    }
    setAccountBackgroundReplacement(null);
    setMessage("固定背景图已更新，该账号下的项目都会使用新图片。");
    await load();
    await loadDetail(selected);
  };
  const openAsset = async (asset: Asset) => {
    const url = `/api/assets/${asset.id}/content`;
    if (!textAssets.has(asset.type)) {
      window.open(url, "_blank", "noopener,noreferrer");
      return;
    }
    const response = await api(url);
    if (!response.ok) {
      setMessage("素材预览读取失败。");
      return;
    }
    setPreview({ asset, text: await response.text() });
  };
  const openSettings = async () => {
    const response = await api("/api/settings");
    if (!response.ok) {
      setMessage("设置读取失败。");
      return;
    }
    const next = (await response.json()) as Settings;
    setSettings(next);
    setSettingsDraft({ ...next.public });
    setSecretDraft({ grok_api_key: "", pexels_api_key: "" });
    setSettingsOpen(true);
  };
  const saveSettings = async (event: FormEvent) => {
    event.preventDefault();
    if (!settingsDraft) return;
    const response = await api("/api/settings", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ public: settingsDraft, secrets: secretDraft }),
    });
    if (!response.ok) {
      setMessage("设置保存失败，请检查填写内容。");
      return;
    }
    const next = (await response.json()) as Settings;
    setSettings(next);
    setSettingsDraft({ ...next.public });
    setSecretDraft({ grok_api_key: "", pexels_api_key: "" });
    setMessage("设置已保存。");
  };

  if (authenticated === null)
    return <div className="splash">正在验证访问权限…</div>;
  if (!authenticated)
    return (
      <main className="login-page">
        <form className="login-card" onSubmit={login}>
          <span className="eyebrow">本机视频工作台</span>
          <h1>视频生产控制台</h1>
          <p>请输入管理口令后继续。</p>
          {message && <div className="notice">{message}</div>}
          <label>
            管理口令
            <input
              autoFocus
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              autoComplete="current-password"
            />
          </label>
          <button type="submit">进入控制台</button>
        </form>
      </main>
    );

  const createIdeaConversation = () => {
    ideaSessionIDRef.current = "";
    ideaGenerationRef.current += 1;
    ideaAbortRef.current?.abort();
    const planningAccount = account || accounts[0]?.id || undefined;
    setIdeaDraft(true);
    setIdeaSession({
      id: "draft",
      account_id: planningAccount,
      title: "新选题规划",
      status: "planning",
      messages: [],
      candidates: [],
    });
    setIdeaTask(null);
    setIdeaInput("");
    setIdeaOpen(true);
  };
  const openIdeaPlanner = async () => {
    const sessionsResponse = await api("/api/ideas");
    if (sessionsResponse.ok) {
      const payload = await sessionsResponse.json();
      const sessions = Array.isArray(payload) ? (payload as IdeaSession[]) : [];
      const planningAccount = account || accounts[0]?.id || "";
      const accountSessions = planningAccount
        ? sessions.filter((session) => session.account_id === planningAccount)
        : sessions;
      setIdeaSessions(accountSessions);
      const existing =
        accountSessions.find((session) => session.id === ideaSession?.id) ||
        accountSessions[0];
      if (existing) {
        setIdeaDraft(false);
        setIdeaSession(existing);
        setIdeaOpen(true);
        await refreshIdea(existing.id);
        return;
      }
    }
    createIdeaConversation();
  };
  const refreshIdea = async (id: string) => {
    ideaSessionIDRef.current = id;
    const generation = ++ideaGenerationRef.current;
    ideaAbortRef.current?.abort();
    const controller = new AbortController();
    ideaAbortRef.current = controller;
    try {
      const response = await api(`/api/ideas/${id}`, { signal: controller.signal });
      if (!response.ok) return;
      const next = (await response.json()) as IdeaSessionDetail;
      if (
        controller.signal.aborted ||
        generation !== ideaGenerationRef.current ||
        ideaSessionIDRef.current !== id ||
        next.session.id !== id
      ) return;
      const messages = next.messages || [];
      setIdeaSession({ ...next.session, messages, candidates: next.candidates || [] });
      setIdeaDraft(false);
      setIdeaSessions((current) =>
        current.map((session) => session.id === next.session.id ? next.session : session),
      );
      const taskID = [...messages].reverse().find((message) => message.task_id)?.task_id;
      if (!taskID) {
        setIdeaTask(null);
        setIdeaRefreshRevision((value) => value + 1);
        return;
      }
      const taskResponse = await api(`/api/tasks/${taskID}`, { signal: controller.signal });
      if (
        taskResponse.ok &&
        generation === ideaGenerationRef.current &&
        ideaSessionIDRef.current === id
      ) setIdeaTask((await taskResponse.json()) as Task);
      if (generation === ideaGenerationRef.current && ideaSessionIDRef.current === id)
        setIdeaRefreshRevision((value) => value + 1);
    } catch (error) {
      if (!isAbortError(error)) setMessage("选题对话暂时无法刷新，请稍后重试。");
    }
  };
  const switchIdeaConversation = async (session: IdeaSession) => {
    ideaSessionIDRef.current = session.id;
    setIdeaDraft(false);
    setIdeaSession(session);
    setIdeaTask(null);
    setIdeaInput("");
    await refreshIdea(session.id);
  };
  const sendIdeaMessage = async (event: FormEvent) => {
    event.preventDefault();
    if (!ideaSession || !ideaInput.trim()) return;
    const sessionSnapshot = ideaSession;
    const content = ideaInput.trim();
    setIdeaInput("");
    let sessionID = sessionSnapshot.id;
    let createdSessionID = "";
    if (ideaDraft) {
      const createResponse = await api("/api/ideas", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          account_id: sessionSnapshot.account_id || account || undefined,
          title: sessionSnapshot.title,
        }),
      });
      if (!createResponse.ok) {
        setMessage("选题会话创建失败");
        setIdeaInput(content);
        return;
      }
      const created = (await createResponse.json()) as IdeaSession;
      sessionID = created.id;
      ideaSessionIDRef.current = created.id;
      createdSessionID = created.id;
      setIdeaDraft(false);
      setIdeaSession({ ...created, messages: [], candidates: [] });
      setIdeaSessions((current) => [created, ...current]);
    }
    const response = await api(`/api/ideas/${sessionID}/messages`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        content,
        account_id: sessionSnapshot.account_id || account || undefined,
        ...(ideaTaskModel.model.trim() ? { model: ideaTaskModel.model.trim() } : {}),
        ...(ideaTaskModel.reasoningEffort
          ? { reasoning_effort: ideaTaskModel.reasoningEffort }
          : {}),
      }),
    });
    if (!response.ok) {
      if (ideaSessionIDRef.current !== sessionID) return;
      let errorMessage = "选题消息发送失败";
      try {
        const payload = (await response.json()) as {
          code?: string;
          message?: string;
        };
        if (payload.message) {
          errorMessage = `${errorMessage}：${payload.message}`;
        } else if (payload.code) {
          errorMessage = `${errorMessage}（${payload.code}）`;
        }
      } catch {
        // Keep the generic message when the server did not return JSON.
      }
      if (createdSessionID) {
        await api(`/api/ideas/${createdSessionID}`, { method: "DELETE" });
        setIdeaDraft(true);
        setIdeaSession({ ...sessionSnapshot, id: "draft", messages: [], candidates: [] });
        setIdeaSessions((current) => current.filter((item) => item.id !== createdSessionID));
      }
      setMessage(errorMessage);
      setIdeaInput(content);
      return;
    }
    setIdeaTaskModel({ model: "", reasoningEffort: "" });
    if (ideaSessionIDRef.current === sessionID) await refreshIdea(sessionID);
  };
  const deleteIdeaConversation = async (session: IdeaSession) => {
    if (session.id === "draft") {
      createIdeaConversation();
      return;
    }
    if (!window.confirm(`确定删除“${session.title}”吗？删除后无法恢复。`)) return;
    const response = await api(`/api/ideas/${session.id}`, { method: "DELETE" });
    if (!response.ok) {
      setMessage(response.status === 409 ? "该对话仍有运行中的任务，暂时不能删除" : "对话删除失败");
      return;
    }
    const remaining = ideaSessions.filter((item) => item.id !== session.id);
    setIdeaSessions(remaining);
    if (ideaSession?.id !== session.id) return;
    if (remaining.length) {
      await switchIdeaConversation(remaining[0]);
    } else {
      createIdeaConversation();
    }
  };
  const selectIdeaCandidate = async (candidate: IdeaCandidate) => {
    if (!ideaSession || ideaCreatingProject) return;
    const sessionID = ideaSession.id;
    setIdeaCreatingProject(candidate.id);
    try {
      const response = await api(`/api/ideas/${sessionID}/select`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          candidate_id: candidate.id,
          // The current sidebar account is the user's confirmation-time
          // choice and must override the account remembered by the planner.
          account_id: ideaSession.account_id || account || undefined,
        }),
      });
      if (!response.ok) {
        let reason = "候选题确认失败";
        try {
          const payload = (await response.json()) as { message?: string };
          if (payload.message) reason = payload.message;
        } catch {
          // Keep the concise fallback.
        }
        setMessage(reason);
        return;
      }
      const result = (await response.json()) as {
        project?: Project;
        topic_card_task_error?: string;
      };
      if (ideaSessionIDRef.current !== sessionID) return;
      if (result.project) {
        setProjects((current) => [
          result.project!,
          ...current.filter((item) => item.id !== result.project!.id),
        ]);
        setIdeaOpen(false);
        setAccount(result.project.account_id);
        setMessage(
          result.topic_card_task_error
            ? `项目已创建，但正式选题卡任务未启动：${result.topic_card_task_error}`
            : `项目已创建到“${accountName(result.project.account_id, accounts)}”，正在把正式选题卡写入 Obsidian。`,
        );
        openProject(result.project);
      } else await refreshIdea(sessionID);
    } finally {
      setIdeaCreatingProject("");
    }
  };

  const loadChatSession = async (session: ChatSession) => {
    const sessionID = session.id;
    chatSessionIDRef.current = sessionID;
    const generation = ++chatGenerationRef.current;
    chatAbortRef.current?.abort();
    const controller = new AbortController();
    chatAbortRef.current = controller;
    try {
      const response = await api(`/api/chat/sessions/${sessionID}`, { signal: controller.signal });
      if (!response.ok) {
        setMessage("实时 Codex 对话服务暂时不可用，请检查设置页状态。");
        return;
      }
      const next = (await response.json()) as ChatDetail;
      if (
        !controller.signal.aborted &&
        generation === chatGenerationRef.current &&
        chatSessionIDRef.current === sessionID &&
        next.session.id === sessionID
      ) {
        setChatDetail(next);
        setChatRefreshRevision((value) => value + 1);
      }
    } catch (error) {
      if (!isAbortError(error) && chatSessionIDRef.current === sessionID)
        setMessage("对话暂时离线，已保留当前消息。");
    }
  };

  const createGeneralChat = async (source: "console" | "desktop" = chatCreationSource) => {
    if (chatCreating) return;
    setChatCreating(true);
    setMessage("正在创建 Codex 对话，请稍候……");
    try {
      const response = await api("/api/chat/sessions", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          kind: "general",
          title: `${source === "desktop" ? "桌面版" : "控制台"}对话 ${new Date().toLocaleDateString("zh-CN")}`,
          source,
          skill_names: [],
        }),
      });
      if (!response.ok) {
        const failure = (await response.json().catch(() => null)) as
          | { code?: string; message?: string }
          | null;
        if (response.status === 404) {
          setMessage("实时 Codex 对话服务尚未在本次启动中加载。请重启控制台后再试。");
        } else if (failure?.message) {
          setMessage(`新建 Codex 对话失败：${failure.message}`);
        } else {
          setMessage("新建 Codex 对话失败，请稍后重试。");
        }
        return;
      }
      const created = (await response.json()) as ChatSession;
      setChatSessions((current) => [created, ...current]);
      await loadChatSession(created);
      setMessage(`${source === "desktop" ? "桌面版" : "控制台"} Codex 对话已创建，可以开始发送消息。`);
    } catch {
      setMessage("新建 Codex 对话失败：控制台暂时无法连接 Codex 服务。");
    } finally {
      setChatCreating(false);
    }
  };

  const openGeneralChat = async () => {
    setChatOpen(true);
    const response = await api("/api/chat/sessions");
    if (!response.ok) {
      setMessage("Codex 对话服务尚未启用。");
      return;
    }
    const sessions = (await response.json()) as ChatSession[];
    setChatSessions(sessions);
    const historyLimit = settings?.public.codex_history_limit || 10;
    const historyResponse = await api(`/api/codex/history?limit=${historyLimit}`);
    if (historyResponse.ok) setHistoryThreads((await historyResponse.json()) as HistoryThread[]);
    const active = sessions.find((item) => item.id === chatDetail?.session.id) || sessions[0];
    if (active) await loadChatSession(active);
    else await createGeneralChat();
  };

  const refreshHistory = async (source = historySource) => {
    const limit = settings?.public.codex_history_limit || 10;
    const suffix = source ? `&source=${encodeURIComponent(source)}` : "";
    const response = await api(`/api/codex/history?limit=${limit}${suffix}`);
    if (response.ok) setHistoryThreads((await response.json()) as HistoryThread[]);
  };

  const applyHistoryThread = async (thread: HistoryThread, mode: "resume" | "fork") => {
    const response = await api(`/api/codex/history/${encodeURIComponent(thread.id)}/${mode}`, { method: "POST" });
    if (!response.ok) {
      setMessage(
        response.status === 409
          ? "原会话正在桌面端运行，请选择“复制到控制台”。"
          : "历史对话接入失败，请检查实时 Codex 对话服务状态。",
      );
      return;
    }
    const session = (await response.json()) as ChatSession;
    setChatSessions((current) => [session, ...current.filter((item) => item.id !== session.id)]);
    await loadChatSession(session);
    await refreshHistory();
  };

  const sendChatMessage = async (event: FormEvent) => {
    event.preventDefault();
    if (!chatDetail || !chatInput.trim() || chatSending) return;
    const sessionID = chatSessionIDRef.current || chatDetail.session.id;
    if (sessionID !== chatDetail.session.id) return;
    const sessionSnapshot = chatDetail.session;
    const content = chatInput.trim();
    setChatInput("");
    setChatSending(true);
    try {
      const response = await api(`/api/chat/sessions/${sessionID}/messages`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          text: content,
          delivery: "auto",
          client_key: globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random()}`,
        }),
      });
      if (!response.ok) {
        setMessage("消息发送失败，请查看实时 Codex 对话服务状态。");
        setChatInput(content);
        return;
      }
      if (chatSessionIDRef.current === sessionID) await loadChatSession(sessionSnapshot);
    } catch (error) {
      if (!isAbortError(error) && chatSessionIDRef.current === sessionID) {
        setMessage("消息发送失败，已保留输入内容。");
        setChatInput(content);
      }
    } finally {
      setChatSending(false);
    }
  };

  const deleteChatSession = async (session: ChatSession) => {
    if (!window.confirm(`删除对话“${session.title}”吗？这只删除控制台映射，不会终止正在运行的 Codex。`)) return;
    const response = await api(`/api/chat/sessions/${session.id}`, { method: "DELETE" });
    if (!response.ok) {
      setMessage("删除对话失败。");
      return;
    }
    const remaining = chatSessions.filter((item) => item.id !== session.id);
    setChatSessions(remaining);
    if (chatSessionIDRef.current !== session.id) return;
    if (remaining.length) await loadChatSession(remaining[0]);
    else {
      chatSessionIDRef.current = "";
      chatGenerationRef.current += 1;
      chatAbortRef.current?.abort();
      setChatDetail(null);
    }
  };

  const visibleChatMessages =
    chatDetail?.messages.filter((item) => !isTechnicalChatMessage(item)) || [];
  const technicalChatMessages =
    chatDetail?.messages.filter(isTechnicalChatMessage) || [];
  const openMontagePhase = taskOpen?.montage ? derivedMontagePhase(taskOpen) : "";
  const modalLayerOpen = Boolean(
    selected || preview || settingsOpen || ideaOpen || chatOpen || taskOpen,
  );
  const isLoopbackBrowser = ["localhost", "127.0.0.1", "::1", "[::1]"].includes(
    window.location.hostname.toLowerCase(),
  );

  return (
    <div className="shell">
      <header aria-hidden={modalLayerOpen || undefined}>
        <div>
          <span className="eyebrow">本机视频工作台</span>
          <h1>视频生产控制台</h1>
        </div>
        <div className="status">
          <span className="dot" />
          本地服务 · 共用爆款库
          {runtime && (
            <span
              className={
                runtime.Running >= runtime.Limit || runtime.Queued > 0
                  ? "runtime-warning"
                  : "runtime-state"
              }
            >
              CLI {runtime.Running}/{runtime.Limit}
              {runtime.Queued > 0 ? ` · 排队 ${runtime.Queued}` : ""}
            </span>
          )}
          <button
            className="header-button"
            onClick={() => void openIdeaPlanner()}
          >
            给我选题
          </button>
          <button className="header-button chat-entry" onClick={() => void openGeneralChat()}>
            Codex 对话
          </button>
          <button className="header-button" onClick={() => void openSettings()}>
            设置
          </button>
          <button className="header-button" onClick={() => void logout()}>
            退出
          </button>
        </div>
      </header>
      <div className="layout" aria-hidden={modalLayerOpen || undefined}>
        <aside>
          <div className="aside-title">
            账号 <span>{accounts.length}</span>
          </div>
          <button
            className={!account ? "selected" : ""}
            onClick={() => setAccount("")}
          >
            全部账号
          </button>
          {accounts.map((item) => (
            <button
              key={item.id}
              className={account === item.id ? "selected" : ""}
              onClick={() => setAccount(item.id)}
            >
              {item.name}
            </button>
          ))}
          <form onSubmit={createAccount} className="add-account">
            <input
              value={newAccount}
              onChange={(event) => setNewAccount(event.target.value)}
              placeholder="添加账号名称"
            />
            <label className="background-pick">
              {accountBackground ? "已选择背景图" : "选择固定背景图"}
              <input
                type="file"
                accept="image/png,image/jpeg,image/webp"
                onChange={(event) =>
                  setAccountBackground(event.target.files?.[0] || null)
                }
              />
            </label>
            <button type="submit">添加账号</button>
          </form>
          <div className="aside-foot">
            每个账号使用一张固定背景图；每个项目独立管理文案、配音、字幕和成片。
          </div>
        </aside>
        <main>
          <div className="toolbar">
            <div>
              <div className="muted">
                {account
                  ? accounts.find((item) => item.id === account)?.name
                  : "全部账号"}
              </div>
              <h2>视频项目</h2>
            </div>
            <form onSubmit={createProject} className="new-project">
              <input
                value={newProject}
                onChange={(event) => setNewProject(event.target.value)}
                placeholder={account ? "新建项目标题" : "先选择账号"}
              />
              <button disabled={!account}>新建项目</button>
            </form>
          </div>
          {message && (
            <div className="notice">
              {message}
              <button onClick={() => setMessage("")}>关闭</button>
            </div>
          )}
          {loading ? (
            <div className="empty">正在读取项目…</div>
          ) : (
            <>
              <div className="board-help">
                <strong>每张卡片 = 一个完整视频项目</strong>
                <span>每一列 = 项目当前制作阶段；文案、配音、SRT、草稿和成片都保存在对应项目详情内，不会串到其他项目。</span>
              </div>
              <div className="board">
                {stages.map((stage) => (
                  <section className="column" key={stage}>
                    <div className="column-head">
                      <span>{stageLabel(stage)}</span>
                      <b>
                        {
                          visible.filter((project) => project.stage === stage)
                            .length
                        }
                      </b>
                    </div>
                    {visible
                      .filter((project) => project.stage === stage)
                      .map((project) => (
                        <button
                          className="project"
                          key={project.id}
                          onClick={() => openProject(project)}
                        >
                          <strong>{project.title}</strong>
                          <small>
                            项目 #{project.id.slice(0, 8)} · {projectStageHint(project.stage)}
                          </small>
                          <div className="project-foot">
                            <span>
                              {accountName(project.account_id, accounts)}
                            </span>
                            <span className="pulse">●</span>
                          </div>
                        </button>
                      ))}
                  </section>
                ))}
              </div>
            </>
          )}
        </main>
      </div>
      {selected && (
        <div
          className="drawer-backdrop"
          aria-hidden={Boolean(taskOpen || chatOpen || ideaOpen || settingsOpen || preview) || undefined}
          onClick={closeProject}
        >
          <aside
            className="drawer"
            role="dialog"
            aria-modal="true"
            aria-labelledby="project-dialog-title"
            tabIndex={-1}
            onClick={(event) => event.stopPropagation()}
          >
            <div className="drawer-head">
              <div>
                <span className="muted">
                  {accountName(selected.account_id, accounts)}
                </span>
                <h2 id="project-dialog-title">{selected.title}</h2>
                <span className="project-identity">项目 #{selected.id.slice(0, 8)}</span>
              </div>
              <button
                className="close"
                onClick={closeProject}
                aria-label="返回项目看板"
              >
                <ArrowLeft aria-hidden="true" size={20} />
              </button>
            </div>
            {detailLoading && !detail ? (
              <div className="empty">正在读取详情…</div>
            ) : detail ? (
                <>
                  <section className="drawer-section">
                    <h3>项目状态</h3>
                    <div className="detail-meta">
                      <span className="stage-badge">
                        {stageLabel(detail.project.stage)}
                      </span>
                      <span>
                        更新于 {formatDate(detail.project.updated_at)}
                      </span>
                    </div>
                    {detail.project.stage === "topic" && !detail.assets?.topic_card ? (
                      <p className="warning">尚未生成正式选题卡；项目专属文案、配音和字幕也还未产生。</p>
                    ) : detail.missing_assets?.length ? (
                      <p className="warning">
                        待补充：
                        {detail.missing_assets
                          .map((item) => assetLabels[item] || item)
                          .join("、")}
                      </p>
                    ) : (
                      <p className="ok">
                        当前已登记 {Object.keys(detail.assets || {}).length} 项项目专属素材
                        {detail.background_reference ? "，并继承 1 张账号固定背景图" : ""}。
                      </p>
                    )}
                  </section>
                  {detail.topic_context && (
                    <section className="drawer-section topic-context">
                      <h3>当前项目选中的题</h3>
                      <strong>{detail.topic_context.title}</strong>
                      {detail.topic_context.summary && <p>{detail.topic_context.summary}</p>}
                      <dl>
                        {detail.topic_context.mother_theme && <><dt>母题</dt><dd>{detail.topic_context.mother_theme}</dd></>}
                        {detail.topic_context.family_conflict && <><dt>家庭冲突</dt><dd>{detail.topic_context.family_conflict}</dd></>}
                        {detail.topic_context.anomaly_framing && <><dt>异常定性</dt><dd>{detail.topic_context.anomaly_framing}</dd></>}
                        {detail.topic_context.narrative_entry && <><dt>叙事入口</dt><dd>{detail.topic_context.narrative_entry}</dd></>}
                        {typeof detail.topic_context.score === "number" && <><dt>选题评分</dt><dd>{detail.topic_context.score}</dd></>}
                      </dl>
                      {!!detail.topic_context.source_refs?.length && (
                        <details>
                          <summary>引用来源（{detail.topic_context.source_refs.length}）</summary>
                          <ul>{detail.topic_context.source_refs.map((ref) => <li key={ref}>{ref}</li>)}</ul>
                        </details>
                      )}
                    </section>
                  )}
                  {publishingPackage && (
                    <section className="drawer-section publishing-desk">
                      <div className="publishing-head">
                        <div>
                          <span className="publishing-kicker">发布时直接使用</span>
                          <h3>视频号发布信息</h3>
                        </div>
                        <span className="publishing-ready">已生成</span>
                      </div>
                      <div className="publishing-block">
                        <div className="publishing-label">
                          <strong>短标题</strong>
                          <span>点击任意一条即可复制</span>
                        </div>
                        <div className="short-title-grid">
                          {publishingShortTitles.map((title, index) => (
                            <button
                              type="button"
                              className="short-title-option"
                              key={`${title}-${index}`}
                              onClick={() => void copyPublishingText(title, "短标题")}
                            >
                              <span>{title}</span>
                              <small>复制</small>
                            </button>
                          ))}
                        </div>
                      </div>
                      <div className="publishing-block">
                        <div className="publishing-label">
                          <strong>视频描述</strong>
                          <span>已包含发布话题</span>
                        </div>
                        <div className="description-stack">
                          {publishingDescriptions.map((description, index) => (
                            <article className="description-option" key={`${description}-${index}`}>
                              <p>{description}</p>
                              <button
                                type="button"
                                onClick={() => void copyPublishingText(description, "视频描述")}
                              >
                                复制整段
                              </button>
                            </article>
                          ))}
                        </div>
                      </div>
                      {!publishingShortTitles.length && !publishingDescriptions.length && (
                        <p className="warning">发布包存在，但没有可用的短标题或视频描述，请重新运行二创文案任务。</p>
                      )}
                    </section>
                  )}
                  <section className="drawer-section">
                    <h3>启动工作流</h3>
                    <TaskModelFields
                      value={projectTaskModel}
                      onChange={setProjectTaskModel}
                      defaults={settings?.public}
                    />
                    <div className="workflow-actions">
                      <button
                        onClick={() =>
                          void startTask(
                            "topic_select",
                            "给我选题，并在 Obsidian 创建候选选题卡。",
                          )
                        }
                      >
                        给我选题
                      </button>
                      <button
                        onClick={() =>
                          void startTask(
                            "topic_deepen",
                            "深化当前选题卡，从爆款库补充依据和可借鉴片段。",
                          )
                        }
                      >
                        {detail.assets?.topic_card ? "深化一下" : "生成正式选题卡"}
                      </button>
                      <button
                        onClick={() =>
                          void startTask(
                            "remix",
                            "根据当前项目素材完成财经爆款二创。",
                          )
                        }
                      >
                        二创文案
                      </button>
                      <button
                        onClick={() =>
                          void startTask(
                            "spoken_format",
                            "把连续版文案转换为口播稿，不删词不漏段。",
                          )
                        }
                      >
                        口播稿
                      </button>
                      <button
                        onClick={() =>
                          void startTask(
                            "montage",
                            "使用当前文案、配音、SRT 和固定背景图生成混剪草稿。",
                          )
                        }
                      >
                        生成混剪
                      </button>
                    </div>
                  </section>
                  <section className="drawer-section">
                    <h3>当前项目专属素材</h3>
                    <p className="section-help">下面的文案、配音、SRT、草稿和成片只属于项目 #{selected.id.slice(0, 8)}。</p>
                    <form className="asset-upload" onSubmit={uploadAsset}>
                      <select
                        value={assetType}
                        onChange={(event) => setAssetType(event.target.value)}
                      >
                        {Object.entries(uploadAssetLabels).map(([value, label]) => (
                          <option key={value} value={value}>
                            {label}
                          </option>
                        ))}
                      </select>
                      <input
                        type="file"
                        onChange={(event) =>
                          setAssetFile(event.target.files?.[0] || null)
                        }
                      />
                      <button disabled={!assetFile}>上传</button>
                    </form>
                    <div className="asset-list">
                      {Object.entries(detail.assets || {}).map(
                        ([type, asset]) => (
                          <button
                            className="asset-row"
                            key={type}
                            onClick={() => void openAsset(asset)}
                          >
                            <span>{assetLabels[type] || type}</span>
                            <div>
                              <strong>{asset.filename}</strong>
                              <small>
                                属于“{selected.title}” · {formatSize(asset.size)} · v{asset.version} · 查看
                              </small>
                            </div>
                          </button>
                        ),
                      )}
                      {!Object.keys(detail.assets || {}).length && (
                        <p className="muted">暂无项目素材</p>
                      )}
                    </div>
                    <h3 className="inherited-title">账号继承素材</h3>
                    <p className="section-help">固定背景图属于账号，可供该账号下的每个项目使用，不会复制成多份。</p>
                    <div className="asset-list">
                      {detail.background_reference ? (
                        <>
                          <button
                            className="asset-row inherited"
                            onClick={() => void openAsset(detail.background_reference!)}
                          >
                            <span>账号固定背景图</span>
                            <div>
                              <strong>{detail.background_reference.filename}</strong>
                              <small>
                                {detail.background_reference.status === "missing"
                                  ? "文件不存在，请重新上传"
                                  : `${formatSize(detail.background_reference.size)} · 继承自账号 · 查看`}
                              </small>
                            </div>
                          </button>
                          <form
                            className="background-replace"
                            onSubmit={replaceAccountBackground}
                          >
                            <label>
                              <span>替换固定背景图</span>
                              <input
                                type="file"
                                accept="image/png,image/jpeg,image/webp"
                                onChange={(event) =>
                                  setAccountBackgroundReplacement(
                                    event.target.files?.[0] || null,
                                  )
                                }
                              />
                            </label>
                            <button disabled={!accountBackgroundReplacement}>
                              重新上传
                            </button>
                          </form>
                        </>
                      ) : (
                        <>
                          <p className="muted">当前账号尚未配置固定背景图</p>
                          <form
                            className="background-replace"
                            onSubmit={replaceAccountBackground}
                          >
                            <label>
                              <span>上传固定背景图</span>
                              <input
                                type="file"
                                accept="image/png,image/jpeg,image/webp"
                                onChange={(event) =>
                                  setAccountBackgroundReplacement(
                                    event.target.files?.[0] || null,
                                  )
                                }
                              />
                            </label>
                            <button disabled={!accountBackgroundReplacement}>
                              上传
                            </button>
                          </form>
                        </>
                      )}
                    </div>
                  </section>
                  <section className="drawer-section">
                    <h3>
                      Codex 任务 <span className="count">{tasks.length}</span>
                    </h3>
                    <div className="task-list">
                      {tasks.map((task) => (
                        <article
                          className="task-row"
                          key={task.id}
                          role="button"
                          tabIndex={0}
                          aria-label={`打开任务详情：${taskTitle(task)}`}
                          onClick={(event) => {
                            const interactive = (event.target as HTMLElement).closest(
                              "button, summary, a, input, select, textarea",
                            );
                            if (interactive && interactive !== event.currentTarget) return;
                            openTask(task);
                          }}
                          onKeyDown={(event) => {
                            if (event.target !== event.currentTarget) return;
                            if (event.key === "Enter" || event.key === " ") {
                              event.preventDefault();
                              openTask(task);
                            }
                          }}
                        >
                          <div className="task-top">
                            <strong>{taskTitle(task)}</strong>
                            <span
                              className={`task-status status-${task.status}`}
                            >
                              {statusLabels[task.status] || task.status}
                            </span>
                          </div>
                          {(task.model || task.reasoning_effort) && (
                            <p className="task-model-actual">
                              {[task.model, task.reasoning_effort].filter(Boolean).join(" · ")}
                            </p>
                          )}
                          {task.prompt_snapshot && (
                            <details>
                              <summary>发送给 CLI 的任务说明</summary>
                              <pre>{task.prompt_snapshot}</pre>
                            </details>
                          )}
                          {task.messages?.length ? (
                            <details>
                              <summary>
                                对话记录（{task.messages.length}）
                              </summary>
                              {task.messages.map((item, index) => (
                                <div className={`task-message ${item.role}`} key={item.id || `${item.role}-${item.created_at}-${index}`}>
                                  <b>{item.role === "user" ? "你" : "Codex"}</b>
                                  <p>{taskMessageContent(item.content)}</p>
                                </div>
                              ))}
                            </details>
                          ) : null}
                          {taskTimeline(task).length ? (
                            <ol className="task-timeline" aria-label="任务进度">
                              {taskTimeline(task).map((item, index) => (
                                <li key={`${task.id}-progress-${index}`}>{item}</li>
                              ))}
                            </ol>
                          ) : null}
                          {task.result_summary && <p>{task.result_summary}</p>}
                          {task.error_message && (
                            <p className="warning">{task.error_message}</p>
                          )}
                          {taskQuestions(task).length > 0 && (
                            <div className="task-question">
                              <strong>Codex 正在问：</strong>
                              {taskQuestions(task).map((question, index) => (
                                <p key={`${task.id}-question-${index}`}>{question}</p>
                              ))}
                            </div>
                          )}
                          {cancellableTaskStatuses.has(task.status) && (
                            <div>
                              {["awaiting_input", "waiting_input"].includes(task.status) &&
                                (task.model || task.reasoning_effort) && (
                                <p className="task-model-continue">
                                  继续使用：{[task.model, task.reasoning_effort].filter(Boolean).join(" · ")}
                                </p>
                              )}
                              <div className="task-actions">
                                {["awaiting_input", "waiting_input"].includes(task.status) && (
                                  <button
                                    onClick={(event) => {
                                      event.stopPropagation();
                                      void answerTask(task);
                                    }}
                                  >
                                    回复
                                  </button>
                                )}
                                <button
                                  className="secondary"
                                  onClick={(event) => {
                                    event.stopPropagation();
                                    void cancelTask(task);
                                  }}
                                >
                                  停止任务
                                </button>
                              </div>
                            </div>
                          )}
                        </article>
                      ))}
                      {!tasks.length && (
                        <p className="muted">暂无 Codex 任务</p>
                      )}
                    </div>
                  </section>
                  <section className="drawer-section danger-zone">
                    <h3>项目管理</h3>
                    <button className="delete-project" onClick={() => void deleteProject()}>
                      删除当前视频项目
                    </button>
                  </section>
                </>
            ) : detailError ? (
              <div className="detail-load-error" role="alert">
                <strong>{detailError}</strong>
                <p>项目已经创建并保留，只有详情读取暂时中断，不会丢失项目。</p>
                <button type="button" onClick={() => void loadDetail(selected)}>
                  重试读取详情
                </button>
              </div>
            ) : null}
          </aside>
        </div>
      )}
      {preview && (
        <div className="modal-backdrop" onClick={() => setPreview(null)}>
          <section
            className="preview-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="preview-dialog-title"
            tabIndex={-1}
            onClick={(event) => event.stopPropagation()}
          >
            <div className="drawer-head">
              <div>
                <span className="muted">
                  {assetLabels[preview.asset.type] || preview.asset.type}
                </span>
                <h2 id="preview-dialog-title">{preview.asset.filename}</h2>
              </div>
              <button className="close" onClick={() => setPreview(null)}>
                ×
              </button>
            </div>
            <pre className="asset-text">{preview.text}</pre>
          </section>
        </div>
      )}
      {settingsOpen && settingsDraft && (
        <div className="modal-backdrop" onClick={() => setSettingsOpen(false)}>
          <form
            className="settings-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="settings-dialog-title"
            tabIndex={-1}
            onClick={(event) => event.stopPropagation()}
            onSubmit={saveSettings}
          >
            <div className="drawer-head">
              <div>
                <span className="muted">本地配置</span>
                <h2 id="settings-dialog-title">控制台设置</h2>
              </div>
              <button
                type="button"
                className="close"
                onClick={() => setSettingsOpen(false)}
              >
                ×
              </button>
            </div>
            <p className="settings-note">
              密钥不会回显；留空表示保持现有值不变。
            </p>
            <p className="settings-note">
              默认值只影响之后新建的任务，不会修改运行中任务，也不会改写本机 Codex 全局配置。
            </p>
            {settings?.restart_required ? (
              <div className="restart-required" role="status">
                <strong>配置已保存，重启控制台后生效</strong>
                <p>
                  并发数、历史显示数量等热更新项已经立即生效；路径、实时 Codex 对话服务、密钥等启动配置会在重启后启用。
                </p>
                {restartChangedFields.length ? (
                  <p>等待重启：{restartChangedFields.join("、")}</p>
                ) : null}
              </div>
            ) : null}
            <label className="settings-field">
              默认模型
              <input
                value={settingsDraft.codex_default_model || ""}
                onChange={(event) =>
                  setSettingsDraft({
                    ...settingsDraft,
                    codex_default_model: event.target.value,
                  })
                }
              />
            </label>
            <label className="settings-field">
              默认推理强度
              <select
                value={settingsDraft.codex_default_reasoning_effort || "medium"}
                onChange={(event) =>
                  setSettingsDraft({
                    ...settingsDraft,
                    codex_default_reasoning_effort: event.target.value as ReasoningEffort,
                  })
                }
              >
                {reasoningEfforts.map((effort) => (
                  <option key={effort} value={effort}>
                    {effort}
                  </option>
                ))}
              </select>
            </label>
            <label className="settings-field">
              同时运行任务数
              <select
                value={settingsDraft.max_codex_concurrency}
                onChange={(event) =>
                  setSettingsDraft({
                    ...settingsDraft,
                    max_codex_concurrency: Number(event.target.value),
                  })
                }
              >
                {[1, 2, 3, 4].map((value) => (
                  <option key={value} value={value}>
                    {value}
                  </option>
                ))}
              </select>
            </label>
            <label className="settings-field checkbox-field">
              <input
                type="checkbox"
                checked={settingsDraft.app_server_enabled || false}
                onChange={(event) =>
                  setSettingsDraft({
                    ...settingsDraft,
                    app_server_enabled: event.target.checked,
                  })
                }
              />
              启用实时 Codex 对话服务
              <small>开启后可新建对话、查看历史并在任务运行中发送引导；保存后需要重启控制台。</small>
            </label>
            <label className="settings-field">
              本机历史显示数量
              <input
                type="number"
                min={5}
                max={50}
                value={settingsDraft.codex_history_limit || 10}
                onChange={(event) =>
                  setSettingsDraft({
                    ...settingsDraft,
                    codex_history_limit: Number(event.target.value),
                  })
                }
              />
              <small>默认显示最近 10 条，可设置 5—50 条。</small>
            </label>
            <label className="settings-field">
              Codex 工作目录白名单（每行一个绝对路径）
              <textarea
                value={(settingsDraft.codex_workspace_roots || []).join("\n")}
                onChange={(event) =>
                  setSettingsDraft({
                    ...settingsDraft,
                    codex_workspace_roots: event.target.value
                      .split("\n")
                      .map((value) => value.trim())
                      .filter(Boolean),
                  })
                }
              />
            </label>
            {settingFields.map(([key, label, placeholder]) => (
              <label className="settings-field" key={key}>
                {label}
                <input
                  value={settingsDraft[key] || ""}
                  placeholder={placeholder}
                  onChange={(event) =>
                    setSettingsDraft({
                      ...settingsDraft,
                      [key]: event.target.value,
                    })
                  }
                />
              </label>
            ))}
            <div className="secret-grid">
              {(["grok_api_key", "pexels_api_key"] as const).map((key) => (
                <label className="settings-field" key={key}>
                  {key === "grok_api_key" ? "Grok API 密钥" : "Pexels API 密钥"}
                  <small>
                    {settings?.secrets[key]?.configured
                      ? "已配置，输入新值才会替换"
                      : "未配置"}
                  </small>
                  <input
                    type="password"
                    value={secretDraft[key]}
                    placeholder="留空保持不变"
                    onChange={(event) =>
                      setSecretDraft({
                        ...secretDraft,
                        [key]: event.target.value,
                      })
                    }
                  />
                </label>
              ))}
            </div>
            <button className="save-settings" type="submit">
              保存设置
            </button>
          </form>
        </div>
      )}
      {ideaOpen && ideaSession && (
        <div className="modal-backdrop">
          <section
            className="preview-modal idea-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="idea-dialog-title"
            tabIndex={-1}
          >
            <div className="drawer-head">
              <div>
                <span className="muted">
                  选题规划 ·{" "}
                  {ideaSession.account_id
                    ? accountName(ideaSession.account_id, accounts)
                    : "未指定账号"}
                </span>
                <h2 id="idea-dialog-title">{ideaSession.title}</h2>
              </div>
              <div className="idea-head-actions">
                <label className="idea-account-control">
                  <span>选题账号</span>
                  <select
                    aria-label="选题账号"
                    value={ideaSession.account_id || ""}
                    onChange={(event) =>
                      setIdeaSession({
                        ...ideaSession,
                        account_id: event.target.value || undefined,
                      })
                    }
                  >
                    <option value="">请选择账号</option>
                    {accounts.map((item) => (
                      <option key={item.id} value={item.id}>
                        {item.name}
                      </option>
                    ))}
                  </select>
                </label>
                <button className="close" onClick={() => setIdeaOpen(false)}>
                  ×
                </button>
              </div>
            </div>
            <div className="idea-workspace">
              <nav className="idea-conversations" aria-label="选题对话">
                <button
                  className="idea-new-conversation"
                  onClick={() => void createIdeaConversation()}
                >
                  新建对话
                </button>
                <div className="idea-conversation-list">
                  {ideaDraft && ideaSession ? (
                    <div className="idea-conversation-item selected">
                      <button
                        className="idea-conversation-select selected"
                        onClick={() => setIdeaSession(ideaSession)}
                      >
                        <strong>新选题规划</strong>
                        <small>未发送</small>
                      </button>
                    </div>
                  ) : null}
                  {ideaSessions.map((session) => (
                    <div className="idea-conversation-item" key={session.id}>
                      <button
                        className={`idea-conversation-select ${session.id === ideaSession.id ? "selected" : ""}`}
                        onClick={() => void switchIdeaConversation(session)}
                      >
                        <strong>{session.title}</strong>
                        <small>{session.status === "planning" ? "规划中" : session.status}</small>
                      </button>
                      <button
                        className="idea-delete-conversation"
                        aria-label={`删除对话 ${session.title}`}
                        title="删除对话"
                        onClick={() => void deleteIdeaConversation(session)}
                      >
                        ×
                      </button>
                    </div>
                  ))}
                </div>
              </nav>
              <div className="idea-current-conversation">
                <div className="idea-messages">
                  {ideaSession.messages?.map((item) => (
                    <div className={`idea-message ${item.role}`} key={item.id}>
                      <b>{item.role === "user" ? "你" : "Codex"}</b>
                      <p>{item.content}</p>
                    </div>
                  ))}
                  {!ideaSession.messages?.length && (
                    <p className="muted">
                      告诉我你想做的财经方向、受众或近期关注的问题。
                    </p>
                  )}
                </div>
                {ideaSession.messages?.length && !ideaSession.candidates?.length ? (
                  <section className="idea-pending" aria-live="polite">
                    <div className="idea-progress-head">
                      <span
                        className={`idea-progress-dot ${ideaTask?.status || "queued"}`}
                      />
                      <strong>
                        {ideaTask
                          ? taskProgressStatus(ideaTask)
                          : "已发送，正在等待 Codex 启动"}
                      </strong>
                    </div>
                    {ideaTask?.events?.length ? (
                      <ol className="idea-progress-events">
                        {ideaTask.events
                          .slice(-3)
                          .reverse()
                          .map((item, index) => (
                            <li key={item.id || `${item.sequence || 0}-${index}`}>
                              {taskEventProgress(item)}
                            </li>
                          ))}
                      </ol>
                    ) : (
                      <p className="idea-progress-note">
                        会自动刷新，无需停留在这个窗口。
                      </p>
                    )}
                  </section>
                ) : null}
                {ideaSession.candidates?.length ? (
                  <div className="idea-candidates">
                    <h3>候选题</h3>
                    {ideaSession.candidates.map((candidate) => (
                      <article key={candidate.id}>
                        <div>
                          <strong>{candidate.title}</strong>
                          <p>{candidate.summary}</p>
                        </div>
                        <button
                          disabled={!!ideaCreatingProject}
                          onClick={() => void selectIdeaCandidate(candidate)}
                        >
                          {ideaCreatingProject === candidate.id ? "创建中…" : "确认并建项目"}
                        </button>
                      </article>
                    ))}
                  </div>
                ) : null}
                <form className="idea-compose" onSubmit={sendIdeaMessage}>
                  <TaskModelFields
                    value={ideaTaskModel}
                    onChange={setIdeaTaskModel}
                    defaults={settings?.public}
                    labelPrefix="选题"
                  />
                  <input
                    autoFocus
                    value={ideaInput}
                    onChange={(event) => setIdeaInput(event.target.value)}
                    placeholder="输入你的想法或追问"
                  />
                  <button disabled={!ideaInput.trim()}>发送</button>
                </form>
              </div>
            </div>
          </section>
        </div>
      )}
      {chatOpen && (
        <div className="modal-backdrop chat-backdrop" onClick={() => setChatOpen(false)}>
          <section
            className="chat-workbench"
            role="dialog"
            aria-modal="true"
            aria-labelledby="chat-dialog-title"
            tabIndex={-1}
            onClick={(event) => event.stopPropagation()}
          >
            <aside className="chat-session-rail">
              <div className="chat-rail-head">
                <strong>Codex 对话</strong>
                <button disabled={chatCreating} onClick={() => { setChatCreationSource("console"); void createGeneralChat("console"); }}>
                  {chatCreating && chatCreationSource === "console" ? "创建中…" : "新建控制台对话"}
                </button>
                <button disabled={chatCreating} onClick={() => { setChatCreationSource("desktop"); void createGeneralChat("desktop"); }}>
                  {chatCreating && chatCreationSource === "desktop" ? "创建中…" : "新建桌面版对话"}
                </button>
              </div>
              <div className="chat-session-list">
                {chatSessions.map((session) => (
                  <div className="chat-session-item" key={session.id}>
                    <button
                      className={chatDetail?.session.id === session.id ? "selected" : ""}
                      onClick={() => void loadChatSession(session)}
                    >
                      <strong>{session.title}</strong>
                        <small>{session.source === "desktop" ? "桌面版会话" : "控制台会话"} · {session.status === "running" ? "Codex 正在处理" : "可以继续对话"}</small>
                    </button>
                    <button
                      className="chat-session-delete"
                      aria-label={`删除对话 ${session.title}`}
                      onClick={() => void deleteChatSession(session)}
                    >
                      ×
                    </button>
                  </div>
                ))}
              </div>
              <div className="history-rail-head">
                <div>
                  <strong>本机历史</strong>
                  <small>最近 {settings?.public.codex_history_limit || 10} 条</small>
                </div>
                <select
                  aria-label="筛选本机历史来源"
                  value={historySource}
                  onChange={(event) => {
                    const source = event.target.value;
                    setHistorySource(source);
                    void refreshHistory(source);
                  }}
                >
                  <option value="">全部</option>
                  <option value="desktop">桌面版</option>
                  <option value="cli">CLI</option>
                  <option value="task">任务</option>
                </select>
              </div>
              <div className="history-thread-list">
                {historyThreads.map((thread) => (
                  <article key={`${thread.source}-${thread.id}`}>
                    <strong>{thread.title || "未命名会话"}</strong>
                    <small>{historySourceLabels[thread.source] || "本机任务"} · {formatDate(thread.recency)}</small>
                    {thread.preview ? <p>{thread.preview}</p> : null}
                    <div>
                      <button
                        disabled={thread.active}
                        title={thread.active ? "该会话正在别处运行" : "恢复原来的 Codex 会话"}
                        onClick={() => void applyHistoryThread(thread, "resume")}
                      >
                        继续原会话
                      </button>
                      <button onClick={() => void applyHistoryThread(thread, "fork")}>
                        复制到控制台
                      </button>
                    </div>
                  </article>
                ))}
                {!historyThreads.length && <p>暂无可接入的本机历史</p>}
              </div>
              <details className="history-help">
                <summary>两个入口有什么区别？</summary>
                <p><b>继续原会话</b>会接回桌面版或 CLI 中的同一个会话；正在别处运行时不能接管。</p>
                <p><b>复制到控制台</b>会保留上下文并新建一份，不影响原会话。</p>
              </details>
            </aside>
            <div className="chat-main">
              <div className="chat-main-head">
                <div>
                  <span className="eyebrow">实时对话</span>
                  <h2 id="chat-dialog-title">{chatDetail?.session.title || "新建一个 Codex 对话"}</h2>
                </div>
                <button className="close" onClick={() => setChatOpen(false)}>×</button>
              </div>
              <div className="chat-messages" aria-live="polite">
                {visibleChatMessages.map((item, index) => (
                  <article className={`chat-bubble ${item.role}`} key={item.id || `${item.role}-${item.created_at}-${index}`}>
                    <b>{item.role === "user" ? "你" : "Codex"}</b>
                    <p>{item.content}</p>
                    {item.role === "user" && item.delivery_status === "queued" ? <small>已排队</small> : null}
                  </article>
                ))}
                {!visibleChatMessages.length && (
                  <div className="chat-empty">
                    <strong>直接告诉 Codex 你要处理什么</strong>
                    <p>任务运行中也可以继续发送补充要求；系统会自动引导当前任务或排入下一轮。</p>
                  </div>
                )}
                {technicalChatMessages.length ? (
                  <details className="chat-technical">
                    <summary>技术记录（{technicalChatMessages.length}）</summary>
                    {technicalChatMessages.map((item, index) => (
                      <pre key={item.id || `${item.kind}-${item.created_at}-${index}`}>{item.content}</pre>
                    ))}
                  </details>
                ) : null}
              </div>
              <form className="chat-compose" onSubmit={sendChatMessage}>
                {chatDetail?.session.status === "running" ? (
                  <p className="chat-compose-note">Codex 正在处理。现在发送会作为补充要求送入当前任务。</p>
                ) : null}
                <textarea
                  value={chatInput}
                  onChange={(event) => setChatInput(event.target.value)}
                  placeholder="输入消息，支持在运行中继续补充要求……"
                  rows={3}
                />
                <button disabled={!chatInput.trim() || chatSending || !chatDetail}>
                  {chatSending ? "发送中" : "发送"}
                </button>
              </form>
            </div>
          </section>
        </div>
      )}
      {taskOpen && (
        <div className="modal-backdrop">
          <section
            className="preview-modal task-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="task-dialog-title"
            tabIndex={-1}
            onClick={(event) => event.stopPropagation()}
          >
            <div className="drawer-head">
              <div>
                <span className="muted">Codex 任务详情</span>
                  <h2 id="task-dialog-title">{taskTitle(taskOpen)}</h2>
                {selected ? <p className="task-project-context">当前项目：{selected.title}</p> : null}
              </div>
              <button className="close" aria-label="关闭任务详情" onClick={closeTask}>
                ×
              </button>
            </div>
            <div className="task-modal-meta">
              <span className={`task-status status-${taskOpen.status}`}>
                {statusLabels[taskOpen.status] || taskOpen.status}
              </span>
              <span>{taskOpen.id}</span>
              {(taskOpen.model || taskOpen.reasoning_effort) && (
                <span>{[taskOpen.model, taskOpen.reasoning_effort].filter(Boolean).join(" · ")}</span>
              )}
              {cancellableTaskStatuses.has(taskOpen.status) && (
                <button className="secondary" onClick={() => void cancelTask(taskOpen)}>
                  停止任务
                </button>
              )}
            </div>
            {taskOpen.action === "montage.execute" && taskOpen.montage ? (
              <section className={`montage-result phase-${openMontagePhase}`}>
                <div className="montage-result-head">
                  <div>
                    <span>剪映草稿登记</span>
                    <h3>{montageHeadline(taskOpen.montage, openMontagePhase)}</h3>
                  </div>
                  <span className="montage-phase-badge">
                    {montagePhaseLabels[openMontagePhase] || "正在准备混剪草稿"}
                  </span>
                </div>
                {taskOpen.montage.workspace ? (
                  <p className="montage-retained">
                    明文产物已保留：{taskOpen.montage.workspace.filename || "未命名草稿"}。登记失败时不会重新生成或删除。
                  </p>
                ) : null}
                {taskOpen.montage.registration_attempts?.[0]?.error_message ? (
                  <p className="warning">{taskOpen.montage.registration_attempts?.[0]?.error_message}</p>
                ) : null}
                {taskOpen.montage.registered_asset ? (
                  <div className="registered-directory">
                    <dl>
                      <dt>正式项目资产</dt><dd>{taskOpen.montage.registered_asset.filename || "未命名草稿"}</dd>
                      <dt>剪映路径</dt>
                      <dd>{directoryManifest?.registered_path || taskOpen.montage.registered_asset.path}</dd>
                    </dl>
                    {directoryManifest ? (
                      <details className="directory-manifest">
                        <summary>目录文件清单（{directoryManifest.entries.length} 项）</summary>
                        <ul>
                          {directoryManifest.entries.slice(0, 80).map((entry) => (
                            <li key={`${entry.kind}-${entry.path}`}>
                              <span>{entry.path}</span>
                              <small>
                                {entry.kind === "directory" ? "文件夹" : formatSize(entry.size)}
                              </small>
                            </li>
                          ))}
                        </ul>
                        {directoryManifest.entries.length > 80 ? (
                          <p>清单较长，这里只展示前 80 项；目录共 {directoryManifest.entries.length} 项。</p>
                        ) : null}
                      </details>
                    ) : null}
                    {isLoopbackBrowser ? (
                      <button
                        type="button"
                        disabled={openingDirectory}
                        onClick={() =>
                          void openRegisteredDirectory(taskOpen.montage!.registered_asset!.id)
                        }
                      >
                        {openingDirectory ? "正在打开…" : "在电脑上打开剪映目录"}
                      </button>
                    ) : null}
                    {directoryManifestStatus ? (
                      <p className="directory-status" aria-live="polite">{directoryManifestStatus}</p>
                    ) : null}
                  </div>
                ) : null}
                {taskOpen.montage.can_retry_registration ? (
                  <button onClick={() => void retryMontageRegistration(taskOpen)}>
                    只重试剪映登记
                  </button>
                ) : null}
              </section>
            ) : null}
            {taskOpen.prompt_snapshot && (
              <details className="technical-diagnostics">
                <summary>任务原始说明</summary>
                <pre className="asset-text">{taskOpen.prompt_snapshot}</pre>
              </details>
            )}
            {taskOpen.messages?.length ? (
              <section>
                <h3>对话记录</h3>
                {taskOpen.messages.map((item, index) => (
                  <div className={`task-message ${item.role}`} key={item.id || `${item.role}-${item.created_at}-${index}`}>
                    <b>{item.role === "user" ? "你" : "Codex"}</b>
                    <p>{taskMessageContent(item.content)}</p>
                  </div>
                ))}
              </section>
            ) : null}
            {taskTimeline(taskOpen).length ? (
              <section>
                <h3>处理进度</h3>
                <ol className="task-timeline task-timeline-expanded">
                  {taskTimeline(taskOpen).map((item, index) => (
                    <li key={`${taskOpen.id}-modal-progress-${index}`}>{item}</li>
                  ))}
                </ol>
              </section>
            ) : null}
            {taskOpen.events?.length ? (
              <details className="technical-diagnostics">
                <summary>技术诊断（{taskOpen.events.length}）</summary>
                {taskOpen.events.map((item, index) => (
                  <p className="event" key={item.id || `${item.sequence || 0}-${index}`}>
                    {item.display_text || item.DisplayText || "技术事件"}
                  </p>
                ))}
              </details>
            ) : null}
            {taskOpen.result_summary && (
              <section className="task-result-summary">
                <h3>完成结果</h3>
                <p>{taskOpen.result_summary}</p>
                {taskOpen.completion_phase ? <small>结果阶段：{taskOpen.completion_phase}</small> : null}
              </section>
            )}
            {taskOpen.error_message && (
              <p className="warning">{taskOpen.error_message}</p>
            )}
            {taskQuestions(taskOpen).length > 0 && (
              <section className="task-question">
                <strong>Codex 正在问：</strong>
                {taskQuestions(taskOpen).map((question, index) => (
                  <p key={`${taskOpen.id}-modal-question-${index}`}>{question}</p>
                ))}
                <form
                  className="task-answer-compose"
                  onSubmit={(event) => {
                    event.preventDefault();
                    if (taskAnswerInput.trim()) void answerTask(taskOpen, taskAnswerInput.trim());
                  }}
                >
                  <textarea
                    rows={3}
                    value={taskAnswerInput}
                    onChange={(event) => setTaskAnswerInput(event.target.value)}
                    placeholder="在这里回答，Codex 会从当前任务继续"
                  />
                  <button disabled={!taskAnswerInput.trim()}>发送回答</button>
                </form>
              </section>
            )}
          </section>
        </div>
      )}
    </div>
  );
}

function stageLabel(stage: string) {
  return (
    (
      {
        topic: "选题准备",
        script: "文案制作",
        assets: "配音字幕",
        mixing: "混剪制作",
        review: "成片审核",
        ready: "待发布",
        published: "已发布",
      } as Record<string, string>
    )[stage] || stage
  );
}
function projectStageHint(stage: string) {
  return (
    (
      {
        topic: "正在确定选题或生成选题卡",
        script: "选题卡已就绪，正在制作文案",
        assets: "文案已登记，正在准备配音和 SRT",
        mixing: "配音和 SRT 已齐，正在制作混剪",
        review: "混剪草稿已登记，等待审核",
        ready: "成片已登记，等待发布",
        published: "已经发布",
      } as Record<string, string>
    )[stage] || stage
  );
}
function accountName(id: string, accounts: Account[]) {
  return accounts.find((account) => account.id === id)?.name || "未分配";
}
function formatSize(size: number) {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}
function formatDate(value?: string) {
  return value
    ? new Date(value).toLocaleString("zh-CN", {
        month: "numeric",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
      })
    : "暂无";
}
export default App;
