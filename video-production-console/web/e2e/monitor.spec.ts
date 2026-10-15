import { readFileSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";

const cover = (n: number) => readFileSync(new URL(`./fixtures/cover${n}.jpg`, import.meta.url));
const now = Date.now();
const iso = (minutesAgo: number) => new Date(now - minutesAgo * 60000).toISOString();

const searchItems = [
  { platform: "douyin", external_id: "7669633110297166527", url: "https://www.douyin.com/video/7669633110297166527", title: "来日照吃什么？分享4家本地人会经常去吃的小馆子", text: "来日照吃什么？分享4家本地人会经常去吃的小馆子#日照#日照美食#日照旅游攻略", author: "日照瞎溜达", cover_url: "https://p3-sign.douyinpic.com/c2.jpeg", video_url: "https://v5.zjcdn.com/a.mp4", duration_s: 77.3, likes: 7491, comments: 248, shares: 7398, collects: 9048, plays: 0, created_at: Math.floor(now / 1000) - 86400 * 72 },
  { platform: "douyin", external_id: "7674191368210826146", url: "https://www.douyin.com/video/7674191368210826146", title: "日照本地人超详细海鲜攻略新鲜出炉！", text: "日照本地人超详细海鲜攻略新鲜出炉！ 包含了避雷吃什么、每个季节吃什么、去哪买怎么吃…这是很多经验总结出来的", author: "Neikyy", cover_url: "https://p3-sign.douyinpic.com/c3.jpeg", video_url: "https://v5.zjcdn.com/b.mp4", duration_s: 117, likes: 7655, comments: 604, shares: 5872, collects: 6340, plays: 0, created_at: Math.floor(now / 1000) - 86400 * 60 },
  { platform: "douyin", external_id: "7658994183646877108", url: "https://www.douyin.com/video/7658994183646877108", title: "好多朋友问我在日照去哪里吃海鲜！其实我一般都是在家里清蒸的…", text: "好多朋友问我在日照去哪里吃海鲜！其实我一般都是在家里清蒸的 但是只会清蒸！如果想吃别的口味我推荐你去这五个地方！", author: "海盐漫游酱", cover_url: "https://p11-sign.douyinpic.com/c4.jpeg", video_url: "", duration_s: 0, likes: 257, comments: 3, shares: 174, collects: 231, plays: 0, created_at: Math.floor(now / 1000) - 86400 * 104 },
  { platform: "douyin", external_id: "7660839667192843554", url: "https://www.douyin.com/video/7660839667192843554", title: "依旧干的噎人。日照吃海鲜教程。去哪买。怎么吃。吃什么。", text: "依旧干的噎人。日照吃海鲜教程。去哪买。怎么吃。吃什么。", author: "🌈え", cover_url: "https://p3-sign.douyinpic.com/c5.jpeg", video_url: "https://v5.zjcdn.com/c.mp4", duration_s: 78, likes: 4377, comments: 283, shares: 1921, collects: 2780, plays: 0, created_at: Math.floor(now / 1000) - 86400 * 97 },
];

const task = { id: "t1", name: "岚山海鲜", platform: "douyin", keyword: "岚山海鲜", interval_minutes: 30, pages: 1, enabled: true, model: "", score_enabled: true, score_threshold: 70, score_focus: "适合岚山海鲜餐馆模仿", auto_collect: true, auto_transcribe: false, auto_analyze: false, auto_insight: true, surge_mode: "both", surge_like_delta: 500, surge_like_ratio: 0.5, surge_comment_delta: 50, surge_comment_ratio: 0.5, surge_min_base: 20, last_run_at: iso(4), next_run_at: new Date(now + 26 * 60000).toISOString(), last_status: "ok", last_error: "", running: false, item_count: 23, unread_count: 3, surge_count: 1, created_at: iso(600), updated_at: iso(4) };
const task2 = { ...task, id: "t2", name: "日照海鲜市场", keyword: "日照海鲜市场", platform: "xhs", interval_minutes: 120, last_status: "failed", last_error: "小红书搜索失败：出口被限流，需要换出口", unread_count: 0, surge_count: 0, item_count: 18, last_run_at: iso(35) };
const history = (base: number, end: number) => Array.from({ length: 8 }, (_, i) => ({ at: iso(240 - i * 30), likes: Math.round(base + ((end - base) * (i / 7) ** 2)), comments: 0 }));
const feedItems = [
  { id: "i1", task_id: "t1", task_name: "岚山海鲜", platform: "douyin", external_id: "7618399307196009764", url: "https://www.douyin.com/video/7618399307196009764", title: "在日照岚山怼一顿海鲜小炒，舒坦", caption: "", author: "海吃王", cover_url: "https://p3.douyinpic.com/c6.jpeg", video_url: "https://v5.zjcdn.com/d.mp4", duration_s: 261, posted_at: Math.floor(now / 1000) - 86400 * 2, likes: 231135, comments: 6647, shares: 90218, collects: 22561, first_seen_at: iso(240), last_seen_at: iso(4), seen_count: 8, score_status: "scored", score: 88, score_reason: "老板现炒+报价开场，3 秒内交代价格和分量，本地餐馆照着拍门槛低。", score_tags: ["价格开场", "后厨实拍", "本地口音"], score_error: "", surge: true, surge_reason: "30 分钟点赞 +18,420（+8.6%），评论 +512", surge_at: iso(4), like_delta: 18420, comment_delta: 512, like_rate_per_hour: 36840, reference_id: "", read: false, history: history(150000, 231135) },
  { id: "i2", task_id: "t1", task_name: "岚山海鲜", platform: "douyin", external_id: "7644140160447874643", url: "", title: "来日照旅游居然发现一家20多年的岚山家常鱼馆", caption: "", author: "罗哈哈吃啥", cover_url: "https://p3.douyinpic.com/c3.jpeg", video_url: "https://v5.zjcdn.com/e.mp4", duration_s: 226, posted_at: Math.floor(now / 1000) - 86400 * 140, likes: 366, comments: 57, shares: 299, collects: 199, first_seen_at: iso(4), last_seen_at: iso(4), seen_count: 1, score_status: "scored", score: 76, score_reason: "老店+本地人背书的选题，评论区在问具体位置，有到店转化意图。", score_tags: ["老店背书", "探店"], score_error: "", surge: false, surge_reason: "", surge_at: "", like_delta: 0, comment_delta: 0, like_rate_per_hour: 0, reference_id: "", read: false, history: [] },
  { id: "i3", task_id: "t1", task_name: "岚山海鲜", platform: "douyin", external_id: "7681309629473838705", url: "", title: "这里是苏鲁交界的日照岚山，9月4号小船海鲜市场的价格", caption: "", author: "渔村阿亮", cover_url: "https://p3.douyinpic.com/c5.jpeg", video_url: "https://v5.zjcdn.com/f.mp4", duration_s: 81, posted_at: Math.floor(now / 1000) - 86400 * 41, likes: 183, comments: 34, shares: 130, collects: 81, first_seen_at: iso(200), last_seen_at: iso(4), seen_count: 7, score_status: "scored", score: 54, score_reason: "报价类内容时效强，但画面单一，适合做系列不适合单条模仿。", score_tags: ["每日报价"], score_error: "", surge: false, surge_reason: "", surge_at: "", like_delta: 6, comment_delta: 1, like_rate_per_hour: 12, reference_id: "r-collected", read: true, history: history(160, 183) },
];

async function mockReferences(page: Page) {
  await page.route("**/api/**", async route => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const method = route.request().method();
    if (!path.startsWith("/api/")) { await route.continue(); return; }
    if (path === "/api/references/preview") {
      const n = { c2: 2, c3: 3, c4: 4, c5: 5, c6: 6 }[(url.searchParams.get("url") || "").match(/c(\d)/)?.[0] ?? "c2"] ?? 2;
      if (url.searchParams.get("kind") === "cover") { await route.fulfill({ status: 200, contentType: "image/jpeg", body: cover(n) }); return; }
      if ((url.searchParams.get("url") || "").includes("expired")) { await route.fulfill({ status: 403, body: "" }); return; }
      await route.fulfill({ status: 200, contentType: "video/mp4", body: readFileSync(new URL("./fixtures/clip.mp4", import.meta.url)) });
      return;
    }
    let body: unknown = {};
    if (path === "/api/auth/me") body = { csrfToken: "t" };
    else if (path === "/api/accounts" || path === "/api/projects" || path === "/api/tasks") body = [];
    else if (path === "/api/settings") body = { public: { model_options: "gpt-5.6-sol\nclaude-opus-5-5" }, secrets: {}, configured_public: {}, active_public: {}, restart_required: false };
    else if (path === "/api/runtime") body = { Limit: 2, Running: 0, Queued: 0 };
    else if (path === "/api/references/status") body = { configured: true, reachable: true, apps: [{ id: "douyin", name: "抖音", ready: true, msg: "" }, { id: "xhs", name: "小红书", ready: true, msg: "" }, { id: "tingwu", name: "听悟", ready: true, msg: "" }] };
    else if (path === "/api/references") body = [];
    else if (path === "/api/references/search") body = { items: searchItems, has_more: true, cursor: "6", search_id: "s" };
    else if (path === "/api/monitors" && method === "GET") body = [task, task2];
    else if (path === "/api/monitors/feed") body = { items: feedItems, counts: { all: 3, surge: 1, high: 2, unread: 2 } };
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  });
}

