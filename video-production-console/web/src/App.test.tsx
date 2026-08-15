// @vitest-environment jsdom

import { QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render as testingRender, screen, waitFor, within } from "@testing-library/react";
import type { ReactElement } from "react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import App from "./App";
import appSource from "./App.tsx?raw";
import taskDialogSource from "./tasks/TaskDetailDialog.tsx?raw";
import { parseLocation } from "./project-workbench/routes";
import { createAppQueryClient } from "./query/client";

function render(ui: ReactElement) {
  return testingRender(
    <QueryClientProvider client={createAppQueryClient()}>{ui}</QueryClientProvider>,
  );
}

beforeEach(() => { window.history.replaceState({}, "", "/projects"); });

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  window.localStorage.removeItem("video-production-console-theme");
  delete document.documentElement.dataset.theme;
  window.history.replaceState({}, "", "/projects");
});

const routedProjectID = "814ebfde-7470-418a-a703-a33596f7e8fe";
const routedImageProjectID = "659340f8-31c0-49d8-a92d-a250f5a23c48";

test("keeps the global modal and notification layers above the mobile action bar", () => {
  const appCss = readFileSync(resolve(process.cwd(), "src/App.css"), "utf8");
  const workbenchCss = readFileSync(resolve(process.cwd(), "src/project-workbench/project-workbench.css"), "utf8");
  const layer = (name: string) => Number(appCss.match(new RegExp(`${name}:\\s*(\\d+)`))?.[1]);
  const action = layer("--layer-workbench-action");
  const notice = layer("--layer-notice");
  const modal = layer("--layer-modal-backdrop");
  const dialog = layer("--layer-modal-dialog");
  const toast = layer("--layer-toast");

  expect(action).toBeGreaterThan(0);
  expect(notice).toBeGreaterThan(action);
  expect(modal).toBeGreaterThan(notice);
  expect(dialog).toBeGreaterThan(modal);
  expect(toast).toBeGreaterThan(dialog);
  expect(workbenchCss).toMatch(/\.mobile-primary-action-bar\s*\{[^}]*z-index:\s*var\(--layer-workbench-action\)/s);
  expect(appCss).toMatch(/\.modal-backdrop\s*\{[^}]*z-index:\s*var\(--layer-modal-backdrop\)/s);
  expect(appCss).toMatch(/\.modal-backdrop\s*>\s*\[role="dialog"\]\s*\{[^}]*z-index:\s*var\(--layer-modal-dialog\)/s);
  const dialogSources = [
    "src/App.tsx",
    "src/assets/AssetPreviewDialog.tsx",
    "src/assets/ReviseDialog.tsx",
    "src/settings/SettingsPanel.tsx",
    "src/tasks/TaskDetailDialog.tsx",
  ].map((file) => readFileSync(resolve(process.cwd(), file), "utf8"));
  const dialogRoles = dialogSources.reduce(
    (total, source) => total + (source.match(/role="dialog"/g)?.length || 0),
    0,
  );
  expect(dialogRoles).toBeGreaterThanOrEqual(5);
  for (const source of dialogSources) {
    const backdrops = source.match(/className="modal-backdrop"/g)?.length || 0;
    const roles = source.match(/role="dialog"/g)?.length || 0;
    expect(roles).toBeGreaterThanOrEqual(backdrops);
  }
});

test("project location parsing accepts UUID detail paths and rejects invalid paths", () => {
  expect(parseLocation("/")).toEqual({ view: "mode-home" });
  expect(parseLocation("/projects")).toEqual({ view: "projects" });
  expect(parseLocation(`/projects/${routedProjectID}`)).toEqual({
    view: "project",
    projectID: routedProjectID,
  });
  expect(parseLocation("/projects/project-1")).toEqual({ view: "not-found" });
  expect(parseLocation("/projects/not-a-uuid/more")).toEqual({ view: "not-found" });
});

test("an invalid direct path preserves URL", async () => {
  window.history.replaceState({}, "", "/projects/not-a-project");
  vi.stubGlobal(
    "fetch",
    baseFetch((path) => {
      if (path === "/api/projects") return json([]);
    }),
  );

  render(<App />);

  const alert = await screen.findByRole("alert");
  expect(within(alert).getByRole("heading", { name: "404", level: 1 })).toBeTruthy();
  expect(within(alert).getByRole("button", { name: "返回制作方式" })).toBeTruthy();
  await waitFor(() => expect(window.location.pathname).toBe("/projects/not-a-project"));
});

test("root chooser navigates to image mode and montage entry navigates to projects", async () => {
  window.history.replaceState({}, "", "/");
  vi.stubGlobal("fetch", baseFetch((path) => path === "/api/projects" ? json([]) : path === "/api/image-projects" ? json([]) : undefined));
  render(<App />);
  expect(await screen.findByRole("heading", { name: "选择制作方式", level: 1 })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "进入图文制作" }));
  expect(window.location.pathname).toBe("/image-projects");
  expect(await screen.findByRole("heading", { name: "图文项目", level: 1 })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "制作方式" }));
  fireEvent.click(await screen.findByRole("button", { name: "进入混剪制作" }));
  expect(window.location.pathname).toBe("/projects");
});

test("the root chooser does not load workflow-specific data", async () => {
  const requests: string[] = [];
  window.history.replaceState({}, "", "/");
  vi.stubGlobal("fetch", baseFetch((path) => {
    requests.push(path);
    if (path === "/api/projects" || path === "/api/image-projects") return json([]);
  }));

  render(<App />);

  expect(await screen.findByRole("heading", { name: "选择制作方式", level: 1 })).toBeTruthy();
  expect(requests).toEqual(["/api/auth/me"]);
});

test("the image project route does not load montage accounts, projects, or runtime", async () => {
  const requests: string[] = [];
  window.history.replaceState({}, "", "/image-projects");
  vi.stubGlobal("fetch", baseFetch((path) => {
    requests.push(path);
    if (path === "/api/projects" || path === "/api/image-projects") return json([]);
  }));

  render(<App />);

  expect(await screen.findByRole("heading", { name: "图文项目", level: 1 })).toBeTruthy();
  expect(requests).not.toContain("/api/accounts");
  expect(requests).not.toContain("/api/projects");
  expect(requests).not.toContain("/api/runtime");
  expect(requests).toContain("/api/settings");
  expect(requests).toContain("/api/image-projects");
});

test("popstate returns to root chooser", async () => {
  window.history.replaceState({}, "", "/image-projects");
  vi.stubGlobal("fetch", baseFetch((path) => path === "/api/image-projects" ? json([]) : path === "/api/projects" ? json([]) : undefined));
  render(<App />);
  await screen.findByRole("heading", { name: "图文项目", level: 1 });
  window.history.pushState({}, "", "/"); window.dispatchEvent(new PopStateEvent("popstate"));
  expect(await screen.findByRole("heading", { name: "选择制作方式", level: 1 })).toBeTruthy();
});

test("a direct image project path restores detail and popstate returns to the image list", async () => {
  const project = {
    id: routedImageProjectID,
    title: "养老现金流图文",
    script: "先看现金流。",
    image_count: 1,
    ratio: "3:4",
    style: "finance_documentary",
    custom_style: "",
    concurrency: 1,
    status: "draft",
    created_at: "2026-08-14T00:00:00Z",
    updated_at: "2026-08-14T00:00:00Z",
  };
  window.history.replaceState({}, "", `/image-projects/${routedImageProjectID}`);
  vi.stubGlobal("fetch", baseFetch((path) => {
    if (path === "/api/projects") return json([]);
    if (path === "/api/image-projects") return json([project]);
    if (path === `/api/image-projects/${routedImageProjectID}`) {
      return json({ project, items: [], publishing_candidates: [] });
    }
  }));

  render(<App />);

  expect(await screen.findByRole("heading", { name: project.title, level: 1 })).toBeTruthy();
  window.history.pushState({}, "", "/image-projects");
  window.dispatchEvent(new PopStateEvent("popstate"));
  expect(await screen.findByRole("heading", { name: "图文项目", level: 1 })).toBeTruthy();
});

