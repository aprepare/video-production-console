// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { RemixLabPage } from "./RemixLabPage";

// React Flow（工作流画布）在 jsdom 里需要 ResizeObserver。
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (!("ResizeObserver" in globalThis)) {
  (globalThis as unknown as { ResizeObserver: typeof ResizeObserverStub }).ResizeObserver =
    ResizeObserverStub;
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.useRealTimers();
  window.localStorage.removeItem("remix-lab:produce-account");
});

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const elderPrompt = {
  id: "elder_stable",
  name: "中老年定稿（生产默认）",
  description: "",
  style: "rewrite",
  stamp: "语感回流 2026-08-25 批注回流2",
  system: "system",
  user: "user",
  builtin: true,
};

const workflowFixture = {
  version: 1,
  name: "默认二创工作流",
  nodes: [
    { id: "source", type: "input", title: "对标原文", x: 0, y: 190, config: {} },
    {
      id: "hook", type: "agent", title: "钩子分析", x: 300, y: 10,
      config: { system_prompt: "钩子分析规则", user_template: "拆：{{source}}", inject_title: "钩子指纹" },
    },
    { id: "writer", type: "writer", title: "写手", x: 600, y: 190, config: {} },
    { id: "selfcheck", type: "selfcheck", title: "机械自检", x: 880, y: 190, config: {} },
    { id: "review", type: "reviewer", title: "审稿终审", x: 1160, y: 190, config: { system_prompt: "审稿规则" } },
    { id: "final", type: "output", title: "定稿与发布包", x: 1440, y: 190, config: {} },
  ],
  edges: [
    ["source", "hook"], ["hook", "writer"],
    ["writer", "selfcheck"], ["selfcheck", "review"], ["review", "final"],
  ],
};

function withLabExtras(
  handler: (path: string, init?: RequestInit) => Promise<Response>,
): (path: string, init?: RequestInit) => Promise<Response> {
  return async (path, init) => {
    if (path === "/api/remix-lab/prompts") return json({ prompts: [elderPrompt] });
    if (path === "/api/remix-lab/active-prompt") {
      return json({ active: false, prompt: elderPrompt });
    }
    if (path === "/api/remix-lab/agent-settings") {
      return json({ model: "", base_url: "", reasoning_effort: "", api_key_configured: false });
    }
    try {
      return await handler(path, init);
    } catch (error) {
      if (error instanceof Error && error.message.startsWith("unexpected")) {
        if (path === "/api/remix-lab/agent/last") return json({ found: false });
        if (path === "/api/remix-lab/agent/history" && (init?.method || "GET") === "GET") {
          return json({ turns: [] });
        }
        if (path.startsWith("/api/remix-lab/workflow") && (init?.method || "GET") === "GET") {
          return json(workflowFixture);
        }
        if (path === "/api/accounts") return json([]);
        if (path === "/api/projects") return json([]);
        if (path === "/api/remix-lab/agent-prompts") return json(agentPromptsFixture);
        if (path.includes("/stages") && (init?.method || "GET") === "GET") {
          return json({ run_id: "", pipeline: "workflow", status: "completed", stages: [], edges: [] });
        }
      }
      throw error;
    }
  };
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

const agentPromptsFixture = {
  prompts: {
    hook_system: "内置钩子提示词",
    facts_search_system: "内置联网事实提示词",
    facts_offline_system: "内置离线事实提示词",
    ammo_system: "内置弹药提示词",
    reviewer_system: "内置审稿提示词",
  },
  defaults: {
    hook_system: "内置钩子提示词",
    facts_search_system: "内置联网事实提示词",
    facts_offline_system: "内置离线事实提示词",
    ammo_system: "内置弹药提示词",
    reviewer_system: "内置审稿提示词",
  },
  overridden: {
    hook_system: false,
    facts_search_system: false,
    facts_offline_system: false,
    ammo_system: false,
    reviewer_system: false,
  },
};

test("creation studio edits and saves agent prompts", async () => {
  const puts: Array<Record<string, string>> = [];
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === "/api/remix-lab/agent-prompts" && (init?.method || "GET") === "GET") {
      return json(agentPromptsFixture);
    }
    if (path === "/api/remix-lab/agent-prompts" && init?.method === "PUT") {
      const body = JSON.parse(String(init.body)) as Record<string, string>;
      puts.push(body);
      return json({
        ...agentPromptsFixture,
        prompts: { ...agentPromptsFixture.prompts, reviewer_system: body.reviewer_system },
        overridden: { ...agentPromptsFixture.overridden, reviewer_system: true },
      });
    }
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "提示词库" }));
  expect(await screen.findByRole("dialog", { name: "提示词库" })).toBeTruthy();
  fireEvent.click(screen.getByRole("tab", { name: "节点提示词" }));
  expect(await screen.findByDisplayValue("内置钩子提示词")).toBeTruthy();

  fireEvent.change(screen.getByLabelText("审稿终审提示词"), {
    target: { value: "自定义审稿规则：只查课尾和互动段。" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存节点提示词" }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0].reviewer_system).toBe("自定义审稿规则：只查课尾和互动段。");
  expect(puts[0].hook_system).toBe("内置钩子提示词");
});

test("remix-lab shows the workflow canvas with start button after loading", async () => {
  const api = vi.fn(withLabExtras(async (path: string) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    throw new Error(`unexpected ${path}`);
  }));
  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  expect(screen.getByRole("heading", { name: "文案创作台" })).toBeTruthy();
  // 画布节点渲染出来了（写手节点上类型标签和标题同名，取全部）
  expect(await screen.findByText("钩子分析")).toBeTruthy();
  expect(screen.getAllByText("写手").length).toBeGreaterThan(0);
  expect(screen.getByText("审稿终审")).toBeTruthy();
  expect(screen.getByText("确认二创")).toBeTruthy();
  expect(screen.getByText("混剪草稿")).toBeTruthy();
  expect(screen.queryByText("字幕关键词")).toBeNull();
  expect(screen.queryByRole("button", { name: /恢复字幕关键词节点/ })).toBeNull();
  expect(screen.queryByRole("button", { name: "显示混剪链" })).toBeNull();
  expect(screen.queryByRole("button", { name: /默认模型档/ })).toBeNull();
  expect(screen.queryByRole("button", { name: "保存工作流" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Agent提示词" })).toBeNull();
  fireEvent.click(screen.getByText("对标原文"));
  expect(await screen.findByRole("button", { name: "开始二创" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "撤销" })).toBeTruthy();
});

test("remix-lab can remove an extra model slot", async () => {
  const api = vi.fn(withLabExtras(async (path: string) => {
    if (path === "/api/remix-lab/defaults") {
      // 预设即完整草稿：两个预设还原成两个槽。
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
          {
            base_url: "https://api.deepseek.com",
            model: "claude-t",
            reasoning_effort: "high",
            run_count: 1,
            api_key_configured: false,
            preset_index: 1,
          },
        ],
      });
    }
    if (path === "/api/remix-lab/experiments") return json([]);
    throw new Error(`unexpected ${path}`);
  }));
  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  await screen.findByRole("button", { name: "模型配置" });
  expect(screen.queryByText("模型槽 2")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "模型配置" }));
  expect(screen.getByRole("dialog", { name: "模型配置" })).toBeTruthy();
  expect(screen.getByText("模型槽 2")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "删除模型槽 2" }));
  expect(screen.queryByText("模型槽 2")).toBeNull();
  expect((screen.getByRole("button", { name: "删除模型槽 1" }) as HTMLButtonElement).disabled).toBe(true);
});

