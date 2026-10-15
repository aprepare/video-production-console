// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { MonitorBoard, Sparkline, intervalLabel, timeAgo } from "./MonitorBoard";
import { CommentInsightPanel, insightMarkdown } from "./CommentInsight";
import { PreviewThumb, previewURL } from "./Preview";
import { relativeDay, type ReferencePost } from "./ReferencesPage";
import type { MonitorFeed, MonitorItem, MonitorTask } from "./monitorTypes";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const task = (extra: Partial<MonitorTask> = {}): MonitorTask => ({
  id: "t1", name: "岚山海鲜", platform: "douyin", keyword: "岚山海鲜", interval_minutes: 30, pages: 1, enabled: true, model: "",
  score_enabled: true, score_threshold: 70, score_focus: "", auto_collect: true, auto_transcribe: false, auto_analyze: false, auto_insight: false,
  surge_mode: "rule", surge_like_delta: 500, surge_like_ratio: 0.5, surge_comment_delta: 50, surge_comment_ratio: 0.5, surge_min_base: 20,
  last_run_at: "", next_run_at: "", last_status: "ok", last_error: "", running: false, item_count: 2, unread_count: 1, surge_count: 1,
  created_at: "2026-10-15T00:00:00Z", updated_at: "2026-10-15T00:00:00Z", ...extra,
});

const item = (extra: Partial<MonitorItem> = {}): MonitorItem => ({
  id: "i1", task_id: "t1", task_name: "岚山海鲜", platform: "douyin", external_id: "7681", url: "https://www.douyin.com/video/7681",
  title: "日照本地人都去哪买海鲜", caption: "", author: "日照瞎溜达", cover_url: "https://p3-sign.douyinpic.com/x.webp", video_url: "https://v5.zjcdn.com/v.mp4",
  duration_s: 50.8, posted_at: 0, likes: 2461, comments: 72, shares: 10, collects: 5, first_seen_at: "2026-10-15T00:00:00Z", last_seen_at: "2026-10-15T00:30:00Z",
  seen_count: 2, score_status: "scored", score: 82, score_reason: "价格对比开头，适合商户模仿", score_tags: ["价格对比"], score_error: "",
  surge: true, surge_reason: "30 分钟点赞 +1,240（+62%）", surge_at: "2026-10-15T00:30:00Z", like_delta: 1240, comment_delta: 30, like_rate_per_hour: 2480,
  reference_id: "", read: false, history: [{ at: "a", likes: 1200, comments: 40 }, { at: "b", likes: 2461, comments: 72 }], ...extra,
});

const feed = (items: MonitorItem[]): MonitorFeed => ({ items, counts: { all: items.length, surge: items.filter((entry) => entry.surge).length, high: 1, unread: 1 } });
const ok = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

function routedApi(routes: Record<string, (init?: RequestInit) => Response>) {
  return vi.fn(async (path: string, init?: RequestInit) => {
    const method = init?.method || "GET";
    const key = Object.keys(routes).find((pattern) => `${method} ${path}`.startsWith(pattern));
    if (!key) throw new Error(`unexpected ${method} ${path}`);
    return routes[key](init);
  });
}

test("helpers format intervals, ages and publish days", () => {
  expect(intervalLabel(30)).toBe("30 分钟");
  expect(intervalLabel(120)).toBe("2 小时");
  expect(intervalLabel(1440)).toBe("每天");
  expect(timeAgo("2026-10-15T00:00:00Z", Date.parse("2026-10-15T00:45:00Z"))).toBe("45 分钟前");
  expect(relativeDay(Date.parse("2026-10-12T00:00:00Z") / 1000, Date.parse("2026-10-15T01:00:00Z"))).toBe("3 天前");
  expect(previewURL("xhs", "cover", "/api/img?u=a")).toBe("/api/references/preview?platform=xhs&kind=cover&url=%2Fapi%2Fimg%3Fu%3Da");
});

test("video thumbnails play on click while image notes stay inert", () => {
  const onPlay = vi.fn();
  const { rerender } = render(<PreviewThumb platform="douyin" coverURL="https://p3.douyinpic.com/a.webp" videoURL="https://v.zjcdn.com/a.mp4" durationS={65} title="海鲜" onPlay={onPlay} />);
  expect(screen.getByText("1:05")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "播放：海鲜" }));
  expect(onPlay).toHaveBeenCalledTimes(1);
  rerender(<PreviewThumb platform="xhs" coverURL="" videoURL="" durationS={0} title="图文" onPlay={onPlay} />);
  expect(screen.queryByRole("button")).toBeNull();
  expect(screen.getByText("图文")).toBeTruthy();
});

