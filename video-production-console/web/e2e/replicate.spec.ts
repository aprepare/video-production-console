import { readFileSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";

const fixture = (name: string) => readFileSync(new URL(`./fixtures/${name}`, import.meta.url));
const now = Date.now();
const iso = (minutesAgo: number) => new Date(now - minutesAgo * 60000).toISOString();
const shotsDir = "../artifacts/replicate-20261016";

const P1 = "6b0c2a8e-3f51-4c1d-9a77-2c5e8f3d1a01";
const P2 = "6b0c2a8e-3f51-4c1d-9a77-2c5e8f3d1a02";
const P3 = "6b0c2a8e-3f51-4c1d-9a77-2c5e8f3d1a03";

type Take = { id: string; provider: "runninghub_h3" | "grok"; status: string; error: string; duration_s: number; width: number; height: number; url: string; cost_note: string; created_at: string };
const take = (id: string, status: string, file = "", error = ""): Take => ({
  id, provider: "runninghub_h3", status, error, duration_s: status === "succeeded" ? 6 : 0, width: 720, height: 1280,
  url: status === "succeeded" ? `/api/replicate/takes/${id}/video#${file}` : "", cost_note: status === "succeeded" ? "约 35 币" : "", created_at: iso(5),
});
const frameFile: Record<string, string> = { s1: "cover6.jpg", s2: "cover3.jpg", s3: "studio-port-0.jpg", s4: "cover5.jpg" };
const shot = (id: string, position: number, purpose: string, motion: string, extra: Record<string, unknown> = {}) => ({
  id, position, purpose, source_start_s: (position - 1) * 2.4, source_end_s: position * 2.4, duration_s: 6,
  first_frame_prompt: "竖屏 9:16 近景，岚山海鲜大排档后厨，一盘刚出锅的辣炒梭子蟹，蒸汽升腾，暖黄灯光，真实手机拍摄质感",
  motion_prompt: motion, negative: "多余手指、肢体扭曲、文字乱码、水印", keep: "梭子蟹八条腿两只大钳，红色外壳不变形",
  first_frame_url: `/api/replicate/shots/${id}/first-frame?v=1`, first_frame_source: "generated", first_frame_status: "ready", first_frame_error: "",
  selected_take_id: "", takes: [] as Take[], ...extra,
});

const generating = {
  id: P1, title: "岚山海鲜小炒 · 复刻", reference_id: "r1", analysis_id: "a1", product: "岚山海鲜大排档，现捞梭子蟹和皮皮虾，明码标价", style_lock: "暖黄灯光、真实手机拍摄、后厨烟火气、9:16 竖屏",
  ratio: "9:16", video_provider: "runninghub_h3", image_model: "", variants: 2, status: "generating", final_url: "", final_duration_s: 0, qc_id: "", error: "", created_at: iso(30), updated_at: iso(1),
  progress: { takes_total: 7, takes_done: 3, takes_failed: 1, takes_running: 3 },
  shots: [
    shot("s1", 1, "价格开场：老板报价", "老板一手拿起梭子蟹对着镜头，嘴型说话，镜头轻微手持晃动", { selected_take_id: "t1a", takes: [take("t1a", "succeeded", "replicate-take-a.mp4"), take("t1b", "succeeded", "replicate-take-b.mp4")] }),
    shot("s2", 2, "后厨爆炒特写", "铁锅大火翻炒，火苗窜起，酱汁裹满蟹壳，镜头缓慢推近", { takes: [take("t2a", "succeeded", "replicate-take-c.mp4"), take("t2b", "failed", "", "画面里出现了人脸变形，已自动判为失败")] }),
    shot("s3", 3, "出锅装盘", "成品装盘，筷子夹起一块蟹钳，拉出酱汁", { takes: [take("t3a", "running"), take("t3b", "queued")] }),
    shot("s4", 4, "门头与价格牌", "镜头从价格牌摇到门头招牌，路人进店", { first_frame_status: "generating", first_frame_url: "", takes: [take("t4a", "running")] }),
    shot("s5", 5, "结尾：到店指引", "", { first_frame_status: "none", first_frame_url: "", first_frame_source: "" }),
  ],
};
const stitched = {
  ...generating, id: P2, title: "日照码头清晨 · 复刻", status: "stitched", final_url: `/api/replicate/${P2}/final?v=2`, final_duration_s: 18, qc_id: "q1",
  progress: { takes_total: 6, takes_done: 6, takes_failed: 0, takes_running: 0 },
  shots: [
    shot("s1", 1, "清晨渔船靠岸", "渔船缓缓靠岸，海鸥飞过", { selected_take_id: "t1a", takes: [take("t1a", "succeeded", "replicate-take-b.mp4"), take("t1b", "succeeded", "replicate-take-a.mp4")] }),
    shot("s2", 2, "卸货与挑选", "渔民把一筐梭子蟹抬上码头", { selected_take_id: "t2a", takes: [take("t2a", "succeeded", "replicate-take-c.mp4")] }),
    shot("s3", 3, "大排档上桌", "整桌海鲜上桌，热气升腾", { selected_take_id: "t3a", takes: [take("t3a", "succeeded", "replicate-take-a.mp4")] }),
  ],
};
const draftProject = { ...generating, id: P3, title: "石臼市场避雷 · 复刻", status: "draft", progress: { takes_total: 0, takes_done: 0, takes_failed: 0, takes_running: 0 }, shots: [], product: "" };

const qc = {
  id: "q1", source: "replicate", source_id: P2, title: "日照码头清晨 · 复刻", product: "梭子蟹", model: "gemini-3.1-pro", status: "completed", duration_s: 18, width: 720, height: 1280, frame_count: 12,
  verdict: "warn", score: 74, error: "", created_at: iso(3), updated_at: iso(2),
  report: { summary: "整体可用。第 2 个镜头里渔民右手有一帧多出一根手指，建议单独重新生成这个镜头；招牌文字轻微模糊。", checked: ["hands", "body", "face", "product", "text", "artifact", "watermark", "quality"], issues: [
    { frame_index: 4, time_s: 7.5, category: "hands", severity: "high", description: "渔民右手出现六根手指", suggestion: "重新生成镜头 2，运动提示词里减少手部特写", shot_position: 2 },
    { frame_index: 7, time_s: 12.5, category: "product", severity: "medium", description: "盘中一只梭子蟹少了一条腿", suggestion: "在「商品保持」里写明八条腿两只大钳", shot_position: 3 },
    { frame_index: 10, time_s: 16.5, category: "text", severity: "low", description: "背景招牌文字有轻微乱码", suggestion: "", shot_position: 3 },
  ] },
  frames: Array.from({ length: 12 }, (_, i) => ({ index: i + 1, time_s: 0.75 + i * 1.5, url: `/api/video-qc/q1/frames/${i + 1}` })),
};
const qcUpload = { ...qc, id: "q2", source: "upload", source_id: "", title: "梭子蟹爆炒_H3生成.mp4", verdict: "pass", score: 92, created_at: iso(60),
  report: { summary: "没有发现明显问题，可以发布。", checked: qc.report.checked, issues: [] } };
const qcRunning = { ...qc, id: "q3", source: "upload", title: "皮皮虾开箱_Grok.mp4", status: "running", verdict: "", score: 0, report: null, frames: [], created_at: iso(0) };

const providers = { default_provider: "runninghub_h3", providers: [
  { id: "runninghub_h3", label: "MiniMax H3（RunningHub）", note: "质量好 · 每条约 3 分钟", available: true, keys: 3, durations: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10], default_duration: 6 },
  { id: "grok", label: "Grok Video", note: "快 · 便宜 · 480p", available: true, durations: [6, 10, 15], default_duration: 6 },
] };
const imageModels = { default_model: "grok-imagine-image-2.0", key_configured: true, key_source: "image_text", models: [
  { id: "grok-imagine-image-2.0", label: "Grok Imagine 2.0", note: "细节最好", available: true },
  { id: "grok-imagine-image", label: "Grok Imagine", note: "快", available: true },
] };
const remixProjects = [
  { id: "c1d2e3f4-0000-4000-8000-000000000001", title: "在日照岚山怼一顿海鲜小炒", account_id: "acc1", stage: "mixing", updated_at: iso(20) },
  { id: "c1d2e3f4-0000-4000-8000-000000000002", title: "家庭存款的变化，普通人该怎么看", account_id: "acc2", stage: "assets", updated_at: iso(200) },
  { id: "c1d2e3f4-0000-4000-8000-000000000003", title: "资产配置：先看风险，再看回报", account_id: "acc2", stage: "published", updated_at: iso(2000) },
  { id: "c1d2e3f4-0000-4000-8000-000000000004", title: "日照码头海鲜采购时间", account_id: "acc1", stage: "review", updated_at: iso(90) },
];

