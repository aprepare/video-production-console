// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { ImageStudio, STUDIO_DRAFT_KEY, generationLabel, relativeTime } from "./ImageStudio";
import type { ImageGeneration, ImageModelsResponse } from "./imageStudioTypes";

const json = (value: unknown, status = 200) => new Response(value === null ? null : JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });

beforeEach(() => { sessionStorage.clear(); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); });

const models: ImageModelsResponse = {
  default_model: "grok-imagine-image-2.0", key_configured: true, key_source: "image_text",
  models: [
    { id: "grok-imagine-image-2.0", label: "Grok Imagine 2.0", note: "细节最好 · 约 15 秒", available: true },
    { id: "grok-imagine-image", label: "Grok Imagine", note: "快 · 约 7 秒", available: true },
  ],
};

const output = (position: number, status: "pending" | "ready" | "failed", extra = {}) => ({
  id: `o${position}`, position, status, mime_type: status === "ready" ? "image/jpeg" : "", width: status === "ready" ? 1024 : 0, height: status === "ready" ? 1536 : 0,
  bytes: 0, error: status === "failed" ? "生图服务繁忙（限流），稍后再试" : "", url: status === "ready" ? `/api/image-studio/outputs/o${position}/image` : "", ...extra,
});

const generation = (extra: Partial<ImageGeneration> = {}): ImageGeneration => ({
  id: "g1", prompt: "小红书封面，旧书桌上的单词本", final_prompt: "小红书封面，旧书桌上的单词本", enhance: false, style: "", custom_style: "",
  model: "grok-imagine-image-2.0", ratio: "3:4", count: 2, status: "completed", error: "", favorite: false,
  created_at: new Date(Date.now() - 5 * 60000).toISOString(), updated_at: new Date().toISOString(),
  outputs: [output(0, "ready"), output(1, "ready")], ...extra,
});

type Handler = (init?: RequestInit) => Response | Promise<Response>;
function routedApi(routes: Record<string, Handler>) {
  return vi.fn(async (path: string, init?: RequestInit) => {
    const key = `${init?.method || "GET"} ${path}`;
    const match = Object.keys(routes).sort((a, b) => b.length - a.length).find((pattern) => key.startsWith(pattern));
    if (!match) throw new Error(`unexpected ${key}`);
    return routes[match](init);
  });
}

test("labels and relative times read naturally", () => {
  expect(generationLabel(generation({ status: "running", outputs: [output(0, "ready"), output(1, "pending")] }))).toBe("已出 1/2");
  expect(generationLabel(generation({ status: "partial", outputs: [output(0, "ready"), output(1, "failed")] }))).toBe("出了 1/2 张");
  expect(generationLabel(generation({ status: "queued" }))).toBe("排队中");
  expect(relativeTime(new Date(Date.now() - 3 * 60000).toISOString())).toBe("3 分钟前");
});

test("submitting the prompt posts the chosen model, ratio and count and shows the new run first", async () => {
  let posted: Record<string, unknown> = {};
  const created = generation({ id: "g9", status: "queued", count: 3, ratio: "9:16", model: "grok-imagine-image", outputs: [output(0, "pending"), output(1, "pending"), output(2, "pending")] });
  const api = routedApi({
    "GET /api/image-studio/models": () => json(models),
    "GET /api/image-studio/generations?": () => json({ items: [generation()], has_more: false }),
    "POST /api/image-studio/generations": (init) => { posted = JSON.parse(String(init?.body)); return json(created, 201); },
    "GET /api/image-studio/generations/g9": () => json(created),
  });
  render(<ImageStudio api={api} />);
  await screen.findByLabelText("出图：小红书封面，旧书桌上的单词本");
  fireEvent.change(screen.getByLabelText("提示词"), { target: { value: "  竖屏封面：海边日落的岚山渔港  " } });
  fireEvent.change(await screen.findByRole("combobox", { name: "图片模型" }), { target: { value: "grok-imagine-image" } });
  fireEvent.click(screen.getByRole("radio", { name: "9:16" }));
  fireEvent.click(screen.getByRole("radio", { name: "3" }));
  fireEvent.click(screen.getByRole("button", { name: "出 3 张图" }));
  await waitFor(() => expect(posted.prompt).toBe("竖屏封面：海边日落的岚山渔港"));
  expect(posted).toMatchObject({ model: "grok-imagine-image", ratio: "9:16", count: 3, enhance: false, final_prompt: "", style: "" });
  const cards = await screen.findAllByRole("article");
  expect(cards[0].getAttribute("aria-label")).toContain("小红书封面");
  expect(within(cards[0]).getAllByText("生成中")).toHaveLength(3);
});

