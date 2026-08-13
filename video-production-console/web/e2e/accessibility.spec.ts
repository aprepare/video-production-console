import { expect, test, type Page } from "@playwright/test";

const projectID = "814ebfde-7470-418a-a703-a33596f7e8fe";
const project = {
  id: projectID,
  account_id: "account-1",
  title: "浏览器验收项目",
  stage: "script",
};

async function mockConsole(page: Page, authenticated: boolean, review = false) {
  const activeProject = review ? { ...project, stage: "review" } : project;
  const longValue = "C:/workspace/very-long-project-path/without-breakpoints/continuous-script-history-identifier-0123456789";
  await page.route("**/*", async (route) => {
    const url = new URL(route.request().url());
    if (!url.pathname.startsWith("/api/")) {
      await route.continue();
      return;
    }
    const path = `${url.pathname}${url.search}`;
    let body: unknown = {};
    let status = 200;

    if (path === "/api/auth/me") {
      if (!authenticated) {
        status = 401;
        body = { error: "unauthorized" };
      } else body = { csrfToken: "browser-test-token" };
    } else if (path === "/api/accounts") {
      body = [{ id: "account-1", name: "演示账号" }];
    } else if (path === "/api/projects") {
      body = [activeProject];
    } else if (path === "/api/settings") {
      body = { public: {}, configured_public: {}, active_public: {}, restart_required: false };
    } else if (path === "/api/runtime") {
      body = { Limit: 2, Running: 0, Queued: 0 };
    } else if (path === `/api/projects/${projectID}`) {
      body = review
        ? {
            project: activeProject,
            assets: {
              continuous_script: {
                id: "asset-long-script",
                type: "continuous_script",
                filename: `${longValue}.txt`,
                mime_type: "text/plain",
                size: 4096,
                version: 1,
                state: "ready",
                created_at: "2026-08-09T12:00:00Z",
              },
              mix_draft: {
                id: "asset-mix-draft",
                type: "mix_draft",
                filename: `${longValue}-mix-draft`,
                mime_type: "inode/directory",
                size: 0,
                version: 1,
                state: "ready",
                created_at: "2026-08-09T12:00:00Z",
              },
            },
            missing_assets: [],
            active_workflow: null,
          }
        : {
            project: activeProject,
            assets: {},
            missing_assets: [],
            active_workflow: null,
            // A topic card unlocks the "开始二创文案" primary action; without it the
            // script stage falls back to "先粘贴同行原文" and this test's mobile
            // action-bar assertion cannot match.
            topic_context: { id: "topic-1", title: "浏览器验收选题" },
          };
    } else if (path === `/api/tasks?project_id=${projectID}`) {
      body = review
        ? [{
            id: "review-task",
            project_id: projectID,
            type: "publishing",
            skill_name: "publishing-copy",
            status: "completed",
            result_summary: longValue,
            created_at: "2026-08-09T12:00:00Z",
            publishing_package: {
              titles: [`审核标题 ${longValue}`],
              description: `审核描述 ${longValue}`,
              topics: ["退休规划", "反诈"],
            },
          }]
        : [];
    }

    await route.fulfill({
      status,
      contentType: "application/json",
      body: JSON.stringify(body),
    });
  });
}

async function expectNoPageOverflow(page: Page) {
  const overflow = await page.evaluate(() => ({
    viewport: document.documentElement.clientWidth,
    page: document.documentElement.scrollWidth,
  }));
  expect(overflow.page).toBeLessThanOrEqual(overflow.viewport);
}

test("login remains keyboard-operable at 320px without page overflow", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 720 });
  await mockConsole(page, false);
  await page.goto("/");

  const password = page.getByLabel("管理口令");
  await expect(password).toBeFocused();
  await password.fill("123321");
  await page.keyboard.press("Tab");

  const submit = page.getByRole("button", { name: "进入控制台" });
  await expect(submit).toBeFocused();
  await expect(submit).toHaveCSS("outline-style", "solid");
  await expectNoPageOverflow(page);
});

test("project workbench exposes landmarks, current step, focus and mobile layout", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 780 });
  await mockConsole(page, true);
  await page.goto(`/projects/${projectID}`);

  await expect(page.getByRole("heading", { name: project.title })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "五阶段生产轨" })).toBeVisible();
  await expect(page.locator('[aria-current="step"]')).toContainText("文案");
  await expect(page.getByRole("region", { name: "当前项目资产" })).toBeVisible();
  await expect(page.getByRole("region", { name: "当前任务摘要" })).toBeVisible();
  await expect(page.getByRole("button", { name: "移动端：开始二创文案" })).toBeVisible();

  await page.getByRole("button", { name: "返回项目看板" }).focus();
  await expect(page.getByRole("button", { name: "返回项目看板" })).toHaveCSS("outline-style", "solid");
  await expectNoPageOverflow(page);
});

test("project workbench keeps its desktop dark-theme visual contract", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockConsole(page, true);
  await page.goto(`/projects/${projectID}`);

  await page.getByLabel("选择项目工作台主题").selectOption("dark");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");

  const title = page.getByRole("heading", { name: project.title });
  const assets = page.getByRole("region", { name: "当前项目资产" });
  const taskSummary = page.getByRole("region", { name: "当前任务摘要" });

  await expect(title).toHaveCSS("color", "rgb(238, 242, 255)");
  await expect(page.locator(".project-workbench")).toHaveCSS("background-color", "rgb(8, 13, 29)");
  await expect(assets).toHaveCSS("background-color", "rgb(21, 29, 57)");
  await expect(assets).toHaveCSS("color", "rgb(238, 242, 255)");
  await expect(taskSummary).toHaveCSS("background-color", "rgb(21, 29, 57)");
  await expect(taskSummary).toHaveCSS("color", "rgb(238, 242, 255)");

  const measurements = await page.evaluate(() =>
    [
      document.documentElement,
      document.body,
      document.querySelector(".project-workbench"),
      document.querySelector(".workbench-grid"),
      document.querySelector(".project-assets"),
      document.querySelector(".project-task-summary"),
    ]
      .filter((element): element is HTMLElement => element instanceof HTMLElement)
      .map((element) => ({
        name: element.className || element.tagName,
        clientWidth: element.clientWidth,
        scrollWidth: element.scrollWidth,
      })),
  );

  for (const measurement of measurements) {
    expect(measurement.scrollWidth, `${measurement.name} has horizontal overflow`).toBeLessThanOrEqual(
      measurement.clientWidth,
    );
  }
});

test("review and task data do not overflow desktop columns", async ({ page }) => {
  await page.setViewportSize({ width: 1180, height: 900 });
  await mockConsole(page, true, true);
  await page.goto(`/projects/${projectID}`);
  await page.getByLabel("选择项目工作台主题").selectOption("dark");

  await expect(page.locator(".workbench-grid--review")).toBeVisible();
  for (const selector of [".project-workbench", ".workbench-grid--review", ".project-assets", ".project-task-summary", ".publishing-review"]) {
    const width = await page.locator(selector).evaluate((element) => ({ client: element.clientWidth, scroll: element.scrollWidth }));
    expect(width.scroll, `${selector} has horizontal overflow`).toBeLessThanOrEqual(width.client);
  }
});
