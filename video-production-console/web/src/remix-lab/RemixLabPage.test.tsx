// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { RemixLabPage } from "./RemixLabPage";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const defaults = {
  remix_base_url: "https://api.example.com",
  remix_model: "gpt-test",
  remix_reasoning_effort: "medium",
  remix_api_key_configured: true,
  presets: [] as Array<{
    base_url: string;
    model: string;
    reasoning_effort: string;
    run_count: number;
    api_key_configured: boolean;
    preset_index: number;
  }>,
};

const experimentID = "123e4567-e89b-12d3-a456-426614174000";
const runID = "223e4567-e89b-12d3-a456-426614174000";

test("remix-lab shows start button after loading defaults", async () => {
  const api = vi.fn(async (path: string) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    throw new Error(`unexpected ${path}`);
  });
  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  expect(await screen.findByRole("button", { name: "开始二创" })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "进化台" })).toBeTruthy();
});

test("remix-lab can remove an extra model slot", async () => {
  const api = vi.fn(async (path: string) => {
    if (path === "/api/remix-lab/defaults") {
      return json({
        ...defaults,
        presets: [
          {
            base_url: "https://api.deepseek.com",
            model: "deepseek-v4-pro",
            reasoning_effort: "",
            run_count: 2,
            api_key_configured: true,
            preset_index: 0,
          },
        ],
      });
    }
    if (path === "/api/remix-lab/experiments") return json([]);
    throw new Error(`unexpected ${path}`);
  });
  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  await screen.findByRole("button", { name: "开始二创" });
  expect(screen.getByText("模型槽 2")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "删除模型槽 2" }));
  expect(screen.queryByText("模型槽 2")).toBeNull();
  expect((screen.getByRole("button", { name: "删除模型槽 1" }) as HTMLButtonElement).disabled).toBe(true);
});

test("remix-lab posts experiment, refreshes history, then navigates", async () => {
  const posts: Array<{ path: string; body: unknown }> = [];
  const onNavigate = vi.fn();
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments" && (!init?.method || init.method === "GET")) {
      return json([]);
    }
    if (path === "/api/remix-lab/experiments" && init?.method === "POST") {
      const body = JSON.parse(String(init.body));
      posts.push({ path, body });
      return json(
        {
          id: experimentID,
          title: "新建实验标题",
          source_text: body.source,
          prompt_stamp: "stamp",
          status: "running",
          created_at: "2026-08-25T00:00:00Z",
          updated_at: "2026-08-25T00:00:00Z",
          slots: [],
          runs: [],
        },
        202,
      );
    }
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  });

  render(<RemixLabPage api={api} onNavigate={onNavigate} />);
  await screen.findByRole("button", { name: "开始二创" });
  fireEvent.change(screen.getByLabelText("原文"), { target: { value: "这是二创原文" } });
  fireEvent.click(screen.getByRole("button", { name: "开始二创" }));

  await waitFor(() => expect(posts).toHaveLength(1));
  expect((posts[0].body as { source: string }).source).toBe("这是二创原文");
  expect(onNavigate).toHaveBeenCalledWith(`/remix-lab/${experimentID}`);
  expect(await screen.findByRole("button", { name: /新建实验标题/ })).toBeTruthy();
});

