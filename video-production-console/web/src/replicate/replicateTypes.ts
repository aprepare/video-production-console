export type ReplicateTake = {
  id: string;
  provider: string;
  status: 'queued' | 'submitting' | 'running' | 'succeeded' | 'failed' | 'canceled';
  error: string;
  duration_s: number;
  width: number;
  height: number;
  url: string;
  cost_note: string;
  created_at: string;
};

export type ReplicateShot = {
  id: string;
  position: number;
  purpose: string;
  source_start_s: number;
  source_end_s: number;
  duration_s: number;
  first_frame_prompt: string;
  motion_prompt: string;
  negative: string;
  keep: string;
  first_frame_url: string;
  first_frame_source: '' | 'generated' | 'uploaded' | 'reference_frame';
  first_frame_status: 'none' | 'generating' | 'ready' | 'failed';
  first_frame_error: string;
  selected_take_id: string;
  takes: ReplicateTake[];
};

export type ReplicateProject = {
  id: string;
  title: string;
  reference_id: string;
  analysis_id: string;
  product: string;
  style_lock: string;
  ratio: '9:16' | '16:9' | '1:1' | '3:4' | '4:3';
  video_provider: 'runninghub_h3' | 'grok';
  image_model: string;
  variants: number;
  status: 'draft' | 'generating' | 'ready' | 'stitching' | 'stitched' | 'failed';
  final_url: string;
  final_duration_s: number;
  qc_id: string;
  error: string;
  created_at: string;
  updated_at: string;
  shots?: ReplicateShot[];
  progress: {
    takes_total: number;
    takes_done: number;
    takes_failed: number;
    takes_running: number;
  };
};

export type ReplicateProvider = {
  id: 'runninghub_h3' | 'grok';
  label: string;
  note: string;
  available: boolean;
  keys?: number;
  durations: number[];
  default_duration: number;
};

export type ReplicateProvidersResponse = {
  providers: ReplicateProvider[];
  default_provider: 'runninghub_h3' | 'grok';
};

export type ReplicateGenerateResponse = {
  project: ReplicateProject;
  skipped: Array<{
    shot_id: string;
    reason: string;
  }>;
};
