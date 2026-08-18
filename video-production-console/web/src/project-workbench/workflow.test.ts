import { describe, expect, test } from "vitest";
import type { ProjectDetail } from "./types";
import {
  canRemakeMontage,
  deriveProductionStage,
  missingProductionInputs,
  nextPrimaryAction,
  productionStages,
} from "./workflow";

const projectID = "814ebfde-7470-418a-a703-a33596f7e8fe";

function detail(
  stage: ProjectDetail["project"]["stage"] = "script",
  assetStates: Record<string, "ready" | "stale" | "failed"> = {},
  currentStep?: "topic_card" | "remix",
  backgroundState?: "ready" | "stale" | "failed",
  backendMissing: string[] = [],
): ProjectDetail {
  return {
    project: {
      id: projectID,
      account_id: "20a65782-6e80-4eed-b0f8-9593c8cc9378",
      title: "养老金选题",
      stage,
    },
    assets: Object.fromEntries(
      Object.entries(assetStates).map(([type, state]) => [
        type,
        {
          id: `${type}-asset`,
          type,
          filename: `${type}.txt`,
          mime_type: "text/plain",
          size: 10,
          version: 1,
          state,
          created_at: "2026-08-08T00:00:00Z",
        },
      ]),
    ),
    background_reference: backgroundState
      ? {
          id: "background-asset",
          type: "account_background",
          filename: "background.png",
          mime_type: "image/png",
          size: 10,
          version: 1,
          state: backgroundState,
          created_at: "2026-08-08T00:00:00Z",
        }
      : null,
    missing_assets: backendMissing,
    active_workflow: currentStep
      ? {
          id: "a6372165-bb5f-4474-9007-cde18d19e7d8",
          project_id: projectID,
          account_id: "20a65782-6e80-4eed-b0f8-9593c8cc9378",
          kind: "remix",
          state: "running",
          current_step: currentStep,
          model: "gpt-5.6-sol",
          reasoning_effort: "medium",
          created_at: "2026-08-08T00:00:00Z",
          updated_at: "2026-08-08T00:00:00Z",
          current_task: {
            id: "2c05dd52-ce7b-4244-9c03-22e887b198bf",
            type: currentStep === "topic_card" ? "topic_commit" : "remix",
            skill_name: "finance-viral-remix",
            status: "running",
            created_at: "2026-08-08T00:00:00Z",
          },
        }
      : null,
  };
}

