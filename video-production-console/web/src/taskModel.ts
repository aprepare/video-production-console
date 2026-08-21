export type ReasoningEffort = "low" | "medium" | "high" | "xhigh" | "max" | "ultra";

export type TaskModelOverride = {
  model: string;
  reasoningEffort: ReasoningEffort | "";
  // Multi-model fan-out for 二创文案: one task per selected model. Empty or
  // absent means the single `model` override (or the inherited default) runs.
  models?: string[];
};

export type TaskModelDefaults = {
  remix_model?: string;
  remix_reasoning_effort?: string;
  spoken_lines_model?: string;
  codex_default_model?: string;
  codex_default_reasoning_effort?: string;
};

export type TaskModelPurpose = "remix" | "spoken" | "codex";

export function inheritedTaskModel(defaults: TaskModelDefaults | undefined, purpose: TaskModelPurpose): string {
  if (purpose === "spoken") {
    // 口播稿 model setting falls back to the remix model, matching the backend.
    return (
      defaults?.spoken_lines_model?.trim() ||
      defaults?.remix_model?.trim() ||
      defaults?.codex_default_model?.trim() ||
      "默认模型"
    );
  }
  if (purpose === "remix") {
    return defaults?.remix_model?.trim() || defaults?.codex_default_model?.trim() || "默认模型";
  }
  return defaults?.codex_default_model?.trim() || "默认模型";
}

export function inheritedTaskEffort(defaults: TaskModelDefaults | undefined, purpose: TaskModelPurpose): string {
  if (purpose === "remix" || purpose === "spoken") {
    return defaults?.remix_reasoning_effort?.trim() || "不设置";
  }
  return defaults?.codex_default_reasoning_effort?.trim() || "默认强度";
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

export function isSelectableModel(value: string): value is SelectableModel {
  return (selectableModels as readonly string[]).includes(value);
}

// modelOptionsList parses the newline-separated model_options setting.
export function modelOptionsList(raw?: string): string[] {
  return (raw || "")
    .split("\n")
    .map((value) => value.trim())
    .filter(Boolean);
}
