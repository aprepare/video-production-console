import { expect, test, type Page } from "@playwright/test";

const images = [
  { id: "659340f8-31c0-49d8-a92d-a250f5a23c48", title: "家庭现金流：把每一笔钱用在生活里", status: "ready", image_count: 12, ratio: "3:4" },
  { id: "959340f8-31c0-49d8-a92d-a250f5a23c49", title: "资产配置：先看风险，再看回报", status: "draft", run_status: "running", image_count: 8, ratio: "3:4" },
];

async function mockStudio(page: Page) {
  await page.route("**/api/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (!path.startsWith("/api/")) { await route.continue(); return; }
    let body: unknown = {};
    if (path === "/api/auth/me") body = { csrfToken: "studio-test-token" };
    else if (path === "/api/accounts") body = [{ id: "account-one", name: "认知漫步" }, { id: "account-two", name: "观局思考" }];
    else if (path === "/api/projects") body = [];
    else if (path === "/api/image-projects") body = images;
    else if (path === "/api/settings") body = { public: { remix_model: "claude-opus-4-6-thinking", image_model: "gpt-image-2", image_text_model: "gpt-5.6-sol", max_image_concurrency: 3 }, secrets: {}, configured_public: {}, active_public: {}, restart_required: false };
    else if (path === "/api/runtime") body = { Limit: 2, Running: 0, Queued: 0 };
    else if (path === "/api/remix-lab/experiments") body = [{ id: "814ebfde-7470-418a-a703-a33596f7e8fe", title: "家庭存款的变化，普通人该怎么看", status: "completed", updated_at: "2026-09-29T09:30:00Z" }];
    else if (path === "/api/remix-lab/defaults") body = { presets: [], remix_model: "claude-opus-4-6-thinking", remix_base_url: "", remix_reasoning_effort: "", remix_api_key_configured: false };
    else if (path === "/api/remix-lab/prompts") body = { prompts: [] };
    else if (path === "/api/remix-lab/workflow") body = { version: 1, name: "原文到定稿", nodes: [
      { id: "source", type: "input", title: "对标原文", x: 0, y: 0, config: {} },
      { id: "writer", type: "writer", title: "写手", x: 300, y: 0, config: {} },
      { id: "final", type: "output", title: "定稿与发布包", x: 600, y: 0, config: {} },
    ], edges: [["source", "writer"], ["writer", "final"]] };
    else if (path === "/api/remix-lab/agent/history") body = { turns: [] };
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  });
}

async function noPageOverflow(page: Page) {
  const sizes = await page.evaluate(() => ({ width: document.documentElement.clientWidth, content: document.documentElement.scrollWidth }));
  expect(sizes.content).toBeLessThanOrEqual(sizes.width);
}

test("desktop studio leaves room to edit and settings remain usable in both themes", async ({ page }) => {
  await mockStudio(page);
  await page.setViewportSize({ width: 1440, height: 960 });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "文案创作台", exact: true })).toBeVisible();
  await expect(page.getByLabel("对标原文", { exact: true })).toBeVisible();
  const editor = await page.getByLabel("对标原文", { exact: true }).boundingBox();
  expect(editor?.width).toBeGreaterThan(380);
  await noPageOverflow(page);
  await page.screenshot({ path: "../artifacts/frontend-studio-20260930/workbench-desktop.png" });
  await page.getByLabel("选择界面主题").selectOption("dark");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.locator(".console-nav")).toHaveCSS("background-color", "rgb(29, 36, 54)");
  await page.screenshot({ path: "../artifacts/frontend-studio-20260930/workbench-dark.png" });
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "控制台设置" })).toBeVisible();
  await page.getByRole("tab", { name: "图文", exact: true }).click();
  await expect(page.getByRole("tabpanel")).toBeVisible();
  await page.screenshot({ path: "../artifacts/frontend-studio-20260930/settings-dark.png" });
  await page.getByRole("button", { name: "关闭设置" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("image library filtering keeps the creation draft and has only one page title", async ({ page }) => {
  await mockStudio(page);
  await page.setViewportSize({ width: 1720, height: 1000 });
  await page.goto("/image-projects");
  await expect(page.getByRole("heading", { level: 1 })).toHaveCount(1);
  await page.getByLabel("最终文案", { exact: true }).fill("家庭现金流需要先看日常收支，再决定长期规划。");
  await expect(page.getByRole("button", { name: "开始生成图片" })).toBeEnabled();
  await page.getByRole("searchbox", { name: "搜索图文项目" }).fill("现金流");
  await expect(page.getByRole("button", { name: /资产配置：/ })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /家庭现金流：/ })).toBeVisible();
  await page.getByLabel("筛选图文项目状态").selectOption("working");
  await expect(page.getByText("没有匹配的项目", { exact: true })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "最终文案", exact: true })).toHaveValue("家庭现金流需要先看日常收支，再决定长期规划。");
  await page.getByRole("button", { name: "清除筛选" }).click();
  await expect(page.getByRole("button", { name: /资产配置：/ })).toBeVisible();
  await noPageOverflow(page);
  await page.screenshot({ path: "../artifacts/frontend-studio-20260930/image-desktop.png" });
});

test("mobile studio, image creation and settings fit without horizontal overflow", async ({ page }) => {
  await mockStudio(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "文案创作台", exact: true })).toBeVisible();
  await noPageOverflow(page);
  await page.screenshot({ path: "../artifacts/frontend-studio-20260930/workbench-mobile.png", fullPage: true });
  await page.getByRole("link", { name: "图文制作", exact: true }).click();
  await expect(page.getByLabel("最终文案", { exact: true })).toBeVisible();
  await noPageOverflow(page);
  await page.getByRole("button", { name: "开始生成图片" }).scrollIntoViewIfNeeded();
  await expect(page.getByRole("button", { name: "开始生成图片" })).toBeVisible();
  await page.screenshot({ path: "../artifacts/frontend-studio-20260930/image-mobile.png", fullPage: true });
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "控制台设置" })).toBeVisible();
  await noPageOverflow(page);
  await page.getByRole("tab", { name: "系统", exact: true }).click();
  await page.screenshot({ path: "../artifacts/frontend-studio-20260930/settings-mobile.png" });
});
