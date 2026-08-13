import { expect, test, type Page } from "@playwright/test";

const idleStatus = {
  state: "degraded",
  counts: { sources: 12, shots: 328, ready_shots: 310, failed_shots: 18 },
  warnings: [{ code: "rights_unknown", count: 2 }],
};

const buildingStatus = {
  state: "scanning",
  counts: idleStatus.counts,
  active_job: { id: "job-1", phase: "ingest", completed_units: 3, total_units: 12 },
  warnings: [],
};

const sourcesPage = {
  sources: [
    {
      id: "source-1",
      kind: "movie",
      subtype: "video",
      origin: "local",
      relative_path: "originals/movies/主旋律片段.mp4",
      status: "failed",
      error_code: "probe_failed",
      size_bytes: 1024,
      duration_ms: 0,
      updated_at: "2026-08-13T00:00:00Z",
    },
  ],
};

async function mockCatalogConsole(page: Page) {
  let building = false;
  await page.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (!url.pathname.startsWith("/api/")) {
      await route.continue();
      return;
    }
    const path = url.pathname;
    let status = 200;
    let body: unknown = {};

    if (path === "/api/auth/me") {
      body = { csrfToken: "csrf-test" };
    } else if (path === "/api/accounts" || path === "/api/projects" || path === "/api/ideas") {
      body = [];
    } else if (path === "/api/runtime") {
      body = { Limit: 4, Running: 0, Queued: 0 };
    } else if (path === "/api/settings") {
      body = { public: {}, settings_version: 1, secrets: {} };
    } else if (path === "/api/media-catalog/status") {
      body = building ? buildingStatus : idleStatus;
    } else if (path === "/api/media-catalog/sources") {
      body = sourcesPage;
    } else if (path === "/api/media-catalog/providers") {
      body = { providers: [{ name: "pexels", configured: false }, { name: "pixabay", configured: false }] };
    } else if (path === "/api/media-catalog/search") {
      body = { assets: [] };
    } else if (path === "/api/media-catalog/import" && request.method() === "POST") {
      status = 201;
      body = { source: sourcesPage.sources[0], created: true, publishability: "local_draft_only" };
    } else if (path === "/api/media-catalog/index" && request.method() === "POST") {
      building = true;
      status = 202;
      body = buildingStatus;
    } else {
      status = 404;
      body = { code: "not_found" };
    }

    await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
  });
}

test("the media library panel shows counts and starts a build", async ({ page }) => {
  await mockCatalogConsole(page);
  await page.goto("/");

  await page.getByRole("button", { name: "素材库" }).click();
  const panel = page.getByRole("dialog", { name: "素材库" });
  await expect(panel).toBeVisible();

  await expect(panel.getByText("12", { exact: true })).toBeVisible();
  await expect(panel.getByText("328", { exact: true })).toBeVisible();
  await expect(panel.getByText("310", { exact: true })).toBeVisible();
  await expect(panel.getByText("18", { exact: true })).toBeVisible();
  await expect(panel.getByText("部分可用")).toBeVisible();
  await expect(panel.getByText(/缺少版权信息/)).toBeVisible();
  await expect(panel.getByText(/主旋律片段\.mp4/)).toBeVisible();
  await expect(panel.getByRole("button", { name: "重试" })).toBeVisible();

  await panel.getByRole("button", { name: "开始建库" }).click();
  await expect(panel.getByText("建库任务已开始。")).toBeVisible();
  await expect(panel.getByRole("progressbar")).toBeVisible();
  await expect(panel.getByText("3/12")).toBeVisible();
});
