// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import type { ImageProject, ImageProjectDetail, ImageProjectItem } from "../types";
import { ImageModeWorkbench } from "./ImageModeWorkbench";

const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });

const project: ImageProject = {
  id: "img-1",
  title: "养老现金流",
  script: "第一句。第二句。",
  image_count: 2,
  ratio: "3:4",
  style: "ledger_investigation",
  custom_style: "",
  concurrency: 2,
  status: "draft",
  created_at: "",
  updated_at: "",
};

const item = (overrides: Partial<ImageProjectItem>): ImageProjectItem => ({
  id: "i1",
  project_id: project.id,
  sequence: 1,
  source_text: "第一句。",
  title: "第一句",
  prompt: "旧提示词",
  status: "pending",
  ...overrides,
});

const detail = (items: ImageProjectItem[], overrides: Partial<ImageProject> = {}): ImageProjectDetail => ({
  project: { ...project, ...overrides },
  items,
});

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

test("uses configured defaults for a new image project", async () => {
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/image-projects" && (!init || !init.method)) return json([]);
    throw new Error(path);
  });
  render(<ImageModeWorkbench api={api} defaultRatio="9:16" defaultStyle="red_ink" defaultConcurrency={5} />);
  expect((await screen.findByLabelText("图片比例") as HTMLSelectElement).value).toBe("9:16");
  expect((screen.getByLabelText("视觉风格") as HTMLSelectElement).value).toBe("red_ink");
  expect((screen.getByLabelText("项目并发") as HTMLSelectElement).value).toBe("5");
});

test("syncs settings defaults that arrive later until the user edits each field", async () => {
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/image-projects" && (!init?.method || init.method === "GET")) return json([]);
    throw new Error(path);
  });
  const view = render(<ImageModeWorkbench api={api} />);
  await screen.findByRole("heading", { name: "图文项目" });

  fireEvent.change(screen.getByLabelText("图片比例"), { target: { value: "4:3" } });
  view.rerender(<ImageModeWorkbench api={api} defaultRatio="9:16" defaultStyle="red_ink" defaultConcurrency={5} />);

  expect((screen.getByLabelText("图片比例") as HTMLSelectElement).value).toBe("4:3");
  expect((screen.getByLabelText("视觉风格") as HTMLSelectElement).value).toBe("red_ink");
  expect((screen.getByLabelText("项目并发") as HTMLSelectElement).value).toBe("5");
});

test("does not let a slow initial list replace a newly created project", async () => {
  let resolveList!: (response: Response) => void;
  const listResponse = new Promise<Response>((resolve) => { resolveList = resolve; });
  const staleProject = { ...project, id: "stale", title: "旧项目", updated_at: "2026-08-13T00:00:00Z" };
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/image-projects" && (!init?.method || init.method === "GET")) return listResponse;
    if (path === "/api/image-projects" && init?.method === "POST") return json(detail([item({})]), 201);
    throw new Error(path);
  });

  render(<ImageModeWorkbench api={api} />);
  fireEvent.change(screen.getByLabelText("项目名称"), { target: { value: project.title } });
  fireEvent.change(screen.getByLabelText("最终文案"), { target: { value: project.script } });
  fireEvent.click(screen.getByRole("button", { name: "创建图文项目" }));
  expect(await screen.findByRole("heading", { name: project.title })).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "返回图文项目" }));
  await act(async () => { resolveList(json([staleProject])); });

  expect(await screen.findByRole("button", { name: /养老现金流/ })).toBeTruthy();
  expect(screen.getByRole("button", { name: /旧项目/ })).toBeTruthy();
});

