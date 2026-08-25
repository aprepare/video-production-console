import { useState } from "react";
import type { Dispatch, FormEvent, SetStateAction } from "react";
import { AccountSwitcher } from "../accounts/AccountSwitcher";
import { messageTone } from "../messageTone";
import { ProjectCreateForm } from "../projects/ProjectCreateForm";
import {
  PROJECT_COLLAPSE_LIMIT,
  accountName,
  formatDate,
  projectStageHint,
  boardStage,
  stageLabel,
  stages,
} from "../projects/stages";
import type { Account, Project, Theme } from "../types";

type RuntimeState = { Running: number; Limit: number; Queued: number };

type ConsoleHomeProps = {
  hidden: boolean;
  theme: Theme;
  onThemeChange: (theme: Theme) => void;
  onOpenImageProjects: () => void;
  onOpenRemixLab: () => void;
  modeTitle: string;
  runtime: RuntimeState | null | undefined;
  onOpenSettings: () => void;
  onLogout: () => void;
  accounts: Account[];
  selectedAccountID: string;
  onSelectAccount: (id: string) => void;
  accountFormOpen: boolean;
  onToggleAccountForm: () => void;
  onCreateAccount: (event: FormEvent) => void;
  newAccountName: string;
  onNewAccountNameChange: (value: string) => void;
  accountBackgroundSelected: boolean;
  onAccountBackgroundChange: (file: File | null) => void;
  newProject: string;
  onNewProjectChange: (value: string) => void;
  onCreateProject: (event: FormEvent) => void;
  message: string;
  onDismissMessage: () => void;
  loading: boolean;
  projects: Project[];
  expandedStages: Set<Project["stage"]>;
  onExpandedStagesChange: Dispatch<SetStateAction<Set<Project["stage"]>>>;
  onOpenProject: (project: Project) => void;
  onDeleteProjects: (ids: string[]) => void;
  deletingProjects?: boolean;
};