test("the default model is sent as an empty string so settings stay the source of truth", async () => {
  let posted: Record<string, unknown> = {};
  const api = routedApi({
    "GET /api/image-studio/models": () => json(models),
    "GET /api/image-studio/generations?": () => json({ items: [], has_more: false }),
    "POST /api/image-studio/generations": (init) => { posted = JSON.parse(String(init?.body)); return json(generation({ status: "queued", outputs: [output(0, "pending"), output(1, "pending")] }), 201); },
    "GET /api/image-studio/generations/g1": () => json(generation()),
  });
  render(<ImageStudio api={api} />);
  expect(await screen.findByText("还没有出过图")).toBeTruthy();
  fireEvent.change(screen.getByLabelText("提示词"), { target: { value: "一杯咖啡" } });
  fireEvent.click(screen.getByRole("button", { name: "出 2 张图" }));
  await waitFor(() => expect(posted.prompt).toBe("一杯咖啡"));
  expect(posted.model).toBe("");
});

test("enhancing shows an editable expanded prompt and the edited text is what gets generated", async () => {
  let posted: Record<string, unknown> = {};
  const api = routedApi({
    "GET /api/image-studio/models": () => json(models),
    "GET /api/image-studio/generations?": () => json({ items: [], has_more: false }),
    "POST /api/image-studio/enhance": () => json({ final_prompt: "暖色手帐风插画，一只橘猫趴在旧木窗台上，午后斜阳，柔和光影", model: "claude-opus-5-5" }),
    "POST /api/image-studio/generations": (init) => { posted = JSON.parse(String(init?.body)); return json(generation({ status: "queued", enhance: true }), 201); },
    "GET /api/image-studio/generations/g1": () => json(generation({ enhance: true })),
  });
  render(<ImageStudio api={api} />);
  fireEvent.change(screen.getByLabelText("提示词"), { target: { value: "橘猫窗台" } });
  fireEvent.click(screen.getByRole("button", { name: /AI 帮我扩写/ }));
  const expanded = await screen.findByLabelText("扩写后的提示词") as HTMLTextAreaElement;
  expect(expanded.value).toContain("暖色手帐风插画");
  fireEvent.change(expanded, { target: { value: "暖色手帐风插画，一只橘猫趴在窗台，加一束向日葵" } });
  fireEvent.click(screen.getByRole("button", { name: "出 2 张图" }));
  await waitFor(() => expect(posted.enhance).toBe(true));
  expect(posted.final_prompt).toBe("暖色手帐风插画，一只橘猫趴在窗台，加一束向日葵");
  fireEvent.click(screen.getByRole("button", { name: "改回用原话" }));
  expect(screen.queryByLabelText("扩写后的提示词")).toBeNull();
});

test("a failed enhancement explains itself and keeps the original prompt usable", async () => {
  const api = routedApi({
    "GET /api/image-studio/models": () => json(models),
    "GET /api/image-studio/generations?": () => json({ items: [], has_more: false }),
    "POST /api/image-studio/enhance": () => json({ code: "enhance_failed", message: "文本模型暂时不可用" }, 502),
  });
  render(<ImageStudio api={api} />);
  fireEvent.change(screen.getByLabelText("提示词"), { target: { value: "橘猫" } });
  fireEvent.click(screen.getByRole("button", { name: /AI 帮我扩写/ }));
  expect((await screen.findByRole("alert")).textContent).toBe("文本模型暂时不可用");
  expect((screen.getByRole("button", { name: "出 2 张图" }) as HTMLButtonElement).disabled).toBe(false);
});

