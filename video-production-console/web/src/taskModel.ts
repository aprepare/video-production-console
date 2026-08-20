export type ReasoningEffort = "low" | "medium" | "high" | "xhigh" | "max" | "ultra";

export type TaskModelOverride = {
  model: string;
  reasoningEffort: ReasoningEffort | "";
};

export type TaskModelDefaults = {
  remix_model?: string;
  remix_reasoning_effort?: string;
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

export function inheritedTaskEffort(defaults: TaskModelDefaults | undefined, purpose: TaskModelPurpose): string {
  if (purpose === "remix") {
    return defaults?.remix_reasoning_effort?.trim() || "不设置";
  }
  return defaults?.codex_default_reasoning_effort?.trim() || "Codex 默认强度";
}

export const reasoningEfforts: ReasoningEffort[] = [
  "low",
  "medium",
  "high",
  "xhigh",
  "max",
  "ultra",
];

export const selectableModels = ["gpt-5.6-sol", "grok-4.6", "gpt-5.6-terra"] as const;

export type SelectableModel = (typeof selectableModels)[number];

export function resolveSelectableModels(allowlist?: readonly string[]): readonly string[] {
  if (allowlist && allowlist.length > 0) return allowlist;
  return selectableModels;
}

export function resolveSelectableEfforts(allowlist?: readonly string[]): readonly string[] {
  if (allowlist && allowlist.length > 0) return allowlist;
  return reasoningEfforts;
}

export function isSelectableModel(value: string, allowlist?: readonly string[]): boolean {
  return resolveSelectableModels(allowlist).includes(value);
}
