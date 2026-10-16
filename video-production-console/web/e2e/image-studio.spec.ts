import { readFileSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";

const fixture = (name: string) => readFileSync(new URL(`./fixtures/${name}`, import.meta.url));
const now = Date.now();
const iso = (minutesAgo: number) => new Date(now - minutesAgo * 60000).toISOString();

const out = (id: string, position: number, status: "pending" | "ready" | "failed", file = "", w = 1024, h = 1536, error = "") => ({
  id, position, status, mime_type: status === "ready" ? "image/jpeg" : "", width: status === "ready" ? w : 0, height: status === "ready" ? h : 0, bytes: 0, error,
  url: status === "ready" ? `/api/image-studio/outputs/${id}/image#${file}` : "",
});

const history = [
  { id: "g1", prompt: "小红书竖版封面，温暖手帐风，旧木书桌上摊开的考研英语单词本，一杯热拿铁，几张手写便利贴", final_prompt: "小红书竖版封面，温暖手帐风……", enhance: false, style: "", custom_style: "", model: "grok-imagine-image-2.0", ratio: "3:4", count: 2, status: "completed", error: "", favorite: true, created_at: iso(3), updated_at: iso(3),
    outputs: [out("o1", 0, "ready", "studio-cover-0.jpg"), out("o2", 1, "ready", "studio-cover-1.jpg")] },
  { id: "g2", prompt: "日照岚山渔港清晨，渔船靠岸，海鸥，暖金色晨光，水彩手绘质感", final_prompt: "竖版插画，日照岚山渔港清晨……", enhance: true, style: "", custom_style: "", model: "grok-imagine-image-2.0", ratio: "3:4", count: 3, status: "partial", error: "有 1 张没出来：生图服务繁忙（限流），稍后再试", favorite: false, created_at: iso(42), updated_at: iso(41),
    outputs: [out("o3", 0, "ready", "studio-port-0.jpg"), out("o4", 1, "ready", "studio-port-1.jpg"), out("o5", 2, "failed", "", 0, 0, "生图服务繁忙（限流），稍后再试")] },
  { id: "g3", prompt: "一只橘猫坐在旧木窗台上，午后阳光，温暖手帐插画风", final_prompt: "一只橘猫坐在旧木窗台上，午后阳光，温暖手帐插画风", enhance: false, style: "", custom_style: "", model: "grok-imagine-image-quality", ratio: "1:1", count: 1, status: "completed", error: "", favorite: false, created_at: iso(180), updated_at: iso(180),
    outputs: [out("o6", 0, "ready", "studio-grok-imagine-image-quality.jpg", 1024, 1024)] },
  { id: "g4", prompt: "赛博朋克风格的城市夜景海报", final_prompt: "赛博朋克风格的城市夜景海报", enhance: false, style: "", custom_style: "", model: "gpt-image-2", ratio: "4:3", count: 2, status: "failed", error: "图片模型「gpt-image-2」网关不支持，换一个模型试试", favorite: false, created_at: iso(1500), updated_at: iso(1500),
    outputs: [out("o7", 0, "failed", "", 0, 0, "图片模型「gpt-image-2」网关不支持，换一个模型试试"), out("o8", 1, "failed", "", 0, 0, "图片模型「gpt-image-2」网关不支持，换一个模型试试")] },
];

const models = { default_model: "grok-imagine-image-2.0", key_configured: true, key_source: "image_text", models: [
  { id: "grok-imagine-image-2.0", label: "Grok Imagine 2.0", note: "细节最好 · 约 15 秒", available: true },
  { id: "grok-imagine-image", label: "Grok Imagine", note: "快 · 约 7 秒", available: true },
  { id: "grok-imagine-image-quality", label: "Grok Imagine Quality", note: "快 · 约 7 秒 · 画质增强", available: true },
  { id: "gemini-3.1-flash-image", label: "Gemini Flash Image", note: "网关暂不支持生图接口", available: false },
] };

async function mockStudio(page: Page, options: { keyConfigured?: boolean; running?: boolean } = {}) {
  let generated: Record<string, unknown> | null = null;
  await page.route("**/api/**", async route => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const method = route.request().method();
    if (!path.startsWith("/api/")) { await route.continue(); return; }
    const image = path.match(/^\/api\/image-studio\/outputs\/(\w+)\/image$/);
    if (image) {
      const all = [...history, ...(generated ? [generated as never] : [])].flatMap((item: { outputs: Array<{ id: string; url: string }> }) => item.outputs);
      const file = all.find(item => item.id === image[1])?.url.split("#")[1] || "studio-cover-0.jpg";
      await route.fulfill({ status: 200, contentType: "image/jpeg", body: fixture(file) });
      return;
    }
    let body: unknown = {};
    let status = 200;
    if (path === "/api/auth/me") body = { csrfToken: "t" };
    else if (path === "/api/accounts" || path === "/api/projects" || path === "/api/tasks") body = [];
    else if (path === "/api/settings") body = { public: { model_options: "claude-opus-5-5\ngpt-5.6-sol", image_model: "grok-imagine-image-2.0" }, secrets: {}, configured_public: {}, active_public: {}, restart_required: false };
    else if (path === "/api/runtime") body = { Limit: 2, Running: 0, Queued: 0 };
    else if (path === "/api/image-projects") body = [];
    else if (path === "/api/image-studio/models") body = { ...models, key_configured: options.keyConfigured !== false };
    else if (path === "/api/image-studio/generations" && method === "GET") {
      const items = options.running
        ? [{ ...history[0], id: "g0", status: "running", created_at: iso(0), count: 3, outputs: [out("p1", 0, "ready", "studio-cover-1.jpg"), out("p2", 1, "pending"), out("p3", 2, "pending")] }, ...history]
        : history;
      body = { items: url.searchParams.get("favorite") === "1" ? items.filter(item => item.favorite) : items, has_more: false };
    }
    else if (path === "/api/image-studio/generations" && method === "POST") {
      const sent = JSON.parse(route.request().postData() || "{}");
      generated = { ...history[0], id: "g-new", prompt: sent.prompt, final_prompt: sent.final_prompt || sent.prompt, model: sent.model || models.default_model, ratio: sent.ratio, count: sent.count, status: "running", favorite: false, created_at: new Date().toISOString(),
        outputs: Array.from({ length: sent.count }, (_, i) => out(`n${i}`, i, "pending")) };
      body = generated; status = 201;
    }
    else if (path === "/api/image-studio/generations/g-new") body = generated;
    else if (path.startsWith("/api/image-studio/generations/")) body = history.find(item => path.endsWith(item.id)) ?? history[0];
    else if (path === "/api/image-studio/enhance") body = { final_prompt: "竖版小红书封面插画，温暖手帐风：一张旧木书桌占据画面下三分之二，桌上摊开一本写满手写英文单词的考研单词本，右上角一杯拉花热拿铁冒着热气，周围散落三四张米黄色便利贴；午后斜阳从左侧窗户照进来，形成柔和的窗格光影；整体色调为奶油白、焦糖棕和淡橄榄绿，牛皮纸与水彩质感，画面顶部留出约四分之一的干净空白用于放标题。", model: "claude-opus-5-5" };
    await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
  });
}