test("running generations are polled until every image arrives", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  let polls = 0;
  const api = routedApi({
    "GET /api/image-studio/models": () => json(models),
    "GET /api/image-studio/generations?": () => json({ items: [generation({ status: "running", outputs: [output(0, "pending"), output(1, "pending")] })], has_more: false }),
    "GET /api/image-studio/generations/g1": () => {
      polls += 1;
      return json(polls === 1
        ? generation({ status: "running", outputs: [output(0, "ready"), output(1, "pending")] })
        : generation({ status: "partial", error: "有 1 张没出来", outputs: [output(0, "ready"), output(1, "failed")] }));
    },
  });
  render(<ImageStudio api={api} />);
  expect(await screen.findByText("正在出图")).toBeTruthy();
  await act(async () => { await vi.advanceTimersByTimeAsync(2100); });
  expect(await screen.findByText("已出 1/2")).toBeTruthy();
  await act(async () => { await vi.advanceTimersByTimeAsync(2100); });
  expect(await screen.findByText("出了 1/2 张")).toBeTruthy();
  expect(screen.getByText("生图服务繁忙（限流），稍后再试")).toBeTruthy();
  expect(screen.getByRole("button", { name: /补出 1 张/ })).toBeTruthy();
  const settled = polls;
  await act(async () => { await vi.advanceTimersByTimeAsync(4500); });
  expect(polls).toBe(settled);
});

test("favorite, retry and delete update the history card", async () => {
  vi.spyOn(window, "confirm").mockReturnValue(true);
  const partial = generation({ status: "partial", outputs: [output(0, "ready"), output(1, "failed")] });
  const api = routedApi({
    "GET /api/image-studio/models": () => json(models),
    "GET /api/image-studio/generations?": () => json({ items: [partial], has_more: false }),
    "PATCH /api/image-studio/generations/g1": () => json({ ...partial, favorite: true }),
    "POST /api/image-studio/generations/g1/retry": () => json({ ...partial, favorite: true, status: "running", outputs: [output(0, "ready"), output(1, "pending")] }, 202),
    "GET /api/image-studio/generations/g1": () => json({ ...partial, favorite: true, status: "running", outputs: [output(0, "ready"), output(1, "pending")] }),
    "DELETE /api/image-studio/generations/g1": () => json(null, 204),
  });
  render(<ImageStudio api={api} />);
  fireEvent.click(await screen.findByRole("button", { name: "收藏这次出图" }));
  expect(await screen.findByRole("button", { name: "取消收藏这次出图" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /补出 1 张/ }));
  expect(await screen.findByText("已出 1/2")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "删除这次出图" }));
  await waitFor(() => expect(screen.queryByRole("article")).toBeNull());
  expect(window.confirm).toHaveBeenCalledWith("删除这次出图和 1 张图片？删除后找不回来。");
});

test("reusing a run fills the composer with its prompt and parameters", async () => {
  const api = routedApi({
    "GET /api/image-studio/models": () => json(models),
    "GET /api/image-studio/generations?": () => json({ items: [generation({ prompt: "岚山渔港日落", ratio: "4:3", count: 4, model: "grok-imagine-image", style: "red_ink", outputs: [0, 1, 2, 3].map((p) => output(p, "ready")) })], has_more: false }),
  });
  vi.spyOn(window, "scrollTo").mockImplementation(() => undefined);
  render(<ImageStudio api={api} />);
  fireEvent.click(await screen.findByRole("button", { name: /再来一组/ }));
  expect((screen.getByLabelText("提示词") as HTMLTextAreaElement).value).toBe("岚山渔港日落");
  expect(screen.getByRole("radio", { name: "4:3" }).getAttribute("aria-checked")).toBe("true");
  expect(screen.getByRole("radio", { name: "4" }).getAttribute("aria-checked")).toBe("true");
  expect((screen.getByRole("combobox", { name: "图片模型" }) as HTMLSelectElement).value).toBe("grok-imagine-image");
  expect((screen.getByRole("combobox", { name: "风格" }) as HTMLSelectElement).value).toBe("red_ink");
  expect(JSON.parse(sessionStorage.getItem(STUDIO_DRAFT_KEY) || "{}").prompt).toBe("岚山渔港日落");
});