async function noPageOverflow(page: Page) {
  const sizes = await page.evaluate(() => ({ width: document.documentElement.clientWidth, content: document.documentElement.scrollWidth }));
  expect(sizes.content).toBeLessThanOrEqual(sizes.width);
}

test("keyword search shows covers, filters videos and opens an in-page preview", async ({ page }) => {
  await mockReferences(page);
  await page.setViewportSize({ width: 1440, height: 960 });
  await page.goto("/references");
  await page.getByRole("tab", { name: "关键词搜索" }).click();
  await page.getByLabel("搜索关键词").fill("日照海鲜去哪吃");
  await page.getByRole("button", { name: "搜索", exact: true }).click();
  await expect(page.getByRole("button", { name: /播放：来日照吃什么/ })).toBeVisible();
  const loaded = await page.locator(".preview-thumb img").evaluateAll(images => images.every(image => (image as HTMLImageElement).naturalWidth > 0));
  expect(loaded).toBe(true);
  await expect(page.locator(".preview-thumb[data-kind='note']")).toHaveCount(1);
  await noPageOverflow(page);
  await page.screenshot({ path: "../artifacts/monitor-20261015/search-covers.png" });
  await page.getByLabel("只看视频").check();
  await expect(page.locator(".reference-result")).toHaveCount(3);
  await page.getByRole("button", { name: /播放：日照本地人超详细/ }).click();
  const dialog = page.getByRole("dialog", { name: /预览：日照本地人超详细/ });
  await expect(dialog).toBeVisible();
  await expect(dialog.locator("video")).toHaveAttribute("src", /\/api\/references\/preview\?platform=douyin&kind=video&url=/);
  await expect(dialog.getByRole("button", { name: "采集并转写" })).toBeVisible();
  await page.screenshot({ path: "../artifacts/monitor-20261015/preview-dialog.png" });
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
});