test("remix-lab opens prompt editor from the library dialog", async () => {
  const api = vi.fn(withLabExtras(async (path: string) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    throw new Error(`unexpected ${path}`);
  }));
  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "提示词库" }));
  expect(screen.getByRole("dialog", { name: "提示词库" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "编辑" }));
  expect(screen.getByRole("dialog", { name: "编辑提示词" })).toBeTruthy();
  expect(screen.getByLabelText("系统提示词")).toBeTruthy();
});

test("remix-lab can delete a history project from the sidebar", async () => {
  const deletes: string[] = [];
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/accounts") return json([{ id: "account-1", name: "认知觉醒", status: "active" }]);
    if (path === "/api/projects" && (!init?.method || init.method === "GET")) {
      return json([
        {
          id: experimentID,
          account_id: "account-1",
          title: "待删项目",
          stage: "script",
          created_at: "2026-08-25T00:00:00Z",
          updated_at: "2026-08-25T00:00:00Z",
        },
      ]);
    }
    if (path === `/api/projects/${experimentID}` && init?.method === "DELETE") {
      deletes.push(path);
      return json({ status: "ok" });
    }
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));
  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  expect(await screen.findByRole("button", { name: /^待删项目/ })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "删除项目 待删项目" }));
  await waitFor(() => expect(deletes).toHaveLength(1));
  expect(screen.queryByRole("button", { name: /待删项目/ })).toBeNull();
});

