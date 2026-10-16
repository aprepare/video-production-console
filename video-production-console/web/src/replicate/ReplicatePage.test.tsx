// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ReplicatePage, takeActive } from "./ReplicatePage";
import { VideoQCResult } from "./VideoQC";
import type { ReplicateProject, ReplicateShot, ReplicateTake } from "./replicateTypes";
import type { VideoQCCheck } from "../references/videoQCTypes";

const json = (value: unknown, status = 200) => new Response(value === null ? null : JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); window.history.replaceState(null, "", "/"); });

const take = (id: string, status: ReplicateTake["status"], extra: Partial<ReplicateTake> = {}): ReplicateTake => ({
  id, provider: "runninghub_h3", status, error: "", duration_s: status === "succeeded" ? 6 : 0, width: 720, height: 1280,
  url: status === "succeeded" ? `/api/replicate/takes/${id}/video` : "", cost_note: "", created_at: "2026-10-16T00:00:00Z", ...extra,
});
const shot = (position: number, extra: Partial<ReplicateShot> = {}): ReplicateShot => ({
  id: `s${position}`, position, purpose: position === 1 ? "价格开场" : "后厨特写", source_start_s: (position - 1) * 2.4, source_end_s: position * 2.4, duration_s: 6,
  first_frame_prompt: "竖屏近景，一盘梭子蟹", motion_prompt: "镜头缓慢推近，热气升腾", negative: "多余手指", keep: "八条腿两只大钳",
  first_frame_url: `/api/replicate/shots/s${position}/first-frame?v=1`, first_frame_source: "generated", first_frame_status: "ready", first_frame_error: "",
  selected_take_id: "", takes: [], ...extra,
});
const project = (extra: Partial<ReplicateProject> = {}): ReplicateProject => ({
  id: "11111111-1111-4111-8111-111111111111", title: "海鲜小炒复刻", reference_id: "r1", analysis_id: "a1", product: "岚山海鲜大排档", style_lock: "暖色写实",
  ratio: "9:16", video_provider: "runninghub_h3", image_model: "", variants: 2, status: "draft", final_url: "", final_duration_s: 0, qc_id: "", error: "",
  created_at: "2026-10-16T00:00:00Z", updated_at: "2026-10-16T00:00:00Z", shots: [shot(1), shot(2)],
  progress: { takes_total: 0, takes_done: 0, takes_failed: 0, takes_running: 0 }, ...extra,
});
const providers = { default_provider: "runninghub_h3", providers: [
  { id: "runninghub_h3", label: "MiniMax H3", note: "质量好 · 每条约 3 分钟", available: true, keys: 3, durations: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10], default_duration: 6 },
  { id: "grok", label: "Grok Video", note: "快 · 便宜 · 480p", available: true, durations: [6, 10, 15], default_duration: 6 },
] };

type Handler = (init?: RequestInit) => Response | Promise<Response>;
function routedApi(routes: Record<string, Handler>) {
  return vi.fn(async (path: string, init?: RequestInit) => {
    const key = `${init?.method || "GET"} ${path}`;
    const match = Object.keys(routes).sort((a, b) => b.length - a.length).find((pattern) => key.startsWith(pattern));
    if (!match) throw new Error(`unexpected ${key}`);
    return routes[match](init);
  });
}

test("takeActive treats queued, submitting and running as in progress", () => {
  expect(takeActive(take("t", "queued"))).toBe(true);
  expect(takeActive(take("t", "running"))).toBe(true);
  expect(takeActive(take("t", "succeeded"))).toBe(false);
  expect(takeActive(take("t", "failed"))).toBe(false);
});