test("an expired preview explains itself instead of a black box", async ({ page }) => {
  await mockReferences(page);
  await page.route("**/api/references/search**", route => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [{ ...searchItems[0], video_url: "https://v5.zjcdn.com/expired.mp4" }], has_more: false, cursor: "", search_id: "s" }) }));
  await page.goto("/references");
  await page.getByRole("tab", { name: "关键词搜索" }).click();
  await page.getByLabel("搜索关键词").fill("x");
  await page.getByRole("button", { name: "搜索", exact: true }).click();
  await page.getByRole("button", { name: /播放：/ }).click();
  await expect(page.getByRole("alert").filter({ hasText: "视频地址已过期或平台拒绝了播放" })).toBeVisible();
});

for (const theme of ["light", "dark"] as const) {
  test(`monitor board prioritises surges and the editor fits (${theme})`, async ({ page }) => {
    await mockReferences(page);
    await page.addInitScript(value => window.localStorage.setItem("video-production-console-theme", value), theme);
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto("/references#monitor");
    await expect(page.getByRole("tab", { name: "监控" })).toHaveAttribute("aria-selected", "true");
    const first = page.locator(".monitor-item").first();
    await expect(first).toHaveAttribute("data-surge", "true");
    await expect(first.getByText("30 分钟点赞 +18,420（+8.6%），评论 +512")).toBeVisible();
    await expect(page.getByText("小红书搜索失败：出口被限流，需要换出口")).toBeVisible();
    await noPageOverflow(page);
    await page.screenshot({ path: `../artifacts/monitor-20261015/monitor-${theme}.png`, fullPage: true });
    await page.getByRole("button", { name: "编辑：岚山海鲜" }).click();
    const dialog = page.getByRole("dialog", { name: "编辑监控" });
    await expect(dialog.getByRole("radio", { name: "规则 + AI" })).toHaveAttribute("aria-checked", "true");
    await expect(dialog.getByLabel("点赞增量阈值")).toHaveValue("500");
    await page.screenshot({ path: `../artifacts/monitor-20261015/monitor-editor-${theme}.png` });
  });
}

test("monitor board stacks on a narrow window without horizontal scroll", async ({ page }) => {
  await mockReferences(page);
  await page.setViewportSize({ width: 820, height: 1000 });
  await page.goto("/references#monitor");
  await expect(page.locator(".monitor-item").first()).toBeVisible();
  await noPageOverflow(page);
  await page.screenshot({ path: "../artifacts/monitor-20261015/monitor-narrow.png", fullPage: true });
});