test("remix-lab navigates home after deleting the open project", async () => {
  const onNavigate = vi.fn();
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/accounts") return json([{ id: "account-1", name: "认知觉醒", status: "active" }]);
    if (path === "/api/projects" && (!init?.method || init.method === "GET")) {
      return json([
        {
          id: experimentID,
          account_id: "account-1",
          title: "打开中的项目",
          stage: "mixing",
          created_at: "2026-08-25T00:00:00Z",
          updated_at: "2026-08-25T00:00:00Z",
        },
      ]);
    }
    if (path === `/api/projects/${experimentID}` && init?.method === "DELETE") {
      return json({ status: "ok" });
    }
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));
  render(<RemixLabPage api={api} selectedProjectID={experimentID} onNavigate={onNavigate} />);
  fireEvent.click(await screen.findByRole("button", { name: "删除项目 打开中的项目" }));
  await waitFor(() => expect(onNavigate).toHaveBeenCalledWith("/"));
});

test("history project opens the linked workflow instead of the old project page", async () => {
  const onNavigate = vi.fn();
  const api = vi.fn(withLabExtras(async (path: string) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/accounts") return json([{ id: "account-1", name: "认知觉醒", status: "active" }]);
    if (path === "/api/projects") {
      return json([
        {
          id: "proj-hist",
          account_id: "account-1",
          title: "现金为王那期",
          stage: "mixing",
          created_at: "2026-08-31T00:00:00Z",
          updated_at: "2026-08-31T00:00:00Z",
        },
      ]);
    }
    if (path === "/api/remix-lab/productions/by-project/proj-hist") {
      return json({
        experiment_id: experimentID,
        run_id: runID,
        project_id: "proj-hist",
        status: "completed",
      });
    }
    throw new Error(`unexpected ${path}`);
  }));
  render(<RemixLabPage api={api} onNavigate={onNavigate} />);
  fireEvent.click(await screen.findByRole("button", { name: /^现金为王那期/ }));
  await waitFor(() => expect(onNavigate).toHaveBeenCalledWith(`/remix-lab/${experimentID}`));
});

test("workflow canvas edits a node prompt in the drawer and saves the graph", async () => {
  const puts: Array<Record<string, unknown>> = [];
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path.startsWith("/api/remix-lab/workflow") && init?.method === "PUT") {
      const body = JSON.parse(String(init.body)) as Record<string, unknown>;
      puts.push(body);
      return json(body);
    }
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  // 点节点开抽屉
  fireEvent.click(await screen.findByText("钩子分析"));
  const promptBox = await screen.findByLabelText("节点系统提示词");
  fireEvent.change(promptBox, { target: { value: "改过的钩子分析规则" } });

  await waitFor(() => expect(puts).toHaveLength(1), { timeout: 2000 });
  const nodes = puts[0].nodes as Array<{ id: string; config: { system_prompt?: string } }>;
  const hook = nodes.find((node) => node.id === "hook");
  expect(hook?.config.system_prompt).toBe("改过的钩子分析规则");
  const edges = puts[0].edges as Array<[string, string]>;
  expect(edges).toContainEqual(["hook", "writer"]);

  fireEvent.click(screen.getByRole("button", { name: "撤销" }));
  await waitFor(() => {
    const undone = puts[puts.length - 1].nodes as Array<{ id: string; config: { system_prompt?: string } }>;
    expect(undone.find((node) => node.id === "hook")?.config.system_prompt).toBe("钩子分析规则");
  }, { timeout: 2000 });
});

test("workflow canvas hides captions by default and saves production params", async () => {
  const puts: Array<Record<string, unknown>> = [];
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path.startsWith("/api/remix-lab/workflow") && init?.method === "PUT") {
      const body = JSON.parse(String(init.body)) as Record<string, unknown>;
      puts.push(body);
      return json(body);
    }
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  await screen.findByText("混剪草稿");
  expect(screen.queryByText("字幕关键词")).toBeNull();

  fireEvent.click(screen.getByText("口播稿"));
  fireEvent.change(await screen.findByLabelText("口播稿任务提示词"), {
    target: { value: "每行不超过十二个字切口播。" },
  });
  fireEvent.change(screen.getByLabelText("口播稿模型"), { target: { value: "gpt-spoken" } });
  fireEvent.click(screen.getByText("配音"));
  fireEvent.change(await screen.findByLabelText("配音音色"), { target: { value: "moss_audio_test" } });
  fireEvent.change(screen.getByLabelText("配音语速"), { target: { value: "1.15" } });
  fireEvent.click(screen.getByText("混剪草稿"));
  fireEvent.change(await screen.findByLabelText("混剪草稿模型"), { target: { value: "gpt-montage" } });

  await waitFor(() => {
    const production = puts[puts.length - 1]?.production as {
      captions_disabled?: boolean;
      spoken_prompt?: string;
      spoken_model?: string;
      narration_voice_id?: string;
      narration_speed?: number;
      montage_model?: string;
    } | undefined;
    expect(production?.captions_disabled).toBe(true);
    expect(production?.spoken_prompt).toBe("每行不超过十二个字切口播。");
    expect(production?.spoken_model).toBe("gpt-spoken");
    expect(production?.narration_voice_id).toBe("moss_audio_test");
    expect(production?.narration_speed).toBe(1.15);
    expect(production?.montage_model).toBe("gpt-montage");
  }, { timeout: 3000 });
});

