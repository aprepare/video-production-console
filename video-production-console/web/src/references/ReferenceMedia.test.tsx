// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ReferenceMedia } from "./ReferenceMedia";
import { ReferenceAnalysis, analysisMarkdown, type ReferenceAnalysisRecord } from "./ReferenceAnalysis";
import type { ReferencePost } from "./ReferencesPage";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const post = (extra: Partial<ReferencePost> = {}): ReferencePost => ({
  id: "r1", platform: "douyin", external_id: "7695", url: "https://v.douyin.com/x/", title: "海鲜自助怎么吃回本", caption: "", author: "阿虫",
  cover_url: "", duration_s: 42, likes: 1940, comments: 22, shares: 531, collects: 0, plays: 0, keyword: "", merchant_id: "",
  transcript_status: "completed", transcript: "开头……", transcript_error: "", notes: "", created_at: "2026-10-13T00:00:00Z",
  media_status: "ready", media_size: 3 * 1024 * 1024, media_error: "", has_video: true, has_cover: true,
  sentences: [{ start_ms: 0, end_ms: 2800, text: "三百五十八一位的海鲜自助" }, { start_ms: 2800, end_ms: 6100, text: "蓝龙飞蟹堆成山" }],
  ...extra,
});

test("a cached video plays from the console and offers an MP4 download", () => {
  render(<ReferenceMedia api={vi.fn()} post={post()} onChange={vi.fn()} onMessage={vi.fn()} />);
  const video = screen.getByLabelText("对标原视频") as HTMLVideoElement;
  expect(video.getAttribute("src")).toBe("/api/references/r1/media");
  expect(video.getAttribute("poster")).toBe("/api/references/r1/cover");
  expect(screen.getByRole("link", { name: /下载 MP4/ }).getAttribute("href")).toBe("/api/references/r1/media?download=1");
  expect(screen.getByText("3.0 MB")).toBeTruthy();
});

test("clicking a transcript sentence seeks the player to that line", () => {
  render(<ReferenceMedia api={vi.fn()} post={post()} onChange={vi.fn()} onMessage={vi.fn()} />);
  const video = screen.getByLabelText("对标原视频") as HTMLVideoElement;
  vi.spyOn(video, "play").mockResolvedValue(undefined);
  fireEvent.click(screen.getByRole("button", { name: /蓝龙飞蟹堆成山/ }));
  expect(video.currentTime).toBeCloseTo(2.8);
});

test("an uncached video can be re-downloaded and failures explain why", async () => {
  const onChange = vi.fn();
  const api = vi.fn(async () => new Response(JSON.stringify(post({ media_status: "downloading" })), { status: 202 }));
  render(<ReferenceMedia api={api} post={post({ media_status: "failed", media_error: "视频地址已过期，请重新采集" })} onChange={onChange} onMessage={vi.fn()} />);
  expect(screen.getByText("视频地址已过期，请重新采集")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /重新下载/ }));
  await waitFor(() => expect(onChange).toHaveBeenCalled());
  expect(api).toHaveBeenCalledWith("/api/references/r1/media", { method: "POST" });
});

test("小红书 image notes say there is no video", () => {
  render(<ReferenceMedia api={vi.fn()} post={post({ platform: "xhs", has_video: false, media_status: "none" })} onChange={vi.fn()} onMessage={vi.fn()} />);
  expect(screen.getByText(/这是图文笔记/)).toBeTruthy();
});