async function mockConsole(page: Page, state: { projects?: unknown[] } = {}) {
  const calls: Array<{ method: string; path: string; body: unknown }> = [];
  await page.route("**/api/**", async route => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const method = route.request().method();
    if (!path.startsWith("/api/")) { await route.continue(); return; }
    const raw = route.request().postData();
    let sent: unknown = null;
    try { sent = raw ? JSON.parse(raw) : null; } catch { sent = raw; }
    calls.push({ method, path, body: sent });
    const media = (type: string, file: string) => route.fulfill({ status: 200, contentType: type, body: fixture(file) });
    const takeVideo = path.match(/^\/api\/replicate\/takes\/(\w+)\/video$/);
    if (takeVideo) {
      const all = [...generating.shots, ...stitched.shots].flatMap(item => item.takes);
      await media("video/mp4", all.find(item => item.id === takeVideo[1])?.url.split("#")[1] || "replicate-take-a.mp4");
      return;
    }
    const frame = path.match(/^\/api\/replicate\/shots\/(\w+)\/first-frame$/);
    if (frame && method === "GET") { await media("image/jpeg", frameFile[frame[1]] || "cover4.jpg"); return; }
    if (/^\/api\/replicate\/[\w-]+\/final$/.test(path)) { await media("video/mp4", "replicate-final.mp4"); return; }
    const qcFrame = path.match(/^\/api\/video-qc\/\w+\/frames\/(\d+)$/);
    if (qcFrame) { await media("image/jpeg", ["cover5.jpg", "studio-port-0.jpg", "cover3.jpg", "cover6.jpg"][Number(qcFrame[1]) % 4]); return; }
    if (/^\/api\/video-qc\/\w+\/video$/.test(path)) { await media("video/mp4", "replicate-final.mp4"); return; }

    let body: unknown = {};
    let status = 200;
    if (path === "/api/auth/me") body = { csrfToken: "t" };
    else if (path === "/api/accounts") body = [{ id: "acc1", name: "岚山海鲜号" }, { id: "acc2", name: "认知漫步" }];
    else if (path === "/api/projects") body = state.projects ?? remixProjects;
    else if (path === "/api/tasks") body = [];
    else if (path === "/api/settings") body = { public: { model_options: "gemini-3.1-pro\nclaude-opus-5-5\ngpt-5.6-sol", replicate_default_provider: "runninghub_h3", replicate_default_variants: 2, runninghub_per_key_concurrency: 1 }, secrets: { runninghub_api_keys: { configured: true } }, configured_public: {}, active_public: {}, restart_required: false };
    else if (path === "/api/runtime") body = { Limit: 2, Running: 0, Queued: 0 };
    else if (path === "/api/mcp/token") body = { token: "fixture-token-0123456789abcdef", endpoint: "http://127.0.0.1:2030/mcp", claude: "claude mcp add --transport http jiying-console http://127.0.0.1:2030/mcp --header \"Authorization: Bearer <token>\"", codex: "[mcp_servers.jiying-console]\nurl = \"http://127.0.0.1:2030/mcp\"", json: "{}" };
    else if (path === "/api/remix-lab/experiments") body = [{ id: "814ebfde-7470-418a-a703-a33596f7e8fe", title: "在日照岚山怼一顿海鲜小炒", status: "completed", updated_at: iso(30) }];
    else if (path === "/api/remix-lab/defaults") body = { presets: [], remix_model: "claude-opus-5-5", remix_base_url: "", remix_reasoning_effort: "", remix_api_key_configured: false };
    else if (path === "/api/remix-lab/prompts") body = { prompts: [] };
    else if (path === "/api/remix-lab/workflow") body = { version: 1, name: "原文到定稿", nodes: [
      { id: "source", type: "input", title: "对标原文", x: 0, y: 0, config: {} },
      { id: "writer", type: "writer", title: "写手", x: 300, y: 0, config: {} },
      { id: "reviewer", type: "reviewer", title: "审稿", x: 600, y: 0, config: {} },
      { id: "final", type: "output", title: "定稿与发布包", x: 900, y: 0, config: {} },
    ], edges: [["source", "writer"], ["writer", "reviewer"], ["reviewer", "final"]] };
    else if (path === "/api/replicate/providers") body = providers;
    else if (path === "/api/image-studio/models") body = imageModels;
    else if (path === "/api/replicate" && method === "GET") body = [generating, stitched, draftProject];
    else if (path === "/api/replicate" && method === "POST") { body = draftProject; status = 201; }
    else if (path === `/api/replicate/${P1}`) body = generating;
    else if (path === `/api/replicate/${P2}`) body = stitched;
    else if (path === `/api/replicate/${P3}`) body = draftProject;
    else if (path === `/api/replicate/${P1}/generate`) { body = { project: generating, skipped: [] }; status = 202; }
    else if (path === `/api/replicate/${P1}/first-frames`) { body = generating; status = 202; }
    else if (path.startsWith("/api/replicate/takes/") && path.endsWith("/select")) body = generating;
    else if (path === "/api/references") body = [{ id: "r1", title: "在日照岚山怼一顿海鲜小炒，舒坦", caption: "" }];
    else if (path === "/api/reference-analyses") body = [{ id: "a1", status: "completed", created_at: iso(40), product: "岚山海鲜大排档" }];
    else if (path === "/api/video-qc" && method === "GET") body = [qcRunning, qc, qcUpload];
    else if (path === "/api/video-qc/q1") body = qc;
    else if (path === "/api/video-qc/q2") body = qcUpload;
    else if (path === "/api/video-qc/q3") body = qcRunning;
    await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
  });
  return calls;
}

