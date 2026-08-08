// @vitest-environment jsdom
/// <reference types="node" />

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
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
    pendingActions: [] as string[],
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
  expect(screen.getByText("剪映草稿")).toBeTruthy();
  expect(screen.getByText("成片")).toBeTruthy();
  expect(screen.getAllByText("存在").length).toBeGreaterThan(0);
  expect(screen.getAllByText("失效").length).toBeGreaterThan(0);
  expect(screen.getAllByText("缺失").length).toBeGreaterThan(0);

  expect(screen.getByLabelText<HTMLInputElement>("上传配音").type).toBe("file");
  expect(screen.getByLabelText<HTMLInputElement>("上传SRT 字幕").type).toBe("file");
  expect(screen.getByLabelText<HTMLInputElement>("上传成片").type).toBe("file");
  expect(screen.getByLabelText<HTMLInputElement>("替换账号背景图").type).toBe("file");

  fireEvent.change(screen.getByLabelText("上传配音"), {
    target: { files: [new File(["audio"], "voice.wav", { type: "audio/wav" })] },
  });
  expect(props.onUpload).toHaveBeenCalledWith("narration", expect.any(File));
  fireEvent.click(screen.getByRole("button", { name: "查看连续文案" }));
  expect(props.onViewAsset).toHaveBeenCalledWith(expect.objectContaining({ type: "continuous_script" }));
});

test("shows the current registered Jianying display name while keeping storage identity in collapsed technical details", () => {
  const detail = fixture();
  detail.project.stage = "review";
  detail.assets.mix_draft = {
    ...asset("mix_draft"),
    id: "ready-mix-version",
    filename: "984c42ec-67b8-4d3f-99e3-d3d7a4b66205",
    mime_type: "inode/directory",
  };
  const registeredTask = {
    ...task,
    id: "984c42ec-67b8-4d3f-99e3-d3d7a4b66205",
    project_id: projectID,
    status: "completed",
    completion_phase: "registered",
    created_at: "2026-08-08T03:00:00Z",
    montage: {
      phase: "registered",
      registered_asset: {
        id: "ready-mix-version",
        filename: "账号_旧标题_b66205",
        display_name: "账号_短标题_b66205",
        storage_name: "984c42ec-67b8-4d3f-99e3-d3d7a4b66205",
        draft_id: "draft-id-123",
        path: "C:\\Jianying\\984c42ec-67b8-4d3f-99e3-d3d7a4b66205",
        sha256: "a".repeat(64),
        created_at: "2026-08-08T03:00:00Z",
      },
      can_retry_registration: false,
    },
  };
  const laterFailedTask = {
    ...registeredTask,
    id: "later-failed-task",
    status: "failed",
    created_at: "2026-08-08T04:00:00Z",
    montage: {
      ...registeredTask.montage,
      registered_asset: {
        ...registeredTask.montage.registered_asset,
        display_name: "错误失败草稿",
      },
    },
  };
  const otherProjectTask = {
    ...registeredTask,
    id: "other-project-task",
    project_id: "other-project",
    created_at: "2026-08-08T05:00:00Z",
  };
  const props = workbenchProps(detail);
  props.tasks = [laterFailedTask, otherProjectTask, registeredTask];
  const { container } = render(<ProjectWorkbench {...props} />);

  expect(screen.getByText("账号_短标题_b66205")).toBeTruthy();
  expect(screen.getByText("已登记 · 可继续编辑")).toBeTruthy();
  expect(screen.queryByText("错误失败草稿")).toBeNull();

  const technical = container.querySelector<HTMLDetailsElement>(".project-asset__technical");
  expect(technical?.open).toBe(false);
  expect(within(technical!).getAllByText("984c42ec-67b8-4d3f-99e3-d3d7a4b66205")).toHaveLength(2);
  expect(within(technical!).getByText("draft-id-123")).toBeTruthy();
  expect(within(technical!).getByText("C:\\Jianying\\984c42ec-67b8-4d3f-99e3-d3d7a4b66205")).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "查看剪映草稿" }));
  expect(props.onViewAsset).toHaveBeenCalledWith(expect.objectContaining({ id: "ready-mix-version" }));
});

test("falls back to the legacy registered filename when display_name is absent", () => {
  const detail = fixture();
  detail.assets.mix_draft = { ...asset("mix_draft"), id: "legacy-mix-version" };
  const legacyTask = {
    ...task,
    id: "legacy-task",
    project_id: projectID,
    status: "completed",
    completion_phase: "registered",
    montage: {
      phase: "registered",
      registered_asset: {
        id: "legacy-mix-version",
        filename: "旧版可读草稿",
        storage_name: "legacy-storage-name",
        path: "C:\\Jianying\\legacy-storage-name",
        sha256: "b".repeat(64),
        created_at: "2026-08-08T00:00:00Z",
      },
      can_retry_registration: false,
    },
  };
  const props = workbenchProps(detail);
  props.tasks = [legacyTask];
  render(<ProjectWorkbench {...props} />);

  expect(screen.getByText("旧版可读草稿")).toBeTruthy();
  expect(screen.getByText("已登记 · 可继续编辑")).toBeTruthy();
});