test("remix-lab flushes pending comment when experimentID changes", async () => {
  const otherID = "323e4567-e89b-12d3-a456-426614174000";
  const patches: Array<{ path: string; body: unknown }> = [];
  const completedRun = {
    id: runID,
    experiment_id: experimentID,
    slot_id: "slot-1",
    run_index: 0,
    status: "completed",
    continuous_script: "可用稿件全文。",
    titles_json: "[]",
    error_message: "",
    comment: "",
    adopted_project_id: "",
  };
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === `/api/remix-lab/experiments/${experimentID}`) {
      return json({
        id: experimentID,
        title: "完成实验",
        source_text: "原文",
        prompt_stamp: "stamp",
        status: "completed",
        created_at: "2026-08-25T00:00:00Z",
        updated_at: "2026-08-25T00:00:00Z",
        slots: [],
        runs: [completedRun],
      });
    }
    if (path === `/api/remix-lab/experiments/${otherID}`) {
      return json({
        id: otherID,
        title: "另一实验",
        source_text: "原文2",
        prompt_stamp: "stamp2",
        status: "completed",
        created_at: "2026-08-25T00:00:00Z",
        updated_at: "2026-08-25T00:00:00Z",
        slots: [],
        runs: [],
      });
    }
    if (path === `/api/remix-lab/runs/${runID}` && init?.method === "PATCH") {
      patches.push({ path, body: JSON.parse(String(init.body)) });
      return json({ status: "ok" });
    }
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  });

  const { rerender } = render(
    <RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />,
  );
  const comment = await screen.findByLabelText("批注");
  fireEvent.change(comment, { target: { value: "切换前批注" } });
  rerender(<RemixLabPage api={api} experimentID={otherID} onNavigate={vi.fn()} />);
  await waitFor(() => expect(patches).toHaveLength(1));
  expect(patches[0].body).toEqual({ comment: "切换前批注" });
});

test("remix-lab flushes pending comment on unmount", async () => {
  const patches: Array<{ path: string; body: unknown }> = [];
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === `/api/remix-lab/experiments/${experimentID}`) {
      return json({
        id: experimentID,
        title: "完成实验",
        source_text: "原文",
        prompt_stamp: "stamp",
        status: "completed",
        created_at: "2026-08-25T00:00:00Z",
        updated_at: "2026-08-25T00:00:00Z",
        slots: [],
        runs: [
          {
            id: runID,
            experiment_id: experimentID,
            slot_id: "slot-1",
            run_index: 0,
            status: "completed",
            continuous_script: "可用稿件全文。",
            titles_json: "[]",
            error_message: "",
            comment: "",
            adopted_project_id: "",
          },
        ],
      });
    }
    if (path === `/api/remix-lab/runs/${runID}` && init?.method === "PATCH") {
      patches.push({ path, body: JSON.parse(String(init.body)) });
      return json({ status: "ok" });
    }
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  });

  const { unmount } = render(
    <RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />,
  );
  const comment = await screen.findByLabelText("批注");
  fireEvent.change(comment, { target: { value: "卸载前批注" } });
  unmount();
  await waitFor(() => expect(patches).toHaveLength(1));
  expect(patches[0].body).toEqual({ comment: "卸载前批注" });
});

test("remix-lab shows script and comment when viewing a completed experiment", async () => {
  const api = vi.fn(async (path: string) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === `/api/remix-lab/experiments/${experimentID}`) {
      return json({
        id: experimentID,
        title: "完成实验",
        source_text: "原文",
        prompt_stamp: "stamp",
        status: "completed",
        created_at: "2026-08-25T00:00:00Z",
        updated_at: "2026-08-25T00:00:00Z",
        slots: [
          {
            id: "slot-1",
            experiment_id: experimentID,
            sort_index: 0,
            label: "槽1",
            base_url: "https://api.example.com",
            model: "gpt-test",
            reasoning_effort: "medium",
            run_count: 1,
            api_key_configured: true,
          },
        ],
        runs: [
          {
            id: runID,
            experiment_id: experimentID,
            slot_id: "slot-1",
            run_index: 0,
            status: "completed",
            continuous_script: "第一句。第二句！第三句？第四句。",
            titles_json: '["题1","题2"]',
            error_message: "",
            comment: "",
            adopted_project_id: "",
          },
        ],
      });
    }
    throw new Error(`unexpected ${path}`);
  });

  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />);
  expect(await screen.findByText("第一句。第二句！第三句？第四句。")).toBeTruthy();
  expect(screen.getByLabelText("批注")).toBeTruthy();
  expect(screen.getByText("题1")).toBeTruthy();
});

