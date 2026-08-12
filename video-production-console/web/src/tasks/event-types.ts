export type TaskEvent = {
  id?: string;
  sequence?: number;
  kind?: string;
  level?: string;
  display_text?: string;
  raw_json?: string;
  created_at?: string;
};

export type SemanticEvent = {
  id?: string;
  sequence?: number;
  kind?: string;
  phase?: string;
  level?: string;
  title?: string;
  detail?: string;
  created_at?: string;
};
