import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import { apiRequest } from "./api/client";
import { AssetPreviewDialog } from "./assets/AssetPreviewDialog";
import { ReviseDialog } from "./assets/ReviseDialog";
import { LoginPage } from "./auth/LoginPage";
import { ChatWorkbenchDialog } from "./chat/ChatWorkbenchDialog";
import { SettingsPanel } from "./settings/SettingsPanel";
import { useConsoleData } from "./console/useConsoleData";
import { IdeaPlannerDialog } from "./idea/IdeaPlannerDialog";
import { queryKeys } from "./query/keys";
import "./App.css";
import "./idea.css";
import { parseLocation } from "./project-workbench/routes";
import type { TaskModelOverride } from "./taskModel";
import { ProjectWorkbench } from "./project-workbench/ProjectWorkbench";
import { accountName } from "./projects/stages";
import { ConsoleHome } from "./shell/ConsoleHome";
import { useRuntimeQuery } from "./runtime/useRuntimeQuery";
import { TaskDetailDialog } from "./tasks/TaskDetailDialog";
import {
  derivedMontagePhase,
  isRunningPhase,
  liveTaskStatuses,
  normalizeSemanticEvents,
  taskQuestions,
  taskTimingPhases,
} from "./tasks/task-view";
import type { SemanticEvent } from "./tasks/event-types";
import type { ProjectDetail as WorkbenchProjectDetail } from "./project-workbench/types";
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
  Theme,
} from "./types";

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

const THEME_STORAGE_KEY = "video-production-console-theme";

function readStoredTheme(): Theme {
  if (typeof window === "undefined") return "light";
  return window.localStorage.getItem(THEME_STORAGE_KEY) === "dark" ? "dark" : "light";
}

const textAssets = new Set([
  "source_script",
  "topic_card",
  "continuous_script",
  "spoken_script",
  "subtitle",
  "subtitle_srt",
]);
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
        <ConsoleHome
          hidden={modalLayerOpen}
          theme={theme}
          onThemeChange={setTheme}
          runtime={runtime}
          onOpenIdeaPlanner={() => void openIdeaPlanner()}
          onOpenConversation={() => void openGeneralChat()}
          onOpenSettings={() => void openSettings()}
          onLogout={() => void logout()}
          accounts={accounts}
          selectedAccountID={account}
          onSelectAccount={setAccount}
          accountFormOpen={accountFormOpen}
          onToggleAccountForm={() => setAccountFormOpen((open) => !open)}
          onCreateAccount={createAccount}
          newAccountName={newAccount}
          onNewAccountNameChange={setNewAccount}
          accountBackgroundSelected={Boolean(accountBackground)}
          onAccountBackgroundChange={setAccountBackground}
          newProject={newProject}
          onNewProjectChange={setNewProject}
          onCreateProject={createProject}
          message={message}
          onDismissMessage={() => setMessage("")}
          loading={loading}
          projects={visible}
          expandedStages={expandedStages}
          onExpandedStagesChange={setExpandedStages}
          onOpenProject={openProject}
        />
      )}
      {preview && (
        <AssetPreviewDialog
          preview={preview}
          draft={previewDraft}
          onDraftChange={setPreviewDraft}
          saving={selectedPendingActions.includes("save-continuous-script")}
          onClose={() => setPreview(null)}
          onSave={(content) => void saveContinuousScript(content)}
          onRevise={() => {
            setPreview(null);
            openReviseDialog();
          }}
        />
      )}
      {reviseOpen && (
        <ReviseDialog
          notes={reviseNotes}
          onNotesChange={setReviseNotes}
          taskModel={projectTaskModel}
          onTaskModelChange={setProjectTaskModel}
          taskModelDefaults={settings?.public}
          submitDisabled={
            !reviseNotes.trim()
            || detail?.assets.continuous_script?.state !== "ready"
            || selectedPendingActions.includes("remix-review")
          }
          submitting={selectedPendingActions.includes("remix-review")}
          onClose={() => setReviseOpen(false)}
          onSubmit={(notes) => void startRemixReview(notes)}
        />
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
        <IdeaPlannerDialog
          session={ideaSession}
          onSessionChange={setIdeaSession}
          sessions={ideaSessions}
          draft={Boolean(ideaDraft)}
          accounts={accounts}
          task={ideaTask}
          creatingProject={ideaCreatingProject}
          input={ideaInput}
          onInputChange={setIdeaInput}
          taskModel={ideaTaskModel}
          onTaskModelChange={setIdeaTaskModel}
          taskModelDefaults={settings?.public}
          onClose={() => setIdeaOpen(false)}
          onCreateConversation={() => void createIdeaConversation()}
          onSwitchConversation={(session) => void switchIdeaConversation(session)}
          onDeleteConversation={(session) => void deleteIdeaConversation(session)}
          onSelectCandidate={(candidate) => void selectIdeaCandidate(candidate)}
          onSubmit={sendIdeaMessage}
        />
      )}
      {chatOpen && (
        <ChatWorkbenchDialog
          detail={chatDetail}
          sessions={chatSessions}
          visibleMessages={visibleChatMessages}
          technicalMessages={technicalChatMessages}
          creating={chatCreating}
          creationSource={chatCreationSource}
          onCreationSourceChange={setChatCreationSource}
          onCreate={(source) => void createGeneralChat(source)}
          onSelectSession={(session) => void loadChatSession(session)}
          onDeleteSession={(session) => void deleteChatSession(session)}
          settings={settings}
          historySource={historySource}
          onHistorySourceChange={(source) => {
            setHistorySource(source);
            void refreshHistory(source);
          }}
          historyThreads={historyThreads}
          onApplyHistoryThread={(thread, mode) => void applyHistoryThread(thread, mode)}
          input={chatInput}
          onInputChange={setChatInput}
          sending={chatSending}
          onClose={() => setChatOpen(false)}
          onSubmit={sendChatMessage}
        />
      )}
      {taskOpen && (
        <TaskDetailDialog
          task={taskOpen}
          projectTitle={selected?.title || ""}
          timingNow={timingNow}
          directoryManifest={directoryManifest}
          directoryManifestStatus={directoryManifestStatus}
          canOpenDirectory={isLoopbackBrowser}
          openingDirectory={openingDirectory}
          answerInput={taskAnswerInput}
          onAnswerInputChange={setTaskAnswerInput}
          onClose={closeTask}
          onCancelTask={(task) => void cancelTask(task)}
          onRetryRegistration={(task) => void retryMontageRegistration(task)}
          onOpenDirectory={(assetID) => void openRegisteredDirectory(assetID)}
          onAnswer={(task, answer) => void answerTask(task, answer)}
        />
      )}
    </div>
  );
}

export default App;
