// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ProjectWorkbench } from "./ProjectWorkbench";
import type { ProjectDetail, ProjectTask } from "./types";

afterEach(cleanup);

const projectID = "814ebfde-7470-418a-a703-a33596f7e8fe";

function asset(type: string, state: "ready" | "stale" | "failed" = "ready") {
  return {
    id: `${type}-asset`,
    type,
    filename: `${type}.txt`,
    mime_type: "text/plain",
    size: 128,
    version: 2,
    state,
    created_at: "2026-08-08T00:00:00Z",
  } as const;
}

function fixture(): ProjectDetail {
  return {
    project: {
      id: projectID,
      account_id: "account-1",
      title: "退休金真相",
      stage: "assets",
      updated_at: "2026-08-08T00:00:00Z",
    },
    assets: {
      continuous_script: asset("continuous_script"),
      narration: asset("narration", "stale"),
      mix_draft: asset("mix_draft", "failed"),
    },
    background_reference: {
      ...asset("account_background"),
      filename: "account-background.png",
      mime_type: "image/png",
    },
    missing_assets: ["subtitle_srt"],
    active_workflow: null,
  };
}

const task: ProjectTask = {
  id: "task-1",
  project_id: projectID,
  type: "montage",
  skill_name: "jianying-montage-draft",
  action: "montage.execute",
  status: "awaiting_input",
  result_summary: "素材已校验，正在等待确认片尾时长。",
  created_at: "2026-08-08T00:00:00Z",
  messages: [
    { id: "m1", role: "assistant", content: "请确认片尾保留几秒。", created_at: "2026-08-08T00:00:00Z" },
  ],
};

function workbenchProps(detail = fixture()) {
  return {
    detail,
    tasks: [task],
    accountName: "稳健养老号",
    onBack: vi.fn(),
    onDelete: vi.fn(),
    onRemix: vi.fn(),
    onMix: vi.fn(),
    onPublish: vi.fn(),
    onUpload: vi.fn(),
    onReplaceBackground: vi.fn(),
    onViewAsset: vi.fn(),
    onOpenConversation: vi.fn(),
    onOpenTask: vi.fn(),
  };
}

function renderWorkbench(detail = fixture()) {
  const props = workbenchProps(detail);
  render(<ProjectWorkbench {...props} />);
  return props;
}

test("renders the desktop project production contract without drawer semantics or deprecated actions", () => {
  renderWorkbench();

  expect(screen.getByRole("heading", { name: "退休金真相" })).toBeTruthy();
  expect(screen.getByText("稳健养老号")).toBeTruthy();
  expect(screen.getByRole("button", { name: "返回项目看板" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "删除当前项目" })).toBeTruthy();
  expect(screen.getByRole("navigation", { name: "五阶段生产轨" })).toBeTruthy();
  for (const label of ["文案", "素材", "混剪", "审核", "已发布"]) {
    expect(screen.getByText(label)).toBeTruthy();
  }
  expect(screen.getByRole("button", { name: "补齐制作素材" })).toBeTruthy();
  expect(screen.getByRole("region", { name: "当前项目资产" })).toBeTruthy();
  expect(screen.getByRole("region", { name: "Codex 对话摘要" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "打开 Codex 对话" })).toBeTruthy();
  expect(screen.getAllByText("素材已校验，正在等待确认片尾时长。").length).toBeGreaterThan(0);
  expect(screen.getByText("请确认片尾保留几秒。")).toBeTruthy();
  expect(screen.queryByRole("dialog")).toBeNull();

  const forbidden = ["给我选题", "深化一下", "生成选题卡", "口播稿", "remix.spoken_format", "待发布"];
  for (const phrase of forbidden) expect(screen.queryByText(phrase, { exact: false })).toBeNull();
});

test("shows every project-scoped production asset with state, meaning, and accessible actions", () => {
  const props = renderWorkbench();

  expect(screen.getByText("连续文案")).toBeTruthy();
  expect(screen.getByText("二创生成的完整连续文本，用于配音和混剪。")).toBeTruthy();
  expect(screen.getByText("配音")).toBeTruthy();
  expect(screen.getByText("SRT 字幕")).toBeTruthy();
  expect(screen.getByText("混剪草稿")).toBeTruthy();
  expect(screen.getByText("成片")).toBeTruthy();
  expect(screen.getAllByText("存在").length).toBeGreaterThan(0);
  expect(screen.getAllByText("失效").length).toBeGreaterThan(0);
  expect(screen.getAllByText("缺失").length).toBeGreaterThan(0);

  expect(screen.getByLabelText<HTMLInputElement>("上传配音").type).toBe("file");
  expect(screen.getByLabelText<HTMLInputElement>("上传SRT 字幕").type).toBe("file");
  expect(screen.getByLabelText<HTMLInputElement>("上传连续文案").type).toBe("file");
  expect(screen.getByLabelText<HTMLInputElement>("上传成片").type).toBe("file");
  expect(screen.getByLabelText<HTMLInputElement>("替换账号背景图").type).toBe("file");

  fireEvent.change(screen.getByLabelText("上传配音"), {
    target: { files: [new File(["audio"], "voice.wav", { type: "audio/wav" })] },
  });
  expect(props.onUpload).toHaveBeenCalledWith("narration", expect.any(File));
  fireEvent.click(screen.getByRole("button", { name: "查看连续文案" }));
  expect(props.onViewAsset).toHaveBeenCalledWith(expect.objectContaining({ type: "continuous_script" }));
});

