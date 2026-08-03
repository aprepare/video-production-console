import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import "./App.css";
import "./idea.css";

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
  created_at: string;
};
type TaskMessage = {
  id: string;
  role: string;
  content: string;
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
type Task = {
  id: string;
  project_id?: string;
  type: string;
  skill_name: string;
  status: string;
  prompt_snapshot?: string;
  result_summary?: string;
  error_message?: string;
  created_at: string;
  messages?: TaskMessage[];
  events?: TaskEvent[];
};
type RuntimeStatus = { Limit: number; Running: number; Queued: number };
type ProjectDetail = {
  project: Project;
  assets: Record<string, Asset>;
  asset_history?: Record<string, Asset[]>;
  missing_assets?: string[];
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
};
type Settings = {
  public: PublicSettings;
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
const textAssets = new Set(["continuous_script", "spoken_script", "subtitle"]);
const settingFields: Array<[keyof PublicSettings, string, string]> = [
  ["baokuan_base_url", "爆款库地址", "http://127.0.0.1:2022"],
  ["obsidian_vault", "Obsidian Vault", "本地 Vault 目录"],
  ["topic_cards_dir", "选题卡目录", "Vault 内选题卡目录"],
  ["grok_base_url", "Grok 服务地址", "OpenAI 兼容 API 地址"],
  ["grok_model", "Grok 模型", "模型名称"],
  ["codex_binary_path", "Codex CLI 路径", "codex 可执行文件路径"],
  ["media_index_path", "素材索引", "媒体索引文件"],
  ["media_root", "媒体素材目录", "本地媒体根目录"],
  ["jianying_root", "剪映草稿目录", "剪映草稿根目录"],
];

function taskEventProgress(event: TaskEvent) {
  const kind = event.kind || event.Kind || "";
  const displayText = event.display_text || event.DisplayText || "";
  const raw = event.raw_json || event.RawJSON || "";
  if (displayText) return displayText;
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
  if (task.status === "completed") return "已完成，正在载入候选选题";
  return statusLabels[task.status] || task.status;
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
  const [newProject, setNewProject] = useState("");
  const [message, setMessage] = useState("");
  const [selected, setSelected] = useState<Project | null>(null);
  const [detail, setDetail] = useState<ProjectDetail | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [detailLoading, setDetailLoading] = useState(false);
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
  const [ideaTask, setIdeaTask] = useState<Task | null>(null);
  const [ideaInput, setIdeaInput] = useState("");
  const [taskOpen, setTaskOpen] = useState<Task | null>(null);
  const [runtime, setRuntime] = useState<RuntimeStatus | null>(null);
  const activeTasks = useMemo(
    () =>
      tasks.filter((task) =>
        [
          "queued",
          "running",
          "awaiting_input",
          "resuming",
          "waiting_input",
        ].includes(task.status),
      ),
    [tasks],
  );

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

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [a, p] = await Promise.all([
        api("/api/accounts"),
        api("/api/projects"),
      ]);
      if (!a.ok || !p.ok) throw new Error("读取控制台数据失败");
      setAccounts(await a.json());
      setProjects(await p.json());
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "控制台服务尚未连接");
    } finally {
      setLoading(false);
    }
  }, [api]);

  const loadDetail = useCallback(
    async (project: Project) => {
      setDetailLoading(true);
      try {
        const [p, t] = await Promise.all([
          api(`/api/projects/${project.id}`),
          api(`/api/tasks?project_id=${project.id}`),
        ]);
        if (!p.ok || !t.ok) throw new Error("读取项目详情失败");
        const listed = (await t.json()) as Task[];
        const fullTasks = await Promise.all(
          listed.map(async (task) => {
            const taskResponse = await api(`/api/tasks/${task.id}`);
            return taskResponse.ok
              ? ((await taskResponse.json()) as Task)
              : task;
          }),
        );
        setDetail(await p.json());
        setTasks(fullTasks);
      } catch (error) {
        setMessage(error instanceof Error ? error.message : "读取项目详情失败");
      } finally {
        setDetailLoading(false);
      }
    },
    [api],
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
    if (!ideaOpen || !ideaSession) return;
    const refresh = async () => {
      const response = await api(`/api/ideas/${ideaSession.id}`);
      if (!response.ok) return;
      const detail = (await response.json()) as IdeaSessionDetail;
      const messages = detail.messages || [];
      setIdeaSession({
        ...detail.session,
        messages,
        candidates: detail.candidates || [],
      });
      const taskID = [...messages]
        .reverse()
        .find((message) => message.task_id)?.task_id;
      if (!taskID) {
        setIdeaTask(null);
        return;
      }
      const taskResponse = await api(`/api/tasks/${taskID}`);
      if (taskResponse.ok) setIdeaTask((await taskResponse.json()) as Task);
    };
    void refresh();
    const timer = window.setInterval(() => void refresh(), 2500);
    return () => window.clearInterval(timer);
  }, [api, ideaOpen, ideaSession?.id]);
  useEffect(() => {
    if (!authenticated) return;
    const refresh = async () => {
      const response = await api("/api/runtime");
      if (response.ok) setRuntime((await response.json()) as RuntimeStatus);
    };
    void refresh();
    const timer = window.setInterval(() => void refresh(), 5000);
    return () => window.clearInterval(timer);
  }, [authenticated, api]);
  useEffect(() => {
    if (!selected) return;
    const timer = window.setInterval(() => void loadDetail(selected), 5000);
    return () => window.clearInterval(timer);
  }, [selected, loadDetail]);
  useEffect(() => {
    if (!selected || !activeTasks.length) return;
    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    const sockets = activeTasks.map((task) => {
      const socket = new WebSocket(
        `${protocol}//${location.host}/api/tasks/${task.id}/events?after=0`,
      );
      socket.onmessage = () => void loadDetail(selected);
      return socket;
    });
    return () => sockets.forEach((socket) => socket.close());
  }, [selected, activeTasks, loadDetail]);
  useEffect(() => {
    const onTaskClick = (event: MouseEvent) => {
      const row = (event.target as HTMLElement).closest(".task-row");
      if (!row) return;
      const rows = Array.from(document.querySelectorAll(".task-row"));
      const index = rows.indexOf(row);
      if (index >= 0 && tasks[index]) setTaskOpen(tasks[index]);
    };
    document.addEventListener("click", onTaskClick);
    return () => document.removeEventListener("click", onTaskClick);
  }, [tasks]);
  useEffect(() => {
    if (!taskOpen) return;
    const latest = tasks.find((task) => task.id === taskOpen.id);
    if (!latest) {
      setTaskOpen(null);
      return;
    }
    if (
      latest.status !== taskOpen.status ||
      latest.result_summary !== taskOpen.result_summary ||
      latest.error_message !== taskOpen.error_message ||
      latest.messages?.length !== taskOpen.messages?.length ||
      latest.events?.length !== taskOpen.events?.length
    )
      setTaskOpen(latest);
  }, [tasks, taskOpen]);

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
  const openProject = (project: Project) => {
    setSelected(project);
    setDetail(null);
    setTasks([]);
    void loadDetail(project);
  };
  const closeProject = () => {
    setSelected(null);
    setDetail(null);
    setTasks([]);
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
  const startTask = async (type: string, prompt: string) => {
    if (type === "topic_select") {
      await openIdeaPlanner();
      return;
    }
    if (!selected) return;
    const response = await api(`/api/projects/${selected.id}/tasks`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ account_id: selected.account_id, type, prompt }),
    });
    if (!response.ok) setMessage("Codex 任务创建失败。");
    else await loadDetail(selected);
  };
  const answerTask = async (task: Task) => {
    const answer = window.prompt("请输入给 Codex 的回复");
    if (!answer?.trim()) return;
    const response = await api(`/api/tasks/${task.id}/answer`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ answer: answer.trim() }),
    });
    if (!response.ok) setMessage("任务回复失败。");
    else if (selected) await loadDetail(selected);
  };
  const cancelTask = async (task: Task) => {
    if (!window.confirm("确定取消这个 Codex 任务吗？")) return;
    const response = await api(`/api/tasks/${task.id}/cancel`, {
      method: "POST",
    });
    if (!response.ok) setMessage("任务取消失败。");
    else if (selected) await loadDetail(selected);
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
          <span className="eyebrow">LOCAL VIDEO OPERATIONS</span>
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

  const openIdeaPlanner = async () => {
    if (ideaSession) {
      setIdeaOpen(true);
      await refreshIdea(ideaSession.id);
      return;
    }
    const planningAccount = account || accounts[0]?.id || undefined;
    const sessionsResponse = await api("/api/ideas");
    if (sessionsResponse.ok) {
      const sessions = (await sessionsResponse.json()) as IdeaSession[];
      const existing = sessions.find(
        (session) =>
          session.status === "planning" &&
          (!planningAccount || session.account_id === planningAccount),
      );
      if (existing) {
        setIdeaSession(existing);
        setIdeaOpen(true);
        await refreshIdea(existing.id);
        return;
      }
    }
    const response = await api("/api/ideas", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        account_id: planningAccount,
        title: "新选题规划",
      }),
    });
    if (!response.ok) {
      setMessage("选题会话创建失败");
      return;
    }
    const created = (await response.json()) as IdeaSession;
    setIdeaSession({ ...created, messages: [], candidates: [] });
    setIdeaInput("");
    setIdeaOpen(true);
  };
  const refreshIdea = async (id: string) => {
    const response = await api(`/api/ideas/${id}`);
    if (!response.ok) return;
    const detail = (await response.json()) as IdeaSessionDetail;
    const messages = detail.messages || [];
    setIdeaSession({
      ...detail.session,
      messages,
      candidates: detail.candidates || [],
    });
    const taskID = [...messages]
      .reverse()
      .find((message) => message.task_id)?.task_id;
    if (!taskID) {
      setIdeaTask(null);
      return;
    }
    const taskResponse = await api(`/api/tasks/${taskID}`);
    if (taskResponse.ok) setIdeaTask((await taskResponse.json()) as Task);
  };
  const sendIdeaMessage = async (event: FormEvent) => {
    event.preventDefault();
    if (!ideaSession || !ideaInput.trim()) return;
    const content = ideaInput.trim();
    setIdeaInput("");
    const response = await api(`/api/ideas/${ideaSession.id}/messages`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        content,
        account_id: ideaSession.account_id || account || undefined,
      }),
    });
    if (!response.ok) {
      setMessage("选题消息发送失败");
      setIdeaInput(content);
      return;
    }
    await refreshIdea(ideaSession.id);
  };
  const selectIdeaCandidate = async (candidate: IdeaCandidate) => {
    if (!ideaSession) return;
    const response = await api(`/api/ideas/${ideaSession.id}/select`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        candidate_id: candidate.id,
        account_id: ideaSession.account_id || account || undefined,
      }),
    });
    if (!response.ok) {
      setMessage("候选题确认失败");
      return;
    }
    const result = (await response.json()) as { project?: Project };
    if (result.project) {
      setProjects((current) => [
        result.project!,
        ...current.filter((item) => item.id !== result.project!.id),
      ]);
      setIdeaOpen(false);
      openProject(result.project);
    } else await refreshIdea(ideaSession.id);
  };

  return (
    <div className="shell">
      <header>
        <div>
          <span className="eyebrow">LOCAL VIDEO OPERATIONS</span>
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
          <button className="header-button" onClick={() => void openSettings()}>
            设置
          </button>
          <button className="header-button" onClick={() => void logout()}>
            退出
          </button>
        </div>
      </header>
      <div className="layout">
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
                          {project.id.slice(0, 8)} ·{" "}
                          {project.missing_assets?.length
                            ? `缺少 ${project.missing_assets.length} 项素材`
                            : "按当前阶段无需补充素材"}
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
          )}
        </main>
      </div>
      {selected && (
        <div className="drawer-backdrop" onClick={closeProject}>
          <aside
            className="drawer"
            onClick={(event) => event.stopPropagation()}
          >
            <div className="drawer-head">
              <div>
                <span className="muted">
                  {accountName(selected.account_id, accounts)}
                </span>
                <h2>{selected.title}</h2>
              </div>
              <button
                className="close"
                onClick={closeProject}
                aria-label="关闭"
              >
                ×
              </button>
            </div>
            {detailLoading && !detail ? (
              <div className="empty">正在读取详情…</div>
            ) : (
              detail && (
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
                    {detail.missing_assets?.length ? (
                      <p className="warning">
                        待补充：
                        {detail.missing_assets
                          .map((item) => assetLabels[item] || item)
                          .join("、")}
                      </p>
                    ) : (
                      <p className="ok">当前阶段所需素材齐全</p>
                    )}
                  </section>
                  <section className="drawer-section">
                    <h3>启动工作流</h3>
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
                        深化一下
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
                    <h3>素材</h3>
                    <form className="asset-upload" onSubmit={uploadAsset}>
                      <select
                        value={assetType}
                        onChange={(event) => setAssetType(event.target.value)}
                      >
                        {Object.entries(assetLabels).map(([value, label]) => (
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
                                {formatSize(asset.size)} · v{asset.version} ·
                                查看
                              </small>
                            </div>
                          </button>
                        ),
                      )}
                      {!Object.keys(detail.assets || {}).length && (
                        <p className="muted">暂无项目素材</p>
                      )}
                    </div>
                  </section>
                  <section className="drawer-section">
                    <h3>
                      Codex 任务 <span className="count">{tasks.length}</span>
                    </h3>
                    <div className="task-list">
                      {tasks.map((task) => (
                        <article className="task-row" key={task.id}>
                          <div className="task-top">
                            <strong>{task.skill_name || task.type}</strong>
                            <span
                              className={`task-status status-${task.status}`}
                            >
                              {statusLabels[task.status] || task.status}
                            </span>
                          </div>
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
                              {task.messages.map((item) => (
                                <div className="task-message" key={item.id}>
                                  <b>{item.role === "user" ? "你" : "Codex"}</b>
                                  <p>{item.content}</p>
                                </div>
                              ))}
                            </details>
                          ) : null}
                          {task.events?.length ? (
                            <details>
                              <summary>
                                运行事件（{task.events.length}）
                              </summary>
                              {task.events.map((item) => (
                                <p className="event" key={item.id}>
                                  {item.display_text || item.kind}
                                </p>
                              ))}
                            </details>
                          ) : null}
                          {task.result_summary && <p>{task.result_summary}</p>}
                          {task.error_message && (
                            <p className="warning">{task.error_message}</p>
                          )}
                          {["awaiting_input", "waiting_input"].includes(
                            task.status,
                          ) && (
                            <div className="task-actions">
                              <button onClick={() => void answerTask(task)}>
                                回复
                              </button>
                              <button
                                className="secondary"
                                onClick={() => void cancelTask(task)}
                              >
                                取消任务
                              </button>
                            </div>
                          )}
                        </article>
                      ))}
                      {!tasks.length && (
                        <p className="muted">暂无 Codex 任务</p>
                      )}
                    </div>
                  </section>
                </>
              )
            )}
          </aside>
        </div>
      )}
      {preview && (
        <div className="modal-backdrop" onClick={() => setPreview(null)}>
          <section
            className="preview-modal"
            onClick={(event) => event.stopPropagation()}
          >
            <div className="drawer-head">
              <div>
                <span className="muted">
                  {assetLabels[preview.asset.type] || preview.asset.type}
                </span>
                <h2>{preview.asset.filename}</h2>
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
            onClick={(event) => event.stopPropagation()}
            onSubmit={saveSettings}
          >
            <div className="drawer-head">
              <div>
                <span className="muted">本地配置</span>
                <h2>控制台设置</h2>
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
          <section className="preview-modal idea-modal">
            <div className="drawer-head">
              <div>
                <span className="muted">
                  选题规划 ·{" "}
                  {ideaSession.account_id
                    ? accountName(ideaSession.account_id, accounts)
                    : "未指定账号"}
                </span>
                <h2>{ideaSession.title}</h2>
              </div>
              <button className="close" onClick={() => setIdeaOpen(false)}>
                ×
              </button>
            </div>
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
                    <button onClick={() => void selectIdeaCandidate(candidate)}>
                      确认建项目
                    </button>
                  </article>
                ))}
              </div>
            ) : null}
            <form className="idea-compose" onSubmit={sendIdeaMessage}>
              <input
                autoFocus
                value={ideaInput}
                onChange={(event) => setIdeaInput(event.target.value)}
                placeholder="输入你的想法或追问"
              />
              <button disabled={!ideaInput.trim()}>发送</button>
            </form>
          </section>
        </div>
      )}
      {taskOpen && (
        <div className="modal-backdrop" onClick={() => setTaskOpen(null)}>
          <section
            className="preview-modal task-modal"
            onClick={(event) => event.stopPropagation()}
          >
            <div className="drawer-head">
              <div>
                <span className="muted">Codex 任务详情</span>
                <h2>{taskOpen.skill_name || taskOpen.type}</h2>
              </div>
              <button className="close" onClick={() => setTaskOpen(null)}>
                ×
              </button>
            </div>
            <div className="task-modal-meta">
              <span className={`task-status status-${taskOpen.status}`}>
                {statusLabels[taskOpen.status] || taskOpen.status}
              </span>
              <span>{taskOpen.id}</span>
            </div>
            {taskOpen.prompt_snapshot && (
              <section>
                <h3>发送给 CLI 的任务说明</h3>
                <pre className="asset-text">{taskOpen.prompt_snapshot}</pre>
              </section>
            )}
            {taskOpen.messages?.length ? (
              <section>
                <h3>对话记录</h3>
                {taskOpen.messages.map((item) => (
                  <div className="task-message" key={item.id}>
                    <b>{item.role === "user" ? "你" : "Codex"}</b>
                    <p>{item.content}</p>
                  </div>
                ))}
              </section>
            ) : null}
            {taskOpen.events?.length ? (
              <section>
                <h3>运行事件</h3>
                {taskOpen.events.map((item) => (
                  <p className="event" key={item.id}>
                    {item.display_text || item.kind}
                  </p>
                ))}
              </section>
            ) : null}
            {taskOpen.result_summary && <p>{taskOpen.result_summary}</p>}
            {taskOpen.error_message && (
              <p className="warning">{taskOpen.error_message}</p>
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
        topic: "选题",
        script: "文案",
        assets: "素材",
        mixing: "混剪",
        review: "审核",
        ready: "待发布",
        published: "已发布",
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