test("creates a final-copy image project with bounded parameters and ordered cards", async () => {
  let createBody: Record<string, unknown> | null = null;
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/image-projects" && (!init?.method || init.method === "GET")) return json([]);
    if (path === "/api/image-projects" && init?.method === "POST") {
      createBody = JSON.parse(String(init.body));
      return json(detail([
        item({ id: "i1", sequence: 1 }),
        item({ id: "i2", sequence: 2, source_text: "第二句。", title: "第二句", prompt: "p2" }),
      ]), 201);
    }
    throw new Error(path);
  });
  render(<ImageModeWorkbench api={api} />);
  expect(await screen.findByRole("heading", { name: "图文项目" })).toBeTruthy();

  const count = screen.getByLabelText<HTMLInputElement>("图片数量");
  expect(count.min).toBe("1");
  expect(count.max).toBe("60");
  expect(within(screen.getByLabelText("图片比例")).getAllByRole("option").map((option) => option.textContent)).toEqual(["3:4", "4:3", "9:16", "1:1"]);

  const exactScript = "  第一句。\n\n第二句。  ";
  fireEvent.change(screen.getByLabelText("项目名称"), { target: { value: "养老现金流" } });
  fireEvent.change(screen.getByLabelText("最终文案"), { target: { value: exactScript } });
  fireEvent.change(count, { target: { value: "2" } });
  fireEvent.change(screen.getByLabelText("图片比例"), { target: { value: "9:16" } });
  fireEvent.change(screen.getByLabelText("视觉风格"), { target: { value: "custom" } });
  fireEvent.change(screen.getByLabelText("自定义风格"), { target: { value: "木刻版画" } });
  fireEvent.change(screen.getByLabelText("项目并发"), { target: { value: "4" } });
  fireEvent.submit(screen.getByRole("button", { name: "创建图文项目" }).closest("form")!);

  expect(await screen.findByText("001")).toBeTruthy();
  expect(screen.getByText("002")).toBeTruthy();
  expect(createBody).toMatchObject({
    title: "养老现金流",
    script: exactScript,
    image_count: 2,
    ratio: "9:16",
    style: "custom",
    custom_style: "木刻版画",
    concurrency: 4,
  });
  expect(api.mock.calls.some(([path]) => String(path).includes("remix"))).toBe(false);
});

test("keeps gallery order and saves edited source, title and prompt", async () => {
  const savedBodies: Record<string, unknown>[] = [];
  const items = [
    item({ id: "i2", sequence: 2, source_text: "第二句。", title: "第二句", prompt: "p2" }),
    item({ id: "i1", sequence: 1 }),
  ];
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/image-projects" && (!init?.method || init.method === "GET")) return json([project]);
    if (path === "/api/image-projects/img-1" && !init) return json(detail(items));
    if (path.endsWith("/items/i1") && init?.method === "PATCH") {
      const body = JSON.parse(String(init.body));
      savedBodies.push(body);
      return json(detail(items.map((current) => current.id === "i1" ? { ...current, ...body } : current)));
    }
    throw new Error(path);
  });
  render(<ImageModeWorkbench api={api} />);
  fireEvent.click(await screen.findByRole("button", { name: /养老现金流/ }));
  const cards = await screen.findAllByRole("article");
  expect(within(cards[0]).getByText("001")).toBeTruthy();
  expect(within(cards[1]).getByText("002")).toBeTruthy();

  fireEvent.change(within(cards[0]).getByLabelText("图片名称"), { target: { value: "新名称" } });
  fireEvent.change(within(cards[0]).getByLabelText("对应原文"), { target: { value: "新原文" } });
  fireEvent.change(within(cards[0]).getByLabelText("图片 001 提示词"), { target: { value: "新提示词" } });
  fireEvent.click(within(cards[0]).getByRole("button", { name: "保存图片 001" }));

  await waitFor(() => expect(savedBodies).toEqual([{ source_text: "新原文", title: "新名称", prompt: "新提示词" }]));
});