test("the lightbox steps through ready images and offers a download", async () => {
  const api = routedApi({
    "GET /api/image-studio/models": () => json(models),
    "GET /api/image-studio/generations?": () => json({ items: [generation()], has_more: false }),
  });
  render(<ImageStudio api={api} />);
  fireEvent.click(await screen.findByRole("button", { name: "查看第 1 张大图" }));
  const dialog = screen.getByRole("dialog", { name: "大图预览" });
  expect(within(dialog).getByText("1 / 2")).toBeTruthy();
  expect(within(dialog).getByRole("link", { name: /下载这张/ }).getAttribute("href")).toBe("/api/image-studio/outputs/o0/image?download=1");
  fireEvent.keyDown(window, { key: "ArrowRight" });
  expect(within(dialog).getByText("2 / 2")).toBeTruthy();
  fireEvent.keyDown(window, { key: "Escape" });
  expect(screen.queryByRole("dialog")).toBeNull();
});

test("missing image credentials block submission and point to settings", async () => {
  const onOpenSettings = vi.fn();
  const api = routedApi({
    "GET /api/image-studio/models": () => json({ ...models, key_configured: false }),
    "GET /api/image-studio/generations?": () => json({ items: [], has_more: false }),
  });
  render(<ImageStudio api={api} onOpenSettings={onOpenSettings} />);
  expect(await screen.findByText("还没配置生图服务的 Key，出不了图。")).toBeTruthy();
  fireEvent.change(screen.getByLabelText("提示词"), { target: { value: "一杯咖啡" } });
  expect((screen.getByRole("button", { name: "出 2 张图" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "打开设置 → 图片" }));
  expect(onOpenSettings).toHaveBeenCalled();
});

test("server errors on submit are shown in plain words", async () => {
  const api = routedApi({
    "GET /api/image-studio/models": () => json(models),
    "GET /api/image-studio/generations?": () => json({ items: [], has_more: false }),
    "POST /api/image-studio/generations": () => json({ code: "image_not_configured", message: "还没配置生图服务，请到「设置 → 图片」填写" }, 409),
  });
  render(<ImageStudio api={api} />);
  fireEvent.change(screen.getByLabelText("提示词"), { target: { value: "一杯咖啡" } });
  fireEvent.click(screen.getByRole("button", { name: "出 2 张图" }));
  expect((await screen.findByRole("alert")).textContent).toBe("还没配置生图服务，请到「设置 → 图片」填写");
});

test("search and the favorites filter are sent to the history query", async () => {
  const api = routedApi({
    "GET /api/image-studio/models": () => json(models),
    "GET /api/image-studio/generations?": () => json({ items: [], has_more: false }),
  });
  render(<ImageStudio api={api} />);
  await screen.findByText("还没有出过图");
  fireEvent.click(screen.getByRole("button", { name: "只看收藏" }));
  await waitFor(() => expect(api.mock.calls.some(([path]) => String(path).includes("favorite=1"))).toBe(true));
  fireEvent.change(screen.getByLabelText("搜索出图历史"), { target: { value: "封面" } });
  await waitFor(() => expect(api.mock.calls.some(([path]) => String(path).includes("q=%E5%B0%81%E9%9D%A2"))).toBe(true));
  expect(await screen.findByText("没有匹配的出图")).toBeTruthy();
});