test("generating all shots submits every shot with first frames in one request", async () => {
  let body: Record<string, unknown> = {};
  const p = project();
  const api = routedApi({
    "GET /api/replicate/providers": () => json(providers),
    "GET /api/image-studio/models": () => json({ default_model: "grok-imagine-image-2.0", key_configured: true, key_source: "image_text", models: [] }),
    [`GET /api/replicate/${p.id}`]: () => json(p),
    [`POST /api/replicate/${p.id}/generate`]: (init) => {
      body = JSON.parse(String(init?.body));
      return json({ project: project({ status: "generating", progress: { takes_total: 4, takes_done: 0, takes_failed: 0, takes_running: 4 }, shots: [shot(1, { takes: [take("a", "running"), take("b", "running")] }), shot(2, { takes: [take("c", "queued"), take("d", "queued")] })] }), skipped: [] }, 202);
    },
  });
  render(<ReplicatePage api={api} projectID={p.id} onNavigate={vi.fn()} />);
  const button = await screen.findByRole("button", { name: /全部生成（2 镜头 × 2 版 = 4 条）/ });
  fireEvent.click(button);
  await waitFor(() => expect(body).toEqual({ shot_ids: ["s1", "s2"], variants: 2 }));
  expect(await screen.findByText(/已同时提交 4 个视频任务/)).toBeTruthy();
  expect(screen.getAllByText("生成中").length).toBeGreaterThan(0);
});

test("a single shot can be regenerated with a custom number of variants", async () => {
  let body: Record<string, unknown> = {};
  const p = project({ shots: [shot(1, { selected_take_id: "a", takes: [take("a", "succeeded")] }), shot(2)] });
  const api = routedApi({
    "GET /api/replicate/providers": () => json(providers),
    "GET /api/image-studio/models": () => json({ default_model: "x", key_configured: true, key_source: "image_text", models: [] }),
    [`GET /api/replicate/${p.id}`]: () => json(p),
    [`POST /api/replicate/${p.id}/generate`]: (init) => { body = JSON.parse(String(init?.body)); return json({ project: p, skipped: [] }, 202); },
  });
  render(<ReplicatePage api={api} projectID={p.id} onNavigate={vi.fn()} />);
  const row = within(await screen.findByRole("listitem", { name: "镜头 1" }));
  fireEvent.change(row.getByLabelText("镜头 1 出几版"), { target: { value: "3" } });
  fireEvent.click(row.getByRole("button", { name: /再生成/ }));
  await waitFor(() => expect(body).toEqual({ shot_ids: ["s1"], variants: 3 }));
});

test("shots without a first frame cannot generate and the bulk button fills frames first", async () => {
  let frameBody: Record<string, unknown> = {};
  const p = project({ shots: [shot(1), shot(2, { first_frame_status: "none", first_frame_url: "", first_frame_source: "" })] });
  const api = routedApi({
    "GET /api/replicate/providers": () => json(providers),
    "GET /api/image-studio/models": () => json({ default_model: "x", key_configured: true, key_source: "image_text", models: [] }),
    [`GET /api/replicate/${p.id}`]: () => json(p),
    [`POST /api/replicate/${p.id}/first-frames`]: (init) => { frameBody = JSON.parse(String(init?.body)); return json(p, 202); },
  });
  render(<ReplicatePage api={api} projectID={p.id} onNavigate={vi.fn()} />);
  const row2 = within(await screen.findByRole("listitem", { name: "镜头 2" }));
  expect((row2.getByRole("button", { name: /生成视频/ }) as HTMLButtonElement).disabled).toBe(true);
  expect(row2.getByText("还没有首帧")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /补齐首帧（1）/ }));
  await waitFor(() => expect(frameBody).toEqual({ shot_ids: ["s2"] }));
});

test("choosing a take selects it and stitching unlocks once every shot has one", async () => {
  const before = project({ shots: [shot(1, { takes: [take("a", "succeeded"), take("b", "succeeded")], selected_take_id: "a" }), shot(2, { takes: [take("c", "succeeded")], selected_take_id: "" })] });
  const after = project({ status: "ready", shots: [shot(1, { takes: [take("a", "succeeded"), take("b", "succeeded")], selected_take_id: "a" }), shot(2, { takes: [take("c", "succeeded")], selected_take_id: "c" })] });
  let stitched = false;
  const api = routedApi({
    "GET /api/replicate/providers": () => json(providers),
    "GET /api/image-studio/models": () => json({ default_model: "x", key_configured: true, key_source: "image_text", models: [] }),
    [`GET /api/replicate/${before.id}`]: () => json(before),
    "POST /api/replicate/takes/c/select": () => json(after),
    [`POST /api/replicate/${before.id}/stitch`]: () => { stitched = true; return json({ ...after, status: "stitching" }, 202); },
  });
  render(<ReplicatePage api={api} projectID={before.id} onNavigate={vi.fn()} />);
  const stitch = await screen.findByRole("button", { name: /拼接成片（已选 1\/2）/ });
  expect((stitch as HTMLButtonElement).disabled).toBe(true);
  const row2 = within(screen.getByRole("listitem", { name: "镜头 2" }));
  fireEvent.click(row2.getByRole("button", { name: "选这版" }));
  const ready = await screen.findByRole("button", { name: /拼接成片（已选 2\/2）/ });
  expect((ready as HTMLButtonElement).disabled).toBe(false);
  fireEvent.click(ready);
  await waitFor(() => expect(stitched).toBe(true));
});

