export type RemixLabApi = (path: string, init?: RequestInit) => Promise<Response>;

export type RemixLabPreset = {
  base_url: string;
  model: string;
  reasoning_effort: string;
  pipeline?: string;
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
  workflow?: boolean;
  created_at: string;
  updated_at: string;
};

/** 工作流定义：节点带各自的模型/通道/提示词配置，边决定数据流向。 */
export type RemixLabWorkflowNodeConfig = {
  model?: string;
  reasoning_effort?: string;
  channel?: string;
  system_prompt?: string;
  user_template?: string;
  inject_title?: string;
  inject_rule?: string;
  prompt_id?: string;
  /** 机械自检节点的阈值覆盖（0/缺省=默认：连抄20%/硬30%、篇幅0.8/硬0.65、返工2轮）。 */
  overlap_max_pct?: number;
  overlap_hard_pct?: number;
  len_min_ratio?: number;
  len_hard_ratio?: number;
  max_rounds?: number;
};

export type RemixLabWorkflowNode = {
  id: string;
  type: "input" | "agent" | "writer" | "selfcheck" | "reviewer" | "output" | string;
  title: string;
  x: number;
  y: number;
  config: RemixLabWorkflowNodeConfig;
};

/** 生产段配置：字幕关键词默认关闭，口播/配音/混剪参数随实验快照冻结。 */
export type RemixLabWorkflowProduction = {
  captions_disabled?: boolean;
  spoken_prompt?: string;
  captions_prompt?: string;
  montage_prompt?: string;
  spoken_model?: string;
  spoken_effort?: string;
  montage_model?: string;
  montage_effort?: string;
  narration_voice_id?: string;
  narration_model?: string;
  narration_emotion?: string;
  narration_speed?: number;
  narration_volume?: number;
  narration_pitch?: number;
};

export type RemixLabWorkflow = {
  version: number;
  name: string;
  nodes: RemixLabWorkflowNode[];
  edges: Array<[string, string]>;
  production?: RemixLabWorkflowProduction;
};

function workflowPath(accountID?: string): string {
  const id = (accountID ?? "").trim();
  return id
    ? `/api/remix-lab/workflow?account_id=${encodeURIComponent(id)}`
    : "/api/remix-lab/workflow";
}

export async function fetchRemixLabWorkflow(
  api: RemixLabApi,
  accountID?: string,
): Promise<RemixLabWorkflow> {
  const response = await api(workflowPath(accountID));
  if (!response.ok) throw new Error("工作流读取失败。");
  return (await response.json()) as RemixLabWorkflow;
}

export async function saveRemixLabWorkflow(
  api: RemixLabApi,
  workflow: RemixLabWorkflow,
  accountID?: string,
): Promise<RemixLabWorkflow> {
  const response = await api(workflowPath(accountID), {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(workflow),
  });
  if (!response.ok) throw new Error(await readAPIError(response, "工作流保存失败。"));
  return (await response.json()) as RemixLabWorkflow;
}

// 用当前工作流开跑一个实验（run_count 1-3；可带生产账号与全自动开关）。
export async function runRemixLabWorkflow(
  api: RemixLabApi,
  source: string,
  runCount: number,
  accountID: string,
  auto: boolean,
): Promise<RemixLabExperiment> {
  const response = await api("/api/remix-lab/workflow/run", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ source, run_count: runCount, account_id: accountID, auto }),
  });
  if (!response.ok) throw new Error(await readAPIError(response, "工作流开跑失败。"));
  return (await response.json()) as RemixLabExperiment;
}

// 确认闸门放行 / 生产失败续跑：定稿进混剪链路。
export type RemixLabProjectProductionLink = {
  experiment_id: string;
  run_id: string;
  project_id: string;
  status?: string;
};

export async function fetchRemixLabProductionByProject(
  api: RemixLabApi,
  projectID: string,
): Promise<RemixLabProjectProductionLink> {
  const response = await api(`/api/remix-lab/productions/by-project/${projectID}`);
  if (!response.ok) throw new Error(await readAPIError(response, "这个项目没有关联工作流。"));
  return (await response.json()) as RemixLabProjectProductionLink;
}

export async function produceRemixLabRun(
  api: RemixLabApi,
  runID: string,
  accountID?: string,
): Promise<void> {
  const response = await api(`/api/remix-lab/runs/${runID}/produce`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ account_id: accountID ?? "" }),
  });
  if (!response.ok) throw new Error(await readAPIError(response, "开始混剪失败。"));
}