test("remix-lab starts a workflow run and stays on the canvas with live progress", async () => {
  const posts: Array<{ path: string; body: unknown }> = [];
  const onNavigate = vi.fn();
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments" && (!init?.method || init.method === "GET")) {
      return json([]);
    }
    if (path === "/api/remix-lab/workflow/run" && init?.method === "POST") {
      const body = JSON.parse(String(init.body));
      posts.push({ path, body });
      return json(
        {
          id: experimentID,
          title: "新建实验标题",
          source_text: body.source,
          prompt_stamp: "stamp",
          status: "running",
          workflow: true,
          created_at: "2026-08-25T00:00:00Z",
          updated_at: "2026-08-25T00:00:00Z",
          slots: [],
          runs: [
            {
              id: runID, experiment_id: experimentID, slot_id: "slot-1", run_index: 1,
              status: "queued", continuous_script: "", titles_json: "",
              package_json: "", draft_v1_json: "", review_json: "",
              error_message: "", comment: "", adopted_project_id: "",
            },
          ],
        },
        202,
      );
    }
    if (path === `/api/remix-lab/runs/${runID}/stages`) {
      return json({
        run_id: runID,
        pipeline: "workflow",
        status: "running",
        stages: [
          { id: "source", kind: "input", title: "对标原文", status: "ok", x: 0, y: 190, output: "这是二创原文" },
          { id: "writer", kind: "agent", title: "写手", status: "missing", x: 600, y: 190 },
        ],
        edges: [["source", "writer"]],
      });
    }
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  render(<RemixLabPage api={api} onNavigate={onNavigate} />);
  fireEvent.click(await screen.findByText("对标原文"));
  await screen.findByRole("button", { name: "开始二创" });
  fireEvent.change(screen.getByLabelText("对标原文"), { target: { value: "这是二创原文" } });
  fireEvent.click(screen.getByRole("button", { name: "开始二创" }));

  await waitFor(() => expect(posts).toHaveLength(1));
  expect((posts[0].body as { source: string }).source).toBe("这是二创原文");
  expect((posts[0].body as { run_count: number }).run_count).toBe(1);
  // 不跳页：画布原地切到运行视图，节点显示等待状态
  expect(onNavigate).not.toHaveBeenCalled();
  expect(await screen.findByRole("button", { name: "返回编辑" })).toBeTruthy();
  expect(await screen.findByText("等待中")).toBeTruthy();
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
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
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
  }));

  const { rerender } = render(
    <RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />,
  );
  fireEvent.click(await screen.findByRole("button", { name: "改稿" }));
  const comment = await screen.findByLabelText("批注");
  fireEvent.change(comment, { target: { value: "切换前批注" } });
  rerender(<RemixLabPage api={api} experimentID={otherID} onNavigate={vi.fn()} />);
  await waitFor(() => expect(patches).toHaveLength(1));
  expect(patches[0].body).toEqual({ comment: "切换前批注" });
});

test("remix-lab flushes pending comment on unmount", async () => {
  const patches: Array<{ path: string; body: unknown }> = [];
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
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
  }));

  const { unmount } = render(
    <RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />,
  );
  fireEvent.click(await screen.findByRole("button", { name: "改稿" }));
  const comment = await screen.findByLabelText("批注");
  fireEvent.change(comment, { target: { value: "卸载前批注" } });
  unmount();
  await waitFor(() => expect(patches).toHaveLength(1));
  expect(patches[0].body).toEqual({ comment: "卸载前批注" });
});

test("remix-lab shows script and comment when viewing a completed experiment", async () => {
  const api = vi.fn(withLabExtras(async (path: string) => {
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
  }));

  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "改稿" }));
  // 正文进了可编辑文本框；长标题/话题/CTA 不再展示。
  expect(await screen.findByDisplayValue("第一句。第二句！第三句？第四句。")).toBeTruthy();
  expect(screen.getByLabelText("批注")).toBeTruthy();
  expect(screen.queryByText("长标题备选")).toBeNull();
  expect(screen.queryByText("话题标签")).toBeNull();
  expect(screen.queryByLabelText("CTA")).toBeNull();
});

test("remix-lab detail shows source text, 1-based run labels, and groups by slot", async () => {
  const api = vi.fn(withLabExtras(async (path: string) => {
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
  }));

  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />);
  expect(await screen.findByRole("button", { name: "改稿" })).toBeTruthy();
  expect(screen.getAllByRole("button", { name: "运行 1" })).toHaveLength(2);
  expect(screen.queryByRole("button", { name: "运行 2" })).toBeNull();
});

