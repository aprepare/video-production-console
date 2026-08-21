// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { ImageModeWorkbench } from "./ImageModeWorkbench";

const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
const project = { id: "p", title: "Project", script: "First sentence. Second sentence.", image_count: 2, ratio: "3:4", style: "ledger_investigation", custom_style: "", concurrency: 2, status: "draft", created_at: "2026-01-01", updated_at: "2026-01-01" };
const item = { id: "i1", project_id: "p", sequence: 1, role: "cover", source_text: "First sentence.", title: "Cover", prompt: "Prompt", status: "pending" };
const candidates = Array.from({ length: 5 }, (_, i) => ({ position: i + 1, title: `Title ${i + 1}`, description: `Description ${i + 1}` }));
const detail = (publishing_candidates = candidates) => ({ project, items: [item], publishing_candidates });
const listResponse = (path: string, init?: RequestInit) => path === "/api/image-projects" && !init?.method ? json([]) : undefined;
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe("ImageModeWorkbench", () => {
  test("defaults and preview/create payload include reasoning_effort and image_count", async () => {
    const bodies: any[] = [];
    const api = vi.fn(async (path: string, init?: RequestInit) => {
      const listed = listResponse(path, init); if (listed) return listed;
      if (path.endsWith("segment-preview")) return json({ segments: [{ sequence: 1, role: "cover", title: "A", source_text: "A" }, { sequence: 2, role: "content", title: "B", source_text: "B" }], publishing_candidates: candidates });
      if (init?.method === "POST") { bodies.push(JSON.parse(String(init.body))); return json(detail(), 201); }
      throw new Error(path);
    });
    render(<ImageModeWorkbench api={api} mode="advanced" defaultReasoningEffort="high" defaultImageAttempts={3} />);
    const boxes = screen.getAllByRole("textbox"); fireEvent.change(boxes[0], { target: { value: "Project" } }); fireEvent.change(boxes[1], { target: { value: project.script } });
    fireEvent.change(screen.getAllByRole("spinbutton")[0], { target: { value: "2" } });
    fireEvent.submit(document.querySelector("form")!);
    await screen.findByText(/001/);
    const buttons = screen.getAllByRole("button"); fireEvent.click(buttons[buttons.length - 1]);
    await waitFor(() => expect(bodies.some((b) => b.reasoning_effort === "high" && b.image_count === 2 && b.image_attempts === 3 && b.text_model === "gpt-5.6-sol")).toBe(true));
  });

  test("advanced form defaults the text model and accepts a typed override", async () => {
    const bodies: any[] = [];
    const api = vi.fn(async (path: string, init?: RequestInit) => {
      const listed = listResponse(path, init); if (listed) return listed;
      if (path.endsWith("segment-preview")) {
        bodies.push(JSON.parse(String(init?.body)));
        return json({ segments: [{ sequence: 1, role: "cover", title: "A", source_text: "A" }], publishing_candidates: candidates });
      }
      throw new Error(path);
    });
    render(<ImageModeWorkbench api={api} mode="advanced" />);
    expect((screen.getByLabelText("文本模型") as HTMLSelectElement).value).toBe("gpt-5.6-sol");
    fireEvent.change(screen.getByLabelText("文本模型"), { target: { value: "grok-4.6" } });
    const boxes = screen.getAllByRole("textbox");
    fireEvent.change(boxes[0], { target: { value: "Project" } });
    fireEvent.change(boxes[1], { target: { value: project.script } });
    fireEvent.submit(document.querySelector("form")!);
    await waitFor(() => expect(bodies.some((b) => b.text_model === "grok-4.6")).toBe(true));
  });

  test("404 retry loads once and callback rerender does not refetch", async () => {
    let gets = 0; const api = vi.fn(async (path: string) => { if (path === "/api/image-projects") return json([]); if (path === "/api/image-projects/p") return ++gets === 1 ? json({}, 404) : json(detail()); throw new Error(path); });
    const view = render(<ImageModeWorkbench api={api} initialProjectID="p" onProjectOpen={() => undefined} />);
    await screen.findByRole("button", { name: /重试|閲嶈瘯/ }); fireEvent.click(screen.getByRole("button", { name: /重试|閲嶈瘯/ })); await screen.findByRole("article");
    view.rerender(<ImageModeWorkbench api={api} initialProjectID="p" onProjectOpen={() => undefined} />); await new Promise((r) => setTimeout(r, 10)); expect(gets).toBe(2);
  });

  test("499 without code shows upstream cancellation detail", async () => {
    const api = vi.fn(async (path: string) => path === "/api/image-projects" ? json([]) : json({ message: "upstream detail" }, 499));
    render(<ImageModeWorkbench api={api} mode="advanced" />); const boxes = screen.getAllByRole("textbox"); fireEvent.change(boxes[0], { target: { value: "X" } }); fireEvent.change(boxes[1], { target: { value: "Y" } }); fireEvent.submit(document.querySelector("form")!); await screen.findByText(/upstream detail/);
  });

  test("opens five publishing candidates nested on the project payload", async () => {
    const api = vi.fn(async (path: string, init?: RequestInit) => path === "/api/image-projects/p" ? json({ project: { ...project, publishing_candidates: candidates }, items: [item] }) : path === "/api/image-projects" && !init?.method ? json([]) : json({}));
    render(<ImageModeWorkbench api={api} initialProjectID="p" />);
    await screen.findByRole("article");
    const button = screen.getAllByRole("button").find((b) => b.textContent?.includes("标题") || b.textContent?.includes("鏍囬"))!;
    fireEvent.click(button);
    await screen.findByRole("dialog");
    expect(screen.getAllByRole("radio")).toHaveLength(5);
    expect(screen.queryByText("暂无候选文案")).toBeNull();
  });

  test("publishing toolbar precedes download and opens five radios", async () => {
    const api = vi.fn(async (path: string, init?: RequestInit) => path === "/api/image-projects/p" ? json(detail()) : path === "/api/image-projects" && !init?.method ? json([]) : json({}));
    render(<ImageModeWorkbench api={api} initialProjectID="p" />); await screen.findByRole("article"); const toolbar = screen.getAllByRole("button"); const publish = toolbar.findIndex((b) => b.textContent?.includes("标题") || b.textContent?.includes("鏍囬")); const download = toolbar.findIndex((b) => b.textContent?.includes("下载") || b.textContent?.includes("涓嬭浇")); expect(publish).toBeGreaterThanOrEqual(0); expect(publish).toBeLessThan(download); fireEvent.click(toolbar[publish]); await screen.findByRole("dialog"); expect(screen.getAllByRole("radio")).toHaveLength(5);
  });

  test("23 Unicode code points disable save", async () => {
    const api = vi.fn(async (path: string, init?: RequestInit) => path === "/api/image-projects/p" ? json(detail([{ ...candidates[0], title: "😀".repeat(23) }, ...candidates.slice(1)])) : path === "/api/image-projects" && !init?.method ? json([]) : json({}));
    render(<ImageModeWorkbench api={api} initialProjectID="p" />); await screen.findByRole("article"); const button = screen.getAllByRole("button").find((b) => b.textContent?.includes("标题") || b.textContent?.includes("鏍囬"))!; fireEvent.click(button); await screen.findByRole("dialog"); expect((screen.getByRole("button", { name: /保存当前候选/ }) as HTMLButtonElement).disabled).toBe(true);
  });

  test("empty candidates do not POST until generate is clicked", async () => {
    const api = vi.fn(async (path: string, init?: RequestInit) => path === "/api/image-projects/p" ? json(detail([])) : path === "/api/image-projects" && !init?.method ? json([]) : json({ publishing_candidates: candidates }));
    render(<ImageModeWorkbench api={api} initialProjectID="p" />); await screen.findByRole("article"); const button = screen.getAllByRole("button").find((b) => b.textContent?.includes("标题") || b.textContent?.includes("鏍囬"))!; fireEvent.click(button); await screen.findByRole("dialog"); expect(api.mock.calls.some(([p, i]) => String(p).includes("publishing-candidates/generate") && i?.method === "POST")).toBe(false); fireEvent.click(screen.getByRole("button", { name: /重新生成发布文案/ })); await waitFor(() => expect(api.mock.calls.some(([p, i]) => String(p).includes("publishing-candidates/generate") && i?.method === "POST")).toBe(true));
  });

  test("list click updates parent id and loads that project exactly once", async () => {
    let detailGets = 0;
    const api = vi.fn(async (path: string) => {
      if (path === "/api/image-projects") return json([project]);
      if (path === "/api/image-projects/p") {
        detailGets += 1;
        return json(detail());
      }
      throw new Error(path);
    });
    function Harness() {
      const [id, setId] = useState<string | undefined>();
      return <ImageModeWorkbench api={api} initialProjectID={id} onProjectOpen={setId} />;
    }
    render(<Harness />);
    fireEvent.click(await screen.findByRole("button", { name: /Project/ }));
    await screen.findByRole("article");
    expect(detailGets).toBe(1);
  });

  test("transient 404 does not replace a held successful detail", async () => {
    let gets = 0;
    const makeApi = () => vi.fn(async (path: string) => {
      if (path === "/api/image-projects") return json([]);
      if (path === "/api/image-projects/p") return ++gets === 1 ? json(detail()) : json({}, 404);
      throw new Error(path);
    });
    const view = render(<ImageModeWorkbench api={makeApi()} initialProjectID="p" />);
    await screen.findByRole("article");
    view.rerender(<ImageModeWorkbench api={makeApi()} initialProjectID="p" />);
    await waitFor(() => expect(gets).toBe(2));
    expect(screen.getByRole("article")).toBeTruthy();
    expect(screen.queryByText("图文项目不存在（404）")).toBeNull();
    expect(screen.queryByText("正在读取图文项目…")).toBeNull();
  });

  test("running project polls quietly and updates success_count", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    try {
      let gets = 0;
      const running = { ...project, run_status: "running", success_count: 0, image_count: 4 };
      const progressed = { ...project, run_status: "running", success_count: 3, image_count: 4 };
      const api = vi.fn(async (path: string) => {
        if (path === "/api/image-projects") return json([]);
        if (path === "/api/image-projects/p") return ++gets === 1 ? json({ project: running, items: [item], publishing_candidates: candidates }) : json({ project: progressed, items: [item], publishing_candidates: candidates });
        throw new Error(path);
      });
      render(<ImageModeWorkbench api={api} initialProjectID="p" />);
      await screen.findByRole("article");
      expect(screen.getByText(/成功 0/)).toBeTruthy();
      await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
      await waitFor(() => expect(screen.getByText(/成功 3/)).toBeTruthy());
      expect(screen.queryByText("正在读取图文项目…")).toBeNull();
      expect(screen.getByRole("article")).toBeTruthy();
      expect(gets).toBeGreaterThanOrEqual(2);
    } finally {
      vi.useRealTimers();
    }
  });

  test("detail keeps a single primary action among toolbar buttons", async () => {
    const api = vi.fn(async (path: string) => path === "/api/image-projects/p" ? json(detail()) : path === "/api/image-projects" ? json([]) : json({}));
    const { container } = render(<ImageModeWorkbench api={api} initialProjectID="p" />);
    await screen.findByRole("article");
    expect(container.querySelectorAll(".image-mode-actions .primary")).toHaveLength(1);
  });

  test("create view puts the project list on the right of the workbench", async () => {
    const api = vi.fn(async (path: string) => path === "/api/image-projects" ? json([project]) : json({}));
    const { container } = render(<ImageModeWorkbench api={api} mode="quick" />);
    expect(await screen.findByRole("button", { name: /Project/ })).toBeTruthy();
    const start = container.querySelector(".image-mode-start");
    const list = container.querySelector(".image-project-list");
    expect(start?.lastElementChild).toBe(list);
    expect(start?.className).toContain("image-mode-start--with-list");
  });

  test("quick mode shows the one-click form and keeps the project list", async () => {
    const api = vi.fn(async (path: string) => path === "/api/image-projects" ? json([project]) : json({}));
    render(<ImageModeWorkbench api={api} mode="quick" defaultImageModel="gpt-image-2" defaultImageAttempts={2} />);
    expect(await screen.findByRole("button", { name: /Project/ })).toBeTruthy();
    expect(screen.queryByLabelText(/项目名称/)).toBeNull();
    expect(screen.getByRole("button", { name: "开始生成图片" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "生成分段建议" })).toBeNull();
  });

  test("quick running detail shows the four-stage rail and imaging count", async () => {
    const running = {
      ...project,
      run_mode: "quick",
      run_phase: "imaging",
      run_status: "running",
      image_count: 11,
      success_count: 4,
      failure_count: 0,
    };
    const api = vi.fn(async (path: string) => path === "/api/image-projects/p" ? json({ project: running, items: [item], publishing_candidates: candidates }) : path === "/api/image-projects" ? json([]) : json({}));
    render(<ImageModeWorkbench api={api} initialProjectID="p" />);
    await screen.findByRole("article");
    const rail = screen.getByRole("list", { name: "生成进度" });
    expect(rail.textContent).toContain("分析文案");
    expect(rail.textContent).toContain("生成提示词");
    expect(rail.textContent).toContain("生成图片 4/11");
    expect(rail.textContent).toContain("完成");
  });

  test("failed quick run posts resume", async () => {
    const failed = { ...project, run_mode: "quick", run_phase: "imaging", run_status: "failed", phase_error: "vendor unavailable", image_count: 2, success_count: 1, failure_count: 1 };
    const api = vi.fn(async (path: string, init?: RequestInit) => {
      if (path === "/api/image-projects") return json([]);
      if (path === "/api/image-projects/p" && !init?.method) return json({ project: failed, items: [item], publishing_candidates: candidates });
      if (path === "/api/image-projects/p/resume" && init?.method === "POST") return json({ project_id: "p", run_status: "running" }, 202);
      throw new Error(path);
    });
    render(<ImageModeWorkbench api={api} initialProjectID="p" />);
    await screen.findByRole("article");
    fireEvent.click(screen.getByRole("button", { name: "继续生成" }));
    await waitFor(() => expect(api.mock.calls.some(([p, i]) => p === "/api/image-projects/p/resume" && i?.method === "POST")).toBe(true));
  });

  test("quick title save PATCHes the project and updates the heading", async () => {
    const quick = { ...project, run_mode: "quick", run_phase: "completed", run_status: "completed", success_count: 1, failure_count: 0 };
    const api = vi.fn(async (path: string, init?: RequestInit) => {
      if (path === "/api/image-projects") return json([]);
      if (path === "/api/image-projects/p" && init?.method === "PATCH") {
        expect(JSON.parse(String(init.body))).toEqual({ title: "新的项目名" });
        return json({ project: { ...quick, title: "新的项目名" }, items: [item], publishing_candidates: candidates });
      }
      if (path === "/api/image-projects/p") return json({ project: quick, items: [item], publishing_candidates: candidates });
      throw new Error(path);
    });
    render(<ImageModeWorkbench api={api} initialProjectID="p" />);
    await screen.findByRole("heading", { name: "Project", level: 1 });
    const titleInput = screen.getByLabelText("图文项目名称") as HTMLInputElement;
    await waitFor(() => expect(titleInput.value).toBe("Project"));
    fireEvent.change(titleInput, { target: { value: "新的项目名" } });
    fireEvent.click(screen.getByRole("button", { name: "保存名称" }));
    expect(await screen.findByRole("heading", { name: "新的项目名", level: 1 })).toBeTruthy();
  });

  test("completed quick project hides source and prompt until advanced edit is shown", async () => {
    const readyItem = { ...item, status: "ready", attempt_count: 1 };
    const quick = { ...project, run_mode: "quick", run_phase: "completed", run_status: "completed", success_count: 1, failure_count: 0 };
    const api = vi.fn(async (path: string) => path === "/api/image-projects/p" ? json({ project: quick, items: [readyItem], publishing_candidates: candidates }) : path === "/api/image-projects" ? json([]) : json({}));
    render(<ImageModeWorkbench api={api} initialProjectID="p" />);
    await screen.findByRole("article");
    expect(screen.queryByLabelText("对应原文")).toBeNull();
    expect(screen.queryByLabelText(/提示词/)).toBeNull();
    expect(screen.getByLabelText("图片名称")).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "图片视频 / 图生视频" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "显示高级编辑" }));
    expect(screen.getByLabelText("对应原文")).toBeTruthy();
    expect(screen.getByLabelText(/提示词/)).toBeTruthy();
  });

  test("saving candidate PATCHes then selects with POST", async () => {
    const api = vi.fn(async (path: string, init?: RequestInit) => path === "/api/image-projects/p" ? json(detail()) : path === "/api/image-projects" && !init?.method ? json([]) : json({}));
    render(<ImageModeWorkbench api={api} initialProjectID="p" />); await screen.findByRole("article"); const button = screen.getAllByRole("button").find((b) => b.textContent?.includes("标题") || b.textContent?.includes("鏍囬"))!; fireEvent.click(button); await screen.findByRole("dialog"); fireEvent.click(screen.getByRole("button", { name: /保存当前候选/ })); await waitFor(() => expect(api.mock.calls.some(([p, i]) => String(p).includes("publishing-candidates/1") && i?.method === "PATCH")).toBe(true)); expect(api.mock.calls.some(([p, i]) => String(p).endsWith("publishing-candidates/select") && i?.method === "POST")).toBe(true);
  });
});