export type RemixLabSlotView = {
  id: string;
  experiment_id: string;
  sort_index: number;
  label: string;
  base_url: string;
  model: string;
  reasoning_effort: string;
  pipeline?: string;
  run_count: number;
  api_key_configured: boolean;
};

/** 生产段状态：确认闸门之后的混剪链路（建项目→口播→配音→混剪）。 */
export type RemixLabProduction = {
  status: "waiting_confirm" | "running" | "completed" | "failed" | string;
  step: string;
  account_id: string;
  auto: boolean;
  project_id?: string;
  spoken_task_id?: string;
  caption_task_id?: string;
  montage_task_id?: string;
  error?: string;
};

export type RemixLabRunView = {
  id: string;
  experiment_id: string;
  slot_id: string;
  run_index: number;
  status: string;
  prompt_id?: string;
  prompt_stamp?: string;
  prompt_name?: string;
  continuous_script: string;
  titles_json: string;
  /** 当前定稿的完整写手 JSON（正文+发布字段），可编辑保存。 */
  package_json: string;
  /** 写手初稿留档（审稿前），用于双版本对照。 */
  draft_v1_json: string;
  /** 最近一轮审稿结论。 */
  review_json: string;
  /** 生产段状态；无则未触发。 */
  production?: RemixLabProduction | null;
  error_message: string;
  comment: string;
  adopted_project_id: string;
};

export type RemixLabReviewIssue = {
  where: string;
  problem: string;
  fix: string;
};

export type RemixLabReviewRecord = {
  verdict: "pass" | "fixed" | "skipped" | "error" | string;
  round: number;
  summary: string;
  issues?: RemixLabReviewIssue[];
  annotations?: string;
  error?: string;
  at: string;
};

/** 发布包编辑字段（写手 JSON 的 draft 形状）。 */
export type RemixLabPackageInput = {
  continuous_script: string;
  titles: string[];
  short_titles: string[];
  descriptions: string[];
  topics: string[];
  cta: string;
};

export type RemixLabPrompt = {
  id: string;
  name: string;
  description: string;
  style: string;
  stamp: string;
  system: string;
  user: string;
  builtin: boolean;
};

export type RemixLabActivePrompt = {
  active: boolean;
  prompt: {
    id: string;
    name: string;
    stamp: string;
    style?: string;
    system?: string;
    user?: string;
  };
};

export type RemixLabAgentSettings = {
  model: string;
  base_url: string;
  reasoning_effort: string;
  api_key_configured: boolean;
};

/** 四路agent（钩子/事实联网/事实离线/弹药/审稿）的系统提示词文本。 */
export type RemixLabAgentPrompts = {
  hook_system: string;
  facts_search_system: string;
  facts_offline_system: string;
  ammo_system: string;
  reviewer_system: string;
};

export type RemixLabAgentPromptsView = {
  prompts: RemixLabAgentPrompts;
  defaults: RemixLabAgentPrompts;
  overridden: Record<string, boolean>;
};

export type RemixLabAgentProposal = {
  id: string;
  type: "upsert_prompt" | "delete_prompt" | "start_experiment" | "set_active_prompt" | string;
  summary: string;
  payload: Record<string, unknown>;
};

export type RemixLabAgentChat = {
  reply: string;
  proposals: RemixLabAgentProposal[];
};

export type RemixLabAgentLast = {
  at: string;
  message: string;
  experiment_id?: string;
  reply?: string;
  proposals?: RemixLabAgentProposal[];
  error?: string;
};

export type RemixLabAgentHistoryTurn = {
  role: "user" | "assistant" | string;
  text: string;
  proposals?: RemixLabAgentProposal[];
  at: string;
};

export type RemixLabExperiment = {
  id: string;
  title: string;
  source_text: string;
  prompt_stamp: string;
  status: string;
  workflow?: boolean;
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
  pipeline?: string;
  run_count: number;
  preset_index?: number | null;
};

export async function fetchRemixLabDefaults(api: RemixLabApi): Promise<RemixLabDefaults> {
  const response = await api("/api/remix-lab/defaults");
  if (!response.ok) throw new Error("创作台默认配置读取失败。");
  return (await response.json()) as RemixLabDefaults;
}

// 「完成」即保存：把模型配置弹窗的槽位草稿持久化为预设，刷新后原样带回。
export async function saveRemixLabPresets(
  api: RemixLabApi,
  slots: RemixLabCreateSlot[],
): Promise<RemixLabDefaults> {
  const response = await api("/api/remix-lab/presets", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ slots }),
  });
  if (!response.ok) throw new Error("模型配置保存失败。");
  return (await response.json()) as RemixLabDefaults;
}

