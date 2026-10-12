import { useMemo, useState } from "react";
import { ArrowUpRight, FolderOpen, RefreshCw, Search } from "lucide-react";
import type { ImageProject } from "../types";
import "./image-project-library.css";

type Props = {
  projects: ImageProject[];
  state: string;
  busy: boolean;
  onOpen: (project: ImageProject) => void;
  onRetry: () => void;
};

function projectState(project: ImageProject) {
  const states = [project.status, project.run_status];
  if (states.some(value => value === "failed" || value === "interrupted" || value === "partial")) return "attention";
  if (states.some(value => ["generating", "running", "queued", "planning", "prompting", "imaging", "pending"].includes(value || ""))) return "working";
  if ((project.failure_count ?? 0) > 0) return "attention";
  if (states.some(value => value === "ready" || value === "completed")) return "ready";
  return "draft";
}

export function ImageProjectLibrary({ projects, state, busy, onOpen, onRetry }: Props) {
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState("all");
  const filtered = useMemo(() => projects.filter(project =>
    project.title.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()) &&
    (filter === "all" || projectState(project) === filter)), [projects, query, filter]);
  const hasFilter = Boolean(query || filter !== "all");
  const clearFilters = () => { setQuery(""); setFilter("all"); };
  return <section className="image-project-list" aria-labelledby="image-project-list-title">
    <header className="image-project-list__head">
      <div><span className="image-kicker">项目库</span><h2 id="image-project-list-title">已有图文项目 <span>{projects.length}</span></h2></div>
      <button type="button" className="image-project-list__refresh" aria-label="刷新项目列表" title="刷新项目列表" disabled={state === "loading"} onClick={onRetry}><RefreshCw size={16} aria-hidden="true" /></button>
    </header>
    <div className="image-project-list__filters">
      <label className="studio-search"><Search size={16} aria-hidden="true" /><input type="search" aria-label="搜索图文项目" placeholder="搜索项目标题" value={query} onChange={event => setQuery(event.target.value)} /></label>
      <select aria-label="筛选图文项目状态" value={filter} onChange={event => setFilter(event.target.value)}>
        <option value="all">全部状态</option><option value="ready">图片就绪</option><option value="working">正在制作</option><option value="attention">需要处理</option><option value="draft">待生成</option>
      </select>
      {hasFilter ? <button type="button" className="image-project-list__clear" onClick={clearFilters}>清除筛选</button> : null}
    </div>
    {state === "loading" ? <p role="status" className="image-library-feedback">正在读取项目列表…</p> : state === "error" ? <div role="alert" className="image-library-feedback"><p>图文项目列表读取失败，已填写的文案会保留。</p><button type="button" onClick={onRetry}>重试读取项目列表</button></div> : null}
    <div className="image-project-list__items">
      {filtered.map(project => {
        const status = projectState(project);
        const label = { ready: "图片就绪", working: "生成中", attention: "需要处理", draft: "待生成" }[status];
        return <button type="button" className="image-library-item" key={project.id} disabled={busy} onClick={() => onOpen(project)}>
          <span className="image-library-item__title"><strong>{project.title}</strong><ArrowUpRight size={15} aria-hidden="true" /></span>
          <span className="image-library-item__meta"><span>{project.image_count} 张 · {project.ratio}</span><span className="studio-status" data-status={status}>{label}</span></span>
        </button>;
      })}
      {filtered.length === 0 && state === "ready" ? <div className="image-library-empty"><FolderOpen size={28} strokeWidth={1.5} aria-hidden="true" /><strong>{projects.length ? "没有匹配的项目" : "还没有图文项目"}</strong><p>{projects.length ? "调整标题或状态筛选，查看其他项目。" : "粘贴定稿后开始制作，项目会保存在这里。"}</p></div> : null}
    </div>
  </section>;
}
