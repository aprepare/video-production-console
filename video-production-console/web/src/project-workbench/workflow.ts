import type { ActiveWorkflow, PrimaryAction, ProductionStage, ProjectDetail } from "./types";

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

export function deriveProductionStage(detail: ProjectDetail): ProductionStage {
  if (detail.project.stage === "published") return "published";
  const assets = detail.assets || {};
  if (assets.mix_draft || assets.final_video) return "review";
  if (assets.continuous_script && assets.narration && assets.subtitle_srt) return "mixing";
  if (assets.continuous_script) return "assets";
  return "script";
}

export function missingProductionInputs(detail: ProjectDetail): string[] {
  const assets = detail.assets || {};
  switch (deriveProductionStage(detail)) {
    case "script":
      return assets.continuous_script ? [] : ["continuous_script"];
    case "assets":
      return ["narration", "subtitle_srt"].filter((type) => !assets[type]);
    case "mixing":
      return assets.mix_draft || assets.final_video ? [] : ["mix_draft"];
    case "review":
      return assets.final_video ? [] : ["final_video"];
    case "published":
      return [];
  }
}

export function nextPrimaryAction(detail: ProjectDetail): PrimaryAction | null {
  const stage = deriveProductionStage(detail);
  if (stage === "published") return null;
  const base: PrimaryAction = stage === "script"
    ? { id: "start-remix", label: "开始二创文案", disabled: false }
    : stage === "assets"
      ? { id: "prepare-assets", label: "补齐制作素材", disabled: false }
      : stage === "mixing"
        ? { id: "start-mixing", label: "开始混剪", disabled: false }
        : { id: "publish", label: "发布成片", disabled: false };
  const workflow = detail.active_workflow;
  const activeLabel = workflow?.state === "running" ? activeStepLabels[workflow.current_step] : undefined;
  return activeLabel
    ? { ...base, label: activeLabel, disabled: true, activeStep: workflow?.current_step }
    : base;
}