test("remix-lab detail shows source text, 1-based run labels, and groups by slot", async () => {
  const api = vi.fn(async (path: string) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === `/api/remix-lab/experiments/${experimentID}`) {
      return json({
        id: experimentID,
        title: "双槽实验",
        source_text: "这是详情原文大框内容",
        prompt_stamp: "stamp",
        status: "completed",
        created_at: "2026-08-25T00:00:00Z",
        updated_at: "2026-08-25T00:00:00Z",
        slots: [
          {
            id: "slot-b",
            experiment_id: experimentID,
            sort_index: 1,
            label: "模型B",
            base_url: "https://api.example.com",
            model: "model-b",
            reasoning_effort: "medium",
            run_count: 1,
            api_key_configured: true,
          },
          {
            id: "slot-a",
            experiment_id: experimentID,
            sort_index: 0,
            label: "模型A",
            base_url: "https://api.example.com",
            model: "model-a",
            reasoning_effort: "medium",
            run_count: 1,
            api_key_configured: true,
          },
        ],
        runs: [
          {
            id: "run-b",
            experiment_id: experimentID,
            slot_id: "slot-b",
            run_index: 1,
            status: "completed",
            continuous_script: "B稿。",
            titles_json: "[]",
            error_message: "",
            comment: "",
            adopted_project_id: "",
          },
          {
            id: "run-a",
            experiment_id: experimentID,
            slot_id: "slot-a",
            run_index: 1,
            status: "completed",
            continuous_script: "A稿。",
            titles_json: "[]",
            error_message: "",
            comment: "",
            adopted_project_id: "",
          },
        ],
      });
    }
    throw new Error(`unexpected ${path}`);
  });

  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />);
  expect(await screen.findByDisplayValue("这是详情原文大框内容")).toBeTruthy();
  expect(screen.getAllByText("运行 1")).toHaveLength(2);
  expect(screen.queryByText("运行 2")).toBeNull();
  expect(screen.getByRole("heading", { name: "模型A" })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "模型B" })).toBeTruthy();
  const headings = screen.getAllByRole("heading", { name: /模型[AB]/ });
  expect(headings.map((node) => node.textContent)).toEqual(["模型A", "模型B"]);
});

test("remix-lab patches comment on blur and adopts into a project", async () => {
  const patches: Array<{ path: string; body: unknown }> = [];
  const adopts: Array<{ path: string; body: unknown }> = [];
  const onNavigate = vi.fn();
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === `/api/remix-lab/experiments/${experimentID}`) {
      return json({
        id: experimentID,
        title: "完成实验",
        source_text: "原文",
        prompt_stamp: "stamp",
        status: "completed",
        created_at: "2026-08-25T00:00:00Z",
        updated_at: "2026-08-25T00:00:00Z",
        slots: [],
        runs: [
          {
            id: runID,
            experiment_id: experimentID,
            slot_id: "slot-1",
            run_index: 0,
            status: "completed",
            continuous_script: "可用稿件全文。",
            titles_json: "[]",
            error_message: "",
            comment: "",
            adopted_project_id: "",
          },
        ],
      });
    }
    if (path === `/api/remix-lab/runs/${runID}` && init?.method === "PATCH") {
      patches.push({ path, body: JSON.parse(String(init.body)) });
      return json({ status: "ok" });
    }
    if (path === "/api/projects") {
      return json([{ id: "project-1", account_id: "a1", title: "目标项目", stage: "script" }]);
    }
    if (path === `/api/remix-lab/runs/${runID}/adopt` && init?.method === "POST") {
      adopts.push({ path, body: JSON.parse(String(init.body)) });
      return json({ status: "ok" });
    }
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  });

  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={onNavigate} />);
  const comment = await screen.findByLabelText("批注");
  fireEvent.change(comment, { target: { value: "开头不够狠" } });
  fireEvent.blur(comment);
  await waitFor(() => expect(patches).toHaveLength(1));
  expect(patches[0].body).toEqual({ comment: "开头不够狠" });

  fireEvent.click(screen.getByRole("button", { name: "采用到项目" }));
  fireEvent.click(await screen.findByRole("button", { name: "目标项目" }));
  await waitFor(() => expect(adopts).toHaveLength(1));
  expect(adopts[0].body).toEqual({ project_id: "project-1" });
  expect(await screen.findByText("已采用，可去项目生成口播")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "打开项目" }));
  expect(onNavigate).toHaveBeenCalledWith("/projects/project-1");
});
