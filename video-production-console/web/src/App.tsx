import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, RotateCcw, X } from "lucide-react";
import { apiRequest } from "./api/client";
import { AccountSwitcher } from "./accounts/AccountSwitcher";
import { LoginPage } from "./auth/LoginPage";
import { messageTone } from "./messageTone";
import { SettingsPanel } from "./settings/SettingsPanel";
import { useConsoleData } from "./console/useConsoleData";
import { queryKeys } from "./query/keys";
import "./App.css";
import "./idea.css";
import { parseLocation } from "./project-workbench/routes";
import { TaskModelFields } from "./TaskModelFields";
import type { TaskModelOverride } from "./taskModel";
import { ProjectWorkbench } from "./project-workbench/ProjectWorkbench";
import { ProjectCreateForm } from "./projects/ProjectCreateForm";
import { useRuntimeQuery } from "./runtime/useRuntimeQuery";
import type { SemanticEvent, TaskEvent } from "./tasks/event-types";
import type {
  MontageResult,
  ProjectDetail as WorkbenchProjectDetail,
} from "./project-workbench/types";
import type {
  Account,
  Asset,
  ChatDetail,
  ChatMessage,
  ChatSession,
  DirectoryManifest,
  HistoryThread,
  IdeaCandidate,
  IdeaSession,
  IdeaSessionDetail,
  Project,
  ProjectDetail,
  PublicSettings,
  Settings,
  Task,
  TaskPhaseRun,
  TaskTimingSummary,
  Theme,
} from "./types";

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