test("a missing direct image project keeps its image-specific 404 state", async () => {
  window.history.replaceState({}, "", `/image-projects/${routedImageProjectID}`);
  vi.stubGlobal("fetch", baseFetch((path) => {
    if (path === "/api/projects") return json([]);
    if (path === "/api/image-projects") return json([]);
    if (path === `/api/image-projects/${routedImageProjectID}`) return json({}, 404);
  }));

  render(<App />);

  expect(await screen.findByText("图文项目不存在（404）")).toBeTruthy();
  expect(window.location.pathname).toBe(`/image-projects/${routedImageProjectID}`);
});

test("the advanced image route stays on the image workbench instead of 404", async () => {
  window.history.replaceState({}, "", "/image-projects/advanced");
  vi.stubGlobal("fetch", baseFetch((path) => path === "/api/image-projects" ? json([]) : path === "/api/projects" ? json([]) : undefined));
  render(<App />);
  expect(await screen.findByRole("heading", { name: "图文项目", level: 1 })).toBeTruthy();
  expect(screen.queryByRole("heading", { name: "404", level: 1 })).toBeNull();
  expect(window.location.pathname).toBe("/image-projects/advanced");
});

test("keeps the add-account action directly inside the account navigation", async () => {
  vi.stubGlobal(
    "fetch",
    baseFetch((path) => {
      if (path === "/api/accounts")
        return json([
          { id: "account-1", name: "天中观局" },
          { id: "account-2", name: "居中观" },
          { id: "account-3", name: "认知漫步" },
          { id: "account-4", name: "观局思考" },
        ]);
      if (path === "/api/projects") return json([]);
    }),
  );

  render(<App />);

  const navigation = await screen.findByRole("navigation", { name: /账号/ });
  const addAccount = within(navigation).getByRole("button", { name: "新增账号" });
  expect(addAccount.previousElementSibling?.classList.contains("account-list")).toBe(true);

  fireEvent.click(addAccount);
  expect(within(navigation).getByLabelText("账号名称")).toBeTruthy();
  expect(addAccount.getAttribute("aria-expanded")).toBe("true");
});

function routedProjectFetch(taskID = "", topicReady = false) {
  const project = {
    id: routedProjectID,
    account_id: "account-1",
    title: "可恢复的视频项目",
    stage: "script",
  };
  return {
    project,
    fetch: baseFetch((path) => {
      if (path === "/api/projects") return json([project]);
      if (path === `/api/projects/${routedProjectID}`)
        return json({ project, assets: {}, missing_assets: [], active_workflow: null, topic_context: topicReady ? {} : null });
      if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
      if (path === `/api/projects/${routedProjectID}/remix`) return json({ id: "workflow-1" }, 201);
      if (taskID && path === `/api/tasks/${taskID}`)
        return json({
          id: taskID,
          project_id: routedProjectID,
          type: "remix",
          skill_name: "finance-viral-remix",
          status: "completed",
          created_at: "2026-08-08T00:00:00Z",
        });
      if (taskID && path === `/api/tasks/${taskID}/semantic-events?limit=20`)
        return json({ events: [] });
      if (taskID && path === `/api/tasks/${taskID}/result`) return json({});
    }),
  };
}

test("clicking a project pushes a durable project path", async () => {
  const fixture = routedProjectFetch();
  vi.stubGlobal("fetch", fixture.fetch);
  const pushState = vi.spyOn(window.history, "pushState");

  render(<App />);
  fireEvent.click(await screen.findByText(fixture.project.title));

  expect(pushState).toHaveBeenCalledWith({}, "", `/projects/${routedProjectID}`);
  expect(window.location.pathname).toBe(`/projects/${routedProjectID}`);
  expect(await screen.findByRole("button", { name: "返回项目看板" })).toBeTruthy();
});

test("production pages return to the chooser without a binary mode switch", async () => {
  vi.stubGlobal("fetch", baseFetch((path) => {
    if (path === "/api/projects") return json([]);
    if (path === "/api/image-projects") return json([]);
  }));

  render(<App />);
  expect(await screen.findByRole("heading", { name: "视频项目" })).toBeTruthy();
  expect(screen.queryByRole("group", { name: "生产模式" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "制作方式" }));
  fireEvent.click(await screen.findByRole("button", { name: "进入图文制作" }));
  expect(await screen.findByRole("heading", { name: "图文项目", level: 1 })).toBeTruthy();
  expect(screen.queryByRole("group", { name: "生产模式" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "制作方式" }));
  expect(await screen.findByRole("heading", { name: "选择制作方式", level: 1 })).toBeTruthy();
});

test("the standalone Codex conversation entry is not exposed", async () => {
  const fixture = routedProjectFetch();
  vi.stubGlobal("fetch", fixture.fetch);

  render(<App />);

  expect(await screen.findByRole("heading", { name: "视频项目" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Codex 对话" })).toBeNull();
});

test("a direct project path restores the same project", async () => {
  window.history.replaceState({}, "", `/projects/${routedProjectID}`);
  const fixture = routedProjectFetch();
  vi.stubGlobal("fetch", fixture.fetch);

  render(<App />);

  // The loading state already renders the title, so wait on the production rail:
  // that only mounts once the project detail and its task list have both landed.
  expect(await screen.findByRole("navigation", { name: "五阶段生产轨" })).toBeTruthy();
  expect(screen.getByRole("heading", { name: fixture.project.title })).toBeTruthy();
  expect(screen.getByRole("button", { name: "返回项目看板" })).toBeTruthy();
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.getByRole("button", { name: "先粘贴同行原文" })).toBeTruthy();
  expect(screen.getByRole("region", { name: "当前项目资产" })).toBeTruthy();
  expect(screen.queryByRole("region", { name: "Codex 对话摘要" })).toBeNull();
  for (const phrase of ["给我选题", "深化一下", "生成选题卡", "口播稿", "remix.spoken_format", "待发布"]) {
    expect(screen.queryByText(phrase, { exact: false })).toBeNull();
  }
});

test("the project workbench starts remix.standard from a saved source script", async () => {
  const taskBodies: Record<string, unknown>[] = [];
  const project = {
    id: routedProjectID,
    account_id: "account-1",
    title: "可恢复的视频项目",
    stage: "script",
  };
  window.history.replaceState({}, "", `/projects/${routedProjectID}`);
  vi.stubGlobal(
    "fetch",
    baseFetch((path, method, init) => {
      if (path === "/api/projects") return json([project]);
      if (path === `/api/projects/${routedProjectID}`)
        return json({
          project,
          assets: { source_script: sourceScriptAsset() },
          missing_assets: [],
        });
      if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
      if (path === "/api/assets/source-1/content") return new Response("同行原文正文", { status: 200 });
      if (path === `/api/projects/${routedProjectID}/tasks` && method === "POST") {
        taskBodies.push(JSON.parse(String(init?.body)));
        return json({ id: "task-remix-1" }, 201);
      }
    }),
  );
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "查看或替换同行原文" }));
  await waitFor(() =>
    expect((screen.getByLabelText("同行原文") as HTMLTextAreaElement).value).toBe("同行原文正文"),
  );
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  fireEvent.click(await screen.findByRole("button", { name: "开始正式二创" }));

  await waitFor(() => expect(taskBodies).toHaveLength(1));
  expect(taskBodies[0]).toMatchObject({
    action: "remix.standard",
    source_version_id: "source-1",
    remix_prompt_style: "rewrite",
  });
});

