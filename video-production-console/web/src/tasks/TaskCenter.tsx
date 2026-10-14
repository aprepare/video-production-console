import { useCallback, useEffect, useMemo, useState } from "react";
import { Activity, Trash2, X } from "lucide-react";
import type { Task } from "../types";
import { liveTaskStatuses, statusLabels, taskTitle } from "./task-view";
import "./task-center.css";

type Api = (path: string, init?: RequestInit) => Promise<Response>;
type ProjectRef = { id: string; title: string };

const failedStatuses = new Set(["failed", "interrupted", "interrupted_on_restart", "canceled", "cancelled"]);
const RECENT_WINDOW_MS = 48 * 60 * 60 * 1000;

function relative(value?: string) {
  if (!value) return "";
  const time = Date.parse(value);
  if (Number.isNaN(time)) return "";
  const minutes = Math.round((Date.now() - time) / 60000);
  if (minutes < 1) return "刚刚";
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  return `${Math.round(hours / 24)} 天前`;
}

/** Live tasks first, then failures from the last 48 hours, then recent completions. */
export function groupTasks(tasks: Task[], now = Date.now()) {
  const recent = (task: Task) => now - Date.parse(task.finished_at || task.created_at) < RECENT_WINDOW_MS;
  const live = tasks.filter((task) => liveTaskStatuses.has(task.status));
  const failed = tasks.filter((task) => failedStatuses.has(task.status) && recent(task));
  const done = tasks.filter((task) => task.status === "completed" && recent(task)).slice(0, 12);
  return { live, failed, done };
}

type Props = {
  api: Api;
  projects: ProjectRef[];
  onOpenProject: (projectID: string) => void;
};

/** Navigation-level entry plus drawer listing every task across projects. */
export function TaskCenter({ api, projects, onOpenProject }: Props) {
  const [open, setOpen] = useState(false);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [error, setError] = useState(false);
  const [dismissError, setDismissError] = useState("");

  const load = useCallback(async () => {
    try {
      const response = await api("/api/tasks?center=1");
      if (!response.ok) throw new Error();
      setTasks(await response.json());
      setError(false);
    } catch {
      setError(true);
    }
  }, [api]);

  useEffect(() => {
    void load();
    const timer = window.setInterval(() => void load(), open ? 4000 : 15000);
    return () => window.clearInterval(timer);
  }, [load, open]);

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") setOpen(false); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  // The shell only loads projects on montage routes; fill titles on demand
  // so the drawer names projects from any page.
  const [fetchedProjects, setFetchedProjects] = useState<ProjectRef[]>([]);
  useEffect(() => {
    if (!open || projects.length) return;
    void (async () => {
      try {
        const response = await api("/api/projects");
        if (response.ok) setFetchedProjects(await response.json());
      } catch { /* titles are optional */ }
    })();
  }, [open, projects.length, api]);
  const dismiss = async (ids: string[]) => {
    if (!ids.length) return;
    setDismissError("");
    try {
      const response = await api("/api/tasks/dismiss", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ ids }) });
      if (!response.ok) {
        const body = await response.json().catch(() => null);
        throw new Error(typeof body?.message === "string" ? body.message : "移除失败，稍后再试。");
      }
      const removed = new Set(ids);
      setTasks((current) => current.filter((task) => !removed.has(task.id)));
    } catch (problem) {
      setDismissError(problem instanceof Error ? problem.message : "移除失败，稍后再试。");
    }
  };
  const groups = useMemo(() => groupTasks(tasks), [tasks]);
  const titles = useMemo(() => new Map((projects.length ? projects : fetchedProjects).map((project) => [project.id, project.title])), [projects, fetchedProjects]);
  const badge = groups.live.length || groups.failed.length;

  const row = (task: Task) => {
    const project = task.project_id ? titles.get(task.project_id) : undefined;
    const failed = failedStatuses.has(task.status);
    const finished = !liveTaskStatuses.has(task.status);
    return <li key={task.id} className="task-center__row">
      {finished ? <button type="button" className="task-center__dismiss" aria-label={`从任务中心移除：${taskTitle(task)}`} title="从任务中心移除（记录仍在项目里）" onClick={() => void dismiss([task.id])}><X size={14} aria-hidden="true" /></button> : null}
      <button type="button" className="task-center__item" disabled={!task.project_id} onClick={() => { if (task.project_id) { setOpen(false); onOpenProject(task.project_id); } }}>
        <span className="task-center__line">
          <strong>{taskTitle(task)}</strong>
          <span className="studio-status" data-status={liveTaskStatuses.has(task.status) ? "working" : failed ? "attention" : "ready"}>{statusLabels[task.status] || task.status}</span>
        </span>
        <span className="task-center__meta">{project || (task.project_id ? "项目已删除" : "无项目")} · {relative(task.finished_at || task.started_at || task.created_at)}</span>
        {failed && (task.error_message || task.result_summary) ? <span className="task-center__error">{task.error_message || task.result_summary}</span> : null}
      </button>
    </li>;
  };

  return <>
    <button type="button" className="console-nav__utility task-center__trigger" aria-label={badge ? `任务中心，${groups.live.length} 个进行中，${groups.failed.length} 个失败` : "任务中心"} title="任务中心" onClick={() => setOpen(true)}>
      <Activity size={18} aria-hidden="true" /><span>任务中心</span>
      {badge ? <span className="task-center__badge" data-tone={groups.failed.length ? "attention" : "working"}>{groups.live.length || groups.failed.length}</span> : null}
    </button>
    {open ? <div className="task-center__backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) setOpen(false); }}>
      <aside className="task-center" role="dialog" aria-modal="true" aria-label="任务中心">
        <header>
          <div><h2>任务中心</h2><p>所有项目的二创、口播、配音、混剪任务。移除只是不在这里显示，记录仍在项目里。</p></div>
          <button type="button" className="studio-button studio-button--quiet" aria-label="关闭任务中心" onClick={() => setOpen(false)}><X size={16} aria-hidden="true" /></button>
        </header>
        {error ? <p className="studio-notice" data-tone="error" role="alert">任务列表读取失败，稍后自动重试。</p> : null}
        {dismissError ? <p className="studio-notice" data-tone="error" role="alert">{dismissError}</p> : null}
        <section aria-label="进行中">
          <h3>进行中 <span>{groups.live.length}</span></h3>
          {groups.live.length ? <ul className="studio-list">{groups.live.map(row)}</ul> : <p className="task-center__empty">没有正在运行的任务。</p>}
        </section>
        <section aria-label="最近失败">
          <div className="task-center__heading">
            <h3>最近失败 <span>{groups.failed.length}</span></h3>
            {groups.failed.length ? <button type="button" className="task-center__clear" aria-label="清除全部失败任务" onClick={() => void dismiss(groups.failed.map((task) => task.id))}><Trash2 size={13} aria-hidden="true" />全部清除</button> : null}
          </div>
          {groups.failed.length ? <ul className="studio-list">{groups.failed.map(row)}</ul> : <p className="task-center__empty">48 小时内没有失败。</p>}
        </section>
        {groups.done.length ? <section aria-label="最近完成">
          <div className="task-center__heading">
            <h3>最近完成</h3>
            <button type="button" className="task-center__clear" aria-label="清除全部已完成任务" onClick={() => void dismiss(groups.done.map((task) => task.id))}><Trash2 size={13} aria-hidden="true" />全部清除</button>
          </div>
          <ul className="studio-list">{groups.done.map(row)}</ul>
        </section> : null}
      </aside>
    </div> : null}
  </>;
}