function writeProjectLocation(
  projectID: string,
  mode: "push" | "replace",
  preserveQuery = false,
) {
  const pathname = projectID ? `/projects/${projectID}` : "/projects";
  const search = preserveQuery ? window.location.search : "";
  const target = `${pathname}${search}`;
  const current = `${window.location.pathname}${window.location.search}`;
  if (mode === "push" && current !== target) window.history.pushState({}, "", target);
  else window.history.replaceState({}, "", target);
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

const THEME_STORAGE_KEY = "video-production-console-theme";
const PROJECT_COLLAPSE_LIMIT = 4;

function readStoredTheme(): Theme {
  if (typeof window === "undefined") return "light";
  return window.localStorage.getItem(THEME_STORAGE_KEY) === "dark" ? "dark" : "light";
}

const stages: Array<Project["stage"]> = [
  "topic",
  "script",
  "assets",
  "mixing",
  "review",
  "published",
];
const assetLabels: Record<string, string> = {
  source_script: "爆款原文",
  topic_card: "正式选题卡",
  continuous_script: "连续文案",
  narration: "配音",
  subtitle_srt: "SRT 字幕",
  account_background: "账号固定背景图",
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
const taskPhaseStateLabels: Record<string, string> = {
  queued: "排队中",
  running: "运行中",
  completed: "已完成",
  failed: "失败",
  canceled: "已取消",
  cancelled: "已取消",
  interrupted: "已中断",
};
const taskActionLabels: Record<string, string> = {
  "topic.brainstorm": "选题分析",
  "topic.commit": "保存选题卡",
  "topic.deepen": "深化选题",
  "remix.standard": "二创文案",
  "remix.enhanced": "增强二创文案",
  "remix.from_topic_card": "根据选题写文案",
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
  const kind = event.kind || "";
  const displayText = event.display_text || "";
  const raw = event.raw_json || "";
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
    id: event.id || "",
    sequence: event.sequence ?? 0,
    kind: event.kind || "",
    phase: event.phase || "",
    level: event.level || "",
    title: (event.title || "").trim(),
    detail: event.detail || "",
    created_at: event.created_at || "",
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

const noTasks: Task[] = [];

function App() {
  const client = useQueryClient();
  const [csrf, setCsrf] = useState("");
  const [theme, setTheme] = useState<Theme>(readStoredTheme);
  const [expandedStages, setExpandedStages] = useState<Set<Project["stage"]>>(
    () => new Set(),
  );
  const [authenticated, setAuthenticated] = useState<boolean | null>(null);
  const [password, setPassword] = useState("");
  const [account, setAccount] = useState("");
  const [newAccount, setNewAccount] = useState("");
  const [accountBackground, setAccountBackground] = useState<File | null>(null);
  const [newProject, setNewProject] = useState("");
  const [message, setMessage] = useState("");
  const [selected, setSelected] = useState<Project | null>(null);
  const [preview, setPreview] = useState<{
    asset: Asset;
    text?: string;
  } | null>(null);
  const [previewDraft, setPreviewDraft] = useState("");
  const [reviseOpen, setReviseOpen] = useState(false);
  const [reviseNotes, setReviseNotes] = useState("");
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsFeedback, setSettingsFeedback] = useState("");
  const [accountFormOpen, setAccountFormOpen] = useState(false);
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
  const [timingNow, setTimingNow] = useState(() => Date.now());
  const [taskAnswerInput, setTaskAnswerInput] = useState("");
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
  const [pendingProjectActions, setPendingProjectActions] = useState<string[]>([]);
  const handledURLRevisionRef = useRef(0);
  const selectedIDRef = useRef("");
  const pendingProjectActionsRef = useRef(new Set<string>());
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
  const lockProjectAction = useCallback((projectID: string, action: string) => {
    const key = `${projectID}:${action}`;
    if ([...pendingProjectActionsRef.current].some((pending) => pending.startsWith(`${projectID}:`)))
      return "";
    pendingProjectActionsRef.current.add(key);
    setPendingProjectActions([...pendingProjectActionsRef.current]);
    return key;
  }, []);
  const unlockProjectAction = useCallback((key: string) => {
    pendingProjectActionsRef.current.delete(key);
    setPendingProjectActions([...pendingProjectActionsRef.current]);
  }, []);
  const clearProjectSelection = useCallback(() => {
    selectedIDRef.current = "";
    if (detailRefreshTimerRef.current !== null)
      window.clearTimeout(detailRefreshTimerRef.current);
    setSelected(null);
  }, []);
  const activeIdeaSessionID = ideaDraft ? undefined : ideaSession?.id;
  const selectedID = selected?.id ?? "";
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.colorScheme = theme;
    window.localStorage.setItem(THEME_STORAGE_KEY, theme);
  }, [theme]);

  const api = useCallback(
    (path: string, init: RequestInit = {}) =>
      apiRequest(path, init, {
        csrfToken: csrf,
        onUnauthorized: () => {
          setAuthenticated(false);
          setCsrf("");
        },
      }),
    [csrf],
  );
  const { accounts, projects, setProjects, loading, failed: consoleDataFailed } =
    useConsoleData<Account, Project>(api, authenticated === true);
  const { data: runtime = null } = useRuntimeQuery(api, authenticated === true);

  const readSettings = useCallback(
    async (signal?: AbortSignal) => {
      const response = await api("/api/settings", { signal });
      if (!response.ok) throw new Error("设置读取失败。");
      return (await response.json()) as Settings;
    },
    [api],
  );
  const settingsQuery = useQuery({
    queryKey: queryKeys.settings(),
    enabled: authenticated === true,
    queryFn: ({ signal }) => readSettings(signal),
  });
  const settings = settingsQuery.data ?? null;

  useEffect(() => {
    if (consoleDataFailed) setMessage("控制台服务尚未连接");
  }, [consoleDataFailed]);

  // Hydrating the list is what makes the workbench usable: the list endpoint only
  // returns task shells, so live/recent/remix tasks are topped up with their
  // detail, progress and result payloads. taskCacheRef keeps finished tasks from
  // being re-fetched on every poll.
  const hydrateTasks = useCallback(
    async (projectID: string, signal?: AbortSignal) => {
      const response = await api(`/api/tasks?project_id=${projectID}`, { signal });
      if (!response.ok) throw new Error("读取项目任务失败");
      const listed = (await response.json()) as Task[];
      const recentIDs = new Set(
        [...listed]
          .sort(
            (left, right) =>
              new Date(right.created_at).getTime() - new Date(left.created_at).getTime(),
          )
          .slice(0, 6)
          .map((task) => task.id),
      );
      return Promise.all(
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
            api(`/api/tasks/${task.id}`, { signal }),
            api(`/api/tasks/${task.id}/semantic-events?limit=20`, { signal }),
            task.action === "montage.execute" || !liveTaskStatuses.has(task.status)
              ? api(`/api/tasks/${task.id}/result`, { signal })
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
    },
    [api],
  );

  // Poll only while something can still change: a running task, or an open task
  // dialog whose timing bar has to keep ticking.
  const detailPollInterval = useCallback(() => {
    if (taskOpenIDRef.current) return 5_000;
    const cached = client.getQueryData<Task[]>(queryKeys.tasks(selectedIDRef.current));
    const live = cached?.some((task) => liveTaskStatuses.has(task.status));
    return live ? 8_000 : (false as const);
  }, [client]);

  // retry stays off so a failed refresh surfaces the retry affordance at once,
  // the way the hand-rolled fetch did.
  const detailQuery = useQuery({
    queryKey: queryKeys.project(selectedID),
    enabled: authenticated === true && Boolean(selectedID),
    refetchInterval: detailPollInterval,
    retry: false,
    queryFn: async ({ signal }) => {
      const response = await api(`/api/projects/${selectedID}`, { signal });
      if (!response.ok) throw new Error("读取项目详情失败");
      return (await response.json()) as ProjectDetail;
    },
  });
  const tasksQuery = useQuery({
    queryKey: queryKeys.tasks(selectedID),
    enabled: authenticated === true && Boolean(selectedID),
    refetchInterval: detailPollInterval,
    retry: false,
    queryFn: ({ signal }) => hydrateTasks(selectedID, signal),
  });

  // The query key carries the project id, so a response can never land on the
  // project the user switched to; the guard only covers a mismatched payload.
  const detail =
    detailQuery.data?.project.id === selectedID ? detailQuery.data : null;
  const tasks = tasksQuery.data ?? noTasks;
  // Both halves used to arrive together, so hold the loading state until the task
  // list has landed too instead of flashing a workbench with no tasks.
  const detailReady = Boolean(detail) && tasksQuery.data !== undefined;
  const detailFailed = detailQuery.isError || tasksQuery.isError;
  const detailError = detailFailed ? "项目详情暂时无法读取" : "";

  const activeTasks = useMemo(
    () => tasks.filter((task) => liveTaskStatuses.has(task.status)),
    [tasks],
  );
  const activeTaskIDs = useMemo(
    () => activeTasks.map((task) => task.id).sort().join(","),
    [activeTasks],
  );

  // Callers await this expecting the workbench to show post-mutation data, so
  // refetch rather than merely invalidate.
  const refreshProject = useCallback(
    async (projectID: string) => {
      await Promise.all([
        client.refetchQueries({ queryKey: queryKeys.project(projectID) }),
        client.refetchQueries({ queryKey: queryKeys.tasks(projectID) }),
      ]);
    },
    [client],
  );
  const loadDetail = useCallback(
    (project: Project) => refreshProject(project.id),
    [refreshProject],
  );

  // Task websockets can fire in bursts; collapse them into one refresh.
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

  // Writes go through mutations so react-query owns the request and the cache
  // refresh that follows it; the per-project action lock stays because it encodes
  // a product rule (one action per project) rather than a fetch concern.
  // A rejected request and a rejecting server mean different things to the user,
  // so these resolve to `ok` for an HTTP failure and only reject when the request
  // itself could not be made. Callers keep their two distinct messages.
  const createProjectMutation = useMutation({
    mutationFn: async (input: { accountID: string; title: string }) => {
      const response = await api("/api/projects", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ account_id: input.accountID, title: input.title }),
      });
      return response.ok;
    },
    onSuccess: async (ok) => {
      if (!ok) return;
      setNewProject("");
      await client.invalidateQueries({ queryKey: queryKeys.projects() });
    },
  });

  const startProjectTaskMutation = useMutation({
    mutationFn: async (input: { projectID: string; body: Record<string, unknown> }) => {
      const response = await api(`/api/projects/${input.projectID}/tasks`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input.body),
      });
      return response.ok;
    },
    onSuccess: (ok, input) => (ok ? refreshProject(input.projectID) : undefined),
  });

  const startRemixWorkflowMutation = useMutation({
    mutationFn: async (input: { projectID: string; body: Record<string, unknown> }) => {
      const response = await api(`/api/projects/${input.projectID}/remix`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input.body),
      });
      return response.ok;
    },
    onSuccess: (ok, input) => (ok ? refreshProject(input.projectID) : undefined),
  });

  // Saving a revision bumps the asset version and marks downstream assets stale,
  // so the refresh has to cover the project detail as well as the task list.
  const saveContinuousScriptMutation = useMutation({
    mutationFn: async (input: { projectID: string; content: string }) => {
      const body = new FormData();
      body.set(
        "file",
        new File([input.content], "continuous-script.txt", { type: "text/plain" }),
      );
      const response = await api(`/api/projects/${input.projectID}/assets/continuous_script`, {
        method: "POST",
        body,
      });
      return response.ok;
    },
    onSuccess: (ok, input) => (ok ? refreshProject(input.projectID) : undefined),
  });

  useEffect(() => {
    if (detailFailed) setMessage("项目刷新暂时中断，将在下次活动时重试。");
  }, [detailFailed]);

  // The board and the header title follow the stage the detail response reports.
  useEffect(() => {
    if (!detail) return;
    const { id, stage } = detail.project;
    setProjects((current) =>
      current.some((item) => item.id === id && item.stage !== stage)
        ? current.map((item) => (item.id === id ? { ...item, stage } : item))
        : current,
    );
    setSelected((current) =>
      current?.id === id && current.stage !== stage ? { ...current, stage } : current,
    );
  }, [detail, setProjects]);

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
    if (!selected || !activeTaskIDs) return;
    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    const sockets = new Map<string, WebSocket>();
    const retryTimers = new Map<string, number>();
    let stopped = false;
    const connect = (taskID: string) => {
      if (stopped) return;
      const socket = new WebSocket(
        `${protocol}//${location.host}/api/tasks/${taskID}/events?after=0`,
      );
      sockets.set(taskID, socket);
      socket.onmessage = () => scheduleDetailRefresh(selected);
      socket.onerror = () => socket.close();
      socket.onclose = () => {
        if (sockets.get(taskID) === socket) sockets.delete(taskID);
        if (stopped) return;
        const timer = window.setTimeout(() => {
          retryTimers.delete(taskID);
          connect(taskID);
        }, 2000);
        retryTimers.set(taskID, timer);
      };
    };
    activeTaskIDs.split(",").forEach(connect);
    return () => {
      stopped = true;
      retryTimers.forEach((timer) => window.clearTimeout(timer));
      sockets.forEach((socket) => socket.close());
    };
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
    setTimingNow(Date.now());
    if (!taskOpen || !taskTimingPhases(taskOpen).some(isRunningPhase)) return;
    const timer = window.setInterval(() => setTimingNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [taskOpen]);
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
  const syncTaskProjectContext = useCallback(
    (task: Task) => {
      const project = projects.find((item) => item.id === task.project_id);
      if (!project) {
        taskOpenIDRef.current = "";
        setTaskOpen(null);
        clearProjectSelection();
        writeProjectLocation("", "replace");
        return false;
      }
      if (window.location.pathname !== `/projects/${project.id}`) {
        writeProjectLocation(project.id, "replace", true);
      }
      if (selectedIDRef.current !== project.id) {
        selectedIDRef.current = project.id;
        setSelected(project);
      }
      return true;
    },
    [clearProjectSelection, projects],
  );
  useEffect(() => {
    if (!authenticated || loading) return;
    const route = parseLocation(window.location.pathname);
    const taskID = new URL(window.location.href).searchParams.get("task") || "";
    if (route.view === "projects") {
      if (!["/", "/projects", "/projects/"].includes(window.location.pathname)) {
        writeProjectLocation("", "replace", Boolean(taskID));
      }
      if (
        !taskID &&
        selectedIDRef.current &&
        handledURLRevisionRef.current !== urlRevision
      ) {
        clearProjectSelection();
      }
      handledURLRevisionRef.current = urlRevision;
      return;
    }
    const project = projects.find((item) => item.id === route.projectID);
    if (!project) {
      taskRestoreAbortRef.current?.abort();
      taskOpenIDRef.current = "";
      setTaskOpen(null);
      clearProjectSelection();
      writeProjectLocation("", "replace");
      handledURLRevisionRef.current = urlRevision;
      return;
    }
    handledURLRevisionRef.current = urlRevision;
    if (selectedIDRef.current === project.id) return;
    selectedIDRef.current = project.id;
    setSelected(project);
  }, [authenticated, clearProjectSelection, loading, projects, urlRevision]);
  useEffect(() => {
    if (!authenticated || !projects.length) return;
    const taskID = new URL(window.location.href).searchParams.get("task") || "";
    if (!taskID) {
      if (taskOpen) setTaskOpen(null);
      return;
    }
    if (taskOpen?.id === taskID) {
      syncTaskProjectContext(taskOpen);
      return;
    }
    taskRestoreAbortRef.current?.abort();
    const controller = new AbortController();
    taskRestoreAbortRef.current = controller;
    void (async () => {
      try {
        const response = await api(`/api/tasks/${taskID}`, { signal: controller.signal });
        if (!response.ok) return;
        const restored = (await response.json()) as Task;
        if (controller.signal.aborted) return;
        if (!syncTaskProjectContext(restored)) return;
        taskOpenIDRef.current = restored.id;
        setTaskOpen(restored);
      } catch (error) {
        if (!isAbortError(error)) setMessage("无法恢复链接中的任务详情。");
      }
    })();
    return () => controller.abort();
  }, [api, authenticated, projects, syncTaskProjectContext, taskOpen, urlRevision]);
  useEffect(() => {
    const dialogs = Array.from(document.querySelectorAll<HTMLElement>('[role="dialog"]'));
    const active = dialogs[dialogs.length - 1];
    const dialogOpen = Boolean(
      preview || reviseOpen || settingsOpen || ideaOpen || chatOpen || taskOpen,
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
      (active?.closest(".modal-backdrop") as HTMLElement | null) || active;
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
      else if (reviseOpen) setReviseOpen(false);
      else if (preview) setPreview(null);
      else if (selected) {
        clearProjectSelection();
        writeProjectLocation("", "push");
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
  }, [chatOpen, clearProjectSelection, ideaOpen, preview, reviseOpen, selected, settingsOpen, taskOpen]);
  useEffect(() => () => {
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
    setSelected(project);
    writeProjectLocation(project.id, "push");
  };
  const closeProject = () => {
    closeTask();
    clearProjectSelection();
    writeProjectLocation("", "push");
  };
  useEffect(() => {
    if (!selected || preview || settingsOpen || ideaOpen || chatOpen || taskOpen) return;
    const returnToBoard = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      taskOpenIDRef.current = "";
      setTaskOpen(null);
      writeTaskQuery("", "replace");
      clearProjectSelection();
      writeProjectLocation("", "push");
    };
    window.addEventListener("keydown", returnToBoard);
    return () => window.removeEventListener("keydown", returnToBoard);
  }, [chatOpen, clearProjectSelection, ideaOpen, preview, selected, settingsOpen, taskOpen]);
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
    setAccountFormOpen(false);
    setMessage("账号已创建。");
    await client.invalidateQueries({ queryKey: queryKeys.accounts() });
  };
  const createProject = async (event: FormEvent) => {
    event.preventDefault();
    if (!newProject.trim() || !account) return;
    const created = await createProjectMutation.mutateAsync({
      accountID: account,
      title: newProject.trim(),
    });
    if (!created) setMessage("项目创建失败。");
  };
  const loadSourceScriptContent = async (assetID: string): Promise<string> => {
    const response = await api(`/api/assets/${assetID}/content`);
    if (!response.ok) throw new Error("同行原文读取失败");
    return response.text();
  };
  const saveSourceScriptAndStartRemix = async (content: string) => {
    if (!selected) return;
    const project = selected;
    const projectID = project.id;
    const lockKey = lockProjectAction(projectID, "source-remix");
    if (!lockKey) return;
    try {
      let sourceVersionID = "";
      const savedSource = detail?.project.id === projectID ? detail.assets.source_script : undefined;
      if (savedSource?.state === "ready") {
        try {
          const existingContent = await loadSourceScriptContent(savedSource.id);
          if (selectedIDRef.current !== projectID) return;
          if (existingContent === content) sourceVersionID = savedSource.id;
        } catch {
          if (selectedIDRef.current !== projectID) return;
        }
      }
      if (!sourceVersionID) {
        const body = new FormData();
        body.set("file", new File([content], "source-script.txt", { type: "text/plain" }));
        const response = await api(`/api/projects/${projectID}/assets/source_script`, {
          method: "POST",
          body,
        });
        if (selectedIDRef.current !== projectID) return;
        if (!response.ok) {
          setMessage("同行原文保存失败，请稍后重试。");
          await loadDetail(project);
          return;
        }
        const asset = (await response.json()) as Asset;
        if (selectedIDRef.current !== projectID) return;
        sourceVersionID = asset.id;
      }
      const started = await startProjectTaskMutation.mutateAsync({
        projectID,
        body: {
          account_id: project.account_id,
          type: "remix",
          action: "remix.standard",
          prompt: "基于当前项目保存的同行原文生成正式连续二创文案，并登记为项目资产。",
          source_version_id: sourceVersionID,
          ...(projectTaskModel.model.trim() ? { model: projectTaskModel.model.trim() } : {}),
          ...(projectTaskModel.reasoningEffort
            ? { reasoning_effort: projectTaskModel.reasoningEffort }
            : {}),
        },
      });
      if (selectedIDRef.current !== projectID) return;
      if (!started) {
        setMessage("原文已保存，但二创任务启动失败，请检查 Codex 配置后重试。");
        await loadDetail(project);
        return;
      }
      setProjectTaskModel({ model: "", reasoningEffort: "" });
      setMessage("同行原文已保存，正式二创任务已启动。完成后会自动出现在项目资产中。");
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("原文保存或二创任务启动失败，请检查网络连接后重试。");
    } finally {
      unlockProjectAction(lockKey);
    }
  };

  const startMontageTask = async (prompt: string) => {
    if (!selected) return;
    const project = selected;
    const projectDetail = detail;
    if (!projectDetail || projectDetail.project.id !== project.id) {
      setMessage("项目详情仍在刷新，请确认当前项目后再启动任务。");
      return;
    }
    const projectID = project.id;
    const lockKey = lockProjectAction(projectID, "montage");
    if (!lockKey) return;
    try {
      const started = await startProjectTaskMutation.mutateAsync({
        projectID,
        body: {
          account_id: project.account_id,
          type: "montage",
          prompt,
          ...(projectTaskModel.model.trim()
            ? { model: projectTaskModel.model.trim() }
            : {}),
          ...(projectTaskModel.reasoningEffort
            ? { reasoning_effort: projectTaskModel.reasoningEffort }
            : {}),
        },
      });
      if (!started) {
        if (selectedIDRef.current === projectID)
          setMessage("混剪任务启动失败，请检查项目素材与 Codex 配置后重试。");
        return;
      }
      if (selectedIDRef.current !== projectID) return;
      setProjectTaskModel({ model: "", reasoningEffort: "" });
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("混剪任务启动失败，请检查网络连接后重试。");
    } finally {
      unlockProjectAction(lockKey);
    }
  };
  const startRemixWorkflow = async () => {
    if (!selected || !detail) return;
    const project = selected;
    const projectID = project.id;
    if (detail.assets?.continuous_script) {
      const confirmed = window.confirm(
        `当前项目已有连续文案 v${detail.assets.continuous_script.version}。再次二创会生成新版本，旧版本仍会保留。确定继续吗？`,
      );
      if (!confirmed) return;
    }
    const lockKey = lockProjectAction(projectID, "remix");
    if (!lockKey) return;
    try {
      const started = await startRemixWorkflowMutation.mutateAsync({
        projectID,
        body: {
          ...(projectTaskModel.model.trim() ? { model: projectTaskModel.model.trim() } : {}),
          ...(projectTaskModel.reasoningEffort
            ? { reasoning_effort: projectTaskModel.reasoningEffort }
            : {}),
        },
      });
      if (!started) {
        if (selectedIDRef.current === projectID)
          setMessage("二创工作流启动失败，请检查当前项目与 Codex 配置后重试。");
        return;
      }
      if (selectedIDRef.current !== projectID) return;
      setProjectTaskModel({ model: "", reasoningEffort: "" });
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("二创工作流启动失败，请检查网络连接后重试。");
    } finally {
      unlockProjectAction(lockKey);
    }
  };
  const publishProject = async () => {
    if (!selected) return;
    const project = selected;
    const projectID = project.id;
    const lockKey = lockProjectAction(projectID, "publish");
    if (!lockKey) return;
    try {
      const response = await api(`/api/projects/${projectID}/publish`, { method: "POST" });
      if (!response.ok) {
        if (selectedIDRef.current === projectID)
          setMessage("发布状态更新失败，请稍后重试。");
        return;
      }
      const published = { ...project, stage: "published" as const };
      setProjects((current) =>
        current.map((item) => item.id === projectID ? published : item),
      );
      if (selectedIDRef.current !== projectID) return;
      setSelected(published);
      client.setQueryData<ProjectDetail>(queryKeys.project(projectID), (current) =>
        current && current.project.id === projectID
          ? { ...current, project: { ...current.project, stage: "published" } }
          : current,
      );
      setMessage("项目已标记为已发布。");
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("发布状态更新失败，请检查网络连接后重试。");
    } finally {
      unlockProjectAction(lockKey);
    }
  };
  const saveContinuousScript = async (content: string) => {
    if (!selected) return;
    const project = selected;
    const projectID = project.id;
    const lockKey = lockProjectAction(projectID, "save-continuous-script");
    if (!lockKey) return;
    try {
      const saved = await saveContinuousScriptMutation.mutateAsync({ projectID, content });
      if (!saved) {
        if (selectedIDRef.current === projectID)
          setMessage("连续文案保存失败，请稍后重试。");
        return;
      }
      if (selectedIDRef.current !== projectID) return;
      setPreview((current) =>
        current?.asset.type === "continuous_script"
          ? { ...current, text: content }
          : current,
      );
      setPreviewDraft(content);
      setMessage("连续文案已保存为新版本；下游配音/字幕等可能已标记为失效。");
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("连续文案保存失败，请检查网络连接后重试。");
    } finally {
      unlockProjectAction(lockKey);
    }
  };
  const startRemixReview = async (notes: string) => {
    if (!selected || !detail?.assets.continuous_script) return;
    const project = selected;
    const projectID = project.id;
    const revisionNotes = notes.trim();
    if (!revisionNotes) {
      setMessage("请先填写修改要求，再打回重做。");
      return;
    }
    const lockKey = lockProjectAction(projectID, "remix-review");
    if (!lockKey) return;
    try {
      const started = await startProjectTaskMutation.mutateAsync({
        projectID,
        body: {
          account_id: project.account_id,
          type: "remix",
          action: "remix.review",
          prompt: `按修改要求重写当前连续文案。\n\n修改要求：\n${revisionNotes}`,
          revision_notes: revisionNotes,
          ...(projectTaskModel.model.trim() ? { model: projectTaskModel.model.trim() } : {}),
          ...(projectTaskModel.reasoningEffort
            ? { reasoning_effort: projectTaskModel.reasoningEffort }
            : {}),
        },
      });
      if (!started) {
        if (selectedIDRef.current === projectID)
          setMessage("打回重做任务启动失败，请检查当前连续文案与 Codex 配置后重试。");
        return;
      }
      if (selectedIDRef.current !== projectID) return;
      setReviseOpen(false);
      setProjectTaskModel({ model: "", reasoningEffort: "" });
      setMessage("已按修改要求打回 AI 重做；完成后会生成新的连续文案版本。");
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("打回重做任务启动失败，请检查网络连接后重试。");
    } finally {
      unlockProjectAction(lockKey);
    }
  };
  const uploadProjectAsset = async (
    type: "narration" | "subtitle_srt",
    file: File,
  ) => {
    if (!selected) return;
    const project = selected;
    const projectID = project.id;
    const lockKey = lockProjectAction(projectID, `upload:${type}`);
    if (!lockKey) return;
    if (type === "narration") {
      const name = file.name.toLowerCase();
      if (!(name.endsWith(".mp3") || name.endsWith(".wav") || name.endsWith(".m4a"))) {
        setMessage("配音仅支持 mp3、wav、m4a 文件。");
        unlockProjectAction(lockKey);
        return;
      }
    }
    if (type === "subtitle_srt" && !file.name.toLowerCase().endsWith(".srt")) {
      setMessage("字幕仅支持 .srt 文件。");
      unlockProjectAction(lockKey);
      return;
    }
    const body = new FormData();
    body.set("file", file);
    try {
      const response = await api(`/api/projects/${projectID}/assets/${type}`, {
        method: "POST",
        body,
      });
      if (!response.ok) {
        if (selectedIDRef.current !== projectID) return;
        let code = "";
        try {
          const payload = (await response.json()) as { code?: string };
          code = payload.code || "";
        } catch {
          /* ignore non-JSON bodies */
        }
        if (response.status === 413 || code === "payload_too_large")
          setMessage("素材过大，配音请控制在 200MB 以内。");
        else if (response.status === 403 || code === "csrf_invalid")
          setMessage("登录状态已失效，请刷新页面后重新登录再上传。");
        else if (response.status === 401 || code === "authentication_required")
          setMessage("未登录或会话过期，请重新登录后再上传。");
        else if (code === "invalid_asset_type")
          setMessage("当前服务不支持该素材类型，请重启控制台到最新版本后重试。");
        else setMessage("素材上传失败，请检查文件格式后重试。");
        return;
      }
      if (selectedIDRef.current === projectID) await loadDetail(project);
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("素材上传失败，请检查网络连接后重试。");
    } finally {
      unlockProjectAction(lockKey);
    }
  };
  const replaceProjectBackground = async (file: File) => {
    if (!selected) return;
    const project = selected;
    const projectID = project.id;
    const lockKey = lockProjectAction(projectID, "upload:account_background");
    if (!lockKey) return;
    const body = new FormData();
    body.set("background", file);
    try {
      const response = await api(`/api/accounts/${project.account_id}/background`, {
        method: "POST",
        body,
      });
      if (!response.ok) {
        if (selectedIDRef.current === projectID)
          setMessage("账号背景图上传失败，请选择 PNG、JPEG 或 WebP 图片后重试。");
        return;
      }
      if (selectedIDRef.current === projectID) await loadDetail(project);
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("账号背景图上传失败，请检查网络连接后重试。");
    } finally {
      unlockProjectAction(lockKey);
    }
  };
  const deleteProject = async () => {
    if (!selected) return;
    const project = selected;
    if (
      !window.confirm(
        `确定删除项目“${project.title}”吗？项目专属文案、配音、SRT、草稿和任务记录都会一并删除，此操作无法恢复。`,
      )
    )
      return;
    const projectID = project.id;
    const lockKey = lockProjectAction(projectID, "delete");
    if (!lockKey) return;
    try {
      const response = await api(`/api/projects/${projectID}`, { method: "DELETE" });
      if (!response.ok) {
        if (selectedIDRef.current === projectID)
          setMessage("项目删除失败，请稍后重试。");
        return;
      }
      setProjects((current) => current.filter((item) => item.id !== projectID));
      if (selectedIDRef.current !== projectID) return;
      closeProject();
      setMessage(`项目“${project.title}”已删除。`);
    } catch (error) {
      if (!isAbortError(error) && selectedIDRef.current === projectID)
        setMessage("项目删除失败，请检查网络连接后重试。");
    } finally {
      unlockProjectAction(lockKey);
    }
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
    const text = await response.text();
    setPreview({ asset, text });
    setPreviewDraft(text);
  };
  const openReviseDialog = () => {
    setReviseOpen(true);
    if (!selected) return;
    const projectID = selected.id;
    void (async () => {
      try {
        const response = await api(`/api/projects/${projectID}/notes/remix`);
        if (!response.ok || selectedIDRef.current !== projectID) return;
        const payload = (await response.json()) as { notes?: string };
        if (payload.notes) setReviseNotes(payload.notes);
      } catch {
        // 没有历史要求时保持空白输入即可。
      }
    })();
  };
  const openSettings = async () => {
    try {
      // staleTime 0 keeps the dialog's always-refetch-on-open behaviour.
      const next = await client.fetchQuery({
        queryKey: queryKeys.settings(),
        queryFn: ({ signal }) => readSettings(signal),
        staleTime: 0,
      });
      setSettingsDraft({ ...next.public });
      setSecretDraft({ grok_api_key: "", pexels_api_key: "" });
      setSettingsFeedback("");
      setSettingsOpen(true);
    } catch {
      setMessage("设置读取失败。");
    }
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
      setSettingsFeedback("设置保存失败，请检查填写内容。");
      return;
    }
    const next = (await response.json()) as Settings;
    client.setQueryData(queryKeys.settings(), next);
    setSettingsDraft({ ...next.public });
    setSecretDraft({ grok_api_key: "", pexels_api_key: "" });
    setSettingsFeedback("设置已保存。");
  };

  if (authenticated === null)
    return <div className="splash">正在验证访问权限…</div>;
  if (!authenticated)
    return (
      <LoginPage
        password={password}
        message={message}
        setPassword={setPassword}
        submit={login}
      />
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
    preview || reviseOpen || settingsOpen || ideaOpen || chatOpen || taskOpen,
  );
  const isLoopbackBrowser = ["localhost", "127.0.0.1", "::1", "[::1]"].includes(
    window.location.hostname.toLowerCase(),
  );
  const selectedPendingActions = selected
    ? pendingProjectActions
        .filter((key) => key.startsWith(`${selected.id}:`))
        .map((key) => key.slice(selected.id.length + 1))
    : [];

  return (
    <div className="shell">
      {selected && detail && detailReady ? (
        <ProjectWorkbench
          detail={detail as WorkbenchProjectDetail}
          tasks={tasks}
          accountName={accountName(selected.account_id, accounts)}
          message={message}
          theme={theme}
          onThemeChange={setTheme}
          onBack={closeProject}
          onDelete={() => void deleteProject()}
          onRemix={() => void startRemixWorkflow()}
          onMix={() => void startMontageTask("使用当前连续文案、配音、SRT 和固定背景图生成混剪草稿。")}
          onPublish={() => void publishProject()}
          onUpload={(type, file) => void uploadProjectAsset(type, file)}
          onSaveSourceScript={(content) => void saveSourceScriptAndStartRemix(content)}
          loadSourceScriptContent={loadSourceScriptContent}
          onReviseContinuousScript={openReviseDialog}
          taskModel={projectTaskModel}
          onTaskModelChange={(value) => setProjectTaskModel(value)}
          taskModelDefaults={settings?.public}
          onReplaceBackground={(file) => void replaceProjectBackground(file)}
          onViewAsset={(asset) => void openAsset(asset)}
          onOpenConversation={() => void openGeneralChat()}
          onOpenTask={(task) => openTask(task as Task)}
          pendingActions={selectedPendingActions}
        />
      ) : selected ? (
        <main className="project-workbench project-workbench--loading">
          <button type="button" className="workbench-icon-button" onClick={closeProject} aria-label="返回项目看板">
            <ArrowLeft size={19} aria-hidden="true" />
          </button>
          <div className="empty">
            <h1>{selected.title}</h1>
            <p>{detailError || "正在读取当前项目…"}</p>
            {detailError ? <button type="button" onClick={() => void loadDetail(selected)}>重试读取详情</button> : null}
          </div>
        </main>
      ) : (
        <>
      <header aria-hidden={modalLayerOpen || undefined}>
        <div>
          <span className="eyebrow">本机视频工作台</span>
          <h1>视频生产控制台</h1>
        </div>
        <div className="status">
          <label className="theme-control">
            主题
            <select
              aria-label="选择界面主题"
              value={theme}
              onChange={(event) => setTheme(event.target.value as Theme)}
            >
              <option value="light">日间</option>
              <option value="dark">夜间</option>
            </select>
          </label>
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
        <AccountSwitcher
          accounts={accounts}
          selectedAccountID={account}
          onSelectAccount={setAccount}
          formOpen={accountFormOpen}
          onToggleForm={() => setAccountFormOpen((open) => !open)}
          onCreate={createAccount}
          newAccountName={newAccount}
          onNewAccountNameChange={setNewAccount}
          backgroundSelected={Boolean(accountBackground)}
          onBackgroundChange={setAccountBackground}
        />
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
            <ProjectCreateForm
              accountSelected={Boolean(account)}
              title={newProject}
              onTitleChange={setNewProject}
              onSubmit={createProject}
            />
          </div>
          {message && (() => {
            const tone = messageTone(message);
            const urgent = tone === "danger";
            return (
              <div
                className={`notice notice--${tone}`}
                role={urgent ? "alert" : "status"}
                aria-live={urgent ? "assertive" : "polite"}
                aria-atomic="true"
              >
                {message}
                <button onClick={() => setMessage("")}>关闭</button>
              </div>
            );
          })()}
          {loading ? (
            <div className="empty">正在读取项目…</div>
          ) : (
            <>
              <section className="project-board" aria-labelledby="project-board-title">
                <div className="board-help">
                  <strong id="project-board-title">项目看板</strong>
                  <span>按生产阶段查看项目；点击项目卡片进入制作工作台。</span>
                </div>
                <div className="board">
                {stages.map((stage) => (
                  <section className={`column column--${stage}`} key={stage}>
                    <div className="column-head">
                      <h3>{stageLabel(stage)}</h3>
                      <b>
                        {
                          visible.filter((project) => project.stage === stage)
                            .length
                        }
                      </b>
                    </div>
                    {(() => {
                      const stageProjects = visible.filter((project) => project.stage === stage);
                      const expanded = expandedStages.has(stage);
                      const shownProjects = expanded
                        ? stageProjects
                        : stageProjects.slice(0, PROJECT_COLLAPSE_LIMIT);
                      return (
                        <>
                          {shownProjects.map((project) => (
                            <button
                              className="project"
                              key={project.id}
                              onClick={() => openProject(project)}
                            >
                              <strong>{project.title}</strong>
                              <small>
                                {projectStageHint(project.stage)}
                              </small>
                              <div className="project-foot">
                                <span>{accountName(project.account_id, accounts)}</span>
                                <span>{formatDate(project.updated_at)}</span>
                              </div>
                            </button>
                          ))}
                          {stageProjects.length > PROJECT_COLLAPSE_LIMIT ? (
                            <button
                              type="button"
                              className="column-toggle"
                              aria-expanded={expanded}
                              onClick={() =>
                                setExpandedStages((current) => {
                                  const next = new Set(current);
                                  if (next.has(stage)) next.delete(stage);
                                  else next.add(stage);
                                  return next;
                                })
                              }
                            >
                              {expanded
                                ? "收起项目"
                                : `展开剩余 ${stageProjects.length - PROJECT_COLLAPSE_LIMIT} 个项目`}
                            </button>
                          ) : null}
                        </>
                      );
                    })()}

                  </section>
                ))}
                </div>
              </section>
            </>
          )}
        </main>
      </div>
        </>
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
            <div className="modal-head">
              <div>
                <span className="muted">
                  {assetLabels[preview.asset.type] || preview.asset.type}
                </span>
                <h2 id="preview-dialog-title">{preview.asset.filename}</h2>
              </div>
              <button className="close" aria-label="关闭素材预览" onClick={() => setPreview(null)}>
                <X size={20} aria-hidden="true" />
              </button>
            </div>
            {preview.asset.type === "continuous_script" ? (
              <>
                <textarea
                  className="asset-editor"
                  aria-label="连续文案正文"
                  value={previewDraft}
                  onChange={(event) => setPreviewDraft(event.target.value)}
                />
                <div className="asset-editor-actions">
                  <button
                    type="button"
                    className="asset-editor-save"
                    onClick={() => void saveContinuousScript(previewDraft.trim())}
                    disabled={
                      !previewDraft.trim()
                      || previewDraft === preview.text
                      || selectedPendingActions.includes("save-continuous-script")
                    }
                    aria-busy={selectedPendingActions.includes("save-continuous-script")}
                  >
                    {selectedPendingActions.includes("save-continuous-script") ? "正在保存…" : "保存修改"}
                  </button>
                  <button
                    type="button"
                    className="asset-editor-revise"
                    onClick={() => {
                      setPreview(null);
                      openReviseDialog();
                    }}
                  >
                    <RotateCcw size={15} aria-hidden="true" />
                    打回重做
                  </button>
                </div>
              </>
            ) : (
              <pre className="asset-text">{preview.text}</pre>
            )}
          </section>
        </div>
      )}
      {reviseOpen && (
        <div className="modal-backdrop" onClick={() => setReviseOpen(false)}>
          <section
            className="preview-modal revise-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="revise-dialog-title"
            tabIndex={-1}
            onClick={(event) => event.stopPropagation()}
          >
            <div className="modal-head">
              <div>
                <span className="muted">连续文案</span>
                <h2 id="revise-dialog-title">打回重做</h2>
              </div>
              <button className="close" aria-label="关闭打回重做" onClick={() => setReviseOpen(false)}>
                <X size={20} aria-hidden="true" />
              </button>
            </div>
            <label className="revise-modal__notes">
              修改要求
              <textarea
                aria-label="二创修改要求"
                value={reviseNotes}
                onChange={(event) => setReviseNotes(event.target.value)}
                placeholder="例如：开场更口语、缩短前 20 秒、少用排比…"
              />
            </label>
            <TaskModelFields
              value={projectTaskModel}
              onChange={setProjectTaskModel}
              defaults={settings?.public}
              labelPrefix="打回"
            />
            <button
              type="button"
              className="revise-modal__submit"
              onClick={() => void startRemixReview(reviseNotes)}
              disabled={
                !reviseNotes.trim()
                || detail?.assets.continuous_script?.state !== "ready"
                || selectedPendingActions.includes("remix-review")
              }
              aria-busy={selectedPendingActions.includes("remix-review")}
            >
              {selectedPendingActions.includes("remix-review") ? "正在打回重做…" : "打回重做"}
            </button>
          </section>
        </div>
      )}
      {settingsOpen && settingsDraft && (
        <SettingsPanel
          settings={settings}
          draft={settingsDraft}
          onDraftChange={setSettingsDraft}
          secretDraft={secretDraft}
          onSecretDraftChange={setSecretDraft}
          feedback={settingsFeedback}
          onClose={() => {
            setSettingsFeedback("");
            setSettingsOpen(false);
          }}
          onSubmit={saveSettings}
        />
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
            <div className="modal-head">
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
                <button className="close" aria-label="关闭选题规划" onClick={() => setIdeaOpen(false)}>
                  <X size={20} aria-hidden="true" />
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
                        <X size={16} aria-hidden="true" />
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
                  <label htmlFor="idea-message-input">发送选题消息</label>
                  <input
                    id="idea-message-input"
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
                <div>
                  <strong>Codex 对话</strong>
                  <small>项目内沟通与本机历史</small>
                </div>
                <button className="chat-create-primary" disabled={chatCreating} onClick={() => { setChatCreationSource("console"); void createGeneralChat("console"); }}>
                  {chatCreating && chatCreationSource === "console" ? "创建中…" : "新建对话"}
                </button>
                <button className="chat-create-secondary" disabled={chatCreating} onClick={() => { setChatCreationSource("desktop"); void createGeneralChat("desktop"); }}>
                  {chatCreating && chatCreationSource === "desktop" ? "创建中…" : "在桌面版新建"}
                </button>
              </div>
              <section className="chat-rail-section chat-current-sessions" aria-label="当前对话">
                <div className="chat-rail-section__head">
                  <strong>当前对话</strong>
                  <small>{chatSessions.length} 个</small>
                </div>
                <div className="chat-session-list">
                  {chatSessions.map((session) => (
                    <div className="chat-session-item" key={session.id}>
                      <button
                        className={chatDetail?.session.id === session.id ? "selected" : ""}
                        onClick={() => void loadChatSession(session)}
                      >
                        <strong>{session.title}</strong>
                        <small>{session.source === "desktop" ? "桌面版" : "控制台"} · {session.status === "running" ? "处理中" : "可继续"}</small>
                      </button>
                      <button
                        className="chat-session-delete"
                        aria-label={`删除对话 ${session.title}`}
                        onClick={() => void deleteChatSession(session)}
                      >
                        <X size={16} aria-hidden="true" />
                      </button>
                    </div>
                  ))}
                  {!chatSessions.length ? <p className="chat-rail-empty">还没有当前对话</p> : null}
                </div>
              </section>
              <section className="chat-rail-section chat-history-section" aria-label="本机历史">
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
                          继续
                        </button>
                        <button className="history-fork" onClick={() => void applyHistoryThread(thread, "fork")}>
                          复制
                        </button>
                      </div>
                    </article>
                  ))}
                  {!historyThreads.length && <p className="chat-rail-empty">暂无可接入的本机历史</p>}
                </div>
                <p className="history-inline-help">“继续”接回原会话；“复制”会新建副本，不影响原会话。</p>
              </section>
            </aside>
            <div className="chat-main">
              <div className="chat-main-head">
                <div>
                  <span className="eyebrow">{chatDetail?.session.source === "desktop" ? "桌面版会话" : "控制台会话"}</span>
                  <h2 id="chat-dialog-title">{chatDetail?.session.title || "新建一个 Codex 对话"}</h2>
                  {chatDetail?.session ? (
                    <p className="chat-session-meta">
                      {chatDetail.session.status === "running" ? "Codex 正在处理" : "可以继续对话"}
                      {chatDetail.session.model ? ` · ${chatDetail.session.model}` : ""}
                      {chatDetail.session.reasoning_effort ? ` · ${chatDetail.session.reasoning_effort}` : ""}
                    </p>
                  ) : null}
                </div>
                <button className="close" aria-label="关闭 Codex 对话" onClick={() => setChatOpen(false)}>
                  <X size={20} aria-hidden="true" />
                </button>
              </div>
              <div className="chat-messages" aria-live="polite">
                {visibleChatMessages.map((item, index) => (
                  <article className={`chat-bubble ${item.role}`} key={item.id || `${item.role}-${item.created_at}-${index}`}>
                    <b>{item.role === "user" ? "你" : "Codex"}</b>
                    <p>{item.content}</p>
                    {item.role === "user" && item.delivery_status === "queued" ? <small>已排队</small> : null}
                  </article>
                ))}
                {!visibleChatMessages.length && !technicalChatMessages.length && (
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
                <div className="chat-compose__inner">
                  {chatDetail?.session.status === "running" ? (
                    <p className="chat-compose-note">Codex 正在处理。现在发送会作为补充要求送入当前任务。</p>
                  ) : null}
                  <label htmlFor="chat-compose-input">发送消息</label>
                  <textarea
                    id="chat-compose-input"
                    value={chatInput}
                    onChange={(event) => setChatInput(event.target.value)}
                    placeholder="描述你要调整的内容或补充要求"
                    rows={3}
                  />
                  <button disabled={!chatInput.trim() || chatSending || !chatDetail}>
                    {chatSending ? "发送中" : "发送"}
                  </button>
                </div>
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
            <div className="modal-head">
              <div>
                <span className="muted">Codex 任务详情</span>
                  <h2 id="task-dialog-title">{taskTitle(taskOpen)}</h2>
                {selected ? <p className="task-project-context">当前项目：{selected.title}</p> : null}
              </div>
              <button className="close" aria-label="关闭任务详情" onClick={closeTask}>
                <X size={20} aria-hidden="true" />
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
              {typeof taskElapsedMS(taskOpen, timingNow) === "number" ? (
                <span aria-label="任务总耗时">总耗时 {formatDuration(taskElapsedMS(taskOpen, timingNow))}</span>
              ) : null}
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
                    <details className="registered-directory-technical">
                      <summary>路径与文件清单</summary>
                      <dl>
                        <dt>正式项目资产</dt><dd>{taskOpen.montage.registered_asset.filename || "未命名草稿"}</dd>
                        <dt>剪映路径</dt>
                        <dd>{directoryManifest?.registered_path || taskOpen.montage.registered_asset.path}</dd>
                      </dl>
                      {directoryManifest ? (
                        <div className="directory-manifest">
                          <p>目录文件清单（{directoryManifest.entries.length} 项）</p>
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
                        </div>
                      ) : null}
                    </details>
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
            {taskOpen.timing_summary || taskOpen.timing_runs?.length || typeof taskElapsedMS(taskOpen, timingNow) === "number" ? (
              <section className="task-timing" aria-label="任务阶段耗时">
                <h3>阶段耗时</h3>
                {taskOpen.timing_summary ? (
                  <p className="task-timing-summary">
                    总计 {formatDuration(timingValue(taskOpen.timing_summary, "total_ms") || taskElapsedMS(taskOpen, timingNow))} · 准备 {formatDuration(timingValue(taskOpen.timing_summary, "preparation_ms"))} · 队列 {formatDuration(timingValue(taskOpen.timing_summary, "queue_ms"))}{taskOpen.timing_summary.queue_estimated ? "（边界估算）" : ""} · 执行 {formatDuration(timingValue(taskOpen.timing_summary, "execution_ms"))}
                  </p>
                ) : (
                  <p className="task-timing-summary">
                    总计 {formatDuration(taskElapsedMS(taskOpen, timingNow))}
                  </p>
                )}
                <ul className="task-timing-list">
                  {taskTimingPhases(taskOpen).map((phase, index) => {
                    const phaseID = phaseStringValue(phase, "id");
                    const phaseKey = phaseStringValue(phase, "phase_key");
                    const state = phaseStringValue(phase, "state");
                    const duration = phaseDurationMS(phase, timingNow);
                    return (
                      <li key={phaseID || `${phaseKey}-${index}`}>
                        <strong>{phaseStringValue(phase, "display_name") || phaseKey || "未命名阶段"}</strong>
                        <span>{taskPhaseStateLabels[state] || state || "暂无状态"}</span>
                        <time aria-label={state === "running" ? "运行时长" : "阶段耗时"}>
                          {formatDuration(duration)}
                        </time>
                      </li>
                    );
                  })}
                </ul>
                {taskOpen.timing_summary?.legacy_without_phases ? <p className="muted">该任务没有已持久化的阶段运行记录，不能据此判定阶段是否开始。</p> : null}
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
                    {item.display_text || "技术事件"}
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
                  <label htmlFor="task-answer-input">回答 Codex</label>
                  <textarea
                    id="task-answer-input"
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

type TimingNumberKey = "total_ms" | "preparation_ms" | "queue_ms" | "execution_ms";
type PhaseStringKey = "id" | "phase_key" | "display_name" | "state" | "started_at";

function timingValue(summary: TaskTimingSummary | undefined, key: TimingNumberKey) {
  const value = summary?.[key];
  return typeof value === "number" ? value : 0;
}
function phaseStringValue(phase: TaskPhaseRun, key: PhaseStringKey) {
  const value = phase[key];
  return typeof value === "string" ? value : "";
}
function taskTimingPhases(task: Task) {
  if (task.timing_runs?.length) return task.timing_runs;
  return task.timing_summary?.phases || [];
}
function isRunningPhase(phase: TaskPhaseRun) {
  return phaseStringValue(phase, "state") === "running";
}
function phaseDurationMS(phase: TaskPhaseRun, now: number) {
  if (typeof phase.duration_ms === "number") return phase.duration_ms;
  if (!isRunningPhase(phase)) return undefined;
  const startedAt = Date.parse(phaseStringValue(phase, "started_at"));
  return Number.isFinite(startedAt) ? Math.max(0, now - startedAt) : undefined;
}
function taskElapsedMS(task: Task, now: number) {
  const summaryTotal = timingValue(task.timing_summary, "total_ms");
  if (summaryTotal > 0) return summaryTotal;
  const finishedAt = Date.parse(task.finished_at || "");
  const startedAt = Date.parse(task.started_at || task.created_at || "");
  if (Number.isFinite(finishedAt) && Number.isFinite(startedAt) && finishedAt >= startedAt) {
    return finishedAt - startedAt;
  }
  if (
    Number.isFinite(startedAt)
    && ["queued", "running", "awaiting_input", "waiting_input", "resuming"].includes(task.status)
  ) {
    return Math.max(0, now - startedAt);
  }
  return undefined;
}
function formatDuration(ms?: number) {
  if (typeof ms !== "number") return "暂无";
  if (ms < 1000) return `${ms} ms`;
  const totalSeconds = ms / 1000;
  if (totalSeconds < 60) return `${totalSeconds.toFixed(1)} s`;
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds - minutes * 60;
  if (minutes < 60) return `${minutes} 分 ${seconds.toFixed(0)} 秒`;
  const hours = Math.floor(minutes / 60);
  const remainMinutes = minutes % 60;
  return `${hours} 小时 ${remainMinutes} 分`;
}

function stageLabel(stage: Project["stage"]) {
  return (
    (
      {
        topic: "选题准备",
        script: "文案制作",
        assets: "配音字幕",
        mixing: "混剪制作",
        review: "成片审核",
        published: "已发布",
      } as Record<string, string>
    )[stage] || stage
  );
}
function projectStageHint(stage: Project["stage"]) {
  return (
    (
      {
        topic: "正在确定选题或生成选题卡",
        script: "选题卡已就绪，正在制作文案",
        assets: "文案已登记，正在准备配音和 SRT",
        mixing: "配音和 SRT 已齐，正在制作混剪",
        review: "检查发布文案并确认发布状态",
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
