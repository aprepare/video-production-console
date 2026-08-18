import { expect, test, type Page } from "@playwright/test";

const project = {
  id: "659340f8-31c0-49d8-a92d-a250f5a23c48",
  title: "养老现金流",
  script: "第一句。第二句。",
  image_count: 2,
  ratio: "3:4",
  style: "ledger_investigation",
  custom_style: "",
  concurrency: 2,
  status: "draft",
  created_at: "2026-08-13T00:00:00Z",
  updated_at: "2026-08-13T00:00:00Z",
};

const items = [
  {
    id: "image-item-1",
    project_id: project.id,
    sequence: 1,
    role: "cover",
    source_text: "第一句。",
    title: "第一句",
    prompt: "提示词一",
    status: "pending",
  },
  {
    id: "image-item-2",
    project_id: project.id,
    sequence: 2,
    role: "content",
    source_text: "第二句。",
    title: "第二句",
    prompt: "提示词二",
    status: "pending",
  },
];

const readyItems = items.map((item) => ({
  ...item,
  status: "ready",
  mime_type: "image/png",
  width: 1024,
  height: 1536,
  updated_at: "2026-08-13T00:01:00Z",
}));

const hashtags = "#存款 #财富管理 #思维提升";
const publishingCandidates = Array.from({ length: 5 }, (_, index) => ({
  position: index + 1,
  title: `标题${index + 1}`,
  description: `描述内容${index + 1}。${hashtags}`,
}));

const partialProject = {
  ...project,
  status: "partial",
  run_mode: "quick",
  run_phase: "completed",
  run_status: "completed",
  image_count: 2,
  success_count: 1,
  failure_count: 1,
  image_attempts: 2,
};

const partialItems = [
  { ...readyItems[0], attempt_count: 1 },
  { ...items[1], status: "failed", error_message: "vendor unavailable", attempt_count: 2 },
];

function settingsBody() {
  return {
    public: {
      listen_addr: "127.0.0.1:2030",
      data_root: "C:\\data",
      max_codex_concurrency: 2,
      codex_default_model: "gpt-5.6-sol",
      codex_default_reasoning_effort: "medium",
      baokuan_base_url: "",
      baokuan_mcp_executable: "",
      obsidian_vault: "",
      topic_cards_dir: "",
      grok_base_url: "",
      grok_model: "",
      remix_base_url: "",
      remix_model: "",
      remix_reasoning_effort: "",
      codex_task_project_root: "",
      image_base_url: "http://127.0.0.1:8320/v1",
      image_model: "gpt-image-2",
      image_text_base_url: "http://127.0.0.1:8320/v1",
      image_text_model: "planner-test",
      image_text_reasoning_effort: "medium",
      max_image_concurrency: 18,
      image_generation_attempts: 2,
      default_image_ratio: "3:4",
      default_image_style: "finance_documentary",
      codex_binary_path: "C:\\codex.exe",
      media_index_path: "",
      media_root: "",
      jianying_root: "",
      machine_profile_path: "",
      app_server_enabled: true,
      codex_workspace_roots: [],
      volc_speech_speaker_id: "",
      volc_speech_resource_id: "",
      tts_provider: "aurastd",
      aurastd_base_url: "https://tts.aurastd.com",
      aurastd_model: "speech-2.8-hd",
      aurastd_voice_id: "moss_audio_6b1797c8-2329-11f1-8c29-36c83b29da67",
      aurastd_speed: 1.21,
      aurastd_volume: 1.4,
      aurastd_pitch: 1,
      aurastd_emotion: "",
      aurastd_language_boost: "Chinese",
      aurastd_modify_pitch: 0,
      aurastd_modify_intensity: 5,
      aurastd_modify_timbre: 6,
      aurastd_sound_effects: "",
    },
    settings_version: 1,
    secrets: {
      image_api_key: { configured: true, masked: "********" },
      image_text_api_key: { configured: true, masked: "********" },
    },
  };
}