test("shows an unnamed draft when no task identity matches a UUID storage filename", () => {
  const detail = fixture();
  detail.assets.mix_draft = {
    ...asset("mix_draft"),
    id: "orphan-mix-version",
    filename: "984c42ec-67b8-4d3f-99e3-d3d7a4b66205",
    mime_type: "inode/directory",
  };
  const props = workbenchProps(detail);
  props.tasks = [];
  const { container } = render(<ProjectWorkbench {...props} />);

  expect(screen.getByText("未命名草稿")).toBeTruthy();
  expect(container.querySelector(".project-asset__display-name")?.textContent).not.toContain("984c42ec");
  expect(within(container.querySelector(".project-asset__technical")!).getByText("984c42ec-67b8-4d3f-99e3-d3d7a4b66205")).toBeTruthy();
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

test("restarts the automatic remix workflow when the generated continuous script is invalid", () => {
  const detail = fixture();
  detail.project.stage = "assets";
  detail.assets = {
    continuous_script: asset("continuous_script", "stale"),
    narration: asset("narration"),
    subtitle_srt: asset("subtitle_srt"),
  };
  detail.missing_assets = ["continuous_script"];
  const props = renderWorkbench(detail);

  fireEvent.click(screen.getByRole("button", { name: "开始二创文案" }));

  expect(props.onRemix).toHaveBeenCalledOnce();
});

test("does not expose manual upload controls for generated continuous scripts or montage drafts", () => {
  renderWorkbench();

  expect(screen.queryByLabelText("上传连续文案")).toBeNull();
  expect(screen.queryByLabelText("上传剪映草稿")).toBeNull();
  expect(screen.getByRole("button", { name: "查看连续文案" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "查看剪映草稿" })).toBeTruthy();
});

test("selects the newest live task consistently in the action panel and conversation", () => {
  const older = { ...task, id: "older", created_at: "2026-08-01T00:00:00Z", result_summary: "旧任务" };
  const newer = { ...task, id: "newer", created_at: "2026-08-09T00:00:00Z", result_summary: "最新任务" };
  const props = workbenchProps();
  props.tasks = [older, newer];
  render(<ProjectWorkbench {...props} />);

  expect(screen.getAllByText("最新任务").length).toBeGreaterThan(0);
  expect(screen.queryByText("旧任务")).toBeNull();
});

test("blocks an unknown backend missing key with an actionable explanation", () => {
  const detail = fixture();
  detail.project.stage = "review";
  detail.assets.final_video = asset("final_video");
  detail.missing_assets = ["future_asset"];
  renderWorkbench(detail);

  const action = screen.getByRole<HTMLButtonElement>("button", { name: "暂无法继续" });
  expect(action.disabled).toBe(true);
  expect(screen.getByText("无法识别项目缺项 future_asset，请刷新项目；若仍存在，请更新控制台服务。")).toBeTruthy();
});

test("disables the current action, delete, and related input while pending", () => {
  const props = workbenchProps();
  props.pendingActions = ["upload:narration", "delete"];
  render(<ProjectWorkbench {...props} />);

  expect(screen.getByRole<HTMLButtonElement>("button", { name: "补齐制作素材" }).disabled).toBe(true);
  expect(screen.getByRole<HTMLButtonElement>("button", { name: "删除当前项目" }).disabled).toBe(true);
  for (const label of ["上传配音", "上传SRT 字幕", "上传成片", "替换账号背景图"]) {
    expect(screen.getByLabelText<HTMLInputElement>(label).disabled).toBe(true);
  }
});

test("labels a terminal task as recent instead of actively processing", () => {
  const completed = { ...task, status: "completed", result_summary: "最近完成的结果" };
  const props = workbenchProps();
  props.tasks = [completed];
  render(<ProjectWorkbench {...props} />);

  expect(screen.getByText("最近任务")).toBeTruthy();
  expect(screen.queryByText("Codex 正在处理")).toBeNull();
  expect(screen.getAllByText("最近完成的结果").length).toBeGreaterThan(0);
});

test("keeps the 1366 by 768 desktop structure without runtime layout classes", () => {
  Object.defineProperty(window, "innerWidth", { configurable: true, value: 1366 });
  Object.defineProperty(window, "innerHeight", { configurable: true, value: 768 });
  window.dispatchEvent(new Event("resize"));

  const { container } = render(<ProjectWorkbench {...workbenchProps()} />);
  window.dispatchEvent(new Event("resize"));

  const workbench = container.querySelector(".project-workbench");
  expect(workbench).toBeTruthy();
  expect(container.querySelector(".project-workbench--compact-desktop")).toBeNull();
  expect(window.innerWidth).toBe(1366);
  expect(window.innerHeight).toBe(768);
  expect(within(workbench as HTMLElement).getByRole("navigation", { name: "五阶段生产轨" })).toBeTruthy();
  expect(within(workbench as HTMLElement).getByRole("region", { name: "下一主动作" })).toBeTruthy();
  expect(within(workbench as HTMLElement).getByRole("region", { name: "当前项目资产" })).toBeTruthy();
  expect(within(workbench as HTMLElement).getByRole("region", { name: "Codex 对话摘要" })).toBeTruthy();
});

test("keeps three workbench columns through 760 pixels and switches to one below it", () => {
  const css = readFileSync(resolve(process.cwd(), "src/project-workbench/project-workbench.css"), "utf8");
  const compactStart = css.indexOf("@media (max-width: 1100px)");
  const mobileStart = css.indexOf("@media (max-width: 759px)");
  const reducedMotionStart = css.indexOf("@media (prefers-reduced-motion", mobileStart);
  const compact = css.slice(compactStart, mobileStart);
  const mobile = css.slice(mobileStart, reducedMotionStart);

  expect(css).toContain("height: 100vh");
  expect(css).toContain("overflow: hidden");
  expect(compact).toContain("grid-template-columns:");
  expect(compact).not.toContain("grid-column: 1 / -1");
  expect(mobile).toContain("display: block");
  expect(css).not.toContain("@media (max-width: 1023px)");
});

test("provides native mobile accordions for project assets and conversation", () => {
  const { container } = render(<ProjectWorkbench {...workbenchProps()} />);
  const assetToggle = screen.getByLabelText("收起或展开项目资产");
  const conversationToggle = screen.getByLabelText("收起或展开 Codex 对话");
  const assetDetails = assetToggle.closest("details") as HTMLDetailsElement | null;
  const conversationDetails = conversationToggle.closest("details") as HTMLDetailsElement | null;

  expect(assetDetails?.open).toBe(true);
  expect(conversationDetails?.open).toBe(true);
  fireEvent.click(assetToggle);
  fireEvent.click(conversationToggle);
  expect(assetDetails?.open).toBe(false);
  expect(conversationDetails?.open).toBe(false);
  expect(container.querySelectorAll(".mobile-accordion")).toHaveLength(2);
});

test("renders the mobile primary action bar as a direct workbench child with a shared action contract", () => {
  const detail = fixture();
  detail.project.stage = "script";
  detail.assets = {};
  detail.missing_assets = [];
  const props = workbenchProps(detail);
  const { container } = render(<ProjectWorkbench {...props} />);
  const workbench = container.querySelector(".project-workbench");
  const mobileBar = container.querySelector(".mobile-primary-action-bar");
  const desktopAction = container.querySelector<HTMLButtonElement>(".desktop-primary-action");
  const mobileAction = mobileBar?.querySelector<HTMLButtonElement>(".primary-action");

  expect(mobileBar?.parentElement).toBe(workbench);
  expect(mobileBar?.closest(".primary-action-panel")).toBeNull();
  expect(desktopAction?.textContent).toBe(mobileAction?.textContent);
  fireEvent.click(mobileAction!);
  expect(props.onRemix).toHaveBeenCalledTimes(1);
});

test("defines vertical mobile production, root action bar visibility, safe area, and 44px touch targets", () => {
  const css = readFileSync(resolve(process.cwd(), "src/project-workbench/project-workbench.css"), "utf8");
  const mobileStart = css.indexOf("@media (max-width: 759px)");
  const reducedStart = css.indexOf("@media (prefers-reduced-motion", mobileStart);
  const desktop = css.slice(0, mobileStart);
  const mobile = css.slice(mobileStart, reducedStart);
  const reduced = css.slice(reducedStart);

  expect(mobile).toContain(".production-rail");
  expect(mobile).toContain("grid-template-columns: 1fr");
  expect(mobile).toContain(".production-rail__line");
  expect(mobile).toContain("width: 2px");
  expect(desktop).toMatch(/\.mobile-primary-action-bar\s*\{[^}]*display:\s*none/);
  expect(mobile).toMatch(/\.mobile-primary-action-bar\s*\{[^}]*display:\s*flex/);
  expect(mobile).toContain("position: fixed");
  expect(mobile).toMatch(/\.desktop-primary-action\s*\{[^}]*display:\s*none/);
  expect(mobile).toContain("padding-bottom: calc(5.5rem + env(safe-area-inset-bottom))");
  expect(mobile).toContain("env(safe-area-inset-bottom)");
  expect(mobile).toMatch(/\.workbench-icon-button,\s*\.workbench-delete\s*\{[^}]*width:\s*44px[^}]*height:\s*44px/);
  expect(mobile).toContain("min-width: 44px");
  expect(mobile).toContain("min-height: 44px");
  expect(mobile).toContain(".mobile-accordion > summary");
  expect(reduced).toContain("animation-duration: 0.01ms !important");
  expect(reduced).toContain("scroll-behavior: auto !important");
});
