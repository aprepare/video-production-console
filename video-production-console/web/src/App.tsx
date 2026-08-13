import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import { apiRequest } from "./api/client";
import { AssetPreviewDialog } from "./assets/AssetPreviewDialog";
import { ReviseDialog } from "./assets/ReviseDialog";
import { LoginPage } from "./auth/LoginPage";
import { SettingsPanel } from "./settings/SettingsPanel";
import { ImageModeWorkbench } from "./image-mode/ImageModeWorkbench";
import { MediaLibraryPanel } from "./media-library/MediaLibraryPanel";
import { useSettingsDialog } from "./settings/useSettingsDialog";
import { useConsoleData } from "./console/useConsoleData";
import { IdeaPlannerDialog } from "./idea/IdeaPlannerDialog";
import { useIdeaPlanner } from "./idea/useIdeaPlanner";
import { queryKeys } from "./query/keys";
import "./App.css";
import "./idea.css";
import { parseLocation } from "./project-workbench/routes";
import { ProjectWorkbench } from "./project-workbench/ProjectWorkbench";
import { accountName } from "./projects/stages";
import { useProjectActions } from "./projects/useProjectActions";
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
  DirectoryManifest,
  Project,
  ProjectDetail,
  Settings,
  Task,
  Theme,
} from "./types";

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
  const [productionMode, setProductionMode] = useState<"montage" | "image">("montage");
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
  const [accountFormOpen, setAccountFormOpen] = useState(false);
  const [mediaLibraryOpen, setMediaLibraryOpen] = useState(false);
  const [taskOpen, setTaskOpen] = useState<Task | null>(null);
  const [timingNow, setTimingNow] = useState(() => Date.now());
  const [taskAnswerInput, setTaskAnswerInput] = useState("");
  const [directoryManifest, setDirectoryManifest] = useState<DirectoryManifest | null>(null);
  const [directoryManifestStatus, setDirectoryManifestStatus] = useState("");
  const [openingDirectory, setOpeningDirectory] = useState(false);
  const [urlRevision, setURLRevision] = useState(0);
  const handledURLRevisionRef = useRef(0);
  const selectedIDRef = useRef("");
  const detailRefreshTimerRef = useRef<number | null>(null);
  const taskCacheRef = useRef(new Map<string, Task>());
  const taskOpenIDRef = useRef("");
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const dialogWasOpenRef = useRef(false);
  const activeDialogRef = useRef<HTMLElement | null>(null);
  const nestedDialogFocusRef = useRef<HTMLElement[]>([]);
  const taskRestoreAbortRef = useRef<AbortController | null>(null);
  const clearProjectSelection = useCallback(() => {
    selectedIDRef.current = "";
    if (detailRefreshTimerRef.current !== null)
      window.clearTimeout(detailRefreshTimerRef.current);
    setSelected(null);
  }, []);
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

  const accountIDs = useMemo(() => accounts.map((item) => item.id), [accounts]);
  const idea = useIdeaPlanner({
    api,
    account,
    accountIDs,
    setMessage,
    onProjectCreated: (project, topicCardTaskError) => {
      setProjects((current) => [
        project,
        ...current.filter((item) => item.id !== project.id),
      ]);
      setAccount(project.account_id);
      setMessage(
        topicCardTaskError
          ? `项目已创建，但正式选题卡任务未启动：${topicCardTaskError}`
          : `项目已创建到“${accountName(project.account_id, accounts)}”，正在把正式选题卡写入 Obsidian。`,
      );
      openProject(project);
    },
  });
  const { open: ideaOpen, setOpen: setIdeaOpen } = idea;
  const settingsPanel = useSettingsDialog({ api, readSettings, setMessage });
  const { open: settingsOpen, setOpen: setSettingsOpen } = settingsPanel;

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
  const projectActions = useProjectActions({
    api,
    selected,
    setSelected,
    detail,
    refreshProject,
    setProjects,
    setMessage,
    selectedIDRef,
    newProjectTitle: newProject,
    selectedAccountID: account,
    onProjectCreated: () => setNewProject(""),
    onContinuousScriptSaved: (content) => {
      setPreview((current) =>
        current?.asset.type === "continuous_script" ? { ...current, text: content } : current,
      );
      setPreviewDraft(content);
    },
    onRemixReviewStarted: () => setReviseOpen(false),
    onProjectDeleted: () => closeProject(),
  });

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
      preview || reviseOpen || settingsOpen || ideaOpen || taskOpen,
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
      } else if (ideaOpen) setIdeaOpen(false);
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
  }, [clearProjectSelection, ideaOpen, preview, reviseOpen, selected, setIdeaOpen, setSettingsOpen, settingsOpen, taskOpen]);
  useEffect(() => () => {
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
    if (!selected || preview || settingsOpen || ideaOpen || taskOpen) return;
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
  }, [clearProjectSelection, ideaOpen, preview, selected, settingsOpen, taskOpen]);
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

  const modalLayerOpen = Boolean(
    preview || reviseOpen || settingsOpen || idea.open || taskOpen || mediaLibraryOpen,
  );
  const isLoopbackBrowser = ["localhost", "127.0.0.1", "::1", "[::1]"].includes(
    window.location.hostname.toLowerCase(),
  );
  const selectedPendingActions = selected
    ? projectActions.pendingActions
        .filter((key) => key.startsWith(`${selected.id}:`))
        .map((key) => key.slice(selected.id.length + 1))
    : [];

  return (
    <div className="shell">
      {productionMode === "image" && !selected ? (
        <>
          <header>
            <div><span className="eyebrow">本机视频工作台</span><h1>视频生产控制台</h1></div>
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
              <div className="mode-switch" role="group" aria-label="生产模式">
                <button type="button" aria-pressed={false} onClick={() => setProductionMode("montage")}>混剪模式</button>
                <button type="button" aria-pressed className="active" onClick={() => setProductionMode("image")}>图文模式</button>
              </div>
              <button className="header-button" onClick={() => void settingsPanel.openDialog()}>设置</button>
              <button className="header-button" onClick={() => void logout()}>退出</button>
            </div>
          </header>
          <ImageModeWorkbench
            api={api}
            defaultRatio={settings?.public.default_image_ratio}
            defaultStyle={settings?.public.default_image_style}
            defaultConcurrency={settings?.public.max_image_concurrency}
            defaultTextModel={settings?.public.image_text_model}
          />
        </>
      ) : selected && detail && detailReady ? (
        <ProjectWorkbench
          detail={detail as WorkbenchProjectDetail}
          tasks={tasks}
          accountName={accountName(selected.account_id, accounts)}
          message={message}
          theme={theme}
          onThemeChange={setTheme}
          onBack={closeProject}
          onDelete={() => void projectActions.deleteProject()}
          onRemix={() => void projectActions.startRemixWorkflow()}
          onMix={() => void projectActions.startMontageTask("使用当前连续文案、配音、SRT 和固定背景图生成混剪草稿。")}
          onPublish={() => void projectActions.publishProject()}
          onUpload={(type, file) => void projectActions.uploadAsset(type, file)}
          onSaveSourceScript={(content) => void projectActions.saveSourceScriptAndStartRemix(content)}
          loadSourceScriptContent={projectActions.loadSourceScriptContent}
          onReviseContinuousScript={openReviseDialog}
          onGenerateNarration={() => void projectActions.generateNarration()}
          taskModel={projectActions.taskModel}
          onTaskModelChange={(value) => projectActions.setTaskModel(value)}
          taskModelDefaults={settings?.public}
          onReplaceBackground={(file) => void projectActions.replaceBackground(file)}
          onViewAsset={(asset) => void openAsset(asset)}
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
          onOpenIdeaPlanner={() => void idea.openPlanner()}
          onOpenMediaLibrary={() => setMediaLibraryOpen(true)}
          onOpenSettings={() => void settingsPanel.openDialog()}
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
          onCreateProject={projectActions.createProject}
          message={message}
          onDismissMessage={() => setMessage("")}
          loading={loading}
          projects={visible}
          expandedStages={expandedStages}
          onExpandedStagesChange={setExpandedStages}
          onOpenProject={openProject}
          mode={productionMode}
          onModeChange={setProductionMode}
        />
      )}
      {preview && (
        <AssetPreviewDialog
          preview={preview}
          draft={previewDraft}
          onDraftChange={setPreviewDraft}
          saving={selectedPendingActions.includes("save-continuous-script")}
          onClose={() => setPreview(null)}
          onSave={(content) => void projectActions.saveContinuousScript(content)}
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
          taskModel={projectActions.taskModel}
          onTaskModelChange={projectActions.setTaskModel}
          taskModelDefaults={settings?.public}
          submitDisabled={
            !reviseNotes.trim()
            || detail?.assets.continuous_script?.state !== "ready"
            || selectedPendingActions.includes("remix-review")
          }
          submitting={selectedPendingActions.includes("remix-review")}
          onClose={() => setReviseOpen(false)}
          onSubmit={(notes) => void projectActions.startRemixReview(notes)}
        />
      )}
      {mediaLibraryOpen && (
        <MediaLibraryPanel api={api} onClose={() => setMediaLibraryOpen(false)} />
      )}
      {settingsOpen && settingsPanel.draft && (
        <SettingsPanel
          settings={settings}
          draft={settingsPanel.draft}
          onDraftChange={settingsPanel.setDraft}
          secretDraft={settingsPanel.secretDraft}
          onSecretDraftChange={settingsPanel.setSecretDraft}
          feedback={settingsPanel.feedback}
          onClose={settingsPanel.close}
          onSubmit={settingsPanel.save}
        />
      )}
      {ideaOpen && idea.session && (
        <IdeaPlannerDialog
          session={idea.session}
          onSessionChange={idea.setSession}
          sessions={idea.sessions}
          draft={Boolean(idea.draft)}
          accounts={accounts}
          task={idea.task}
          creatingProject={idea.creatingProject}
          input={idea.input}
          onInputChange={idea.setInput}
          taskModel={idea.taskModel}
          onTaskModelChange={idea.setTaskModel}
          taskModelDefaults={settings?.public}
          onClose={() => setIdeaOpen(false)}
          onCreateConversation={() => void idea.createConversation()}
          onSwitchConversation={(session) => void idea.switchConversation(session)}
          onDeleteConversation={(session) => void idea.deleteConversation(session)}
          onSelectCandidate={(candidate) => void idea.selectCandidate(candidate)}
          onSubmit={idea.sendMessage}
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
