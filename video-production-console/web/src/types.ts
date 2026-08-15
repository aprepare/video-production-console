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

export type NarrationGeneration = {
  narration: Asset;
  subtitle_srt: Asset;
  captions?: number;
  duration_seconds?: number;
  billed_characters?: number;
  warnings?: string[];
};

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
  remix_base_url: string;
  remix_model: string;
  remix_reasoning_effort: ReasoningEffort | "";
  codex_task_project_root: string;
  image_base_url: string;
  image_model: string;
  image_text_base_url: string;
  image_text_model: string;
  image_text_reasoning_effort: ReasoningEffort | "";
  max_image_concurrency: number;
  image_generation_attempts: number;
  image_stream: boolean;
  default_image_ratio: "3:4" | "4:3" | "9:16" | "1:1";
  default_image_style: string;
  codex_binary_path: string;
  media_index_path: string;
  media_root: string;
  jianying_root: string;
  machine_profile_path: string;
  app_server_enabled: boolean;
  codex_workspace_roots: string[];
  codex_default_model: string;
  codex_default_reasoning_effort: ReasoningEffort;
  volc_speech_speaker_id: string;
  volc_speech_resource_id: string;
  media_catalog_path: string;
  ffmpeg_path: string;
  ffprobe_path: string;
  vision_base_url: string;
  vision_model: string;
  embedding_base_url: string;
  embedding_model: string;
  pexels_api_base_url: string;
  pixabay_api_base_url: string;
  max_external_results_per_query: number;
};

export type Settings = {
  public: PublicSettings;
  configured_public?: PublicSettings;
  active_public?: PublicSettings;
  restart_required?: boolean;
  settings_version: number;
  secrets: Record<string, { configured: boolean; masked: string }>;
};

export type ImageRunPhase = "idle" | "planning" | "prompting" | "imaging" | "completed";
export type ImageRunStatus = "idle" | "running" | "failed" | "completed" | "interrupted";

export type PublishingCandidate = { position: number; title: string; description: string };

export type ImageProject = {
  id: string;
  title: string;
  script: string;
  image_count: number;
  ratio: "3:4" | "4:3" | "9:16" | "1:1";
  style: string;
  custom_style: string;
  concurrency: number;
  status: "draft" | "generating" | "ready" | "partial" | "failed";
  run_mode?: "manual" | "quick";
  run_phase?: ImageRunPhase;
  run_status?: ImageRunStatus;
  phase_error?: string;
  publishing_error?: string;
  success_count?: number;
  failure_count?: number;
  image_attempts?: number;
  text_model?: string;
  reasoning_effort?: ReasoningEffort | "";
  image_model?: string;
  publishing_candidates?: PublishingCandidate[];
  selected_position?: number;
  created_at: string;
  updated_at: string;
};

export type QuickImageProjectRequest = {
  script: string;
  image_count: number;
  ratio: ImageProject["ratio"];
  style: string;
  custom_style: string;
  concurrency: number;
  text_model: string;
  reasoning_effort: ReasoningEffort | "";
  image_model: string;
  image_attempts: number;
};

export type QuickImageProjectResponse = {
  project_id: string;
  run_status: ImageRunStatus;
};

export type ImageProjectItem = {
  id: string;
  project_id: string;
  sequence: number;
  role?: "cover" | "content";
  source_text: string;
  title: string;
  prompt: string;
  status: "pending" | "generating" | "ready" | "failed";
  mime_type?: string;
  width?: number;
  height?: number;
  error_message?: string;
  attempt_count?: number;
  updated_at?: string;
};

export type ImageProjectDetail = { project: ImageProject; items: ImageProjectItem[]; publishing_candidates?: PublishingCandidate[]; selected_position?: number; publishing?: { current?: PublishingCandidate; all?: PublishingCandidate[] } };

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


export type DirectoryManifest = {
  asset_id: string;
  registered_path: string;
  entries: Array<{ path: string; kind: string; size: number }>;
};

export type PublicStringSettingKey = {
  [Key in keyof PublicSettings]: PublicSettings[Key] extends string ? Key : never;
}[keyof PublicSettings];
