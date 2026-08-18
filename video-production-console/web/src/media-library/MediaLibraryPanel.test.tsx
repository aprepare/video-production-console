// @vitest-environment jsdom

import { QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render as testingRender, screen, waitFor } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, expect, test, vi } from "vitest";
import type { MediaCatalogSourcesPage, MediaCatalogStatus } from "../api/mediaCatalog";
import { createAppQueryClient } from "../query/client";
import { MediaLibraryPanel } from "./MediaLibraryPanel";
import { catalogStatusRefetchInterval } from "./status-polling";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });

function render(ui: ReactElement) {
  const client = createAppQueryClient();
  client.setDefaultOptions({ queries: { retry: false, refetchOnWindowFocus: false } });
  return testingRender(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

const readyStatus: MediaCatalogStatus = {
  state: "degraded",
  counts: { sources: 12, shots: 328, ready_shots: 310, failed_shots: 18 },
  warnings: [
    { code: "ffmpeg_not_configured", count: 1 },
    { code: "rights_unknown", count: 2 },
  ],
};

const failedSourcePage: MediaCatalogSourcesPage = {
  sources: [
    {
      id: "source-1",
      kind: "movie",
      subtype: "video",
      origin: "local",
      relative_path: "originals/movies/沉默的荣耀.mp4",
      status: "failed",
      error_code: "probe_failed",
      size_bytes: 1024,
      duration_ms: 0,
      updated_at: "2026-08-13T00:00:00Z",
    },
  ],
};

function catalogApi(overrides: {
  status?: () => Response;
  sources?: (path: string) => Response;
  index?: () => Response;
  retry?: () => Response;
  providers?: () => Response;
  search?: (path: string) => Response;
  import?: () => Response;
} = {}) {
  const calls: Array<{ path: string; method: string }> = [];
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    const method = (init?.method || "GET").toUpperCase();
    calls.push({ path, method });
    if (path === "/api/media-catalog/status") return overrides.status ? overrides.status() : json(readyStatus);
    if (path === "/api/media-catalog/providers") {
      return overrides.providers
        ? overrides.providers()
        : json({ providers: [{ name: "pexels", configured: true }, { name: "pixabay", configured: false }] });
    }
    if (path.startsWith("/api/media-catalog/search")) {
      return overrides.search ? overrides.search(path) : json({ assets: [] });
    }
    if (path === "/api/media-catalog/import") {
      return overrides.import
        ? overrides.import()
        : json({
            source: failedSourcePage.sources[0],
            created: true,
            publishability: "local_draft_only",
          }, 201);
    }
    if (path.startsWith("/api/media-catalog/sources?") || path === "/api/media-catalog/sources") {
      if (method === "POST") return overrides.retry ? overrides.retry() : json(readyStatus);
      return overrides.sources ? overrides.sources(path) : json(failedSourcePage);
    }
    if (path.startsWith("/api/media-catalog/sources/") && path.endsWith("/retry"))
      return overrides.retry ? overrides.retry() : json(readyStatus);
    if (path === "/api/media-catalog/index") return overrides.index ? overrides.index() : json(readyStatus, 202);
    throw new Error(`unexpected request ${method} ${path}`);
  });
  return { api, calls };
}

test("shows the configuration hint when the catalog is not configured", async () => {
  const { api } = catalogApi({
    status: () => json({ code: "catalog_not_configured", message: "素材库未配置。" }, 503),
    sources: () => json({ code: "catalog_not_configured", message: "素材库未配置。" }, 503),
  });
  render(<MediaLibraryPanel api={api} onClose={() => {}} />);

  expect(await screen.findByText(/素材库尚未配置/)).toBeTruthy();
  expect(screen.getByText(/请在设置中填写「素材库目录」和「媒体素材目录」/)).toBeTruthy();
});

test("shows counts, state, and humanized warning messages", async () => {
  const { api } = catalogApi();
  render(<MediaLibraryPanel api={api} onClose={() => {}} />);

  expect(await screen.findByText("素材源")).toBeTruthy();
  expect(screen.getByText("12").closest(".media-stat")).toBeTruthy();
  expect(screen.getByText("328")).toBeTruthy();
  expect(screen.getByText("310")).toBeTruthy();
  expect(screen.getByText("18")).toBeTruthy();
  expect(screen.getByText("部分可用")).toBeTruthy();
  expect(screen.getByText(/请安装 FFmpeg 并在设置中填写路径/)).toBeTruthy();
  expect(screen.getByText(/缺少版权信息/)).toBeTruthy();
});