async function noPageOverflow(page: Page) {
  const sizes = await page.evaluate(() => ({ width: document.documentElement.clientWidth, content: document.documentElement.scrollWidth }));
  expect(sizes.content).toBeLessThanOrEqual(sizes.width);
}

async function imagesLoaded(page: Page) {
  await page.waitForFunction(() => [...document.querySelectorAll(".studio-tile img")].every(img => (img as HTMLImageElement).complete && (img as HTMLImageElement).naturalWidth > 0));
}

for (const theme of ["light", "dark"] as const) {
  test(`single image studio shows the composer and a visual history (${theme})`, async ({ page }) => {
    await mockStudio(page);
    await page.addInitScript(value => window.localStorage.setItem("video-production-console-theme", value), theme);
    await page.setViewportSize({ width: 1440, height: 1100 });
    await page.goto("/image-projects#single");
    await expect(page.getByRole("tab", { name: "单张出图" })).toHaveAttribute("aria-selected", "true");
    await expect(page.getByRole("heading", { name: "单张出图", level: 1 })).toBeVisible();
    await expect(page.getByRole("article")).toHaveCount(4);
    await imagesLoaded(page);
    await expect(page.getByText("图片模型「gpt-image-2」网关不支持，换一个模型试试").first()).toBeVisible();
    await noPageOverflow(page);
    await page.screenshot({ path: `../artifacts/image-studio-20261016/studio-${theme}.png`, fullPage: true });
  });
}