test("the project workbench starts remix.standard with the wash prompt style", async () => {
  const taskBodies: Record<string, unknown>[] = [];
  const project = {
    id: routedProjectID,
    account_id: "account-1",
    title: "可恢复的视频项目",
    stage: "script",
  };
  window.history.replaceState({}, "", `/projects/${routedProjectID}`);
  vi.stubGlobal(
    "fetch",
    baseFetch((path, method, init) => {
      if (path === "/api/projects") return json([project]);
      if (path === `/api/projects/${routedProjectID}`)
        return json({
          project,
          assets: { source_script: sourceScriptAsset() },
          missing_assets: [],
        });
      if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
      if (path === "/api/assets/source-1/content") return new Response("同行原文正文", { status: 200 });
      if (path === `/api/projects/${routedProjectID}/tasks` && method === "POST") {
        taskBodies.push(JSON.parse(String(init?.body)));
        return json({ id: "task-remix-1" }, 201);
      }
    }),
  );
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "查看或替换同行原文" }));
  await waitFor(() =>
    expect((screen.getByLabelText("同行原文") as HTMLTextAreaElement).value).toBe("同行原文正文"),
  );
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  fireEvent.click(screen.getByRole("radio", { name: /洗稿/ }));
  fireEvent.click(await screen.findByRole("button", { name: "开始正式二创" }));

  await waitFor(() => expect(taskBodies).toHaveLength(1));
  expect(taskBodies[0]).toMatchObject({
    action: "remix.standard",
    source_version_id: "source-1",
    remix_prompt_style: "wash",
  });
});

test("the project workbench starts mixing through the formal montage task API", async () => {
  const project = { id: routedProjectID, account_id: "account-1", title: "混剪项目", stage: "mixing" };
  const requests: Array<{ path: string; body?: Record<string, unknown> }> = [];
  window.history.replaceState({}, "", `/projects/${routedProjectID}`);
  vi.stubGlobal("fetch", baseFetch((path, method, init) => {
    if (path === "/api/projects") return json([project]);
    if (path === `/api/projects/${routedProjectID}`) return json({
      project,
      assets: {
        continuous_script: testAsset("continuous_script"),
        narration: testAsset("narration"),
        subtitle_srt: testAsset("subtitle_srt"),
      },
      background_reference: testAsset("account_background"),
      missing_assets: [],
    });
    if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
    if (path === `/api/projects/${routedProjectID}/tasks` && method === "POST") {
      requests.push({ path, body: JSON.parse(String(init?.body)) });
      return json({ id: "montage-task" }, 202);
    }
  }));
  render(<App />);

  fireEvent.click(await screen.findByRole("button", { name: "开始风景混剪" }));

  await waitFor(() => expect(requests).toHaveLength(1));
  expect(requests[0].body).toMatchObject({ type: "montage" });
  expect(JSON.stringify(requests[0].body)).not.toContain("spoken");
});

test("the project workbench starts movie mixing through the movie montage task API", async () => {
  const project = { id: routedProjectID, account_id: "account-1", title: "电影混剪项目", stage: "mixing" };
  const requests: Array<{ path: string; body?: Record<string, unknown> }> = [];
  window.history.replaceState({}, "", `/projects/${routedProjectID}`);
  vi.stubGlobal("fetch", baseFetch((path, method, init) => {
    if (path === "/api/projects") return json([project]);
    if (path === `/api/projects/${routedProjectID}`) return json({
      project,
      assets: {
        continuous_script: testAsset("continuous_script"),
        narration: testAsset("narration"),
        subtitle_srt: testAsset("subtitle_srt"),
      },
      background_reference: testAsset("account_background"),
      missing_assets: [],
    });
    if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
    if (path === `/api/projects/${routedProjectID}/tasks` && method === "POST") {
      requests.push({ path, body: JSON.parse(String(init?.body)) });
      return json({ id: "movie-montage-task" }, 202);
    }
  }));
  render(<App />);

  fireEvent.click((await screen.findAllByRole("button", { name: "开始电影混剪" }))[0]);

  await waitFor(() => expect(requests).toHaveLength(1));
  expect(requests[0].body).toMatchObject({ type: "movie_montage" });
});

test("the assets stage generates narration and subtitles through the project narration endpoint", async () => {
  const project = { id: routedProjectID, account_id: "account-1", title: "配音项目", stage: "assets" };
  const requests: Array<{ path: string; method: string; body: unknown }> = [];
  window.history.replaceState({}, "", `/projects/${routedProjectID}`);
  vi.stubGlobal("fetch", baseFetch((path, method, init) => {
    if (path === "/api/projects") return json([project]);
    if (path === `/api/projects/${routedProjectID}`) return json({
      project,
      assets: { continuous_script: testAsset("continuous_script") },
      background_reference: testAsset("account_background"),
      missing_assets: ["narration", "subtitle_srt"],
    });
    if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
    if (path === `/api/projects/${routedProjectID}/narration` && method === "POST") {
      requests.push({ path, method, body: init?.body });
      return json({
        narration: testAsset("narration"),
        subtitle_srt: testAsset("subtitle_srt"),
        captions: 12,
        duration_seconds: 43.216,
        billed_characters: 220,
        warnings: ["caption 3 reads at 9.8 units/s"],
      }, 201);
    }
  }));
  render(<App />);

  fireEvent.click(await screen.findByRole("button", { name: "生成配音与字幕" }));

  await waitFor(() => expect(requests).toHaveLength(1));
  expect(requests[0]).toMatchObject({ path: `/api/projects/${routedProjectID}/narration`, method: "POST" });
  expect(requests[0].body).toBeUndefined();
  expect(await screen.findByText("配音与字幕已生成，但有 1 条提醒，建议打开字幕确认。")).toBeTruthy();
});

test("the published action posts the project publish endpoint", async () => {
  const project = { id: routedProjectID, account_id: "account-1", title: "待确认项目", stage: "review" };
  const requests: string[] = [];
  window.history.replaceState({}, "", `/projects/${routedProjectID}`);
  vi.stubGlobal("fetch", baseFetch((path, method) => {
    if (path === "/api/projects") return json([project]);
    if (path === `/api/projects/${routedProjectID}`) return json({
      project,
      assets: { mix_draft: testAsset("mix_draft") },
      missing_assets: [],
    });
    if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
    if (path === `/api/projects/${routedProjectID}/publish` && method === "POST") {
      requests.push(path);
      return json({ ...project, stage: "published" });
    }
  }));
  render(<App />);

  fireEvent.click(await screen.findByRole("button", { name: "将当前项目标记为已发布" }));

  await waitFor(() => expect(requests).toEqual([`/api/projects/${routedProjectID}/publish`]));
});

function testAsset(type: string) {
  return {
    id: `${type}-asset`, type, filename: `${type}.dat`, mime_type: "application/octet-stream",
    size: 12, version: 1, state: "ready", created_at: "2026-08-08T00:00:00Z",
  };
}

function sourceScriptAsset(id = "source-1") {
  return {
    id,
    type: "source_script",
    filename: "source-script.txt",
    mime_type: "text/plain",
    size: 12,
    version: 1,
    state: "ready",
    created_at: "2026-08-08T00:00:00Z",
  };
}

