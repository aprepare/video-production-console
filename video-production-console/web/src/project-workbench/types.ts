export type ProductionStage = "script" | "assets" | "mixing" | "review" | "published";
export type ProjectStage = ProductionStage | "archived";

export type ProjectSummary = {
  id: string;
  account_id: string;
  title: string;
  stage: ProjectStage;
  publication_status?: "draft" | "producing" | "ready_to_publish" | "published" | "archived";
  created_at?: string;
  updated_at?: string;
  published_at?: string | null;
  publish_note?: string | null;
};

export type ProjectAsset = {
  id: string;
  type: string;
  filename: string;
  mime_type: string;
  size: number;
  sha256?: string;
  version: number;
  state: "missing" | "ready" | "stale" | "generating" | "failed";
  status?: string;
  created_at: string;
};

export type RegisteredMontageAsset = {
  id: string;
  filename?: string;
  display_name?: string;
  storage_name?: string;
  draft_id?: string;
  path: string;
  sha256: string;
  created_at: string;
};

export type MontageResult = {
  phase: string;
  workspace?: { id: string; filename: string; mime_type: string; size: number; created_at: string };
  registration_attempts?: Array<{
    id: string;
    attempt: number;
    state: string;
    registered_path?: string;
    receipt_path?: string;
    error_code?: string;
    error_message?: string;
    started_at: string;
    finished_at?: string;
  }>;
  registered_asset?: RegisteredMontageAsset | null;
  can_retry_registration: boolean;
};

export type PublishingPackage = {
  titles?: string[];
  top_titles?: Array<{ rank: number; title: string; reason: string }>;
  short_titles?: string[];
  descriptions?: string[];
  description?: string;
  topics?: string[];
  cta?: string;
};

export type ProjectTask = {
  id: string;
  project_id?: string;
  type: string;
  skill_name: string;
  status: string;
  action?: string;
  completion_phase?: string;
  result_summary?: string;
  error_message?: string;
  model?: string;
  reasoning_effort?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
  prompt_snapshot?: string;
  messages?: Array<{
    id: string;
    role: string;
    content: string;
    question_schema?: string;
    created_at: string;
  }>;
  semantic_events?: Array<{
    title?: string;
    detail?: string;
    phase?: string;
    created_at?: string;
  }>;
  montage?: MontageResult;
  publishing_package?: PublishingPackage;
};

export type ActiveWorkflow = {
  id: string;
  project_id: string;
  account_id: string;
  kind: "remix";
  state: "running" | "completed" | "failed" | "canceled";
  current_step: "topic_card" | "remix" | "completed";
  topic_task_id?: string;
  remix_task_id?: string;
  model: string;
  reasoning_effort: string;
  created_at: string;
  updated_at: string;
  current_task: ProjectTask | null;
};

export type ProjectDetail = {
  project: ProjectSummary;
  assets: Record<string, ProjectAsset>;
  asset_history?: Record<string, ProjectAsset[]>;
  background_reference?: ProjectAsset | null;
  topic_context?: unknown;
  missing_assets?: string[];
  active_workflow?: ActiveWorkflow | null;
};

export type PrimaryActionID =
  | "start-remix"
  | "start-source-remix"
  | "prepare-assets"
  | "start-mixing"
  | "publish";

export type PrimaryAction = {
  id: PrimaryActionID;
  label: string;
  disabled: boolean;
  activeStep?: ActiveWorkflow["current_step"];
};
