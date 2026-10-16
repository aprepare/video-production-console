export type VideoQCIssue = {
  frame_index: number;
  time_s: number;
  category: string;
  severity: 'high' | 'medium' | 'low';
  description: string;
  suggestion: string;
};

export type VideoQCReport = {
  summary: string;
  issues: VideoQCIssue[];
  checked: string[];
};

export type VideoQCFrame = {
  index: number;
  time_s: number;
  url: string;
};

export type VideoQCCheck = {
  id: string;
  source: 'upload' | 'reference' | 'image_video_job';
  source_id: string;
  title: string;
  product: string;
  model: string;
  status: 'queued' | 'running' | 'completed' | 'failed';
  duration_s: number;
  width: number;
  height: number;
  frame_count: number;
  verdict: '' | 'pass' | 'warn' | 'fail';
  score: number;
  error: string;
  created_at: string;
  updated_at: string;
  report?: VideoQCReport | null;
  frames?: VideoQCFrame[];
};