test("sparkline describes the like trend", () => {
  render(<Sparkline history={[{ at: "a", likes: 100, comments: 0 }, { at: "b", likes: 1500, comments: 0 }]} />);
  expect(screen.getByRole("img").getAttribute("aria-label")).toBe("点赞走势：100 → 1500");
});

test("the board lists tasks and puts surging items first with their reason and score", async () => {
  const api = routedApi({
    "GET /api/monitors/feed": () => ok(feed([item()])),
    "GET /api/monitors": () => ok([task()]),
  });
  render(<MonitorBoard api={api} disabled={false} onMessage={vi.fn()} onOpenReference={vi.fn()} />);
  expect(await screen.findByText("30 分钟点赞 +1,240（+62%）")).toBeTruthy();
  expect(screen.getByLabelText("AI 评分 82")).toBeTruthy();
  expect(screen.getByText("价格对比开头，适合商户模仿")).toBeTruthy();
  const tasks = screen.getByRole("region", { name: "监控任务" });
  expect(within(tasks).getByText("岚山海鲜")).toBeTruthy();
  expect(within(tasks).getByText("1 未读")).toBeTruthy();
});

test("creating a monitor posts the draft and selects the new task", async () => {
  const created = task({ id: "t9", name: "日照绿茶", keyword: "日照绿茶", running: true });
  let posted: Record<string, unknown> = {};
  const api = routedApi({
    "GET /api/monitors/feed": () => ok(feed([])),
    "POST /api/monitors": (init) => { posted = JSON.parse(String(init?.body)); return ok(created, 201); },
    "GET /api/monitors": () => ok([]),
  });
  const onMessage = vi.fn();
  render(<MonitorBoard api={api} disabled={false} onMessage={onMessage} onOpenReference={vi.fn()} />);
  fireEvent.click((await screen.findAllByRole("button", { name: /新建/ }))[0]);
  const dialog = screen.getByRole("dialog", { name: "新建监控" });
  fireEvent.change(within(dialog).getByPlaceholderText("例：岚山海鲜"), { target: { value: " 日照绿茶 " } });
  fireEvent.change(within(dialog).getByLabelText("点赞增量阈值"), { target: { value: "800" } });
  fireEvent.change(within(dialog).getByLabelText("点赞增幅阈值"), { target: { value: "30" } });
  fireEvent.click(within(dialog).getByRole("radio", { name: "规则 + AI" }));
  fireEvent.click(within(dialog).getByRole("button", { name: "创建并立即搜一次" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(posted).toMatchObject({ keyword: "日照绿茶", surge_like_delta: 800, surge_like_ratio: 0.3, surge_mode: "both", interval_minutes: 30 });
  expect(onMessage).toHaveBeenCalledWith({ tone: "ok", text: "已创建「日照绿茶」，正在搜第一轮。" });
});

test("an empty keyword is rejected before any request", async () => {
  const api = routedApi({ "GET /api/monitors/feed": () => ok(feed([])), "GET /api/monitors": () => ok([]) });
  render(<MonitorBoard api={api} disabled={false} onMessage={vi.fn()} onOpenReference={vi.fn()} />);
  fireEvent.click((await screen.findAllByRole("button", { name: /新建/ }))[0]);
  fireEvent.click(screen.getByRole("button", { name: "创建并立即搜一次" }));
  expect(screen.getByRole("alert").textContent).toBe("请填写关键词。");
  expect(api.mock.calls.some(([, init]) => (init as RequestInit | undefined)?.method === "POST")).toBe(false);
});

test("collecting a feed item records the reference and a running task blocks a second run", async () => {
  const api = routedApi({
    "GET /api/monitors/feed": () => ok(feed([item()])),
    "POST /api/monitors/items/i1/collect": () => ok({ id: "r7" }),
    "POST /api/monitors/t1/run": () => ok({ code: "monitor_running", message: "这个任务正在运行" }, 409),
    "PATCH /api/monitors/items/i1": () => ok(item({ read: true })),
    "GET /api/monitors": () => ok([task()]),
  });
  const onMessage = vi.fn();
  render(<MonitorBoard api={api} disabled={false} onMessage={onMessage} onOpenReference={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "采集并转写" }));
  expect(await screen.findByRole("button", { name: /打开素材/ })).toBeTruthy();
  expect(onMessage).toHaveBeenCalledWith({ tone: "ok", text: "已采进素材库，口播正在转写。" });
  fireEvent.click(screen.getByRole("button", { name: "立即搜一次：岚山海鲜" }));
  await waitFor(() => expect(onMessage).toHaveBeenCalledWith({ tone: "error", text: "这个任务正在运行" }));
});

test("failed runs show the platform error on the task card", async () => {
  const api = routedApi({
    "GET /api/monitors/feed": () => ok(feed([])),
    "GET /api/monitors": () => ok([task({ last_status: "failed", last_error: "小红书搜索失败：出口被限流，需要换出口" })]),
  });
  render(<MonitorBoard api={api} disabled={false} onMessage={vi.fn()} onOpenReference={vi.fn()} />);
  expect(await screen.findByText("小红书搜索失败：出口被限流，需要换出口")).toBeTruthy();
  expect(screen.getByText("上次失败")).toBeTruthy();
});

const reference = (extra: Partial<ReferencePost> = {}): ReferencePost => ({
  id: "r1", platform: "douyin", external_id: "7695", url: "", title: "海鲜自助怎么吃回本", caption: "", author: "阿虫", cover_url: "", duration_s: 42,
  likes: 1940, comments: 220, shares: 0, collects: 0, plays: 0, keyword: "", merchant_id: "", transcript_status: "completed", transcript: "", transcript_error: "",
  notes: "", created_at: "2026-10-13T00:00:00Z", ...extra,
});

test("comment insight renders opportunities with quoted evidence and exports markdown", async () => {
  const report = {
    summary: "大家最怕被宰秤",
    sentiment: { positive: 50, neutral: 30, negative: 20 },
    pain_points: [{ point: "怕鬼秤", detail: "外地游客不会挑", mentions: 12, quotes: ["上次买三斤回去称只有两斤"] }],
    opportunities: [{ title: "公开过秤", why: "信任是第一顾虑", action: "每条视频固定拍电子秤读数", quotes: ["有没有不坑人的店"] }],
    content_ideas: [{ title: "当面复称", hook: "我拿自己的秤去市场", angle: "实测" }],
  };
  const api = routedApi({
    "GET /api/comment-insights?reference_id=r1": () => ok([{ id: "c1", reference_id: "r1", status: "completed", model: "m", comment_count: 180, error: "", created_at: "2026-10-15T00:00:00Z", updated_at: "" }]),
    "GET /api/comment-insights/c1": () => ok({ id: "c1", reference_id: "r1", status: "completed", model: "m", comment_count: 180, error: "", created_at: "2026-10-15T00:00:00Z", updated_at: "", report }),
  });
  render(<CommentInsightPanel api={api} post={reference()} onMessage={vi.fn()} />);
  expect(await screen.findByText("公开过秤")).toBeTruthy();
  expect(screen.getByText("基于 180 条评论")).toBeTruthy();
  expect(screen.getByText("上次买三斤回去称只有两斤")).toBeTruthy();
  expect(screen.getByText("12 人提到")).toBeTruthy();
  const markdown = insightMarkdown(reference(), report);
  expect(markdown).toContain("## 机会点");
  expect(markdown).toContain("  > 有没有不坑人的店");
});

test("starting an insight posts the chosen model and reports failures", async () => {
  const api = routedApi({
    "GET /api/comment-insights?reference_id=r1": () => ok([]),
    "POST /api/comment-insights": () => ok({ code: "insight_failed", message: "评论太少，没法归纳" }, 400),
  });
  const onMessage = vi.fn();
  render(<CommentInsightPanel api={api} post={reference()} onMessage={onMessage} />);
  fireEvent.click(await screen.findByRole("button", { name: /分析评论/ }));
  await waitFor(() => expect(onMessage).toHaveBeenCalledWith({ tone: "error", text: "评论太少，没法归纳" }));
  const call = api.mock.calls.find(([path]) => path === "/api/comment-insights");
  expect(JSON.parse(String((call?.[1] as RequestInit).body))).toEqual({ reference_id: "r1", model: "" });
});
