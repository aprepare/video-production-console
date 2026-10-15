export type MonitorTask = {
  id: string
  name: string
  platform: 'douyin' | 'xhs'
  keyword: string
  interval_minutes: number
  pages: number
  enabled: boolean
  model: string
  score_enabled: boolean
  score_threshold: number
  score_focus: string
  auto_collect: boolean
  auto_transcribe: boolean
  auto_analyze: boolean
  auto_insight: boolean
  surge_mode: 'rule' | 'ai' | 'both'
  surge_like_delta: number
  surge_like_ratio: number
  surge_comment_delta: number
  surge_comment_ratio: number
  surge_min_base: number
  last_run_at: string
  next_run_at: string
  last_status: '' | 'ok' | 'failed'
  last_error: string
  running: boolean
  item_count: number
  unread_count: number
  surge_count: number
  created_at: string
  updated_at: string
};

export type MonitorItem = {
  id: string
  task_id: string
  task_name: string
  platform: 'douyin' | 'xhs'
  external_id: string
  url: string
  title: string
  caption: string
  author: string
  cover_url: string
  video_url: string
  duration_s: number
  posted_at: number
  likes: number
  comments: number
  shares: number
  collects: number
  first_seen_at: string
  last_seen_at: string
  seen_count: number
  score_status: 'none' | 'pending' | 'scored' | 'failed' | 'skipped'
  score: number
  score_reason: string
  score_tags: string[]
  score_error: string
  surge: boolean
  surge_reason: string
  surge_at: string
  like_delta: number
  comment_delta: number
  like_rate_per_hour: number
  reference_id: string
  read: boolean
  history: { at: string; likes: number; comments: number }[]
};

export type MonitorRun = {
  id: string
  task_id: string
  status: 'running' | 'completed' | 'failed'
  trigger: 'schedule' | 'manual'
  found: number
  new_count: number
  surge_count: number
  scored_count: number
  collected_count: number
  error: string
  started_at: string
  finished_at: string
};

export type CommentInsight = {
  id: string
  reference_id: string
  status: 'queued' | 'running' | 'completed' | 'failed'
  model: string
  comment_count: number
  error: string
  created_at: string
  updated_at: string
  report?: CommentInsightReport
};

export type CommentInsightReport = {
  summary: string
  sentiment: { positive: number; neutral: number; negative: number }
  pain_points: { point: string; detail: string; mentions: number; quotes: string[] }[]
  questions: { question: string; mentions: number; quotes: string[] }[]
  needs: { need: string; quotes: string[] }[]
  opportunities: { title: string; why: string; action: string; quotes: string[] }[]
  content_ideas: { title: string; hook: string; angle: string }[]
  objections: string[]
  keywords: { word: string; count: number }[]
};