test("enhance, generate and watch placeholders fill in", async ({ page }) => {
  await mockStudio(page);
  await page.setViewportSize({ width: 1440, height: 1100 });
  await page.goto("/image-projects#single");
  await page.getByLabel("提示词").fill("小红书封面，考研单词本和咖啡，温暖手帐风");
  await page.getByRole("button", { name: /AI 帮我扩写/ }).click();
  await expect(page.getByLabel("扩写后的提示词")).toHaveValue(/竖版小红书封面插画/);
  await page.getByRole("radio", { name: "3", exact: true }).click();
  await page.screenshot({ path: "../artifacts/image-studio-20261016/studio-enhanced.png" });
  await page.getByRole("button", { name: "出 3 张图" }).click();
  const first = page.getByRole("article").first();
  await expect(first.getByText("生成中")).toHaveCount(3);
  await expect(first).toContainText("小红书封面，考研单词本和咖啡");
  await page.screenshot({ path: "../artifacts/image-studio-20261016/studio-generating.png" });
});

test("the lightbox shows the full image with its prompt and download", async ({ page }) => {
  await mockStudio(page);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/image-projects#single");
  await imagesLoaded(page);
  await page.getByRole("button", { name: "查看第 2 张大图" }).first().click();
  const dialog = page.getByRole("dialog", { name: "大图预览" });
  await expect(dialog.locator("img")).toBeVisible();
  await page.waitForFunction(() => { const img = document.querySelector(".studio-lightbox__stage img") as HTMLImageElement | null; return !!img && img.complete && img.naturalWidth > 0; });
  await expect(dialog.getByRole("link", { name: /下载这张/ })).toHaveAttribute("href", /download=1/);
  await page.screenshot({ path: "../artifacts/image-studio-20261016/studio-lightbox.png" });
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
});

test("missing image key is called out before anything is spent", async ({ page }) => {
  await mockStudio(page, { keyConfigured: false });
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto("/image-projects#single");
  await expect(page.getByText("还没配置生图服务的 Key，出不了图。")).toBeVisible();
  await page.getByLabel("提示词").fill("一杯咖啡");
  await expect(page.getByRole("button", { name: "出 2 张图" })).toBeDisabled();
});

test("the studio stacks on a narrow window without horizontal scroll", async ({ page }) => {
  await mockStudio(page, { running: true });
  await page.setViewportSize({ width: 820, height: 1100 });
  await page.goto("/image-projects#single");
  await expect(page.getByRole("article").first()).toBeVisible();
  await noPageOverflow(page);
  await page.screenshot({ path: "../artifacts/image-studio-20261016/studio-narrow.png", fullPage: true });
});

test("the script tab still works and its image model is a dropdown", async ({ page }) => {
  await mockStudio(page);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/image-projects");
  await expect(page.getByRole("tab", { name: "文案配图" })).toHaveAttribute("aria-selected", "true");
  const select = page.getByRole("combobox", { name: "图片模型" });
  await expect(select).toHaveValue("grok-imagine-image-2.0");
  await expect(page.getByText("细节最好 · 约 15 秒")).toBeVisible();
  await page.getByRole("tab", { name: "单张出图" }).click();
  await expect(page).toHaveURL(/#single$/);
  await page.getByRole("tab", { name: "文案配图" }).click();
  await expect(page.getByRole("button", { name: "开始生成图片" })).toBeVisible();
});
