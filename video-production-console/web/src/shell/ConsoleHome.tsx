import type { Dispatch, FormEvent, SetStateAction } from "react";
import { AccountSwitcher } from "../accounts/AccountSwitcher";
import { messageTone } from "../messageTone";
import { ProjectCreateForm } from "../projects/ProjectCreateForm";
import {
  PROJECT_COLLAPSE_LIMIT,
  accountName,
  formatDate,
  projectStageHint,
  stageLabel,
  stages,
} from "../projects/stages";
import type { Account, Project, Theme } from "../types";

type RuntimeState = { Running: number; Limit: number; Queued: number };

type ConsoleHomeProps = {
  hidden: boolean;
  theme: Theme;
  onThemeChange: (theme: Theme) => void;
  runtime: RuntimeState | null | undefined;
  onOpenIdeaPlanner: () => void;
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
};

export function ConsoleHome({
  hidden,
  theme,
  onThemeChange,
  runtime,
  onOpenIdeaPlanner,
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
}: ConsoleHomeProps) {
  const tone = messageTone(message);
  const urgent = tone === "danger";

  return (
    <>
      <header aria-hidden={hidden || undefined}>
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
              onChange={(event) => onThemeChange(event.target.value as Theme)}
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
          <button className="header-button" onClick={onOpenIdeaPlanner}>
            给我选题
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
            <ProjectCreateForm
              accountSelected={Boolean(selectedAccountID)}
              title={newProject}
              onTitleChange={onNewProjectChange}
              onSubmit={onCreateProject}
            />
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
                <span>按生产阶段查看项目；点击项目卡片进入制作工作台。</span>
              </div>
              <div className="board">
                {stages.map((stage) => {
                  const stageProjects = projects.filter((project) => project.stage === stage);
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
                      {shownProjects.map((project) => (
                        <button
                          className="project"
                          key={project.id}
                          onClick={() => onOpenProject(project)}
                        >
                          <strong>{project.title}</strong>
                          <small>{projectStageHint(project.stage)}</small>
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