function deferredResponse() {
  let resolve!: (response: Response) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<Response>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

test("locks a pending remix against double click and unlocks after completion", async () => {
  const project = { id: routedProjectID, account_id: "account-1", title: "请求锁项目", stage: "script" };
  const pending = deferredResponse();
  let remixRequests = 0;
  window.history.replaceState({}, "", `/projects/${routedProjectID}`);
  vi.stubGlobal("fetch", baseFetch((path, method) => {
    if (path === "/api/projects") return json([project]);
    if (path === `/api/projects/${routedProjectID}`)
      return json({ project, assets: { source_script: sourceScriptAsset() }, missing_assets: [] });
    if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
    if (path === "/api/assets/source-1/content") return new Response("同行原文正文", { status: 200 });
    if (path === `/api/projects/${routedProjectID}/tasks` && method === "POST") {
      remixRequests += 1;
      return pending.promise;
    }
  }));
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "查看或替换同行原文" }));
  await waitFor(() =>
    expect((screen.getByLabelText("同行原文") as HTMLTextAreaElement).value).toBe("同行原文正文"),
  );
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  const action = await screen.findByRole<HTMLButtonElement>("button", { name: "开始正式二创" });

  fireEvent.click(action);
  fireEvent.click(action);

  await waitFor(() => expect(remixRequests).toBe(1));
  expect(action.disabled).toBe(true);
  pending.resolve(json({ id: "task-1" }, 201));
  await waitFor(() => expect(action.disabled).toBe(false));
});

test("serializes every mutation for one project while allowing another project to proceed", async () => {
  const projectA = { id: routedProjectID, account_id: "account-1", title: "项目 A", stage: "script" };
  const projectB = { id: "94a1ddc8-7972-464e-b485-a849c7886be3", account_id: "account-1", title: "项目 B", stage: "script" };
  const remixA = deferredResponse();
  const remixB = deferredResponse();
  const mutations: string[] = [];
  window.history.replaceState({}, "", `/projects/${projectA.id}`);
  vi.stubGlobal("confirm", vi.fn(() => true));
  vi.stubGlobal("fetch", baseFetch((path, method) => {
    if (path === "/api/projects") return json([projectA, projectB]);
    if (path === `/api/projects/${projectA.id}`)
      return json({ project: projectA, assets: { source_script: sourceScriptAsset("source-a") }, missing_assets: [] });
    if (path === `/api/projects/${projectB.id}`)
      return json({ project: projectB, assets: { source_script: sourceScriptAsset("source-b") }, missing_assets: [] });
    if (path === `/api/tasks?project_id=${projectA.id}` || path === `/api/tasks?project_id=${projectB.id}`) return json([]);
    if (path === "/api/assets/source-a/content" || path === "/api/assets/source-b/content")
      return new Response("同行原文正文", { status: 200 });
    if (path === `/api/projects/${projectA.id}/tasks` && method === "POST") {
      mutations.push("remix-a");
      return remixA.promise;
    }
    if (path === `/api/projects/${projectB.id}/tasks` && method === "POST") {
      mutations.push("remix-b");
      return remixB.promise;
    }
    if (path === `/api/projects/${projectA.id}` && method === "DELETE") {
      mutations.push("delete-a");
      return new Response(null, { status: 204 });
    }
  }));

  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "查看或替换同行原文" }));
  await waitFor(() =>
    expect((screen.getByLabelText("同行原文") as HTMLTextAreaElement).value).toBe("同行原文正文"),
  );
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  fireEvent.click(await screen.findByRole("button", { name: "开始正式二创" }));
  await waitFor(() => expect(mutations).toEqual(["remix-a"]));

  const deleteButton = screen.getByRole<HTMLButtonElement>("button", { name: "删除当前项目" });
  expect(deleteButton.disabled).toBe(true);
  for (const label of ["上传配音", "上传SRT 字幕", "上传账号背景图"]) {
    expect(screen.getByLabelText<HTMLInputElement>(label).disabled).toBe(true);
  }
  fireEvent.click(deleteButton);
  expect(mutations).toEqual(["remix-a"]);

  fireEvent.click(screen.getByRole("button", { name: "返回项目看板" }));
  fireEvent.click(await screen.findByText("项目 B"));
  fireEvent.click(await screen.findByRole("button", { name: "查看或替换同行原文" }));
  await waitFor(() =>
    expect((screen.getByLabelText("同行原文") as HTMLTextAreaElement).value).toBe("同行原文正文"),
  );
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  fireEvent.click(await screen.findByRole("button", { name: "开始正式二创" }));
  await waitFor(() => expect(mutations).toEqual(["remix-a", "remix-b"]));

  remixA.resolve(json({ id: "task-a" }, 201));
  remixB.resolve(json({ id: "task-b" }, 201));
});

test("turns a rejected remix request into an actionable error and allows retry", async () => {
  const project = { id: routedProjectID, account_id: "account-1", title: "错误恢复项目", stage: "script" };
  let attempts = 0;
  window.history.replaceState({}, "", `/projects/${routedProjectID}`);
  vi.stubGlobal("fetch", baseFetch((path, method) => {
    if (path === "/api/projects") return json([project]);
    if (path === `/api/projects/${routedProjectID}`)
      return json({ project, assets: { source_script: sourceScriptAsset() }, missing_assets: [] });
    if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
    if (path === "/api/assets/source-1/content") return new Response("同行原文正文", { status: 200 });
    if (path === `/api/projects/${routedProjectID}/tasks` && method === "POST") {
      attempts += 1;
      return attempts === 1
        ? Promise.reject(new Error("network down"))
        : json({ id: "task-retry" }, 201);
    }
  }));
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "查看或替换同行原文" }));
  await waitFor(() =>
    expect((screen.getByLabelText("同行原文") as HTMLTextAreaElement).value).toBe("同行原文正文"),
  );
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  const action = await screen.findByRole<HTMLButtonElement>("button", { name: "开始正式二创" });

  fireEvent.click(action);

  expect(await screen.findByText("原文保存或二创任务启动失败，请检查网络连接后重试。")).toBeTruthy();
  await waitFor(() => expect(action.disabled).toBe(false));
  fireEvent.click(action);
  await waitFor(() => expect(attempts).toBe(2));
});

test("does not let a completed project A publish request abort or replace project B", async () => {
  const projectA = { id: routedProjectID, account_id: "account-1", title: "项目 A", stage: "review" };
  const projectB = { id: "94a1ddc8-7972-464e-b485-a849c7886be3", account_id: "account-1", title: "项目 B", stage: "script" };
  const publish = deferredResponse();
  const detailB = deferredResponse();
  let detailBSignal: AbortSignal | undefined;
  window.history.replaceState({}, "", `/projects/${projectA.id}`);
  vi.stubGlobal("fetch", baseFetch((path, method, init) => {
    if (path === "/api/projects") return json([projectA, projectB]);
    if (path === `/api/projects/${projectA.id}`) return json({
      project: projectA,
      assets: { mix_draft: testAsset("mix_draft") },
      missing_assets: [],
    });
    if (path === `/api/projects/${projectB.id}`) {
      detailBSignal = init?.signal as AbortSignal;
      return detailB.promise;
    }
    if (path === `/api/tasks?project_id=${projectA.id}` || path === `/api/tasks?project_id=${projectB.id}`) return json([]);
    if (path === `/api/projects/${projectA.id}/publish` && method === "POST") return publish.promise;
  }));
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "将当前项目标记为已发布" }));
  fireEvent.click(screen.getByRole("button", { name: "返回项目看板" }));
  fireEvent.click(await screen.findByText("项目 B"));
  expect(await screen.findByRole("heading", { name: "项目 B" })).toBeTruthy();

  publish.resolve(json({ ...projectA, stage: "published" }));
  await waitFor(() => expect(detailBSignal).toBeTruthy());
  expect(detailBSignal?.aborted).toBe(false);
  detailB.resolve(json({ project: projectB, assets: {}, missing_assets: [] }));

  expect(await screen.findByRole("heading", { name: "项目 B" })).toBeTruthy();
  expect(await screen.findByRole("region", { name: "当前项目资产" })).toBeTruthy();
  expect(window.location.pathname).toBe(`/projects/${projectB.id}`);
});

