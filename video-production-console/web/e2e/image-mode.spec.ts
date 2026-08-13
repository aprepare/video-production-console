import { expect, test, type Page } from "@playwright/test";

const project = {
  id: "image-project-1",
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

async function mockImageConsole(page: Page) {
  let created = false;
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
      body = {
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
          image_base_url: "http://127.0.0.1:8320/v1",
          image_model: "gpt-image-2",
          image_text_base_url: "http://127.0.0.1:8320/v1",
          image_text_model: "planner-test",
          max_image_concurrency: 18,
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
        },
        settings_version: 1,
        secrets: {
          image_api_key: { configured: true, masked: "********" },
          image_text_api_key: { configured: true, masked: "********" },
        },
      };
    } else if (path === "/api/image-projects" && request.method() === "GET") {
      body = created ? [project] : [];
    } else if (path === "/api/image-projects/segment-preview" && request.method() === "POST") {
      body = {
        model: "planner-test",
        segments: [
          { sequence: 1, role: "cover", title: "第一句", source_text: "第一句。", rationale: "封面反差" },
          { sequence: 2, role: "content", title: "第二句", source_text: "第二句。", rationale: "正文信息" },
        ],
      };
    } else if (path === "/api/image-projects" && request.method() === "POST") {
      created = true;
      status = 201;
      body = { project, items };
    } else if (path === `/api/image-projects/${project.id}`) {
      body = { project, items };
    } else if (path === `/api/image-projects/${project.id}/generate` && request.method() === "POST") {
      body = { project: { ...project, status: "ready" }, items: readyItems };
    } else if (path === `/api/image-projects/${project.id}/download` && request.method() === "GET") {
      await route.fulfill({ status: 200, contentType: "application/zip", body: "PK-test" });
      return;
    } else {
      status = 404;
      body = { code: "not_found" };
    }

    await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
  });
}

test("image mode creates an ordered project without entering remix", async ({ page }) => {
  await mockImageConsole(page);
  await page.goto("/");

  await page.getByRole("button", { name: "图文模式" }).click();
  await expect(page.getByRole("heading", { name: "图文项目", exact: true })).toBeVisible();
  await expect(page.getByText("先看 AI 分段建议", { exact: false })).toBeVisible();

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