test("remix-lab patches comment on blur and adopts into a project", async () => {
  const patches: Array<{ path: string; body: unknown }> = [];
  const adopts: Array<{ path: string; body: unknown }> = [];
  const onNavigate = vi.fn();
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
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
  }));

  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={onNavigate} />);
  fireEvent.click(await screen.findByRole("button", { name: "改稿" }));
  const comment = await screen.findByLabelText("批注");
  fireEvent.change(comment, { target: { value: "开头不够狠" } });
  fireEvent.blur(comment);
  await waitFor(() => expect(patches).toHaveLength(1));
  expect(patches[0].body).toEqual({ comment: "开头不够狠" });

  fireEvent.click(screen.getByRole("button", { name: "采用到项目" }));
  fireEvent.click(await screen.findByRole("button", { name: "目标项目" }));
  await waitFor(() => expect(adopts).toHaveLength(1));
  expect(adopts[0].body).toEqual({ project_id: "project-1" });
  expect(await screen.findByText(/已进项目/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "回到工作流" }));
  expect(onNavigate).toHaveBeenCalledWith(`/remix-lab/${experimentID}`);
});

const workbenchRun = {
  id: runID,
  experiment_id: experimentID,
  slot_id: "slot-1",
  run_index: 1,
  status: "completed",
  continuous_script: "这是审稿后的定稿正文，讲家里那笔钱该往哪放，一次说明白，足够四十个字了吧，再补几个字凑够下限。",
  titles_json: '["标题甲"]',
  package_json: JSON.stringify({
    continuous_script:
      "这是审稿后的定稿正文，讲家里那笔钱该往哪放，一次说明白，足够四十个字了吧，再补几个字凑够下限。",
    titles: ["标题甲"],
    short_titles: ["板标题甲", "副标题乙", "备选丙"],
    descriptions: ["描述一", "描述二", "描述三"],
    topics: ["#楼市", "#房贷", "#家庭理财", "#财经"],
    cta: "",
  }),
  draft_v1_json: JSON.stringify({ continuous_script: "这是写手初稿正文。" }),
  review_json: JSON.stringify({
    verdict: "fixed",
    round: 1,
    summary: "改了开头一处。",
    issues: [{ where: "旧开头", problem: "口语度", fix: "换成二选一逼问" }],
    at: "2026-08-29T00:00:00Z",
  }),
  error_message: "",
  comment: "",
  adopted_project_id: "",
};

function workbenchExperiment() {
  return {
    id: experimentID,
    title: "创作台实验",
    source_text: "原文",
    prompt_stamp: "stamp",
    status: "completed",
    created_at: "2026-08-25T00:00:00Z",
    updated_at: "2026-08-25T00:00:00Z",
    slots: [],
    runs: [workbenchRun],
  };
}