test("contains no legacy project drawer or bypass production controls in App source", () => {
  const shellSources = [appSource, taskDialogSource];
  for (const source of shellSources) {
    for (const forbidden of ["renderLegacyProjectDrawer", "remix.spoken_format", "口播稿", "topic_deepen", "spoken_format", "给我选题", "IdeaPlannerDialog"]) {
      expect(source).not.toContain(forbidden);
    }
    expect(source).not.toContain("×");
    expect(source).not.toContain("●");
    for (const mojibake of ["锟", "�", "Ã", "鈥"]) expect(source).not.toContain(mojibake);
  }
  expect(appSource).not.toContain("{selected && (\n        <div\n          className=\"drawer-backdrop\"");
  expect(taskDialogSource).toContain('<details className="registered-directory-technical">');
  expect(taskDialogSource).toContain('<summary>路径与文件清单</summary>');
});

test.each([
  ["narration", "上传配音"],
  ["subtitle_srt", "上传SRT 字幕"],
] as const)("uploads %s to its formal project endpoint and refreshes detail", async (type, label) => {
  const project = { id: routedProjectID, account_id: "account-upload", title: "上传素材项目", stage: "assets" };
  const uploads: Array<{ path: string; body: FormData }> = [];
  let detailReads = 0;
  window.history.replaceState({}, "", `/projects/${routedProjectID}`);
  vi.stubGlobal("fetch", baseFetch((path, method, init) => {
    if (path === "/api/projects") return json([project]);
    if (path === `/api/projects/${routedProjectID}` && method === "GET") {
      detailReads += 1;
      return json({
        project,
        assets: { continuous_script: testAsset("continuous_script") },
        background_reference: testAsset("account_background"),
        missing_assets: [type],
      });
    }
    if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
    if (path === `/api/projects/${routedProjectID}/assets/${type}` && method === "POST") {
      uploads.push({ path, body: init?.body as FormData });
      return json(testAsset(type), 201);
    }
  }));
  render(<App />);
  const file = type === "narration"
    ? new File([type], "voice.mp3", { type: "audio/mpeg" })
    : new File([type], "subs.srt", { type: "application/x-subrip" });

  fireEvent.change(await screen.findByLabelText(label), { target: { files: [file] } });

  await waitFor(() => expect(uploads).toHaveLength(1));
  expect(uploads[0].path).toBe(`/api/projects/${routedProjectID}/assets/${type}`);
  expect(uploads[0].body).toBeInstanceOf(FormData);
  expect(uploads[0].body.get("file")).toBe(file);
  await waitFor(() => expect(detailReads).toBeGreaterThan(1));
});

test("replaces the selected project's account background through the existing account endpoint and refreshes detail", async () => {
  const project = { id: routedProjectID, account_id: "account-from-project", title: "背景素材项目", stage: "assets" };
  const uploads: Array<{ path: string; body: FormData }> = [];
  let detailReads = 0;
  window.history.replaceState({}, "", `/projects/${routedProjectID}`);
  vi.stubGlobal("fetch", baseFetch((path, method, init) => {
    if (path === "/api/projects") return json([project]);
    if (path === `/api/projects/${routedProjectID}` && method === "GET") {
      detailReads += 1;
      return json({
        project,
        assets: { continuous_script: testAsset("continuous_script") },
        background_reference: { ...testAsset("account_background"), mime_type: "image/png" },
        missing_assets: [],
      });
    }
    if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
    if (path === "/api/accounts/account-from-project/background" && method === "POST") {
      uploads.push({ path, body: init?.body as FormData });
      return json(testAsset("account_background"), 201);
    }
  }));
  render(<App />);
  const file = new File(["png"], "background.png", { type: "image/png" });

  fireEvent.change(await screen.findByLabelText("替换账号背景图"), { target: { files: [file] } });

  await waitFor(() => expect(uploads).toHaveLength(1));
  expect(uploads[0].path).toBe("/api/accounts/account-from-project/background");
  expect(uploads[0].body.get("background")).toBe(file);
  await waitFor(() => expect(detailReads).toBeGreaterThan(1));
});

test("saves a source script before starting remix.standard with its version id", async () => {
  const project = { id: routedProjectID, account_id: "account-source", title: "同行原文项目", stage: "script" };
  const calls: Array<{ path: string; body?: RequestInit["body"] }> = [];
  let detailReads = 0;
  window.history.replaceState({}, "", `/projects/${routedProjectID}`);
  vi.stubGlobal("fetch", baseFetch((path, method, init) => {
    if (path === "/api/projects") return json([project]);
    if (path === `/api/projects/${routedProjectID}`) {
      detailReads += 1;
      return json({ project, assets: {}, missing_assets: [] });
    }
    if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
    if (path === `/api/projects/${routedProjectID}/assets/source_script` && method === "POST") {
      calls.push({ path, body: init?.body });
      return json({ ...testAsset("source_script"), id: "source-version-1" }, 201);
    }
    if (path === `/api/projects/${routedProjectID}/tasks` && method === "POST") {
      calls.push({ path, body: init?.body });
      return json({ id: "remix-task" }, 201);
    }
  }));
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "粘贴同行原文" }));
  fireEvent.change(await screen.findByLabelText("同行原文"), { target: { value: "同行原文正文" } });
  fireEvent.click(screen.getByRole("button", { name: "保存原文并开始二创" }));

  await waitFor(() => expect(calls).toHaveLength(2));
  expect(calls[0].path).toContain("/assets/source_script");
  expect(calls[0].body).toBeInstanceOf(FormData);
  expect((calls[0].body as FormData).get("file")).toBeInstanceOf(File);
  expect(JSON.parse(String(calls[1].body))).toMatchObject({
    type: "remix",
    action: "remix.standard",
    source_version_id: "source-version-1",
    remix_prompt_style: "rewrite",
  });
  await waitFor(() => expect(detailReads).toBeGreaterThan(1));
});

test("locks source save and remix against double clicks", async () => {
  const project = { id: routedProjectID, account_id: "account-source", title: "同行原文锁", stage: "script" };
  const upload = deferredResponse();
  let uploads = 0;
  let tasks = 0;
  window.history.replaceState({}, "", `/projects/${routedProjectID}`);
  vi.stubGlobal("fetch", baseFetch((path, method) => {
    if (path === "/api/projects") return json([project]);
    if (path === `/api/projects/${routedProjectID}`) return json({ project, assets: {}, missing_assets: [] });
    if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
    if (path === `/api/projects/${routedProjectID}/assets/source_script` && method === "POST") {
      uploads += 1;
      return upload.promise;
    }
    if (path === `/api/projects/${routedProjectID}/tasks` && method === "POST") {
      tasks += 1;
      return json({ id: "remix-task" }, 201);
    }
  }));
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "粘贴同行原文" }));
  fireEvent.change(await screen.findByLabelText("同行原文"), { target: { value: "正文" } });
  const save = screen.getByRole("button", { name: "保存原文并开始二创" });
  fireEvent.click(save);
  fireEvent.click(save);
  await waitFor(() => expect(uploads).toBe(1));
  upload.resolve(json({ ...testAsset("source_script"), id: "source-version-1" }, 201));
  await waitFor(() => expect(tasks).toBe(1));
});

test("does not create a remix task when source script upload fails", async () => {
  const project = { id: routedProjectID, account_id: "account-source", title: "失败同行原文", stage: "script" };
  let tasks = 0;
  window.history.replaceState({}, "", `/projects/${routedProjectID}`);
  vi.stubGlobal("fetch", baseFetch((path, method) => {
    if (path === "/api/projects") return json([project]);
    if (path === `/api/projects/${routedProjectID}`) return json({ project, assets: {}, missing_assets: [] });
    if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
    if (path === `/api/projects/${routedProjectID}/assets/source_script` && method === "POST")
      return json({ error: "upload failed" }, 500);
    if (path === `/api/projects/${routedProjectID}/tasks` && method === "POST") {
      tasks += 1;
      return json({});
    }
  }));
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "粘贴同行原文" }));
  fireEvent.change(await screen.findByLabelText("同行原文"), { target: { value: "正文" } });
  fireEvent.click(screen.getByRole("button", { name: "保存原文并开始二创" }));

  await screen.findByText("同行原文保存失败，请稍后重试。");
  expect(tasks).toBe(0);
});