export async function fetchRemixLabExperiments(
  api: RemixLabApi,
): Promise<RemixLabExperimentSummary[]> {
  const response = await api("/api/remix-lab/experiments");
  if (!response.ok) throw new Error("创作台实验列表读取失败。");
  return (await response.json()) as RemixLabExperimentSummary[];
}

export async function createRemixLabExperiment(
  api: RemixLabApi,
  source: string,
  slots: RemixLabCreateSlot[],
  promptIDs: string[],
): Promise<RemixLabExperiment> {
  const response = await api("/api/remix-lab/experiments", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ source, slots, prompt_ids: promptIDs }),
  });
  if (!response.ok) throw new Error("创作台实验创建失败。");
  return (await response.json()) as RemixLabExperiment;
}

export async function fetchRemixLabPrompts(api: RemixLabApi): Promise<RemixLabPrompt[]> {
  const response = await api("/api/remix-lab/prompts");
  if (!response.ok) throw new Error("提示词库读取失败。");
  const body = (await response.json()) as { prompts?: RemixLabPrompt[] };
  return body.prompts ?? [];
}

export async function upsertRemixLabPrompt(
  api: RemixLabApi,
  prompt: Partial<RemixLabPrompt>,
): Promise<RemixLabPrompt> {
  const response = await api("/api/remix-lab/prompts", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(prompt),
  });
  if (!response.ok) throw new Error("提示词保存失败。");
  return (await response.json()) as RemixLabPrompt;
}

export async function deleteRemixLabPrompt(api: RemixLabApi, id: string): Promise<void> {
  const response = await api(`/api/remix-lab/prompts/${id}`, { method: "DELETE" });
  if (!response.ok) throw new Error("提示词删除失败。");
}

export async function fetchRemixLabActivePrompt(api: RemixLabApi): Promise<RemixLabActivePrompt> {
  const response = await api("/api/remix-lab/active-prompt");
  if (!response.ok) throw new Error("当前二创提示词读取失败。");
  return (await response.json()) as RemixLabActivePrompt;
}

export async function setRemixLabActivePrompt(api: RemixLabApi, id: string): Promise<void> {
  const response = await api("/api/remix-lab/active-prompt", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id }),
  });
  if (!response.ok) throw new Error("设为系统二创失败。");
}

export async function fetchRemixLabAgentSettings(api: RemixLabApi): Promise<RemixLabAgentSettings> {
  const response = await api("/api/remix-lab/agent-settings");
  if (!response.ok) throw new Error("智能体设置读取失败。");
  return (await response.json()) as RemixLabAgentSettings;
}

export async function saveRemixLabAgentSettings(
  api: RemixLabApi,
  body: { model: string; base_url: string; reasoning_effort: string; api_key: string },
): Promise<RemixLabAgentSettings> {
  const response = await api("/api/remix-lab/agent-settings", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!response.ok) throw new Error("智能体设置保存失败。");
  return (await response.json()) as RemixLabAgentSettings;
}

/** 一次运行的工作流分解：节点（各阶段实际输入输出）+ 连线。 */
export type RemixLabRunStage = {
  id: string;
  kind: "input" | "agent" | "gate" | "output" | string;
  title: string;
  status: "ok" | "failed" | "skipped" | "missing" | string;
  x?: number;
  y?: number;
  model?: string;
  ms?: number;
  error?: string;
  prompt_key?: string;
  system_prompt?: string;
  user_prompt?: string;
  output?: string;
  extra?: Record<string, unknown>;
};

export type RemixLabRunStagesView = {
  run_id: string;
  pipeline: string;
  status: string;
  stages: RemixLabRunStage[];
  edges: Array<[string, string]>;
  production?: RemixLabProduction | null;
};

export async function fetchRemixLabRunStages(
  api: RemixLabApi,
  runID: string,
): Promise<RemixLabRunStagesView> {
  const response = await api(`/api/remix-lab/runs/${runID}/stages`);
  if (!response.ok) throw new Error("工作流分解读取失败。");
  return (await response.json()) as RemixLabRunStagesView;
}

export async function fetchRemixLabAgentPrompts(
  api: RemixLabApi,
): Promise<RemixLabAgentPromptsView> {
  const response = await api("/api/remix-lab/agent-prompts");
  if (!response.ok) throw new Error("Agent提示词读取失败。");
  return (await response.json()) as RemixLabAgentPromptsView;
}