test("the stitched film can be quality checked with a chosen model", async () => {
  let qcBody: Record<string, unknown> = {};
  const done = project({ status: "stitched", final_url: "/api/replicate/x/final?v=1", final_duration_s: 12, shots: [shot(1, { takes: [take("a", "succeeded")], selected_take_id: "a" })] });
  const api = routedApi({
    "GET /api/replicate/providers": () => json(providers),
    "GET /api/image-studio/models": () => json({ default_model: "x", key_configured: true, key_source: "image_text", models: [] }),
    [`GET /api/replicate/${done.id}`]: () => json(done),
    [`POST /api/replicate/${done.id}/qc`]: (init) => { qcBody = JSON.parse(String(init?.body)); return json({ id: "q1", status: "queued" }, 202); },
  });
  render(<ReplicatePage api={api} projectID={done.id} onNavigate={vi.fn()} />);
  expect(await screen.findByRole("link", { name: /下载成片/ })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /AI 质检/ }));
  await waitFor(() => expect(qcBody).toEqual({ model: "" }));
});

test("quality results list issues by severity and jump to the frame time", () => {
  const check: VideoQCCheck = {
    id: "q1", source: "replicate", source_id: "p1", title: "成片", product: "", model: "gemini", status: "completed", duration_s: 12, width: 720, height: 1280, frame_count: 14,
    verdict: "fail", score: 41, error: "", created_at: "", updated_at: "",
    report: { summary: "第 2 个镜头手部畸形", checked: ["hands", "product"], issues: [
      { frame_index: 3, time_s: 2, category: "hands", severity: "high", description: "右手出现六根手指", suggestion: "重新生成这个镜头", shot_position: 1 },
      { frame_index: 9, time_s: 8, category: "text", severity: "low", description: "招牌文字有轻微乱码", suggestion: "" },
    ] },
    frames: [{ index: 3, time_s: 2, url: "/f/3" }, { index: 9, time_s: 8, url: "/f/9" }],
  };
  const onSeekShot = vi.fn();
  render(<VideoQCResult check={check} onSeekShot={onSeekShot} />);
  expect(screen.getByText("不合格")).toBeTruthy();
  expect(screen.getByText("41 分")).toBeTruthy();
  expect(screen.getByText("严重")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /跳到 00:02.0：右手出现六根手指/ }));
  expect(onSeekShot).toHaveBeenCalledWith(2);
});

test("the list page opens the new project dialog when coming from an analysis", async () => {
  window.history.replaceState(null, "", "/replicate?analysis=a1");
  const api = routedApi({
    "GET /api/replicate?limit": () => json([]),
    "GET /api/references?limit": () => json([{ id: "r1", title: "海鲜小炒", caption: "" }]),
    "GET /api/reference-analyses?reference_id=r1": () => json([{ id: "a1", status: "completed", created_at: "2026-10-16T00:00:00Z", product: "梭子蟹" }]),
    "POST /api/replicate": () => json(project(), 201),
  });
  const onNavigate = vi.fn();
  render(<ReplicatePage api={api} onNavigate={onNavigate} />);
  const dialog = await screen.findByRole("dialog", { name: "新建复刻项目" });
  await act(async () => { await Promise.resolve(); });
  expect((await within(dialog).findByLabelText("我的产品或场景") as HTMLTextAreaElement).value).toBe("梭子蟹");
  fireEvent.click(within(dialog).getByRole("button", { name: "用这份拆解开始" }));
  await waitFor(() => expect(onNavigate).toHaveBeenCalledWith(`/replicate/${project().id}`));
});