async function noPageOverflow(page: Page) {
  const sizes = await page.evaluate(() => ({ width: document.documentElement.clientWidth, content: document.documentElement.scrollWidth }));
  expect(sizes.content).toBeLessThanOrEqual(sizes.width);
}
async function mediaReady(page: Page) {
  await page.waitForFunction(() => [...document.querySelectorAll("main img")].filter(img => !img.closest("details:not([open])")).every(img => (img as HTMLImageElement).complete && (img as HTMLImageElement).naturalWidth > 0)
    && [...document.querySelectorAll("main video")].every(video => (video as HTMLVideoElement).readyState >= 2));
}
const themed = async (page: Page, theme: "light" | "dark") => page.addInitScript(value => window.localStorage.setItem("video-production-console-theme", value), theme);

for (const theme of ["light", "dark"] as const) {
  test(`replicate project list and a generating workspace (${theme})`, async ({ page }) => {
    await mockConsole(page);
    await themed(page, theme);
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto("/replicate");
    await expect(page.getByRole("heading", { name: "AI 复刻", level: 1 })).toBeVisible();
    await expect(page.getByRole("link", { name: /AI 复刻/ })).toHaveAttribute("aria-current", "page");
    await expect(page.getByRole("button", { name: /岚山海鲜小炒 · 复刻/ })).toBeVisible();
    await expect(page.getByText("视频 3/7")).toBeVisible();
    await noPageOverflow(page);
    await page.screenshot({ path: `${shotsDir}/list-${theme}.png` });

    await page.getByRole("button", { name: /岚山海鲜小炒 · 复刻/ }).click();
    await expect(page).toHaveURL(new RegExp(`/replicate/${P1}$`));
    await expect(page.getByRole("button", { name: "全部生成（3 镜头 × 2 版 = 6 条）" })).toBeEnabled();
    await expect(page.getByRole("button", { name: "补齐首帧（1）" })).toBeEnabled();
    const shot2 = page.getByRole("listitem", { name: "镜头 2" });
    await expect(shot2.getByText("画面里出现了人脸变形，已自动判为失败")).toBeVisible();
    await expect(page.getByRole("listitem", { name: "镜头 5" }).getByRole("button", { name: "生成视频" })).toBeDisabled();
    await expect(page.getByRole("button", { name: "拼接成片（已选 1/5）" })).toBeDisabled();
    await mediaReady(page);
    await noPageOverflow(page);
    await page.screenshot({ path: `${shotsDir}/workspace-${theme}.png`, fullPage: true });
  });
}

