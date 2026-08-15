// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { SettingsPanel } from "./SettingsPanel";
import type { PublicSettings, Settings } from "../types";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const draft: PublicSettings = {
  listen_addr: "127.0.0.1:8080",
  data_root: "C:\\data",
  max_codex_concurrency: 2,
  baokuan_base_url: "",
  baokuan_mcp_executable: "",
  obsidian_vault: "",
  topic_cards_dir: "",
  grok_base_url: "",
  grok_model: "",
  remix_base_url: "",
  remix_model: "",
  image_base_url: "http://images.example.test/v1",
  image_model: "gpt-image-2",
  image_text_base_url: "http://text.example.test/v1",
  image_text_model: "planner-test",
  image_text_reasoning_effort: "high",
  max_image_concurrency: 3,
  image_generation_attempts: 2,
  image_stream: false,
  default_image_ratio: "3:4",
  default_image_style: "finance_documentary",
  codex_binary_path: "",
  media_index_path: "",
  media_root: "",
  jianying_root: "",
  machine_profile_path: "",
  app_server_enabled: false,
  codex_workspace_roots: [],
  codex_default_model: "gpt-default",
  codex_default_reasoning_effort: "high",
  volc_speech_speaker_id: "S_volc_speaker",
  volc_speech_resource_id: "seed-icl-2.0",
  media_catalog_path: "C:\\media\\catalog.db",
  ffmpeg_path: "",
  ffprobe_path: "",
  vision_base_url: "https://vision.example.test/v1",
  vision_model: "vision-test",
  embedding_base_url: "",
  embedding_model: "",
  pexels_api_base_url: "https://api.pexels.com",
  pixabay_api_base_url: "https://pixabay.com",
  max_external_results_per_query: 20,
};

test("image stream keepalive toggle is editable and defaults off", () => {
  const { onDraftChange } = renderPanel();
  const toggle = screen.getByRole("checkbox", { name: /生图流式保活/ }) as HTMLInputElement;
  expect(toggle.checked).toBe(false);
  expect(screen.getByText(/仅当兼容服务支持stream:true时开启/)).toBeTruthy();
  fireEvent.click(toggle);
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, image_stream: true });
});

const emptySecretDraft = {
  grok_api_key: "",
  remix_api_key: "",
  pexels_api_key: "",
  volc_speech_api_key: "",
  image_api_key: "",
  image_text_api_key: "",
  vision_api_key: "",
  embedding_api_key: "",
  pixabay_api_key: "",
};

const settings: Settings = {
  public: draft,
  settings_version: 1,
  secrets: {
    grok_api_key: { configured: false, masked: "" },
    remix_api_key: { configured: false, masked: "" },
    pexels_api_key: { configured: false, masked: "" },
    volc_speech_api_key: { configured: true, masked: "********" },
    image_api_key: { configured: false, masked: "" },
    image_text_api_key: { configured: false, masked: "" },
    vision_api_key: { configured: false, masked: "" },
    embedding_api_key: { configured: false, masked: "" },
    pixabay_api_key: { configured: false, masked: "" },
  },
};

function renderPanel(overrides: Partial<Parameters<typeof SettingsPanel>[0]> = {}) {
  const onDraftChange = vi.fn();
  const onSecretDraftChange = vi.fn();
  render(
    <SettingsPanel
      settings={settings}
      draft={draft}
      onDraftChange={onDraftChange}
      secretDraft={{ ...emptySecretDraft }}
      onSecretDraftChange={onSecretDraftChange}
      feedback=""
      onClose={() => {}}
      onSubmit={(event) => event.preventDefault()}
      {...overrides}
    />,
  );
  return { onDraftChange, onSecretDraftChange };
}