test("retries only remix after a saved matching source script task failure", async () => {
  const project = { id: routedProjectID, account_id: "account-source", title: "重试同行原文", stage: "script" };
  let detailReads = 0;
  let uploads = 0;
  let taskAttempts = 0;
  window.history.replaceState({}, "", `/projects/${routedProjectID}`);
  vi.stubGlobal("fetch", baseFetch((path, method) => {
    if (path === "/api/projects") return json([project]);
    if (path === `/api/projects/${routedProjectID}`) {
      detailReads += 1;
      return json({
        project,
        assets: detailReads > 1 ? { source_script: { ...testAsset("source_script"), id: "source-version-1" } } : {},
        missing_assets: [],
      });
    }
    if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
    if (path === `/api/assets/source-version-1/content`) return new Response("正文", { status: 200 });
    if (path === `/api/projects/${routedProjectID}/assets/source_script` && method === "POST") {
      uploads += 1;
      return json({ ...testAsset("source_script"), id: "source-version-1" }, 201);
    }
    if (path === `/api/projects/${routedProjectID}/tasks` && method === "POST") {
      taskAttempts += 1;
      return taskAttempts === 1 ? json({ error: "task failed" }, 500) : json({ id: "remix-task" }, 200);
    }
  }));
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "粘贴同行原文" }));
  const source = await screen.findByLabelText("同行原文");
  fireEvent.change(source, { target: { value: "正文" } });
  fireEvent.click(screen.getByRole("button", { name: "保存原文并开始二创" }));
  await screen.findByText("原文已保存，但二创任务启动失败，请检查模型配置后重试。");
  await waitFor(() => expect(detailReads).toBeGreaterThan(1));
  fireEvent.click(await screen.findByRole("button", { name: "查看或替换同行原文" }));
  fireEvent.click(screen.getByRole("button", { name: "保存原文并开始二创" }));

  await waitFor(() => expect(taskAttempts).toBe(2));
  expect(uploads).toBe(1);
});

test.each([false, true])("deletes only after confirmation=%s and returns to the board on success", async (confirmed) => {
  const project = { id: routedProjectID, account_id: "account-1", title: "待删除项目", stage: "script" };
  const deletes: string[] = [];
  window.history.replaceState({}, "", `/projects/${routedProjectID}`);
  vi.stubGlobal("confirm", vi.fn(() => confirmed));
  vi.stubGlobal("fetch", baseFetch((path, method) => {
    if (path === "/api/projects") return json([project]);
    if (path === `/api/projects/${routedProjectID}` && method === "GET") return json({ project, assets: {}, missing_assets: [] });
    if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
    if (path === `/api/projects/${routedProjectID}` && method === "DELETE") {
      deletes.push(path);
      return new Response(null, { status: 204 });
    }
  }));
  render(<App />);

  fireEvent.click(await screen.findByRole("button", { name: "删除当前项目" }));

  expect(confirm).toHaveBeenCalledOnce();
  if (confirmed) {
    await waitFor(() => expect(deletes).toEqual([`/api/projects/${routedProjectID}`]));
    await waitFor(() => expect(window.location.pathname).toBe("/projects"));
    expect(screen.getByRole("heading", { name: "视频项目" })).toBeTruthy();
  } else {
    expect(deletes).toEqual([]);
    expect(window.location.pathname).toBe(`/projects/${routedProjectID}`);
  }
});

test("browser Back returns from a project path to the board", async () => {
  window.history.replaceState({}, "", "/projects");
  const fixture = routedProjectFetch();
  vi.stubGlobal("fetch", fixture.fetch);
  render(<App />);
  fireEvent.click(await screen.findByText(fixture.project.title));
  await screen.findByRole("button", { name: "返回项目看板" });

  window.history.back();

  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "返回项目看板" })).toBeNull(),
  );
  expect(window.location.pathname).toBe("/projects");
  expect(screen.getByText(fixture.project.title)).toBeTruthy();
});

test("direct project routing preserves task query restoration", async () => {
  const taskID = "2c05dd52-ce7b-4244-9c03-22e887b198bf";
  window.history.replaceState({}, "", `/projects/${routedProjectID}?task=${taskID}`);
  const fixture = routedProjectFetch(taskID);
  vi.stubGlobal("fetch", fixture.fetch);

  render(<App />);

  await waitFor(() => expect(fixture.fetch).toHaveBeenCalledWith(`/api/tasks/${taskID}`, expect.anything()));
  expect(screen.getAllByText(fixture.project.title).length).toBeGreaterThan(0);
  expect(window.location.pathname).toBe(`/projects/${routedProjectID}`);
  expect(window.location.search).toBe(`?task=${taskID}`);
});

test("task restoration canonicalizes a mismatched project path to the task project", async () => {
  const pathProject = {
    id: "814ebfde-7470-418a-a703-a33596f7e8fe",
    account_id: "account-1",
    title: "路径项目 A",
    stage: "script",
  };
  const taskProject = {
    id: "94a1ddc8-7972-464e-b485-a849c7886be3",
    account_id: "account-1",
    title: "任务项目 B",
    stage: "script",
  };
  const taskID = "2c05dd52-ce7b-4244-9c03-22e887b198bf";
  window.history.replaceState({}, "", `/projects/${pathProject.id}?task=${taskID}`);
  vi.stubGlobal(
    "fetch",
    baseFetch((path) => {
      if (path === "/api/projects") return json([pathProject, taskProject]);
      if (path === `/api/projects/${pathProject.id}`)
        return json({ project: pathProject, assets: {}, missing_assets: [] });
      if (path === `/api/projects/${taskProject.id}`)
        return json({ project: taskProject, assets: {}, missing_assets: [] });
      if (path === `/api/tasks?project_id=${pathProject.id}`) return json([]);
      if (path === `/api/tasks?project_id=${taskProject.id}`) return json([]);
      if (path === `/api/tasks/${taskID}`)
        return json({
          id: taskID,
          project_id: taskProject.id,
          type: "remix",
          skill_name: "finance-viral-remix",
          status: "completed",
          created_at: "2026-08-08T00:00:00Z",
        });
    }),
  );

  render(<App />);

  const closeTask = await screen.findByRole("button", { name: "关闭任务详情" });
  await waitFor(() => expect(window.location.pathname).toBe(`/projects/${taskProject.id}`));
  expect(window.location.search).toBe(`?task=${taskID}`);
  window.history.replaceState({}, "", `/projects/${pathProject.id}?task=${taskID}`);
  fireEvent.popState(window);
  await waitFor(() => expect(window.location.pathname).toBe(`/projects/${taskProject.id}`));
  fireEvent.click(closeTask);
  expect(window.location.pathname).toBe(`/projects/${taskProject.id}`);
  expect(window.location.search).toBe("");
  expect(await screen.findByRole("heading", { name: taskProject.title })).toBeTruthy();
  expect(screen.queryByRole("heading", { name: pathProject.title })).toBeNull();
});