test("bulk generation and a single-shot retry send the right shots and variants", async ({ page }) => {
  const calls = await mockConsole(page);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(`/replicate/${P1}`);
  await page.getByRole("button", { name: "全部生成（3 镜头 × 2 版 = 6 条）" }).click();
  await expect(page.getByRole("status").filter({ hasText: "已同时提交" })).toBeVisible();
  const shot2 = page.getByRole("listitem", { name: "镜头 2" });
  await shot2.getByLabel("镜头 2 出几版").selectOption("3");
  await shot2.getByRole("button", { name: "再生成" }).click();
  await expect.poll(() => calls.filter(call => call.path.endsWith("/generate")).map(call => call.body)).toEqual([
    { shot_ids: ["s1", "s2", "s3"], variants: 2 },
    { shot_ids: ["s2"], variants: 3 },
  ]);
  await shot2.getByRole("button", { name: "提示词" }).click();
  await expect(shot2.getByLabel("运动提示词")).toHaveValue(/铁锅大火翻炒/);
  await expect(shot2.getByLabel("商品保持")).toHaveValue(/八条腿两只大钳/);
  await page.screenshot({ path: `${shotsDir}/workspace-prompts-open.png` });
});

for (const theme of ["light", "dark"] as const) {
  test(`stitched film with an AI quality report (${theme})`, async ({ page }) => {
    await mockConsole(page);
    await themed(page, theme);
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto(`/replicate/${P2}`);
    await expect(page.getByRole("button", { name: "重新拼接" })).toBeEnabled();
    await expect(page.getByRole("button", { name: "重新质检" })).toBeEnabled();
    const issues = page.getByRole("list", { name: "问题清单" });
    await expect(issues.getByRole("listitem")).toHaveCount(3);
    await expect(issues.getByText("严重")).toBeVisible();
    await expect(page.getByText("有瑕疵")).toBeVisible();
    await page.locator("#rp-final-title").scrollIntoViewIfNeeded();
    await mediaReady(page);
    await noPageOverflow(page);
    await page.locator(".rp-final").screenshot({ path: `${shotsDir}/final-qc-${theme}.png` });
  });
}