test("creation studio shows review verdict, saves edited package, and reworks with annotations", async () => {
  const packagePuts: Array<{ body: Record<string, unknown> }> = [];
  const reworks: Array<{ body: Record<string, unknown> }> = [];
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === `/api/remix-lab/experiments/${experimentID}`) return json(workbenchExperiment());
    if (path === `/api/remix-lab/runs/${runID}/package` && init?.method === "PUT") {
      packagePuts.push({ body: JSON.parse(String(init.body)) as Record<string, unknown> });
      return json({ status: "ok" });
    }
    if (path === `/api/remix-lab/runs/${runID}/rework` && init?.method === "POST") {
      reworks.push({ body: JSON.parse(String(init.body)) as Record<string, unknown> });
      return json({ status: "ok" });
    }
    if (path === `/api/remix-lab/runs/${runID}` && init?.method === "PATCH") return json({ status: "ok" });
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "改稿" }));
  // 审稿结论与双版本对照
  expect(await screen.findByText("审稿已修订")).toBeTruthy();
  expect(screen.getByText("这是写手初稿正文。")).toBeTruthy();
  expect(screen.getByDisplayValue("板标题甲")).toBeTruthy();

  // 编辑正文并保存
  const script = screen.getByLabelText("运行 1 正文");
  fireEvent.change(script, {
    target: { value: "改过之后的定稿正文，讲家里那笔钱该往哪放，一次说明白，足够四十个字了吧，再补几个字凑够下限。" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存修改" }));
  await waitFor(() => expect(packagePuts).toHaveLength(1));
  expect(String(packagePuts[0].body.continuous_script)).toContain("改过之后的定稿正文");
  expect(packagePuts[0].body.short_titles).toEqual(["板标题甲", "副标题乙", "备选丙"]);

  // 批注打回
  fireEvent.change(screen.getByLabelText("批注"), { target: { value: "开头再狠一点" } });
  fireEvent.click(screen.getByRole("button", { name: "按批注打回重做" }));
  await waitFor(() => expect(reworks).toHaveLength(1));
  expect(reworks[0].body).toEqual({ annotations: "开头再狠一点" });
});

test("run workbench opens the workflow view and inspects the failed stage", async () => {
  const stagesFixture = {
    run_id: runID,
    pipeline: "multi_agent",
    status: "completed",
    stages: [
      { id: "source", kind: "input", title: "对标原文", status: "ok", output: "原文" },
      {
        id: "hook", kind: "agent", title: "钩子分析", status: "ok", model: "m-main", ms: 1200,
        prompt_key: "hook_system", system_prompt: "钩子提示词", output: '{"hook_type":"数字砸脸"}',
      },
      { id: "facts", kind: "agent", title: "事实核查", status: "ok", output: "{}" },
      { id: "ammo", kind: "agent", title: "弹药库", status: "failed", error: "ammo agent down", prompt_key: "ammo_system" },
      { id: "writer", kind: "agent", title: "写手", status: "ok", prompt_key: "writer", system_prompt: "sys", user_prompt: "user", output: "{}" },
      { id: "selfcheck", kind: "gate", title: "机械自检", status: "ok", extra: { events: [{ round: 0, verdict: "pass", overlap_pct: 12 }] } },
      { id: "review", kind: "agent", title: "审稿终审", status: "ok", prompt_key: "reviewer_system", system_prompt: "审稿", output: '{"verdict":"pass"}' },
      { id: "final", kind: "output", title: "定稿与发布包", status: "ok", output: "{}" },
    ],
    edges: [
      ["source", "hook"], ["source", "facts"], ["source", "ammo"],
      ["hook", "writer"], ["facts", "writer"], ["ammo", "writer"],
      ["writer", "selfcheck"], ["selfcheck", "review"], ["review", "final"],
    ],
  };
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === `/api/remix-lab/experiments/${experimentID}`) return json(workbenchExperiment());
    if (path === `/api/remix-lab/runs/${runID}/stages`) return json(stagesFixture);
    if (path === `/api/remix-lab/runs/${runID}` && init?.method === "PATCH") return json({ status: "ok" });
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />);
  // 历史/实验直接摊在工作流画布上
  expect(await screen.findByText("钩子分析")).toBeTruthy();
  expect(screen.getByText("写手")).toBeTruthy();
  expect(screen.getByText("审稿终审")).toBeTruthy();
  // 默认选中第一个失败节点，检视器里能看到错误
  expect(await screen.findByText("ammo agent down")).toBeTruthy();
  expect(screen.getByRole("button", { name: "编辑这路Agent提示词" })).toBeTruthy();
});

test("run workbench confirms the produce gate and posts the account", async () => {
  const produces: Array<Record<string, string>> = [];
  const gateExperiment = () => {
    const exp = workbenchExperiment();
    return {
      ...exp,
      runs: exp.runs.map((run) => ({
        ...run,
        production: {
          status: "waiting_confirm", step: "confirm", account_id: "", auto: false,
        },
      })),
    };
  };
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === `/api/remix-lab/experiments/${experimentID}`) return json(gateExperiment());
    if (path === "/api/accounts") {
      return json([{ id: "acct-9", name: "观局思考", status: "active" }]);
    }
    if (path === `/api/remix-lab/runs/${runID}/produce` && init?.method === "POST") {
      produces.push(JSON.parse(String(init.body)) as Record<string, string>);
      return json({ status: "ok" }, 202);
    }
    if (path === `/api/remix-lab/runs/${runID}` && init?.method === "PATCH") return json({ status: "ok" });
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />);
  expect(await screen.findByRole("button", { name: "确认开始混剪" })).toBeTruthy();
  // 账号列表拉回来后自动选中第一个
  await waitFor(() => expect((screen.getByLabelText("混剪账号") as HTMLSelectElement).value).toBe("acct-9"));
  fireEvent.click(screen.getByRole("button", { name: "确认开始混剪" }));
  await waitFor(() => expect(produces).toHaveLength(1));
  expect(produces[0].account_id).toBe("acct-9");
});

