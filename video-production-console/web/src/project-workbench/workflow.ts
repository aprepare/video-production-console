import type { ActiveWorkflow, PrimaryAction, ProductionStage, ProjectDetail } from "./types";

const liveTaskStatuses = ["queued", "running", "awaiting_input", "waiting_input", "resuming"] as const;

export function isLiveTaskStatus(status: string) {
  return (liveTaskStatuses as readonly string[]).includes(status);
}

export const productionStages: ReadonlyArray<{ id: ProductionStage; label: string }> = [
  { id: "script", label: "文案" },
  { id: "assets", label: "素材" },
  { id: "mixing", label: "混剪" },
  { id: "review", label: "审核" },
  { id: "published", label: "已发布" },
];

const activeStepLabels: Partial<Record<ActiveWorkflow["current_step"], string>> = {
  topic_card: "正在生成选题卡",
  remix: "正在二创文案",
};

function isReady(detail: ProjectDetail, type: string) {
  return detail.assets?.[type]?.state === "ready";
}

function isBackgroundReady(detail: ProjectDetail) {
  return detail.background_reference?.state === "ready";
}

export function deriveProductionStage(detail: ProjectDetail): ProductionStage {
  if (detail.project.stage === "published") return "published";
  // Final exported video is not part of the console workflow; only a ready mix draft advances review.
  if (isReady(detail, "mix_draft")) return "review";
  if (
    isReady(detail, "continuous_script") &&
    isReady(detail, "narration") &&
    isReady(detail, "subtitle_srt") &&
    isBackgroundReady(detail)
  ) return "mixing";
  if (isReady(detail, "continuous_script") && isReady(detail, "spoken_script")) return "assets";
  return "script";
}

export function missingProductionInputs(detail: ProjectDetail): string[] {
  const derivedStage = deriveProductionStage(detail);
  let computed: string[];
  switch (derivedStage) {
    case "script":
      computed = [
        ...(!isReady(detail, "continuous_script") ? ["continuous_script"] : []),
        ...(isReady(detail, "continuous_script") && !isReady(detail, "spoken_script") ? ["spoken_script"] : []),
      ];
      break;
    case "assets":
      computed = ["narration", "subtitle_srt"].filter((type) => !isReady(detail, type));
      if (!isBackgroundReady(detail)) computed.push("account_background");
      break;
    case "mixing":
      computed = isReady(detail, "mix_draft") ? [] : ["mix_draft"];
      break;
    case "review":
      computed = [];
      break;
    case "published":
      computed = [];
      break;
  }
  const backendMissing = detail.project.stage === derivedStage
    ? detail.missing_assets || []
    : [];
  // final_video is intentionally out of the workbench; ignore legacy backend hints.
  return [...new Set([...backendMissing, ...computed])].filter((type) => type !== "final_video");
}

export function montageInputsReady(detail: ProjectDetail): boolean {
  return isReady(detail, "continuous_script")
    && isReady(detail, "narration")
    && isReady(detail, "subtitle_srt")
    && isBackgroundReady(detail);
}

export function canRemakeMontage(detail: ProjectDetail): boolean {
  if (!montageInputsReady(detail)) return false;
  const mixState = detail.assets?.mix_draft?.state;
  return mixState === "ready" || mixState === "stale" || deriveProductionStage(detail) === "published";
}

// 文案已定稿、剪映草稿还没出来时，才允许一键跑口播→配音→混剪。
// 已有 ready 草稿不再自动重做，避免覆盖人工改过的剪映工程。
export function canOneClickProduce(detail: ProjectDetail): boolean {
  if (detail.project.stage === "published") return false;
  if (!isReady(detail, "continuous_script")) return false;
  if (isReady(detail, "mix_draft")) return false;
  return true;
}

export function nextPrimaryAction(detail: ProjectDetail): PrimaryAction | null {
  const stage = deriveProductionStage(detail);
  if (stage === "published") return null;
  const base: PrimaryAction = stage === "script"
    ? !isReady(detail, "continuous_script")
      ? isReady(detail, "source_script")
        ? { id: "start-source-remix", label: "开始正式二创", disabled: false }
        : { id: "start-source-remix", label: "先粘贴同行原文", disabled: true }
      : { id: "start-spoken-lines", label: "生成口播稿", disabled: false }
    : stage === "assets"
      ? { id: "prepare-assets", label: "补齐制作素材", disabled: false }
      : stage === "mixing"
        ? { id: "start-mixing", label: "开始风景混剪", disabled: false }
        : { id: "publish", label: "确认已发布", disabled: false };
  const workflow = detail.active_workflow;
  const activeLabel = workflow?.state === "running" ? activeStepLabels[workflow.current_step] : undefined;
  return activeLabel
    ? { ...base, label: activeLabel, disabled: true, activeStep: workflow?.current_step }
    : base;
}