test("video QC tab lists checks and shows the selected report with its video", async ({ page }) => {
  await mockConsole(page);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/replicate#qc");
  await expect(page.getByRole("tab", { name: "视频质检" })).toHaveAttribute("aria-selected", "true");
  const history = page.getByRole("complementary", { name: "质检记录" });
  await expect(history.getByText("检查中")).toBeVisible();
  await history.getByRole("button", { name: /^日照码头清晨/ }).click();
  await expect(page.getByRole("region", { name: "质检结果" }).locator("video")).toHaveAttribute("src", "/api/video-qc/q1/video");
  await expect(page.getByRole("button", { name: "开始质检" })).toBeDisabled();
  await mediaReady(page);
  await noPageOverflow(page);
  await page.screenshot({ path: `${shotsDir}/qc-tab.png`, fullPage: true });
});

test("starting from an analysis opens the new project dialog preset", async ({ page }) => {
  const calls = await mockConsole(page);
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto("/replicate?analysis=a1");
  const dialog = page.getByRole("dialog", { name: "新建复刻项目" });
  await expect(dialog.getByLabel("选择拆解")).toHaveValue("a1");
  await expect(dialog.getByLabel("我的产品或场景")).toHaveValue("岚山海鲜大排档");
  await dialog.getByRole("radio", { name: "3:4" }).click();
  await page.screenshot({ path: `${shotsDir}/new-project.png` });
  await dialog.getByRole("button", { name: "用这份拆解开始" }).click();
  await expect(page).toHaveURL(new RegExp(`/replicate/${P3}$`));
  expect(calls.find(call => call.method === "POST" && call.path === "/api/replicate")?.body).toEqual({ reference_id: "r1", analysis_id: "a1", product: "岚山海鲜大排档", ratio: "3:4" });
});