test("creation studio imports the package into a new montage project", async () => {
  const created: Array<Record<string, unknown>> = [];
  const uploads: Array<{ path: string; file: unknown }> = [];
  const adopts: Array<Record<string, unknown>> = [];
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === `/api/remix-lab/experiments/${experimentID}`) return json(workbenchExperiment());
    if (path === "/api/accounts") {
      return json([
        { id: "acct-1", name: "观局思考", status: "active" },
        { id: "acct-2", name: "停用号", status: "inactive" },
      ]);
    }
    if (path === "/api/projects" && init?.method === "POST") {
      created.push(JSON.parse(String(init.body)) as Record<string, unknown>);
      return json({ id: "proj-9", account_id: "acct-1", title: "板标题甲" });
    }
    if (path === "/api/projects/proj-9/assets/continuous_script" && init?.method === "POST") {
      const form = init.body as FormData;
      uploads.push({ path, file: form.get("file") });
      return json({ status: "ok" });
    }
    if (path === `/api/remix-lab/runs/${runID}/adopt` && init?.method === "POST") {
      adopts.push(JSON.parse(String(init.body)) as Record<string, unknown>);
      return json({ status: "ok" });
    }
    if (path === `/api/remix-lab/runs/${runID}` && init?.method === "PATCH") return json({ status: "ok" });
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "改稿" }));
  fireEvent.click(await screen.findByRole("button", { name: "导入混剪（新建项目）" }));
  expect(await screen.findByLabelText("导入账号")).toBeTruthy();
  expect((screen.getByLabelText("项目名") as HTMLInputElement).value).toBe("板标题甲");
  fireEvent.click(screen.getByRole("button", { name: "确认导入" }));

  await waitFor(() => expect(adopts).toHaveLength(1));
  expect(created[0]).toEqual({ account_id: "acct-1", title: "板标题甲" });
  expect(uploads).toHaveLength(1);
  const uploaded = uploads[0].file as Blob;
  const payload = JSON.parse(await uploaded.text()) as Record<string, unknown>;
  expect(payload.short_titles).toEqual(["板标题甲", "副标题乙", "备选丙"]);
  expect(String(payload.continuous_script)).toContain("这是审稿后的定稿正文");
  expect(adopts[0]).toEqual({ project_id: "proj-9" });
  expect(await screen.findByText(/已进项目/)).toBeTruthy();
});

test("remix-lab shows agent thinking progress until the reply arrives", async () => {
  let finishChat: (value: Response) => void = () => {};
  const chatPending = new Promise<Response>((resolve) => {
    finishChat = resolve;
  });
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === "/api/remix-lab/agent/chat" && init?.method === "POST") return chatPending;
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));
  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  fireEvent.change(await screen.findByLabelText("对智能体说"), { target: { value: "对照历史实验" } });
  fireEvent.click(screen.getByRole("button", { name: "发送" }));
  expect(await screen.findByRole("status", { name: /正在思考/ })).toBeTruthy();
  expect(screen.getByRole("progressbar")).toBeTruthy();
  expect(screen.getByRole("button", { name: /思考中/ })).toBeTruthy();
  finishChat(json({ reply: "先按历史批注改一版", proposals: [] }));
  expect(await screen.findByText("先按历史批注改一版")).toBeTruthy();
  expect(screen.queryByRole("progressbar")).toBeNull();
  expect(screen.getByRole("button", { name: "发送" })).toBeTruthy();
});

test("remix-lab restores the saved agent conversation", async () => {
  const api = vi.fn(withLabExtras(async (path: string) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === "/api/remix-lab/agent/history") {
      return json({
        turns: [
          { role: "user", text: "帮我分析批注", at: new Date().toISOString() },
          {
            role: "assistant",
            text: "中老年定稿和骨肉分离都不错",
            proposals: [{ id: "p1", type: "upsert_prompt", summary: "补篇幅", payload: {} }],
            at: new Date().toISOString(),
          },
          { role: "user", text: "接着改骨肉分离", at: new Date().toISOString() },
          { role: "assistant", text: "骨肉分离锁课名", proposals: [], at: new Date().toISOString() },
        ],
      });
    }
    throw new Error(`unexpected ${path}`);
  }));
  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  expect(await screen.findByText("帮我分析批注")).toBeTruthy();
  expect(screen.getByText("中老年定稿和骨肉分离都不错")).toBeTruthy();
  expect(screen.getByText("接着改骨肉分离")).toBeTruthy();
  expect(screen.getByText("骨肉分离锁课名")).toBeTruthy();
  expect(screen.getByText("补篇幅")).toBeTruthy();
});

test("remix-lab sends on Enter and starts a fresh chat", async () => {
  const chats: string[] = [];
  let historyCleared = false;
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === "/api/remix-lab/agent/chat" && init?.method === "POST") {
      chats.push((JSON.parse(String(init.body)) as { message: string }).message);
      return json({ reply: "记住了这轮", proposals: [] });
    }
    if (path === "/api/remix-lab/agent/history" && init?.method === "DELETE") {
      historyCleared = true;
      return json({ status: "ok" });
    }
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));
  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  const input = await screen.findByLabelText("对智能体说");
  fireEvent.change(input, { target: { value: "回车直接发" } });
  fireEvent.keyDown(input, { key: "Enter" });
  expect(await screen.findByText("记住了这轮")).toBeTruthy();
  expect(chats).toEqual(["回车直接发"]);

  fireEvent.click(screen.getByRole("button", { name: "新对话" }));
  await waitFor(() => expect(historyCleared).toBe(true));
  expect(screen.queryByText("记住了这轮")).toBeNull();
});