describe("production workflow view model", () => {
  test("does not expose deprecated workflow model names", () => {
    const deprecatedNames = [
      ["spoken", "script"].join("_"),
      ["Stage", "Ready"].join(""),
      ["Stage", "Topic"].join(""),
    ];
    expect(deprecatedNames.some((name) => JSON.stringify(productionStages).includes(name))).toBe(
      false,
    );
  });

  test("exposes exactly the five production stages", () => {
    expect(productionStages.map(({ id, label }) => [id, label])).toEqual([
      ["script", "文案"],
      ["assets", "素材"],
      ["mixing", "混剪"],
      ["review", "审核"],
      ["published", "已发布"],
    ]);
  });

  test("derives script, assets, mixing, and review from formal current assets", () => {
    expect(deriveProductionStage(detail())).toBe("script");
    expect(deriveProductionStage(detail("script", { continuous_script: "ready" }))).toBe("assets");
    expect(
      deriveProductionStage(
        detail(
          "assets",
          {
            continuous_script: "ready",
            narration: "ready",
            subtitle_srt: "ready",
          },
          undefined,
          "ready",
        ),
      ),
    ).toBe("mixing");
    expect(deriveProductionStage(detail("mixing", { mix_draft: "ready" }))).toBe("review");
    // final_video is not a workbench gate; alone it must not advance the stage.
    expect(deriveProductionStage(detail("mixing", { final_video: "ready" }))).toBe("script");
  });

  test("derives published only from the backend project stage", () => {
    expect(deriveProductionStage(detail("published"))).toBe("published");
    expect(deriveProductionStage(detail("review", { mix_draft: "ready" }))).toBe("review");
  });

  test("ignores current assets that are not explicitly ready", () => {
    expect(deriveProductionStage(detail("assets", { continuous_script: "stale" }))).toBe(
      "script",
    );
    expect(
      deriveProductionStage(
        detail("mixing", {
          continuous_script: "ready",
          narration: "failed",
          subtitle_srt: "ready",
          mix_draft: "stale",
        }),
      ),
    ).toBe("assets");
    const legacyPayload = detail("assets", { continuous_script: "ready" });
    delete (legacyPayload.assets.continuous_script as { state?: string }).state;
    expect(deriveProductionStage(legacyPayload)).toBe("script");
  });

  test("reports the missing formal inputs for the next production transition", () => {
    expect(missingProductionInputs(detail())).toEqual(["continuous_script"]);
    expect(
      missingProductionInputs(detail("script", { continuous_script: "ready" }, undefined, "stale")),
    ).toEqual(["narration", "subtitle_srt", "account_background"]);
    expect(
      missingProductionInputs(
        detail(
          "assets",
          { continuous_script: "ready", narration: "ready", subtitle_srt: "ready" },
          undefined,
          "ready",
        ),
      ),
    ).toEqual(["mix_draft"]);
    expect(missingProductionInputs(detail("review", { mix_draft: "ready" }))).toEqual([]);
  });

  test("merges backend missing assets and rejects stale production inputs", () => {
    expect(
      missingProductionInputs(
        detail(
          "assets",
          { continuous_script: "ready", narration: "stale", subtitle_srt: "stale" },
          undefined,
          "stale",
          ["narration", "account_background"],
        ),
      ),
    ).toEqual(["narration", "account_background", "subtitle_srt"]);
  });

  test("does not merge backend missing assets from a different durable stage", () => {
    expect(
      missingProductionInputs(
        detail(
          "review",
          { continuous_script: "stale" },
          undefined,
          undefined,
          ["mix_draft"],
        ),
      ),
    ).toEqual(["continuous_script"]);
  });

  test("ignores legacy final_video missing hints from the backend", () => {
    expect(
      missingProductionInputs(
        detail(
          "mixing",
          {
            continuous_script: "ready",
            narration: "ready",
            subtitle_srt: "ready",
          },
          undefined,
          "ready",
          ["final_video", "mix_draft"],
        ),
      ),
    ).toEqual(["mix_draft"]);
  });

  test("maps active workflow steps to a disabled primary action", () => {
    expect(nextPrimaryAction(detail("script", {}, "topic_card"))).toMatchObject({
      id: "start-source-remix",
      label: "正在生成选题卡",
      disabled: true,
      activeStep: "topic_card",
    });
    expect(nextPrimaryAction(detail("script", {}, "remix"))).toMatchObject({
      id: "start-source-remix",
      label: "正在二创文案",
      disabled: true,
      activeStep: "remix",
    });
  });

  test("selects the next primary action from durable state", () => {
    expect(nextPrimaryAction(detail())).toMatchObject({
      id: "start-source-remix",
      label: "先粘贴同行原文",
      disabled: true,
    });
    expect(nextPrimaryAction(detail("script", { source_script: "ready" }))).toMatchObject({
      id: "start-source-remix",
      label: "开始正式二创",
      disabled: false,
    });
    expect(nextPrimaryAction(detail("assets", { continuous_script: "stale" }))).toMatchObject({
      id: "start-source-remix",
      label: "先粘贴同行原文",
      disabled: true,
    });
    expect(nextPrimaryAction(detail("script", { continuous_script: "ready" }))).toMatchObject({
      id: "prepare-assets",
      disabled: false,
    });
    expect(
      nextPrimaryAction(
        detail(
          "assets",
          {
            continuous_script: "ready",
            narration: "ready",
            subtitle_srt: "ready",
          },
          undefined,
          "ready",
        ),
      ),
    ).toMatchObject({ id: "start-mixing", disabled: false });
    expect(nextPrimaryAction(detail("review", { mix_draft: "ready" }))).toMatchObject({
      id: "publish",
      label: "确认已发布",
      disabled: false,
    });
    expect(nextPrimaryAction(detail("published"))).toBeNull();
  });

  test("allows remaking a montage after a registered draft exists", () => {
    const readyInputs = {
      continuous_script: "ready" as const,
      narration: "ready" as const,
      subtitle_srt: "ready" as const,
      mix_draft: "ready" as const,
    };
    expect(canRemakeMontage(detail("review", readyInputs, undefined, "ready"))).toBe(true);
    expect(canRemakeMontage(detail("mixing", {
      continuous_script: "ready",
      narration: "ready",
      subtitle_srt: "ready",
    }, undefined, "ready"))).toBe(false);
    expect(canRemakeMontage(detail("review", { mix_draft: "ready" }))).toBe(false);
  });

  test("allows publishing after mix draft without any final video asset", () => {
    expect(nextPrimaryAction(detail("review", { mix_draft: "ready" }))).toMatchObject({
      id: "publish",
      label: "确认已发布",
      disabled: false,
    });
    expect(
      nextPrimaryAction(
        detail("review", { mix_draft: "ready", final_video: "failed" }),
      ),
    ).toMatchObject({ id: "publish" });
  });
});