test("disables the single primary action while the automatic remix workflow is active", () => {
  const detail = fixture();
  detail.project.stage = "script";
  detail.assets = {};
  detail.active_workflow = {
    id: "workflow-1",
    project_id: projectID,
    account_id: "account-1",
    kind: "remix",
    state: "running",
    current_step: "topic_card",
    model: "gpt-5.6-sol",
    reasoning_effort: "medium",
    created_at: "2026-08-08T00:00:00Z",
    updated_at: "2026-08-08T00:00:00Z",
    current_task: null,
  };

  renderWorkbench(detail);

  expect(screen.getByRole<HTMLButtonElement>("button", { name: "正在生成选题卡" }).disabled).toBe(true);
});

test("runs mixing from the single primary action when all formal inputs are ready", () => {
  const detail = fixture();
  detail.assets.narration = asset("narration");
  detail.assets.subtitle_srt = asset("subtitle_srt");
  delete detail.assets.mix_draft;
  const props = renderWorkbench(detail);

  fireEvent.click(screen.getByRole("button", { name: "开始混剪" }));

  expect(props.onMix).toHaveBeenCalledOnce();
});

test("keeps the published button wording while exposing its operation through aria-label", () => {
  const detail = fixture();
  detail.project.stage = "review";
  detail.assets.final_video = asset("final_video");
  const props = renderWorkbench(detail);

  const publish = screen.getByRole("button", { name: "将当前项目标记为已发布" });
  expect(publish.textContent).toContain("已发布");
  fireEvent.click(publish);

  expect(props.onPublish).toHaveBeenCalledOnce();
});

test.each([
  ["narration", "上传配音"],
  ["subtitle_srt", "上传SRT 字幕"],
  ["account_background", "上传账号背景图"],
] as const)("routes a single missing %s input to its matching upload control", (missingType, uploadLabel) => {
  const detail = fixture();
  detail.project.stage = "assets";
  detail.assets = {
    continuous_script: asset("continuous_script"),
    narration: asset("narration"),
    subtitle_srt: asset("subtitle_srt"),
  };
  detail.background_reference = { ...asset("account_background"), mime_type: "image/png" };
  detail.missing_assets = [missingType];
  if (missingType === "account_background") {
    detail.background_reference = null;
  } else {
    delete detail.assets[missingType];
  }
  const click = vi.fn();
  renderWorkbench(detail);
  screen.getByLabelText(uploadLabel).addEventListener("click", click);

  fireEvent.click(screen.getByRole("button", { name: "补齐制作素材" }));

  expect(click).toHaveBeenCalledOnce();
});

test("routes an invalid inherited background to the replacement control", () => {
  const detail = fixture();
  detail.project.stage = "assets";
  detail.assets = {
    continuous_script: asset("continuous_script"),
    narration: asset("narration"),
    subtitle_srt: asset("subtitle_srt"),
  };
  detail.background_reference = { ...asset("account_background", "stale"), mime_type: "image/png" };
  detail.missing_assets = ["account_background"];
  const click = vi.fn();
  renderWorkbench(detail);
  screen.getByLabelText("替换账号背景图").addEventListener("click", click);

  fireEvent.click(screen.getByRole("button", { name: "补齐制作素材" }));

  expect(click).toHaveBeenCalledOnce();
});

test("routes an invalid continuous script to its own upload control instead of restarting remix", () => {
  const detail = fixture();
  detail.project.stage = "assets";
  detail.assets = {
    continuous_script: asset("continuous_script", "stale"),
    narration: asset("narration"),
    subtitle_srt: asset("subtitle_srt"),
  };
  detail.missing_assets = ["continuous_script"];
  const click = vi.fn();
  renderWorkbench(detail);
  screen.getByLabelText("上传连续文案").addEventListener("click", click);

  fireEvent.click(screen.getByRole("button", { name: "补齐制作素材" }));

  expect(click).toHaveBeenCalledOnce();
});

test("guards the 1366 by 768 compact desktop structure without pretending to measure visibility", () => {
  Object.defineProperty(window, "innerWidth", { configurable: true, value: 1366 });
  Object.defineProperty(window, "innerHeight", { configurable: true, value: 768 });
  window.dispatchEvent(new Event("resize"));

  const { container } = render(<ProjectWorkbench {...workbenchProps()} />);
  window.dispatchEvent(new Event("resize"));

  const workbench = container.querySelector(".project-workbench--compact-desktop");
  expect(workbench).toBeTruthy();
  expect(window.innerWidth).toBe(1366);
  expect(window.innerHeight).toBe(768);
  expect(within(workbench as HTMLElement).getByRole("navigation", { name: "五阶段生产轨" })).toBeTruthy();
  expect(within(workbench as HTMLElement).getByRole("region", { name: "下一主动作" })).toBeTruthy();
  expect(within(workbench as HTMLElement).getByRole("region", { name: "当前项目资产" })).toBeTruthy();
  expect(within(workbench as HTMLElement).getByRole("region", { name: "Codex 对话摘要" })).toBeTruthy();
});