export function ConsoleHome({
  hidden,
  theme,
  onThemeChange,
  onOpenImageProjects,
  onOpenRemixLab,
  modeTitle,
  runtime,
  onOpenSettings,
  onLogout,
  accounts,
  selectedAccountID,
  onSelectAccount,
  accountFormOpen,
  onToggleAccountForm,
  onCreateAccount,
  newAccountName,
  onNewAccountNameChange,
  accountBackgroundSelected,
  onAccountBackgroundChange,
  newProject,
  onNewProjectChange,
  onCreateProject,
  message,
  onDismissMessage,
  loading,
  projects,
  expandedStages,
  onExpandedStagesChange,
  onOpenProject,
  onDeleteProjects,
  deletingProjects = false,
}: ConsoleHomeProps) {
  const tone = messageTone(message);
  const urgent = tone === "danger";
  const [selecting, setSelecting] = useState(false);
  const [selectedIDs, setSelectedIDs] = useState<string[]>([]);
  const visibleIDs = projects.map((project) => project.id);
  const selectedCount = selectedIDs.filter((id) => visibleIDs.includes(id)).length;
  const allSelected = visibleIDs.length > 0 && visibleIDs.every((id) => selectedIDs.includes(id));

  const toggleSelected = (id: string) => {
    setSelectedIDs((current) =>
      current.includes(id) ? current.filter((item) => item !== id) : [...current, id],
    );
  };
  const leaveSelectMode = () => {
    setSelecting(false);
    setSelectedIDs([]);
  };
  const confirmBatchDelete = () => {
    const ids = selectedIDs.filter((id) => visibleIDs.includes(id));
    if (ids.length === 0) return;
    const titles = projects.filter((project) => ids.includes(project.id)).map((project) => project.title);
    const preview = titles.slice(0, 3).join("、");
    const extra = titles.length > 3 ? ` 等 ${titles.length} 个` : titles.length > 1 ? ` 共 ${titles.length} 个` : "";
    if (
      !window.confirm(
        `确定删除“${preview}”${extra}吗？项目专属文案、配音、SRT、草稿和任务记录都会一并删除，此操作无法恢复。`,
      )
    )
      return;
    onDeleteProjects(ids);
    leaveSelectMode();
  };

  return (
    <>
      <header aria-hidden={hidden || undefined}>
        <div>
          <span className="eyebrow">视频生产控制台</span>
          <h1>{modeTitle}</h1>
        </div>
        <div className="status">
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
          <button type="button" className="header-button" onClick={onOpenImageProjects}>
            图文制作
          </button>
          {runtime && (
            <span
              className={
                runtime.Running >= runtime.Limit || runtime.Queued > 0
                  ? "runtime-warning"
                  : "runtime-state"
              }
            >
              任务 {runtime.Running}/{runtime.Limit}
              {runtime.Queued > 0 ? ` · 排队 ${runtime.Queued}` : ""}
            </span>
          )}
          <button type="button" className="header-button" onClick={onOpenRemixLab}>
            进化台
          </button>
          <button className="header-button" onClick={onOpenSettings}>
            设置
          </button>
          <button className="header-button" onClick={onLogout}>
            退出
          </button>
        </div>
      </header>
      <div className="layout" aria-hidden={hidden || undefined}>
        <AccountSwitcher
          accounts={accounts}
          selectedAccountID={selectedAccountID}
          onSelectAccount={onSelectAccount}
          formOpen={accountFormOpen}
          onToggleForm={onToggleAccountForm}
          onCreate={onCreateAccount}
          newAccountName={newAccountName}
          onNewAccountNameChange={onNewAccountNameChange}
          backgroundSelected={accountBackgroundSelected}
          onBackgroundChange={onAccountBackgroundChange}
        />
        <main>
          <div className="toolbar">
            <div>
              <div className="muted">
                {selectedAccountID
                  ? accounts.find((item) => item.id === selectedAccountID)?.name
                  : "全部账号"}
              </div>
              <h2>视频项目</h2>
            </div>
            <div className="toolbar-actions">
              <ProjectCreateForm
                accountSelected={Boolean(selectedAccountID)}
                title={newProject}
                onTitleChange={onNewProjectChange}
                onSubmit={onCreateProject}
              />
              {selecting ? (
                <div className="batch-bar" role="group" aria-label="批量删除项目">
                  <span className="batch-bar-count">已选 {selectedCount} 个</span>
                  <button
                    type="button"
                    className="batch-bar-button"
                    disabled={visibleIDs.length === 0}
                    onClick={() => setSelectedIDs(allSelected ? [] : visibleIDs)}
                  >
                    {allSelected ? "取消全选" : "全选"}
                  </button>
                  <button
                    type="button"
                    className="batch-bar-button batch-bar-button--danger"
                    disabled={selectedCount === 0 || deletingProjects}
                    onClick={confirmBatchDelete}
                  >
                    删除所选
                  </button>
                  <button type="button" className="batch-bar-button" onClick={leaveSelectMode}>
                    取消
                  </button>
                </div>
              ) : (
                <button
                  type="button"
                  className="batch-select"
                  disabled={projects.length === 0 || deletingProjects}
                  onClick={() => setSelecting(true)}
                >
                  批量删除
                </button>
              )}
            </div>
          </div>
          {message && (
            <div
              className={`notice notice--${tone}`}
              role={urgent ? "alert" : "status"}
              aria-live={urgent ? "assertive" : "polite"}
              aria-atomic="true"
            >
              {message}
              <button onClick={onDismissMessage}>关闭</button>
            </div>
          )}
          {loading ? (
            <div className="empty">正在读取项目…</div>
          ) : (
            <section className="project-board" aria-labelledby="project-board-title">
              <div className="board-help">
                <strong id="project-board-title">项目看板</strong>
              </div>
              <div className="board">
                {stages.map((stage) => {
                  const stageProjects = projects.filter((project) => boardStage(project.stage) === stage);
                  const expanded = expandedStages.has(stage);
                  const shownProjects = expanded
                    ? stageProjects
                    : stageProjects.slice(0, PROJECT_COLLAPSE_LIMIT);
                  return (
                    <section className={`column column--${stage}`} key={stage}>
                      <div className="column-head">
                        <h3>{stageLabel(stage)}</h3>
                        <b>{stageProjects.length}</b>
                      </div>
                      {shownProjects.map((project) => {
                        const isSelected = selectedIDs.includes(project.id);
                        return (
                          <button
                            type="button"
                            className={isSelected ? "project project--selected" : "project"}
                            key={project.id}
                            aria-pressed={selecting ? isSelected : undefined}
                            aria-label={selecting ? `选择项目 ${project.title}` : undefined}
                            onClick={() => (selecting ? toggleSelected(project.id) : onOpenProject(project))}
                          >
                            {selecting ? (
                              <span
                                className="project-check"
                                aria-hidden="true"
                                data-checked={isSelected || undefined}
                              />
                            ) : null}
                            <strong>{project.title}</strong>
                            <small>{projectStageHint(project.stage)}</small>
                            <div className="project-foot">
                              <span>{accountName(project.account_id, accounts)}</span>
                              <span>{formatDate(project.updated_at)}</span>
                            </div>
                          </button>
                        );
                      })}
                      {stageProjects.length > PROJECT_COLLAPSE_LIMIT ? (
                        <button
                          type="button"
                          className="column-toggle"
                          aria-expanded={expanded}
                          onClick={() =>
                            onExpandedStagesChange((current) => {
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
                    </section>
                  );
                })}
              </div>
            </section>
          )}
        </main>
      </div>
    </>
  );
}
