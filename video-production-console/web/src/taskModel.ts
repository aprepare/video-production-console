export type ReasoningEffort = "low" | "medium" | "high" | "xhigh" | "max" | "ultra";

export type TaskModelOverride = {
  model: string;
  reasoningEffort: ReasoningEffort | "";
};

export type TaskModelDefaults = {
  remix_model?: string;
  codex_default_model?: string;
  codex_default_reasoning_effort?: string;
};

export type TaskModelPurpose = "remix" | "codex";

export function inheritedTaskModel(defaults: TaskModelDefaults | undefined, purpose: TaskModelPurpose): string {
  if (purpose === "remix") {
    return defaults?.remix_model?.trim() || defaults?.codex_default_model?.trim() || "默认模型";
  }
  return defaults?.codex_default_model?.trim() || "默认模型";
}

export const reasoningEfforts: ReasoningEffort[] = [
  "low",
  "medium",
  "high",
  "xhigh",
  "max",
  "ultra",
];