test("the replicate workspace stacks on a narrow window", async ({ page }) => {
  await mockConsole(page);
  await page.setViewportSize({ width: 820, height: 1100 });
  await page.goto(`/replicate/${P1}`);
  await expect(page.getByRole("listitem", { name: "镜头 1" })).toBeVisible();
  await mediaReady(page);
  await noPageOverflow(page);
  await page.screenshot({ path: `${shotsDir}/workspace-narrow.png`, fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole("listitem", { name: "镜头 1" })).toBeVisible();
  await noPageOverflow(page);
  await page.screenshot({ path: `${shotsDir}/workspace-mobile.png`, fullPage: true });
});

for (const theme of ["light", "dark"] as const) {
  test(`AI access page explains MCP and copies the setup (${theme})`, async ({ page, context }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await mockConsole(page);
    await themed(page, theme);
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto("/ai-access");
    await expect(page.getByRole("heading", { level: 1 })).toContainText("一键接入控制台 MCP");
    await expect(page.getByRole("link", { name: "AI 接入" })).toHaveAttribute("aria-current", "page");
    await expect(page.getByText("fixture-token-0123456789abcdef")).toHaveCount(0);
    await page.getByRole("button", { name: "显示令牌" }).click();
    await expect(page.getByText("fixture-token-0123456789abcdef")).toBeVisible();
    await page.getByRole("button", { name: "隐藏令牌" }).click();
    await page.getByRole("button", { name: "复制 Claude Code 命令" }).click();
    await expect(page.getByRole("button", { name: "已复制" }).first()).toBeVisible();
    await noPageOverflow(page);
    await page.screenshot({ path: `${shotsDir}/ai-access-${theme}.png`, fullPage: true });
  });
}

test("the AI access page fits a phone", async ({ page }) => {
  await mockConsole(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/ai-access");
  await expect(page.getByRole("heading", { name: "三步接入" })).toBeVisible();
  await noPageOverflow(page);
  await page.screenshot({ path: `${shotsDir}/ai-access-mobile.png`, fullPage: true });
});

for (const theme of ["light", "dark"] as const) {
  test(`copy remix page is simplified (${theme})`, async ({ page }) => {
    await mockConsole(page);
    await themed(page, theme);
    await page.setViewportSize({ width: 1440, height: 960 });
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "文案二创", exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: "文案二创" })).toHaveAttribute("aria-current", "page");
    for (const gone of ["撤销", "重做", "提示词库"]) await expect(page.getByRole("button", { name: gone, exact: true })).toHaveCount(0);
    await expect(page.getByRole("list", { name: "文案二创流程" })).toBeVisible();
    await noPageOverflow(page);
    await page.screenshot({ path: `${shotsDir}/copy-${theme}.png` });
    await page.getByRole("button", { name: "模型", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "模型" });
    await expect(dialog.getByRole("group", { name: "写手" })).toBeVisible();
    await expect(dialog.getByRole("group", { name: "审稿" })).toBeVisible();
    await expect(dialog.getByText("高级设置（策划、参考模型、旧版连接）")).toBeVisible();
    await page.screenshot({ path: `${shotsDir}/copy-models-${theme}.png` });
  });
}

test("montage board lists finished copy projects and filters them", async ({ page }) => {
  await mockConsole(page);
  await page.setViewportSize({ width: 1440, height: 960 });
  await page.goto("/projects");
  await expect(page.getByRole("heading", { name: /混剪/, level: 1 })).toBeVisible();
  await expect(page.getByRole("link", { name: "混剪" })).toHaveAttribute("aria-current", "page");
  const board = page.getByRole("region", { name: "混剪项目" });
  await expect(board.getByRole("listitem")).toHaveCount(4);
  await board.getByRole("tab", { name: "已发布" }).click();
  await expect(board.getByRole("listitem")).toHaveCount(1);
  await board.getByRole("tab", { name: "全部" }).click();
  await noPageOverflow(page);
  await page.screenshot({ path: `${shotsDir}/montage-board.png` });
});

test("settings has an AI replicate tab for RunningHub keys", async ({ page }) => {
  await mockConsole(page);
  await page.setViewportSize({ width: 1440, height: 960 });
  await page.goto("/replicate");
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await page.getByRole("tab", { name: "AI 复刻", exact: true }).click();
  await expect(page.getByLabel("默认视频模型")).toHaveValue("runninghub_h3");
  await expect(page.getByLabel("每个镜头默认出几版")).toHaveValue("2");
  await expect(page.getByText(/已配置，输入新内容会整体替换/)).toBeVisible();
  await page.screenshot({ path: `${shotsDir}/settings-replicate.png` });
});