test("Escape closes a project with board history semantics and reopening does not duplicate paths", async () => {
  window.history.replaceState({}, "", "/projects");
  const fixture = routedProjectFetch();
  vi.stubGlobal("fetch", fixture.fetch);
  const pushState = vi.spyOn(window.history, "pushState");
  render(<App />);

  fireEvent.click(await screen.findByText(fixture.project.title));
  await screen.findByRole("button", { name: "返回项目看板" });
  fireEvent.keyDown(window, { key: "Escape" });

  await waitFor(() => expect(window.location.pathname).toBe("/projects"));
  expect(screen.queryByRole("button", { name: "返回项目看板" })).toBeNull();
  fireEvent.click(screen.getByText(fixture.project.title));
  expect(window.location.pathname).toBe(`/projects/${routedProjectID}`);
  expect(pushState.mock.calls.map((call) => call[2])).toEqual([
    `/projects/${routedProjectID}`,
    "/projects",
    `/projects/${routedProjectID}`,
  ]);
});

test("an unknown UUID project path clears project and task state", async () => {
  const missingID = "f47e33a2-dd0c-4fee-a68c-51b05a9f902e";
  window.history.replaceState({}, "", `/projects/${missingID}?task=missing-task`);
  vi.stubGlobal(
    "fetch",
    baseFetch((path) => {
      if (path === "/api/projects") return json([]);
    }),
  );

  render(<App />);

  await waitFor(() => expect(window.location.pathname).toBe("/projects"));
  expect(window.location.search).toBe("");
  expect(screen.queryByRole("button", { name: "返回项目看板" })).toBeNull();
  expect(screen.queryByRole("button", { name: "关闭任务详情" })).toBeNull();
});

test("uses the light theme by default and restores the selected theme", async () => {
  vi.stubGlobal("fetch", baseFetch((path) => {
    if (path === "/api/projects") return json([]);
  }));

  const firstRender = render(<App />);
  await screen.findByRole("heading", { name: "视频项目" });
  expect(document.documentElement.dataset.theme).toBe("light");

  fireEvent.change(screen.getByRole("combobox", { name: "选择界面主题" }), {
    target: { value: "dark" },
  });
  await waitFor(() => {
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(window.localStorage.getItem("video-production-console-theme")).toBe("dark");
  });

  firstRender.unmount();
  cleanup();
  render(<App />);
  await screen.findByRole("heading", { name: "视频项目" });
  expect(document.documentElement.dataset.theme).toBe("dark");
});

test("collapses a stage after four projects and toggles the remaining projects", async () => {
  const projects = Array.from({ length: 5 }, (_, index) => ({
    id: `814ebfde-7470-418a-a703-a33596f7e8${index}`,
    account_id: "account-1",
    title: `折叠项目 ${index + 1}`,
    stage: "script",
  }));
  vi.stubGlobal("fetch", baseFetch((path) => {
    if (path === "/api/projects") return json(projects);
  }));

  render(<App />);
  await screen.findByText("折叠项目 1");
  expect(screen.getByText("折叠项目 4")).toBeTruthy();
  expect(screen.queryByText("折叠项目 5")).toBeNull();

  const expand = screen.getByRole("button", { name: "展开剩余 1 个项目" });
  fireEvent.click(expand);
  expect(screen.getByText("折叠项目 5")).toBeTruthy();
  expect(screen.getByRole("button", { name: "收起项目" })).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "收起项目" }));
  expect(screen.queryByText("折叠项目 5")).toBeNull();
});

test("does not show a collapse control for empty or short stages", async () => {
  const projects = Array.from({ length: 4 }, (_, index) => ({
    id: `94a1ddc8-7972-464e-b485-a849c7886be${index}`,
    account_id: "account-1",
    title: `少量项目 ${index + 1}`,
    stage: "script",
  }));
  vi.stubGlobal("fetch", baseFetch((path) => {
    if (path === "/api/projects") return json(projects);
  }));

  render(<App />);
  await screen.findByText("少量项目 1");
  expect(screen.queryByRole("button", { name: /展开剩余|收起项目/ })).toBeNull();
});

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const publicSettings = {
  listen_addr: "127.0.0.1:8080",
  data_root: "data",
  max_codex_concurrency: 2,
  baokuan_base_url: "",
  baokuan_mcp_executable: "",
  obsidian_vault: "",
  topic_cards_dir: "",
  grok_base_url: "",
  grok_model: "",
  remix_base_url: "",
  remix_model: "",
  codex_binary_path: "codex",
  media_index_path: "",
  media_root: "",
  jianying_root: "",
  codex_default_model: "gpt-default",
  codex_default_reasoning_effort: "high",
};

function baseFetch(
  handler: (path: string, method: string, init?: RequestInit) => Response | Promise<Response> | undefined,
) {
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = typeof input === "string" ? input : input.toString();
    const method = (init?.method || "GET").toUpperCase();
    const response = await handler(path, method, init);
    if (response) return response;
    if (path === "/api/auth/me") return json({ csrfToken: "csrf" });
    if (path === "/api/accounts")
      return json([{ id: "account-1", name: "认知觉醒" }]);
    if (path === "/api/runtime") return json({ Limit: 4, Running: 0, Queued: 0 });
    if (path === "/api/settings")
      return json({ public: publicSettings, settings_version: 1, secrets: {} });
    throw new Error(`unexpected request: ${method} ${path}`);
  });
}

test("settings show model defaults and use the PUT response as the saved draft", async () => {
  let savedBody: Record<string, unknown> | undefined;
  vi.stubGlobal(
    "fetch",
    baseFetch((path, method, init) => {
      if (path === "/api/projects") return json([]);
      if (path === "/api/settings" && method === "PUT") {
        savedBody = JSON.parse(String(init?.body));
        return json({
          public: {
            ...publicSettings,
            codex_default_model: "gpt-from-server",
            codex_default_reasoning_effort: "ultra",
          },
          settings_version: 2,
          secrets: {},
        });
      }
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "设置" }));

  const model = await screen.findByRole("textbox", { name: "默认模型" });
  const effort = screen.getByRole("combobox", { name: "默认推理强度" });
  expect((model as HTMLInputElement).value).toBe("gpt-default");
  expect((effort as HTMLSelectElement).value).toBe("high");
  expect(
    screen.getByText(
      "默认值只影响之后新建的任务，不会修改运行中任务，也不会改写本机 Codex 全局配置。",
    ),
  ).toBeTruthy();

  fireEvent.change(model, { target: { value: "gpt-edited" } });
  fireEvent.change(effort, { target: { value: "max" } });
  fireEvent.click(screen.getByRole("button", { name: "保存设置" }));

  await waitFor(() => expect((model as HTMLInputElement).value).toBe("gpt-from-server"));
  expect((effort as HTMLSelectElement).value).toBe("ultra");
  expect(savedBody).toMatchObject({
    public: {
      codex_default_model: "gpt-edited",
      codex_default_reasoning_effort: "max",
    },
  });
});