test("shows the active phase and progress while a build runs", async () => {
  const building: MediaCatalogStatus = {
    ...readyStatus,
    state: "analyzing",
    active_job: { id: "job-1", phase: "analyzing", completed_units: 61, total_units: 100 },
    warnings: [],
  };
  const { api } = catalogApi({ status: () => json(building) });
  render(<MediaLibraryPanel api={api} onClose={() => {}} />);

  expect(await screen.findByText("正在分析")).toBeTruthy();
  const progress = screen.getByRole("progressbar") as HTMLProgressElement;
  expect(progress.value).toBe(61);
  expect(progress.max).toBe(100);
  expect(screen.getByText("61/100")).toBeTruthy();
});

test("starting a build posts to the index route and reports conflicts", async () => {
  const { api, calls } = catalogApi({
    index: () => json({ code: "catalog_job_active", message: "已有建库任务。" }, 409),
  });
  render(<MediaLibraryPanel api={api} onClose={() => {}} />);

  fireEvent.click(await screen.findByRole("button", { name: "开始建库" }));

  await waitFor(() =>
    expect(calls.some((call) => call.path === "/api/media-catalog/index" && call.method === "POST")).toBe(true),
  );
  expect(await screen.findByText(/已有建库任务正在进行/)).toBeTruthy();
});

test("kind and status filters go to the sources query", async () => {
  const { api, calls } = catalogApi();
  render(<MediaLibraryPanel api={api} onClose={() => {}} />);
  await screen.findByText("素材源");

  fireEvent.change(screen.getByLabelText("按类型筛选"), { target: { value: "movie" } });
  fireEvent.change(screen.getByLabelText("按状态筛选"), { target: { value: "failed" } });

  await waitFor(() =>
    expect(
      calls.some((call) => call.path === "/api/media-catalog/sources?kind=movie&status=failed"),
    ).toBe(true),
  );
});

test("a failed source row offers a retry that posts to the retry route", async () => {
  const { api, calls } = catalogApi();
  render(<MediaLibraryPanel api={api} onClose={() => {}} />);

  expect(await screen.findByText(/沉默的荣耀\.mp4/)).toBeTruthy();
  expect(screen.getByText(/视频探测失败/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /重试/ }));

  await waitFor(() =>
    expect(
      calls.some(
        (call) => call.path === "/api/media-catalog/sources/source-1/retry" && call.method === "POST",
      ),
    ).toBe(true),
  );
});

test("searching the stock library posts the query and can import a hit", async () => {
  const { api, calls } = catalogApi({
    search: () =>
      json({
        assets: [
          {
            provider: "pexels",
            id: "101",
            kind: "image",
            download_url: "https://images.pexels.com/a.jpg",
            page_url: "https://www.pexels.com/photo/101/",
            creator: "Ada",
            license_code: "pexels",
            license_url: "https://www.pexels.com/license/",
            width: 800,
            height: 600,
          },
        ],
      }),
  });
  render(<MediaLibraryPanel api={api} onClose={() => {}} />);
  await screen.findByText("素材源");

  fireEvent.change(screen.getByLabelText("关键词"), { target: { value: "湖面" } });
  fireEvent.click(screen.getByRole("button", { name: "搜索图库" }));

  expect(await screen.findByText("Ada")).toBeTruthy();
  expect(screen.getByText(/pexels · 800×600/)).toBeTruthy();
  await waitFor(() =>
    expect(calls.some((call) => call.path === "/api/media-catalog/search?provider=pexels&q=%E6%B9%96%E9%9D%A2")).toBe(true),
  );

  fireEvent.click(screen.getByRole("button", { name: /^导入$/ }));
  expect(await screen.findByText("已导入外部素材。")).toBeTruthy();
  await waitFor(() =>
    expect(calls.some((call) => call.path === "/api/media-catalog/import" && call.method === "POST")).toBe(true),
  );
});

test("unconfigured providers keep local indexing available", async () => {
  const { api } = catalogApi({
    providers: () => json({ providers: [{ name: "pexels", configured: false }, { name: "pixabay", configured: false }] }),
  });
  render(<MediaLibraryPanel api={api} onClose={() => {}} />);
  expect(await screen.findByText(/未配置 Pexels \/ Pixabay 密钥时仍可本地建库/)).toBeTruthy();
  expect(screen.getByRole("button", { name: "开始建库" })).toBeTruthy();
});

test("polling runs every two seconds only while a job is active", () => {
  expect(catalogStatusRefetchInterval(undefined)).toBe(false);
  expect(catalogStatusRefetchInterval(readyStatus)).toBe(false);
  expect(
    catalogStatusRefetchInterval({
      ...readyStatus,
      state: "scanning",
      active_job: { id: "job", phase: "ingest", completed_units: 0, total_units: 0 },
    }),
  ).toBe(2000);
  for (const state of ["ready", "degraded", "failed"] as const) {
    expect(
      catalogStatusRefetchInterval({
        ...readyStatus,
        state,
        active_job: { id: "job", phase: "ingest", completed_units: 1, total_units: 1 },
      }),
    ).toBe(false);
  }
});
