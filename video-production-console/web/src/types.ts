// Console-wide view models. Workbench-owned shapes stay in
// project-workbench/types.ts and model overrides stay in taskModel.ts; this file
// only holds the types the shell and its panels share.
import type { ReasoningEffort } from "./taskModel";
import type { TaskEvent } from "./tasks/event-types";
import type {
  ProjectAsset,
  ProjectDetail as WorkbenchProjectDetail,
  ProjectStage,
  ProjectSummary,
  ProjectTask as WorkbenchProjectTask,
  PublishingPackage,
} from "./project-workbench/types";

export type Theme = "light" | "dark";

export type Account = { id: string; name: string; status?: string };

export type Project = Omit<ProjectSummary, "stage"> & {
  stage: ProjectStage | "topic" | "ready";
  missing_assets?: string[];
};

export type Asset = ProjectAsset;

export type TaskPhaseRun = {
  id?: string;
  task_id?: string;
  phase_key?: string;
  display_name?: string;
  attempt?: number;
  source?: string;
  state?: string;
  started_at?: string;
  running_at?: string;
  finished_at?: string;
  duration_ms?: number;
};

export type TaskTimingSummary = {
  task_id?: string;
  total_ms?: number;
  preparation_ms?: number;
  queue_ms?: number;
  execution_ms?: number;
  queue_estimated?: boolean;
  legacy_without_phases?: boolean;
  phases?: TaskPhaseRun[];
};

export type Task = WorkbenchProjectTask & {
  model?: string;
  reasoning_effort?: ReasoningEffort;
  events?: TaskEvent[];
  timing_summary?: TaskTimingSummary;
  timing_runs?: TaskPhaseRun[];
  publishing_package?: PublishingPackage;
};

export type ProjectDetail = Omit<WorkbenchProjectDetail, "project" | "topic_context"> & {
  project: Project;
  topic_context?: IdeaCandidate | null;
};

export type PublicSettings = {
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

export type Settings = {
  public: PublicSettings;
  configured_public?: PublicSettings;
  active_public?: PublicSettings;
  restart_required?: boolean;
  settings_version: number;
  secrets: Record<string, { configured: boolean; masked: string }>;
};

export type IdeaMessage = {
  id: string;
  role: string;
  content: string;
  task_id?: string;
  createdAt?: string;
  created_at?: string;
};

export type IdeaCandidate = {
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

export type IdeaSession = {
  id: string;
  accountID?: string;
  account_id?: string;
  title: string;
  status: string;
  messages?: IdeaMessage[];
  candidates?: IdeaCandidate[];
};

export type IdeaSessionDetail = {
  session: IdeaSession;
  messages?: IdeaMessage[];
  candidates?: IdeaCandidate[];
};

export type ChatMessage = {
  id: string;
  role: string;
  kind: string;
  content: string;
  delivery_status: string;
  turn_id?: string;
  created_at: string;
};

export type ChatSession = {
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

export type ChatDetail = { session: ChatSession; messages: ChatMessage[] };

export type HistoryThread = {
  id: string;
  title: string;
  preview: string;
  source: "desktop" | "cli" | "task";
  model?: string;
  reasoning_effort?: string;
  active: boolean;
  recency: string;
};

export type DirectoryManifest = {
  asset_id: string;
  registered_path: string;
  entries: Array<{ path: string; kind: string; size: number }>;
};

export type PublicStringSettingKey = {
  [Key in keyof PublicSettings]: PublicSettings[Key] extends string ? Key : never;
}[keyof PublicSettings];
