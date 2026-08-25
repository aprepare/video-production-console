export type RemixLabApi = (path: string, init?: RequestInit) => Promise<Response>;

export type RemixLabPreset = {
  base_url: string;
  model: string;
  reasoning_effort: string;
  run_count: number;
  api_key_configured: boolean;
  preset_index: number;
};

export type RemixLabDefaults = {
  remix_base_url: string;
  remix_model: string;
  remix_reasoning_effort: string;
  remix_api_key_configured: boolean;
  presets: RemixLabPreset[];
};

export type RemixLabExperimentSummary = {
  id: string;
  title: string;
  prompt_stamp: string;
  status: string;
  created_at: string;
  updated_at: string;
};

export type RemixLabSlotView = {
  id: string;
  experiment_id: string;
  sort_index: number;
  label: string;
  base_url: string;
  model: string;
  reasoning_effort: string;
  run_count: number;
  api_key_configured: boolean;
};

export type RemixLabRunView = {
  id: string;
  experiment_id: string;
  slot_id: string;
  run_index: number;
  status: string;
  continuous_script: string;
  titles_json: string;
  error_message: string;
  comment: string;
  adopted_project_id: string;
};

export type RemixLabExperiment = {
  id: string;
  title: string;
  source_text: string;
  prompt_stamp: string;
  status: string;
  created_at: string;
  updated_at: string;
  slots: RemixLabSlotView[];
  runs: RemixLabRunView[];
};

export type RemixLabCreateSlot = {
  base_url: string;
  model: string;
  api_key: string;
  reasoning_effort: string;
  run_count: number;
  preset_index?: number | null;
};

export async function fetchRemixLabDefaults(api: RemixLabApi): Promise<RemixLabDefaults> {
  const response = await api("/api/remix-lab/defaults");
  if (!response.ok) throw new Error("进化台默认配置读取失败。");
  return (await response.json()) as RemixLabDefaults;
}

export async function fetchRemixLabExperiments(
  api: RemixLabApi,
): Promise<RemixLabExperimentSummary[]> {
  const response = await api("/api/remix-lab/experiments");
  if (!response.ok) throw new Error("进化台实验列表读取失败。");
  return (await response.json()) as RemixLabExperimentSummary[];
}

export async function createRemixLabExperiment(
  api: RemixLabApi,
  source: string,
  slots: RemixLabCreateSlot[],
): Promise<RemixLabExperiment> {
  const response = await api("/api/remix-lab/experiments", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ source, slots }),
  });
  if (!response.ok) throw new Error("进化台实验创建失败。");
  return (await response.json()) as RemixLabExperiment;
}

export async function fetchRemixLabExperiment(
  api: RemixLabApi,
  id: string,
): Promise<RemixLabExperiment> {
  const response = await api(`/api/remix-lab/experiments/${id}`);
  if (!response.ok) throw new Error("进化台实验详情读取失败。");
  return (await response.json()) as RemixLabExperiment;
}

export async function patchRemixLabRunComment(
  api: RemixLabApi,
  runID: string,
  comment: string,
): Promise<void> {
  const response = await api(`/api/remix-lab/runs/${runID}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ comment }),
  });
  if (!response.ok) throw new Error("批注保存失败。");
}

export async function adoptRemixLabRun(
  api: RemixLabApi,
  runID: string,
  projectID: string,
): Promise<void> {
  const response = await api(`/api/remix-lab/runs/${runID}/adopt`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ project_id: projectID }),
  });
  if (!response.ok) throw new Error("采用到项目失败。");
}