test("a project stays visible when its detail request fails", async () => {
  const requests: Array<{ path: string; method: string }> = [];
  const project = {
    id: routedProjectID,
    account_id: "account-1",
    title: "存款到期新变化",
    stage: "script",
  };
  vi.stubGlobal(
    "fetch",
    baseFetch((path, method) => {
      requests.push({ path, method });
      if (path === "/api/projects") return json([project]);
      if (path === `/api/projects/${routedProjectID}`)
        return json({ message: "temporary failure" }, 500);
      if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByText(project.title));

  expect(await screen.findByRole("heading", { name: project.title })).toBeTruthy();
  expect(await screen.findByText("项目详情暂时无法读取")).toBeTruthy();
  expect(screen.getByRole("button", { name: "重试读取详情" })).toBeTruthy();
  expect(requests.some((item) => item.method === "DELETE")).toBe(false);
});

test("tasks show their actual model and awaiting replies keep it read-only", async () => {
  const project = {
    id: routedProjectID,
    account_id: "account-1",
    title: "养老金选题",
    stage: "topic",
  };
  const task = {
    id: "task-1",
    project_id: routedProjectID,
    type: "remix",
    skill_name: "二创",
    status: "awaiting_input",
    created_at: "2026-08-04T00:00:00Z",
    model: "gpt-actual",
    reasoning_effort: "max",
  };
  vi.stubGlobal(
    "fetch",
    baseFetch((path) => {
      if (path === "/api/projects") return json([project]);
      if (path === `/api/projects/${routedProjectID}`)
        return json({ project, assets: {}, missing_assets: [] });
      if (path === `/api/tasks?project_id=${routedProjectID}`) return json([task]);
      if (path === "/api/tasks/task-1") return json(task);
      if (path === "/api/tasks/task-1/semantic-events?limit=20")
        return json({ events: [] });
      if (path === "/api/tasks/task-1/result") return json({});
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByText("养老金选题"));

  const actualModel = await screen.findByText("gpt-actual");
  expect(actualModel).toBeTruthy();
  expect(screen.queryByText("gpt-actual · max")).toBeNull();
  expect(actualModel.parentElement?.querySelector("input, select")).toBeNull();
});

test("a running task exposes a stop action and sends the cancellation request", async () => {
  const requests: Array<{ path: string; method: string }> = [];
  const project = {
    id: routedProjectID,
    account_id: "account-1",
    title: "养老金选题",
    stage: "script",
  };
  const task = {
    id: "task-running",
    project_id: routedProjectID,
    type: "remix",
    skill_name: "finance-viral-remix",
    status: "running",
    created_at: "2026-08-07T00:00:00Z",
    model: "gpt-5.6-sol",
    reasoning_effort: "medium",
  };
  vi.stubGlobal("confirm", vi.fn(() => true));
  vi.stubGlobal(
    "fetch",
    baseFetch((path, method) => {
      requests.push({ path, method });
      if (path === "/api/projects") return json([project]);
      if (path === `/api/projects/${routedProjectID}`)
        return json({ project, assets: {}, missing_assets: [] });
      if (path === `/api/tasks?project_id=${routedProjectID}`) return json([task]);
      if (path === "/api/tasks/task-running") return json(task);
      if (path === "/api/tasks/task-running/semantic-events?limit=20")
        return json({ events: [] });
      if (path === "/api/tasks/task-running/cancel" && method === "POST")
        return json({ ...task, status: "cancelled" });
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByText("养老金选题"));
  fireEvent.click(await screen.findByRole("button", { name: /finance-viral-remix.*处理中/ }));
  fireEvent.click(await screen.findByRole("button", { name: "停止任务" }));

  await waitFor(() =>
    expect(requests).toContainEqual({
      path: "/api/tasks/task-running/cancel",
      method: "POST",
    }),
  );
});

test("a running task exposes a live phase duration instead of an empty persisted duration", async () => {
  const project = {
    id: routedProjectID,
    account_id: "account-1",
    title: "实时耗时项目",
    stage: "script",
  };
  const startedAt = new Date(Date.now() - 5000).toISOString();
  const task = {
    id: "task-timing-running",
    project_id: routedProjectID,
    type: "remix",
    skill_name: "finance-viral-remix",
    status: "running",
    created_at: startedAt,
    timing_summary: {
      task_id: "task-timing-running",
      total_ms: 5000,
      preparation_ms: 0,
      queue_ms: 0,
      execution_ms: 5000,
      phases: [],
      legacy_without_phases: false,
    },
    timing_runs: [
      {
        id: "phase-running",
        phase_key: "codex_execution",
        display_name: "Codex 执行",
        state: "running",
        started_at: startedAt,
        duration_ms: null,
      },
    ],
  };
  vi.stubGlobal(
    "fetch",
    baseFetch((path) => {
      if (path === "/api/projects") return json([project]);
      if (path === `/api/projects/${routedProjectID}`)
        return json({ project, assets: {}, missing_assets: [] });
      if (path === `/api/tasks?project_id=${routedProjectID}`) return json([task]);
      if (path === "/api/tasks/task-timing-running") return json(task);
      if (path === "/api/tasks/task-timing-running/semantic-events?limit=20")
        return json({ events: [] });
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByText("实时耗时项目"));
  fireEvent.click(
    await screen.findByRole("button", { name: /finance-viral-remix.*处理中/ }),
  );

  expect(await screen.findByText("模型执行")).toBeTruthy();
  const timingRegion = screen.getByRole("region", { name: "任务阶段耗时" });
  expect(within(timingRegion).getByText("运行中")).toBeTruthy();
  expect(within(timingRegion).getByLabelText("运行时长").textContent).toMatch(/^\d+\.\d s$/);
});

test("a timing summary with no phases preserves the legacy empty-phase meaning", async () => {
  const project = {
    id: routedProjectID,
    account_id: "account-1",
    title: "旧任务项目",
    stage: "script",
  };
  const task = {
    id: "task-timing-legacy",
    project_id: routedProjectID,
    type: "remix",
    skill_name: "finance-viral-remix",
    status: "completed",
    created_at: "2026-08-07T00:00:00Z",
    timing_summary: {
      task_id: "task-timing-legacy",
      total_ms: 3000,
      preparation_ms: 0,
      queue_ms: 1000,
      execution_ms: 2000,
      phases: [],
      legacy_without_phases: true,
    },
    timing_runs: [],
  };
  vi.stubGlobal(
    "fetch",
    baseFetch((path) => {
      if (path === "/api/projects") return json([project]);
      if (path === `/api/projects/${routedProjectID}`)
        return json({ project, assets: {}, missing_assets: [] });
      if (path === `/api/tasks?project_id=${routedProjectID}`) return json([task]);
      if (path === "/api/tasks/task-timing-legacy") return json(task);
      if (path === "/api/tasks/task-timing-legacy/semantic-events?limit=20")
        return json({ events: [] });
      if (path === "/api/tasks/task-timing-legacy/result") return json({});
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByText("旧任务项目"));
  fireEvent.click(
    await screen.findByRole("button", { name: /finance-viral-remix.*已完成/ }),
  );

  expect(
    await screen.findByText(
      "该任务没有已持久化的阶段运行记录，不能据此判定阶段是否开始。",
    ),
  ).toBeTruthy();
  expect(screen.queryByText("未命名阶段")).toBeNull();
});

test("an existing continuous script advances to asset preparation without a second remix control", async () => {
  const taskRequests: string[] = [];
  const project = {
    id: routedProjectID,
    account_id: "account-1",
    title: "养老金选题",
    stage: "script",
  };
  vi.stubGlobal("confirm", vi.fn(() => false));
  vi.stubGlobal(
    "fetch",
    baseFetch((path, method) => {
      if (path === "/api/projects") return json([project]);
      if (path === `/api/projects/${routedProjectID}`)
        return json({
          project,
          assets: {
            continuous_script: {
              id: "script-1",
              type: "continuous_script",
              filename: "continuous_script.txt",
              size: 1024,
              version: 1,
              state: "ready",
            },
          },
          missing_assets: [],
        });
      if (path === `/api/tasks?project_id=${routedProjectID}`) return json([]);
      if (path === `/api/projects/${routedProjectID}/tasks` && method === "POST") {
        taskRequests.push(path);
        return json({ id: "task-new" }, 201);
      }
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByText("养老金选题"));
  expect(await screen.findByRole("button", { name: "补齐制作素材" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "开始二创文案" })).toBeNull();
  expect(confirm).not.toHaveBeenCalled();
  expect(taskRequests).toHaveLength(0);
});

test("the home board no longer exposes topic planning", async () => {
  vi.stubGlobal("fetch", baseFetch((path) => {
    if (path === "/api/projects") return json([]);
  }));
  render(<App />);
  expect(await screen.findByRole("heading", { name: "视频项目" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "给我选题" })).toBeNull();
  expect(screen.queryByRole("heading", { name: "选题准备" })).toBeNull();
});