test("saves unsaved item fields before regenerating the image", async () => {
  const calls: string[] = [];
  let currentItems = [item({ status: "ready" })];
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/image-projects" && (!init?.method || init.method === "GET")) return json([project]);
    if (path === "/api/image-projects/img-1" && !init) return json(detail(currentItems));
    if (path.endsWith("/items/i1") && init?.method === "PATCH") {
      calls.push("save");
      currentItems = [{ ...currentItems[0], ...JSON.parse(String(init.body)) }];
      return json(detail(currentItems));
    }
    if (path.endsWith("/items/i1/generate") && init?.method === "POST") {
      calls.push("generate");
      return json(detail(currentItems));
    }
    throw new Error(path);
  });
  render(<ImageModeWorkbench api={api} />);
  fireEvent.click(await screen.findByRole("button", { name: /养老现金流/ }));
  const card = (await screen.findAllByRole("article"))[0];
  fireEvent.change(within(card).getByLabelText("图片 001 提示词"), { target: { value: "未保存的新提示词" } });
  fireEvent.click(within(card).getByRole("button", { name: "重新生成 001" }));

  await waitFor(() => expect(calls).toEqual(["save", "generate"]));
  expect(JSON.parse(String(api.mock.calls.find(([path, init]) => path.endsWith("/items/i1") && init?.method === "PATCH")?.[1]?.body))).toMatchObject({ prompt: "未保存的新提示词" });
});

test("generates missing images, regenerates one and exposes preview, dimensions and zip download", async () => {
  let currentItems: ImageProjectItem[] = [item({})];
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/image-projects" && (!init?.method || init.method === "GET")) return json([project]);
    if (path === "/api/image-projects/img-1" && !init) return json(detail(currentItems));
    if (path.endsWith("/items/i1") && init?.method === "PATCH") return json(detail(currentItems));
    if (path.endsWith("/generate") && init?.method === "POST") {
      currentItems = [item({ status: "ready", width: 1080, height: 1440, mime_type: "image/png" })];
      return json(detail(currentItems, { status: "ready" }));
    }
    throw new Error(path);
  });
  render(<ImageModeWorkbench api={api} />);
  fireEvent.click(await screen.findByRole("button", { name: /养老现金流/ }));
  fireEvent.click(await screen.findByRole("button", { name: "生成缺失图片" }));
  await waitFor(() => expect(api).toHaveBeenCalledWith("/api/image-projects/img-1/generate", expect.objectContaining({ method: "POST" })));
  expect(await screen.findByText("实际尺寸：1080×1440")).toBeTruthy();

  const preview = screen.getByRole("button", { name: "预览图片 001" });
  expect(preview.querySelector("img")?.getAttribute("src")).toContain("/items/i1/image");
  preview.focus();
  fireEvent.click(preview);
  const dialog = screen.getByRole("dialog", { name: /^图片 001 预览/ });
  const close = screen.getByRole("button", { name: "关闭图片预览" });
  expect(dialog.getAttribute("aria-labelledby")).toBeTruthy();
  expect(document.activeElement).toBe(close);
  fireEvent.keyDown(document, { key: "Tab", shiftKey: true });
  expect(document.activeElement).toBe(close);
  fireEvent.keyDown(document, { key: "Escape" });
  expect(screen.queryByRole("dialog", { name: /^图片 001 预览/ })).toBeNull();
  expect(document.activeElement).toBe(preview);

  fireEvent.click(screen.getByRole("button", { name: "重新生成 001" }));
  await waitFor(() => expect(api).toHaveBeenCalledWith("/api/image-projects/img-1/items/i1/generate", expect.objectContaining({ method: "POST" })));
  expect((screen.getByRole("button", { name: "打包下载" }) as HTMLButtonElement).disabled).toBe(false);
});

test.each([false, true])("deletes only after confirmation=%s", async (confirmed) => {
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(confirmed);
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/image-projects" && (!init?.method || init.method === "GET")) return json([project]);
    if (path === "/api/image-projects/img-1" && !init) return json(detail([item({})]));
    if (path === "/api/image-projects/img-1" && init?.method === "DELETE") return new Response(null, { status: 204 });
    throw new Error(path);
  });
  render(<ImageModeWorkbench api={api} />);
  fireEvent.click(await screen.findByRole("button", { name: /养老现金流/ }));
  fireEvent.click(await screen.findByRole("button", { name: "删除图文项目" }));

  expect(confirm).toHaveBeenCalledOnce();
  await waitFor(() => {
    const deletes = api.mock.calls.filter(([, init]) => init?.method === "DELETE");
    expect(deletes).toHaveLength(confirmed ? 1 : 0);
  });
  if (confirmed) expect(await screen.findByRole("heading", { name: "图文项目" })).toBeTruthy();
});