test("remix-lab recovers last reply after a dropped chat", async () => {
  let chatAttempted = false;
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === "/api/remix-lab/agent/last") {
      if (!chatAttempted) return json({ found: false });
      return json({
        found: true,
        last: {
          at: new Date().toISOString(),
          message: "对照历史实验",
          reply: "找回的回复",
          proposals: [],
        },
      });
    }
    if (path === "/api/remix-lab/agent/chat" && init?.method === "POST") {
      chatAttempted = true;
      throw new Error("Failed to fetch");
    }
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));
  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  fireEvent.change(await screen.findByLabelText("对智能体说"), { target: { value: "对照历史实验" } });
  fireEvent.click(screen.getByRole("button", { name: "发送" }));
  expect(await screen.findByText("找回的回复")).toBeTruthy();
  expect(screen.getByText("刚才连接断了，已从后台找回上一轮回复。")).toBeTruthy();
});

const accountA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa";
const accountB = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb";

function labAccounts() {
  return [
    { id: accountA, name: "账号A", status: "active" },
    { id: accountB, name: "账号B", status: "active" },
  ];
}

function labProjects() {
  return [
    {
      id: "11111111-1111-1111-1111-111111111111",
      account_id: accountA,
      title: "A项目一",
      stage: "script",
      created_at: "2026-08-25T00:00:00Z",
      updated_at: "2026-08-25T00:00:00Z",
    },
    {
      id: "22222222-2222-2222-2222-222222222222",
      account_id: accountA,
      title: "A项目二",
      stage: "script",
      created_at: "2026-08-25T00:00:00Z",
      updated_at: "2026-08-25T01:00:00Z",
    },
    {
      id: "33333333-3333-3333-3333-333333333333",
      account_id: accountB,
      title: "B项目",
      stage: "mixing",
      created_at: "2026-08-25T00:00:00Z",
      updated_at: "2026-08-25T02:00:00Z",
    },
  ];
}

test("switching account remounts the canvas and fetches workflow with account_id", async () => {
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/accounts") return json(labAccounts());
    if (path === "/api/projects") return json(labProjects());
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  expect(await screen.findByText("钩子分析")).toBeTruthy();
  expect(await screen.findByRole("option", { name: "账号A" })).toBeTruthy();

  fireEvent.change(screen.getByLabelText("切换账号工作流"), { target: { value: accountA } });
  await waitFor(() => {
    const paths = api.mock.calls.map((call) => String(call[0]));
    expect(paths).toContain(`/api/remix-lab/workflow?account_id=${accountA}`);
  });
});

test("history projects group by account and filter after switching to A", async () => {
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/accounts") return json(labAccounts());
    if (path === "/api/projects") return json(labProjects());
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  expect(await screen.findByRole("button", { name: /^A项目一/ })).toBeTruthy();
  expect(screen.getByRole("button", { name: /^A项目二/ })).toBeTruthy();
  expect(screen.getByRole("button", { name: /^B项目/ })).toBeTruthy();
  const groupTitles = [...document.querySelectorAll(".remix-lab__history-group-title")].map((node) => node.textContent);
  expect(groupTitles).toEqual(["账号A", "账号B"]);

  fireEvent.change(screen.getByLabelText("切换账号工作流"), { target: { value: accountA } });
  await waitFor(() => {
    expect(screen.queryByRole("button", { name: /^B项目/ })).toBeNull();
  });
  expect(screen.getByRole("button", { name: /^A项目一/ })).toBeTruthy();
  expect(screen.getByRole("button", { name: /^A项目二/ })).toBeTruthy();
});

test("settings button calls onOpenSettings once", async () => {
  const onOpenSettings = vi.fn();
  const api = vi.fn(withLabExtras(async (path: string) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    throw new Error(`unexpected ${path}`);
  }));

  render(<RemixLabPage api={api} onNavigate={vi.fn()} onOpenSettings={onOpenSettings} />);
  fireEvent.click(await screen.findByRole("button", { name: "设置" }));
  expect(onOpenSettings).toHaveBeenCalledTimes(1);
});

test("new account form shows the account name field", async () => {
  const api = vi.fn(withLabExtras(async (path: string) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    throw new Error(`unexpected ${path}`);
  }));

  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "新增账号" }));
  expect(screen.getByLabelText("账号名称")).toBeTruthy();
});