test("remix model fields are editable independently", () => {
  const { onDraftChange, onSecretDraftChange } = renderPanel();
  fireEvent.change(screen.getByRole("textbox", { name: "二创服务地址" }), {
    target: { value: "http://127.0.0.1:2001/v1" },
  });
  expect(onDraftChange).toHaveBeenCalledWith({
    ...draft,
    remix_base_url: "http://127.0.0.1:2001/v1",
  });
  fireEvent.change(screen.getByRole("textbox", { name: "二创模型" }), {
    target: { value: "gpt-5.6-sol" },
  });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, remix_model: "gpt-5.6-sol" });
  fireEvent.change(screen.getByLabelText("二创 API 密钥"), {
    target: { value: "new-remix-key" },
  });
  expect(onSecretDraftChange).toHaveBeenCalledWith({
    ...emptySecretDraft,
    remix_api_key: "new-remix-key",
  });
});

test("the Volcengine voice IDs are editable public fields", () => {
  const { onDraftChange } = renderPanel();

  const speaker = screen.getByRole("textbox", { name: "火山音色 ID" });
  const resource = screen.getByRole("textbox", { name: "火山语音资源 ID" });
  expect((speaker as HTMLInputElement).value).toBe("S_volc_speaker");
  expect((resource as HTMLInputElement).value).toBe("seed-icl-2.0");

  fireEvent.change(speaker, { target: { value: "S_other_speaker" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, volc_speech_speaker_id: "S_other_speaker" });
});

test("standalone conversation and local history controls are not shown", () => {
  renderPanel();

  expect(screen.queryByText("启用实时 Codex 对话服务")).toBeNull();
  expect(screen.queryByText("本机历史显示数量")).toBeNull();
  expect(screen.getByText("启用任务实时交互服务")).toBeTruthy();
});

test("the Volcengine API key is masked and only sent when a new value is typed", () => {
  const { onSecretDraftChange } = renderPanel();

  const key = screen.getByLabelText(/火山语音 API Key/) as HTMLInputElement;
  expect(key.type).toBe("password");
  expect(key.value).toBe("");
  expect(screen.getAllByText("已配置，输入新值才会替换")).toHaveLength(1);

  fireEvent.change(key, { target: { value: "new-volc-key" } });
  expect(onSecretDraftChange).toHaveBeenCalledWith({
    ...emptySecretDraft,
    volc_speech_api_key: "new-volc-key",
  });
});

test("image generation settings and secret are editable", () => {
  const { onDraftChange, onSecretDraftChange } = renderPanel();
  const baseURL = screen.getByRole("textbox", { name: "生图服务地址" });
  expect((screen.getByRole("textbox", { name: "生图模型" }) as HTMLInputElement).value).toBe("gpt-image-2");
  fireEvent.change(baseURL, { target: { value: "https://images.example.test/v1" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, image_base_url: "https://images.example.test/v1" });
  fireEvent.change(screen.getByRole("combobox", { name: /同时生成图片数/ }), { target: { value: "18" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, max_image_concurrency: 18 });
  expect((screen.getByRole("combobox", { name: /每张图片最多请求次数/ }) as HTMLSelectElement).value).toBe("2");
  fireEvent.change(screen.getByRole("combobox", { name: /每张图片最多请求次数/ }), { target: { value: "4" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, image_generation_attempts: 4 });
  expect((screen.getByRole("textbox", { name: "图文文本模型" }) as HTMLInputElement).value).toBe("planner-test");
  expect((screen.getByRole("combobox", { name: /图文文本思考强度/ }) as HTMLSelectElement).value).toBe("high");
  fireEvent.change(screen.getByRole("combobox", { name: /图文文本思考强度/ }), { target: { value: "xhigh" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, image_text_reasoning_effort: "xhigh" });
  fireEvent.change(screen.getByRole("combobox", { name: "默认图片比例" }), { target: { value: "9:16" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, default_image_ratio: "9:16" });
  fireEvent.change(screen.getByRole("combobox", { name: "默认视觉风格" }), { target: { value: "red_ink" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, default_image_style: "red_ink" });
  fireEvent.change(screen.getByLabelText(/生图 API Key/), { target: { value: "new-image-key" } });
  expect(onSecretDraftChange).toHaveBeenCalledWith({
    ...emptySecretDraft,
    image_api_key: "new-image-key",
  });
  fireEvent.change(screen.getByLabelText(/图文文本模型 API Key/), { target: { value: "new-text-key" } });
  expect(onSecretDraftChange).toHaveBeenCalledWith({
    ...emptySecretDraft,
    image_text_api_key: "new-text-key",
  });
  expect(screen.getByText(/HTTP 会明文传输二创、Grok、生图或图文文本模型 API Key/)).toBeTruthy();
});

test("the media library settings group exposes catalog, FFmpeg, and analysis fields", () => {
  const { onDraftChange } = renderPanel();

  expect(screen.getByText("素材库", { selector: "h3" })).toBeTruthy();
  const catalogPath = screen.getByRole("textbox", { name: "素材库目录" }) as HTMLInputElement;
  expect(catalogPath.value).toBe("C:\\media\\catalog.db");
  fireEvent.change(catalogPath, { target: { value: "D:\\library\\catalog.db" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, media_catalog_path: "D:\\library\\catalog.db" });

  fireEvent.change(screen.getByRole("textbox", { name: "FFmpeg 路径" }), { target: { value: "C:\\tools\\ffmpeg.exe" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, ffmpeg_path: "C:\\tools\\ffmpeg.exe" });
  fireEvent.change(screen.getByRole("textbox", { name: "FFprobe 路径" }), { target: { value: "C:\\tools\\ffprobe.exe" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, ffprobe_path: "C:\\tools\\ffprobe.exe" });

  expect((screen.getByRole("textbox", { name: "视觉分析服务地址" }) as HTMLInputElement).value).toBe("https://vision.example.test/v1");
  fireEvent.change(screen.getByRole("textbox", { name: "视觉分析模型" }), { target: { value: "vision-next" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, vision_model: "vision-next" });
  fireEvent.change(screen.getByRole("textbox", { name: "向量服务地址" }), { target: { value: "https://embed.example.test/v1" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, embedding_base_url: "https://embed.example.test/v1" });
  fireEvent.change(screen.getByRole("textbox", { name: "向量模型" }), { target: { value: "embed-next" } });
  expect(onDraftChange).toHaveBeenCalledWith({ ...draft, embedding_model: "embed-next" });
});

test("the media library secrets are masked and only sent when typed", () => {
  const { onSecretDraftChange } = renderPanel({
    settings: {
      ...settings,
      secrets: { ...settings.secrets, vision_api_key: { configured: true, masked: "********" } },
    },
  });

  const vision = screen.getByLabelText(/视觉分析 API Key/) as HTMLInputElement;
  expect(vision.type).toBe("password");
  expect(vision.value).toBe("");

  fireEvent.change(vision, { target: { value: "new-vision-key" } });
  expect(onSecretDraftChange).toHaveBeenCalledWith({ ...emptySecretDraft, vision_api_key: "new-vision-key" });
  fireEvent.change(screen.getByLabelText(/向量模型 API Key/), { target: { value: "new-embed-key" } });
  expect(onSecretDraftChange).toHaveBeenCalledWith({ ...emptySecretDraft, embedding_api_key: "new-embed-key" });
  fireEvent.change(screen.getByLabelText(/Pixabay API 密钥/), { target: { value: "new-pixabay-key" } });
  expect(onSecretDraftChange).toHaveBeenCalledWith({ ...emptySecretDraft, pixabay_api_key: "new-pixabay-key" });
});

test("an old settings response without image_base_url remains editable without an HTTP warning", () => {
  const { image_base_url: _imageBaseURL, ...legacyDraft } = draft;

  renderPanel({ draft: legacyDraft as PublicSettings });

  expect((screen.getByRole("textbox", { name: "生图服务地址" }) as HTMLInputElement).value).toBe("");
  expect(screen.queryByText(/HTTP 会以明文传输生图 API Key/)).toBeNull();
});
