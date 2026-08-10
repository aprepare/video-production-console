export type TaskEvent = {
  id?: string;
  sequence?: number;
  kind?: string;
  level?: string;
  display_text?: string;
  raw_json?: string;
  created_at?: string;
  Kind?: string;
  Level?: string;
  DisplayText?: string;
  RawJSON?: string;
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
  ID?: string;
  Sequence?: number;
  Kind?: string;
  Phase?: string;
  Level?: string;
  Title?: string;
  Detail?: string;
  CreatedAt?: string;
};