const report: ReferenceAnalysisRecord = {
  id: "a1", reference_id: "r1", status: "completed", model: "grok-4.6-fast", focus: "", frame_count: 2, error: "",
  created_at: "2026-10-13T01:00:00Z", updated_at: "2026-10-13T01:02:00Z",
  frames: [{ index: 0, time_s: 0.3, shot: 1, url: "/api/reference-analyses/a1/frames/0" }, { index: 1, time_s: 4.5, shot: 2, url: "/api/reference-analyses/a1/frames/1" }],
  report: {
    summary: "价格反差开场，三秒内亮出招牌",
    hook: { type: "价格反差", first_3s: "报价配满桌海鲜", why_it_works: "数字和画面同时冲击" },
    structure: [{ start_s: 0, end_s: 3, beat: "钩子", what_happens: "报价", purpose: "停留" }, { start_s: 3, end_s: 12, beat: "招牌", what_happens: "逐个展示", purpose: "建立值得感" }],
    replicate: { shot_list: [{ shot: "俯拍满桌", duration_s: 2, how_to_shoot: "手机举高俯拍", line: "人均六十吃到撑" }], script_skeleton: ["【开头：价格反差】"], variables: ["价格", "招牌菜"], avoid: ["原作者的人设"] },
  },
};

function analysisApi(records: ReferenceAnalysisRecord[]) {
  const calls: Array<{ path: string; body?: unknown }> = [];
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    calls.push({ path, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    if (path.startsWith("/api/reference-analyses?")) return new Response(JSON.stringify(records));
    if (path === "/api/setup/models") return new Response(JSON.stringify({ models: [{ id: "grok-4.6-fast" }, { id: "gemini-3.8-flash-high" }] }));
    if (path === "/api/reference-analyses" && init?.method === "POST") return new Response(JSON.stringify({ ...report, id: "a2", status: "queued", report: undefined, frames: undefined }), { status: 202 });
    if (path.startsWith("/api/reference-analyses/")) return new Response(JSON.stringify(records.find((record) => path.endsWith(record.id)) ?? report));
    return new Response("{}", { status: 404 });
  });
  return { api, calls };
}

test("a finished breakdown renders the hook, beats and shooting plan; timestamps seek", async () => {
  const onSeek = vi.fn();
  const { api } = analysisApi([report]);
  render(<ReferenceAnalysis api={api} post={post()} onSeek={onSeek} onMessage={vi.fn()} />);
  expect(await screen.findByText("价格反差开场，三秒内亮出招牌")).toBeTruthy();
  expect(screen.getByText("手机举高俯拍")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "00:03–00:12" }));
  expect(onSeek).toHaveBeenCalledWith(3);
  fireEvent.click(screen.getByRole("button", { name: "跳到 00:04" }));
  expect(onSeek).toHaveBeenCalledWith(4.5);
});

test("starting a breakdown sends the chosen model and focus", async () => {
  const { api, calls } = analysisApi([]);
  render(<ReferenceAnalysis api={api} post={post()} onSeek={vi.fn()} onMessage={vi.fn()} />);
  await screen.findByRole("option", { name: "gemini-3.8-flash-high" });
  fireEvent.change(screen.getByLabelText("拆解模型"), { target: { value: "gemini-3.8-flash-high" } });
  fireEvent.change(screen.getByLabelText("分析重点（可选）"), { target: { value: "重点看字幕" } });
  fireEvent.click(screen.getByRole("button", { name: /开始拆解/ }));
  await waitFor(() => expect(calls.find((call) => call.path === "/api/reference-analyses" && call.body)?.body).toEqual({ reference_id: "r1", model: "gemini-3.8-flash-high", focus: "重点看字幕" }));
});

test("breakdown waits for the local video", async () => {
  const { api } = analysisApi([]);
  render(<ReferenceAnalysis api={api} post={post({ media_status: "downloading" })} onSeek={vi.fn()} onMessage={vi.fn()} />);
  expect(screen.getByRole("button", { name: /开始拆解/ }).hasAttribute("disabled")).toBe(true);
  expect(screen.getByText(/原视频下载到本机后才能拆解/)).toBeTruthy();
});

test("the markdown export carries the shooting plan and what not to copy", () => {
  const markdown = analysisMarkdown(post(), report);
  expect(markdown).toContain("# 拆解：海鲜自助怎么吃回本");
  expect(markdown).toContain("| 俯拍满桌 | 2s | 手机举高俯拍 | 人均六十吃到撑 |");
  expect(markdown).toContain("- 原作者的人设");
  expect(markdown).toContain("00:03–00:12 **招牌**");
});