async function mockImageConsole(page: Page, mode: "advanced" | "quick" = "advanced") {
  let created = false;
  let detailGets = 0;
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
      body = settingsBody();
    } else if (path === "/api/image-projects" && request.method() === "GET") {
      body = created ? [mode === "quick" ? partialProject : project] : [];
    } else if (path === "/api/image-projects/segment-preview" && request.method() === "POST") {
      body = {
        model: "planner-test",
        segments: [
          { sequence: 1, role: "cover", title: "第一句", source_text: "第一句。", rationale: "封面反差" },
          { sequence: 2, role: "content", title: "第二句", source_text: "第二句。", rationale: "正文信息" },
        ],
        publishing_candidates: publishingCandidates,
      };
    } else if (path === "/api/image-projects/quick-generate" && request.method() === "POST") {
      created = true;
      status = 202;
      body = { project_id: project.id, run_status: "running" };
    } else if (path === "/api/image-projects" && request.method() === "POST") {
      created = true;
      status = 201;
      body = { project, items, publishing_candidates: publishingCandidates };
    } else if (path === `/api/image-projects/${project.id}` && request.method() === "GET") {
      if (mode === "quick") {
        created = true;
        detailGets += 1;
        if (detailGets === 1) {
          body = {
            project: { ...project, run_mode: "quick", run_phase: "planning", run_status: "running", image_count: 2, success_count: 0, failure_count: 0, image_attempts: 2 },
            items: [],
            publishing_candidates: publishingCandidates,
          };
        } else if (detailGets === 2) {
          body = {
            project: { ...project, run_mode: "quick", run_phase: "imaging", run_status: "running", image_count: 2, success_count: 1, failure_count: 0, image_attempts: 2 },
            items: [partialItems[0], items[1]],
            publishing_candidates: publishingCandidates,
          };
        } else {
          body = { project: partialProject, items: partialItems, publishing_candidates: publishingCandidates };
        }
      } else {
        body = { project, items, publishing_candidates: publishingCandidates };
      }
    } else if (path === `/api/image-projects/${project.id}/generate` && request.method() === "POST") {
      body = { project: { ...project, status: "ready" }, items: readyItems, publishing_candidates: publishingCandidates };
    } else if (path === `/api/image-projects/${project.id}/download` && request.method() === "GET") {
      await route.fulfill({ status: 200, contentType: "application/zip", body: "PK-test" });
      return;
    } else if (path.includes("/items/") && path.endsWith("/image")) {
      await route.fulfill({ status: 200, contentType: "image/png", body: Buffer.from("89504e470d0a1a0a", "hex") });
      return;
    } else {
      status = 404;
      body = { code: "not_found" };
    }

    await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
  });
}

test("advanced image route keeps the manual segment-preview flow", async ({ page }) => {
  await mockImageConsole(page, "advanced");
  await page.goto("/");

  await page.getByRole("button", { name: "进入图文制作" }).click();
  await expect(page.getByRole("heading", { name: "图文项目", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "高级手动模式" }).click();
  await expect(page).toHaveURL(/\/image-projects\/advanced\/?$/);
  await expect(page.getByLabel("项目名称")).toBeVisible();

  await page.getByLabel("项目名称").fill("养老现金流");
  await page.getByLabel("最终文案").fill("第一句。第二句。");
  await page.getByLabel("建议张数").fill("2");
  await page.getByLabel("图片比例").selectOption("3:4");
  await page.getByLabel("视觉风格").selectOption("ledger_investigation");
  await page.getByLabel("项目并发").selectOption("18");
  await page.getByRole("button", { name: "生成分段建议" }).click();
  await expect(page.getByText("001 封面", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "确认分段并生成提示词" }).click();

  await expect(page.getByRole("heading", { name: "养老现金流" })).toBeVisible();
  await expect(page.getByText("001 封面", { exact: true })).toBeVisible();
  await expect(page.getByText("002", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "生成缺失图片" })).toBeVisible();
  const download = page.getByRole("button", { name: "打包下载" });
  await expect(download).toBeDisabled();
  await page.getByRole("button", { name: "生成缺失图片" }).click();
  await expect(download).toBeEnabled();
  await download.click();
  await expect(page.getByText("图文图片包已开始下载。")).toBeVisible();
  await expect(page.getByText("二创", { exact: true })).toHaveCount(0);
});

test("quick generate recovers after refresh and opens publishing copy with hashtags", async ({ page }) => {
  await mockImageConsole(page, "quick");
  await page.goto("/image-projects");

  await expect(page.getByRole("heading", { name: "图文项目", exact: true })).toBeVisible();
  await expect(page.getByLabel("项目名称")).toHaveCount(0);
  await page.getByLabel("最终文案").fill("第一句。第二句。");
  await page.getByRole("button", { name: "开始生成图片" }).click();

  await expect(page).toHaveURL(new RegExp(`/image-projects/${project.id}/?$`));
  await expect(page.getByRole("heading", { name: "养老现金流" })).toBeVisible();
  await expect(page.getByLabel("生成进度")).toBeVisible();
  await expect(page.getByText("生成失败")).toBeVisible({ timeout: 10_000 });

  await page.reload();
  await expect(page).toHaveURL(new RegExp(`/image-projects/${project.id}/?$`));
  await expect(page.getByRole("heading", { name: "养老现金流" })).toBeVisible();
  await expect(page.getByText("生成失败")).toBeVisible();
  await expect(page.getByRole("button", { name: "重新生成 002" })).toBeVisible();

  const publish = page.getByRole("button", { name: "图文标题及描述" });
  const download = page.getByRole("button", { name: "打包下载" });
  const buttonNames = await page.getByRole("button").allTextContents();
  expect(buttonNames.findIndex((name) => name.includes("图文标题及描述"))).toBeLessThan(buttonNames.findIndex((name) => name.includes("打包下载")));
  await expect(publish).toBeVisible();
  await expect(download).toBeVisible();
  await publish.click();
  await expect(page.getByRole("dialog", { name: "图文标题及描述" })).toBeVisible();
  await expect(page.getByLabel("描述")).toContainText(hashtags);
});