// 保存agent提示词：与默认一致的字段后端会自动回落为跟随默认。
export async function saveRemixLabAgentPrompts(
  api: RemixLabApi,
  prompts: RemixLabAgentPrompts,
): Promise<RemixLabAgentPromptsView> {
  const response = await api("/api/remix-lab/agent-prompts", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(prompts),
  });
  if (!response.ok) throw new Error(await readAPIError(response, "Agent提示词保存失败。"));
  return (await response.json()) as RemixLabAgentPromptsView;
}

async function readAPIError(response: Response, fallback: string): Promise<string> {
  try {
    const body = (await response.json()) as { message?: string };
    const message = body.message?.trim();
    if (message) return message;
  } catch {
    // keep fallback
  }
  return fallback;
}

export async function fetchRemixLabAgentHistory(
  api: RemixLabApi,
): Promise<RemixLabAgentHistoryTurn[]> {
  const response = await api("/api/remix-lab/agent/history");
  if (!response.ok) return [];
  const body = (await response.json()) as { turns?: RemixLabAgentHistoryTurn[] };
  return body.turns ?? [];
}

export async function clearRemixLabAgentHistory(api: RemixLabApi): Promise<void> {
  const response = await api("/api/remix-lab/agent/history", { method: "DELETE" });
  if (!response.ok) throw new Error("清空对话失败。");
}

export async function fetchRemixLabAgentLast(api: RemixLabApi): Promise<RemixLabAgentLast | null> {
  const response = await api("/api/remix-lab/agent/last");
  if (!response.ok) return null;
  const body = (await response.json()) as { found?: boolean; last?: RemixLabAgentLast };
  if (!body.found || !body.last) return null;
  return body.last;
}

export async function chatRemixLabAgent(
  api: RemixLabApi,
  message: string,
  experimentID?: string,
): Promise<RemixLabAgentChat> {
  const response = await api("/api/remix-lab/agent/chat", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ message, experiment_id: experimentID || "" }),
  });
  if (!response.ok) throw new Error(await readAPIError(response, "智能体请求失败。"));
  return (await response.json()) as RemixLabAgentChat;
}

export async function confirmRemixLabAgentProposal(
  api: RemixLabApi,
  proposalID: string,
): Promise<{ type: string; result?: { id?: string } }> {
  const response = await api("/api/remix-lab/agent/confirm", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ proposal_id: proposalID }),
  });
  if (!response.ok) throw new Error("草案确认失败。");
  return (await response.json()) as { type: string; result?: { id?: string } };
}

export async function deleteRemixLabExperiment(api: RemixLabApi, id: string): Promise<void> {
  const response = await api(`/api/remix-lab/experiments/${id}`, { method: "DELETE" });
  if (!response.ok) throw new Error("创作台实验删除失败。");
}

export async function fetchRemixLabExperiment(
  api: RemixLabApi,
  id: string,
): Promise<RemixLabExperiment> {
  const response = await api(`/api/remix-lab/experiments/${id}`);
  if (!response.ok) throw new Error("创作台实验详情读取失败。");
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

// 保存操作员编辑后的定稿（正文+发布包字段），编辑内容原样入库。
export async function saveRemixLabRunPackage(
  api: RemixLabApi,
  runID: string,
  pkg: RemixLabPackageInput,
): Promise<void> {
  const response = await api(`/api/remix-lab/runs/${runID}/package`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(pkg),
  });
  if (!response.ok) throw new Error(await readAPIError(response, "定稿保存失败。"));
}

// 断点重试：失败运行整体重试（agent产物复用）；指定 nodeID 时该节点作废重跑，
// 写手链路重做后自动续走后面的环节。
export async function retryRemixLabRun(
  api: RemixLabApi,
  runID: string,
  nodeID?: string,
): Promise<void> {
  const response = await api(`/api/remix-lab/runs/${runID}/retry`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ node_id: nodeID ?? "" }),
  });
  if (!response.ok) throw new Error(await readAPIError(response, "重试提交失败。"));
}

// 把批注交给审稿agent打回重做：run 会回到生成中，完成后带回修订稿。
export async function reworkRemixLabRun(
  api: RemixLabApi,
  runID: string,
  annotations: string,
): Promise<void> {
  const response = await api(`/api/remix-lab/runs/${runID}/rework`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ annotations }),
  });
  if (!response.ok) throw new Error(await readAPIError(response, "打回重做提交失败。"));
}
