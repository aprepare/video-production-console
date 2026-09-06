// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
  fireEvent.click(screen.getByRole("button", { name: /对标原文/ }));
  expect(await screen.findByRole("button", { name: "开始二创" })).toBeTruthy();
  expect(screen.getByText(/对比写手模型/)).toBeTruthy();
  expect(screen.getByText(/审稿沿用已有设置/)).toBeTruthy();
  expect(screen.getByRole("button", { name: "撤销" })).toBeTruthy();
});

test("creation studio keeps account overview and history available without a published library entry", async () => {
  const api = vi.fn(withLabExtras(async (path: string) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    throw new Error(`unexpected ${path}`);
  }));
  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  await screen.findByText("钩子分析");

  expect(screen.queryByRole("button", { name: "文案库" })).toBeNull();
  expect(screen.getByRole("heading", { name: "历史项目" })).toBeTruthy();
  fireEvent.change(screen.getByRole("searchbox", { name: "搜索历史" }), {
    target: { value: "历史稿件" },
  });
  expect(screen.getByDisplayValue("历史稿件")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "账号总览" }));
  expect(await screen.findByRole("dialog", { name: "账号总览" })).toBeTruthy();
  fireEvent.keyDown(window, { key: "Escape" });
  expect(screen.queryByRole("dialog", { name: "账号总览" })).toBeNull();
  expect(screen.getByDisplayValue("历史稿件")).toBeTruthy();
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
  expect(screen.queryByText("写手预设 2")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "模型配置" }));
  expect(screen.getByRole("dialog", { name: "模型配置" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "连接与旧版预设" }));
  expect(screen.getByText("写手预设 2")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "删除模型槽 2" }));
  expect(screen.queryByText("写手预设 2")).toBeNull();
  expect((screen.getByRole("button", { name: "删除模型槽 1" }) as HTMLButtonElement).disabled).toBe(true);
});

test("Fast preset saves and survives reopening model settings", async () => {
  let saved = {
    ...defaults,
    presets: [{ base_url: "https://example.test", model: "chosen", reasoning_effort: "high", service_tier: "priority", run_count: 1, api_key_configured: true, preset_index: 0 }],
  };
  const api = vi.fn(withLabExtras(async (path, init) => {
    if (path === "/api/remix-lab/defaults") return json(saved);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === "/api/remix-lab/presets" && init?.method === "PUT") {
      const body = JSON.parse(String(init.body));
      expect(body.slots[0]).toMatchObject({ model: "chosen", reasoning_effort: "high", service_tier: "default" });
      saved = { ...saved, presets: body.slots.map((slot: Record<string, unknown>) => ({ ...slot, api_key_configured: true, preset_index: 0 })) };
      return json(saved);
    }
    throw new Error(`unexpected ${path}`);
  }));
  const page = render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "模型配置" }));
  fireEvent.click(screen.getByRole("button", { name: "连接与旧版预设" }));
  const fast = screen.getByRole("button", { name: "模型槽 1 Fast 加速模式" });
  expect(fast.getAttribute("aria-pressed")).toBe("true");
  fireEvent.click(fast);
  fireEvent.click(screen.getByRole("button", { name: "保存并完成" }));
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "连接与旧版预设" })).toBeNull());
  page.unmount();
  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "模型配置" }));
  fireEvent.click(screen.getByRole("button", { name: "连接与旧版预设" }));
  expect(screen.getByRole("button", { name: "模型槽 1 Fast 加速模式" }).getAttribute("aria-pressed")).toBe("false");
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

test("writer Fast saves independently and preserves reasoning effort", async () => {
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
  fireEvent.click((await screen.findAllByText("写手"))[0]);
  const effort = await screen.findByLabelText("写手思考强度");
  fireEvent.change(effort, { target: { value: "high" } });
  fireEvent.click(await screen.findByRole("button", { name: "写手 Fast 加速模式" }));

  await waitFor(() => expect(puts).toHaveLength(1), { timeout: 2000 });
  const nodes = puts[0].nodes as Array<{ type: string; config: { reasoning_effort?: string; model?: string; service_tier?: string } }>;
  const writer = nodes.find((node) => node.type === "writer");
  const reviewer = nodes.find((node) => node.type === "reviewer");
  expect(writer?.config.reasoning_effort).toBe("high");
  expect(writer?.config.service_tier).toBe("priority");
  expect(reviewer?.config.service_tier ?? "").toBe("");
  expect(reviewer?.config.model ?? "").toBe("");
  expect(reviewer?.config.reasoning_effort ?? "").toBe("");
  fireEvent.click(screen.getByRole("button", { name: "写手 Fast 加速模式" }));
  await waitFor(() => expect(puts).toHaveLength(2), { timeout: 2000 });
  const updated = puts[1].nodes as typeof nodes;
  expect(updated.find((node) => node.type === "writer")?.config).toMatchObject({
    reasoning_effort: "high", service_tier: "default",
  });
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
  fireEvent.click(await screen.findByRole("button", { name: /对标原文/ }));
  await screen.findByRole("button", { name: "开始二创" });
  fireEvent.change(screen.getByLabelText("对标原文"), { target: { value: "这是二创原文" } });
  fireEvent.click(screen.getByRole("button", { name: "开始二创" }));

  await waitFor(() => expect(posts).toHaveLength(1));
  expect((posts[0].body as { source: string }).source).toBe("这是二创原文");
  expect((posts[0].body as { run_count: number }).run_count).toBe(1);
  // 不跳页：画布原地切到运行视图，节点显示等待状态
  expect(onNavigate).not.toHaveBeenCalled();
  expect(await screen.findByRole("button", { name: "返回编辑" })).toBeTruthy();
  expect(await screen.findByRole("button", { name: /写手.*待执行/ })).toBeTruthy();
});

test("remix-lab imports a pasted draft at the final node and skips remix", async () => {
  const posts: Array<{ path: string; body: unknown }> = [];
  const script = "手工定稿正文。".repeat(8);
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments" && (!init?.method || init.method === "GET")) {
      return json([]);
    }
    if (path.startsWith("/api/remix-lab/workflow") && init?.method === "PUT") {
      return json(JSON.parse(String(init.body)));
    }
    if (path === "/api/remix-lab/workflow/import-draft" && init?.method === "POST") {
      const body = JSON.parse(String(init.body));
      posts.push({ path, body });
      return json(
        {
          id: experimentID,
          title: "手工定稿",
          source_text: "（手工定稿）",
          prompt_stamp: "manual-draft",
          status: "completed",
          workflow: true,
          created_at: "2026-08-25T00:00:00Z",
          updated_at: "2026-08-25T00:00:00Z",
          slots: [],
          runs: [
            {
              id: runID, experiment_id: experimentID, slot_id: "slot-1", run_index: 1,
              status: "completed", continuous_script: body.continuous_script, titles_json: "[]",
              package_json: JSON.stringify(body), draft_v1_json: "", review_json: "",
              error_message: "", comment: "", adopted_project_id: "",
              production: { status: "waiting_confirm", step: "confirm", account_id: "", auto: false },
            },
          ],
        },
        201,
      );
    }
    if (path === `/api/remix-lab/runs/${runID}/stages`) {
      return json({
        run_id: runID,
        pipeline: "workflow",
        status: "completed",
        stages: [
          { id: "source", kind: "input", title: "对标原文", status: "skipped", x: 0, y: 190 },
          { id: "writer", kind: "agent", title: "写手", status: "skipped", x: 600, y: 190 },
          { id: "final", kind: "output", title: "定稿与发布包", status: "ok", x: 1440, y: 190, output: JSON.stringify({ continuous_script: script }) },
        ],
        edges: [["source", "writer"], ["writer", "final"]],
        production: { status: "waiting_confirm", step: "confirm", account_id: "", auto: false },
      });
    }
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByText("定稿与发布包"));
  fireEvent.change(await screen.findByLabelText("定稿正文"), { target: { value: script } });
  fireEvent.change(screen.getByLabelText("板标题"), { target: { value: "板面主标题" } });
  fireEvent.click(screen.getByRole("button", { name: "作为定稿导入" }));

  await waitFor(() => expect(posts).toHaveLength(1));
  const body = posts[0].body as { continuous_script: string; short_titles: string[] };
  expect(body.continuous_script).toBe(script);
  expect(body.short_titles).toEqual(["板面主标题"]);
  expect(await screen.findByRole("button", { name: "返回编辑" })).toBeTruthy();
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
  // 多模型对比：运行页签按模型名区分，而不是两个一样的「运行 1」。
  expect(screen.getByRole("button", { name: "model-a" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "model-b" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "运行 1" })).toBeNull();
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
  fireEvent.click(await screen.findByRole("button", { name: /目标项目 · 账号/ }));
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
    // 审稿结论自带两版快照：正文和发布字段都能并排对照
    before: {
      continuous_script: "这是写手初稿正文。",
      titles: ["标题甲"],
      short_titles: ["旧板标题", "旧副标题", "旧备选"],
      descriptions: ["旧描述一", "旧描述二"],
      topics: ["#楼市", "#房贷", "#家庭理财", "#财经"],
      cta: "",
    },
    revised: {
      continuous_script:
        "这是审稿后的定稿正文，讲家里那笔钱该往哪放，一次说明白，足够四十个字了吧，再补几个字凑够下限。",
      titles: ["标题甲"],
      short_titles: ["板标题甲", "副标题乙", "备选丙"],
      descriptions: ["描述一", "描述二", "描述三"],
      topics: ["#楼市", "#房贷", "#家庭理财", "#财经"],
      cta: "",
    },
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

test("historical project aligns the account selector and switching accounts leaves its production gate", async () => {
  window.localStorage.setItem("remix-lab:produce-account", "cloud");
  const production = {run_id: runID, account_id: "research", status: "waiting_confirm", step: "confirm", auto: false};
  const detail = {...workbenchExperiment(), account_id: "research", runs: [{...workbenchRun, production}]};
  const navigate = vi.fn();
  const api = vi.fn(withLabExtras(async (path: string) => {
    if (path === "/api/accounts") return json([{id:"cloud",name:"云中观局",status:"active"},{id:"research",name:"认知研习",status:"active"}]);
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === `/api/remix-lab/experiments/${experimentID}`) return json(detail);
    if (path.endsWith("/stages")) return json({run_id:runID,status:"completed",stages:[],edges:[],production});
    throw new Error(`unexpected GET ${path}`);
  }));
  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={navigate} />);
  await waitFor(() => expect((screen.getByLabelText("切换账号工作流") as HTMLSelectElement).value).toBe("research"));
  await waitFor(() => expect((screen.getByLabelText("混剪账号") as HTMLSelectElement).value).toBe("research"));
  fireEvent.change(screen.getByLabelText("切换账号工作流"), {target:{value:"cloud"}});
  expect(navigate).toHaveBeenCalledWith("/");
  expect(window.localStorage.getItem("remix-lab:produce-account")).toBe("cloud");
  expect(screen.queryByRole("button", {name:"确认开始混剪"})).toBeNull();
});

test("late historical detail cannot restore an account after the user switched away", async () => {
  window.localStorage.setItem("remix-lab:produce-account", "research");
  let finish!: (value: Response) => void;
  const pending = new Promise<Response>(resolve => { finish=resolve; });
  const navigate=vi.fn();
  const api=vi.fn(withLabExtras(async(path:string)=>{
    if(path==="/api/accounts") return json([{id:"cloud",name:"云中观局",status:"active"},{id:"research",name:"认知研习",status:"active"}]);
    if(path==="/api/remix-lab/defaults") return json(defaults);
    if(path==="/api/remix-lab/experiments") return json([]);
    if(path===`/api/remix-lab/experiments/${experimentID}`) return pending;
    throw new Error(`unexpected GET ${path}`);
  }));
  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={navigate} />);
  await screen.findByRole("option", {name:"云中观局"});
  fireEvent.change(screen.getByLabelText("切换账号工作流"),{target:{value:"cloud"}});
  finish(json({...workbenchExperiment(),account_id:"research"}));
  await waitFor(()=>expect(navigate).toHaveBeenCalledWith("/"));
  expect((screen.getByLabelText("切换账号工作流") as HTMLSelectElement).value).toBe("cloud");
  expect(screen.queryByRole("button",{name:"改稿"})).toBeNull();
});

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
  // 审稿结论与审稿前/后对照：正文默认只看改动（句级 diff），短标题、描述并排给出
  expect(await screen.findByText("审稿已修订")).toBeTruthy();
  expect(screen.getByText(/审稿改了 2 句（删 1 · 加 1）/)).toBeTruthy();
  const deleted = screen.getByText("这是写手初稿正文。");
  expect(deleted.className).toContain("remix-lab-diff__del");
  // 切到并排全文再切回来
  fireEvent.click(screen.getByRole("button", { name: "并排全文" }));
  expect(screen.getAllByText(/这是写手初稿正文。/).length).toBeGreaterThan(0);
  fireEvent.click(screen.getByRole("button", { name: "只看改动" }));
  expect(screen.getByText("旧板标题")).toBeTruthy();
  expect(screen.getByText("旧描述一")).toBeTruthy();
  expect(screen.getByText(/审稿改了 3 处字段：正文、短标题、视频描述/)).toBeTruthy();
  // 话题、长标题两版一致，只显示一遍
  expect(screen.getAllByText("两版一致").length).toBe(2);
  expect(screen.getByDisplayValue("板标题甲")).toBeTruthy();

  // 不采用审稿改的短标题：一键回填写手那版到编辑器
  fireEvent.click(screen.getByRole("button", { name: "短标题用审稿前" }));
  expect(screen.getByDisplayValue("旧板标题")).toBeTruthy();
  expect(screen.queryByDisplayValue("板标题甲")).toBeNull();
  // 正文默认是审稿后那版；采用审稿前正文再切回审稿后
  fireEvent.click(screen.getByRole("button", { name: "正文用审稿前" }));
  expect(screen.getByDisplayValue("这是写手初稿正文。")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "正文用审稿后" }));

  // 编辑正文并保存：短标题保存的是采用的审稿前版本
  const script = screen.getByLabelText("运行 1 正文");
  fireEvent.change(script, {
    target: { value: "改过之后的定稿正文，讲家里那笔钱该往哪放，一次说明白，足够四十个字了吧，再补几个字凑够下限。" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存修改" }));
  await waitFor(() => expect(packagePuts).toHaveLength(1));
  expect(String(packagePuts[0].body.continuous_script)).toContain("改过之后的定稿正文");
  expect(packagePuts[0].body.short_titles).toEqual(["旧板标题", "旧副标题", "旧备选"]);
  expect(packagePuts[0].body.descriptions).toEqual(["描述一", "描述二", "描述三"]);

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
  expect(await screen.findByRole("button", { name: /写手/ })).toBeTruthy();
  expect(await screen.findByRole("button", { name: /审稿终审/ })).toBeTruthy();
  // 默认选中第一个失败节点，检视器里能看到错误
  expect(await screen.findByText("ammo agent down")).toBeTruthy();
  expect(screen.getByRole("button", { name: "编辑这路Agent提示词" })).toBeTruthy();
  // 审稿节点：结论按人话展示，原始 JSON 收进折叠
  fireEvent.click(screen.getByText("审稿终审"));
  expect(await screen.findByText("审稿通过")).toBeTruthy();
  expect(screen.getByText("原始 review.json")).toBeTruthy();
});

test("final node shows the tidy script and copy buttons instead of raw JSON", async () => {
  const finalPackage = {
    continuous_script: "明年开始，9样东西掉价掉到你不敢认。\n\n先把自家日子过稳当了。",
    titles: ["候选标题一", "候选标题二"],
    short_titles: ["板面主标题", "副标题", "备选"],
    descriptions: ["描述一：这条内容顺手存一下。"],
    topics: ["#楼市", "#财经"],
    cta: "课名《财富觉醒方法论》在主页橱窗，5块钱。",
  };
  const stagesFixture = {
    run_id: runID,
    pipeline: "workflow",
    status: "completed",
    stages: [
      { id: "source", kind: "input", title: "对标原文", status: "ok", x: 0, y: 190, output: "原文" },
      { id: "final", kind: "output", title: "定稿与发布包", status: "ok", x: 600, y: 190, output: JSON.stringify(finalPackage) },
    ],
    edges: [["source", "final"]],
  };
  const packagePuts: Array<Record<string, unknown>> = [];
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === `/api/remix-lab/experiments/${experimentID}`) return json(workbenchExperiment());
    if (path === `/api/remix-lab/runs/${runID}/stages`) return json(stagesFixture);
    if (path === `/api/remix-lab/runs/${runID}/package` && init?.method === "PUT") {
      packagePuts.push(JSON.parse(String(init.body)) as Record<string, unknown>);
      return json({ status: "ok" });
    }
    if (path === `/api/remix-lab/runs/${runID}` && init?.method === "PATCH") return json({ status: "ok" });
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByText("定稿与发布包"));

  // 正文按文章排版展示（原始 JSON 折叠里还有一份，允许多处匹配）
  expect((await screen.findAllByText(/明年开始，9样东西掉价掉到你不敢认/)).length).toBeGreaterThan(0);
  expect(screen.getByRole("button", { name: "复制正文" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "板面主标题" })).toBeTruthy();
  // 发布包精简：不再列候选标题，话题已接在描述末尾、不单独给复制按钮
  expect(screen.queryByRole("button", { name: "候选标题一" })).toBeNull();
  expect(screen.getByRole("button", { name: "复制描述 1" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "复制话题 #楼市 #财经" })).toBeNull();
  // 原始 JSON 收进折叠里备查
  expect(screen.getByText("原始 JSON")).toBeTruthy();

  // 就地编辑定稿：改正文和短标题后保存，混剪用新稿
  fireEvent.click(screen.getByRole("button", { name: "编辑定稿" }));
  const editedScript = "手改后的口播正文。".repeat(10);
  fireEvent.change(screen.getByLabelText("编辑定稿正文"), { target: { value: editedScript } });
  fireEvent.change(screen.getByLabelText("编辑短标题"), { target: { value: "新板面主标题\n新副标题\n新备选" } });
  fireEvent.click(screen.getByRole("button", { name: "保存定稿" }));
  await waitFor(() => expect(packagePuts).toHaveLength(1));
  expect(packagePuts[0].continuous_script).toBe(editedScript);
  expect(packagePuts[0].short_titles).toEqual(["新板面主标题", "新副标题", "新备选"]);
  expect(packagePuts[0].topics).toEqual(["#楼市", "#财经"]);
  // 保存成功后退出编辑态
  await waitFor(() => expect(screen.queryByLabelText("编辑定稿正文")).toBeNull());
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
    // 真实后端有生产记录时 stages 响应总带 production；不带的话画布会把
    // 闸门状态覆盖成 null，确认按钮消失（之前靠通用兜底 fixture 是碰运气）。
    if (path === `/api/remix-lab/runs/${runID}/stages`) {
      return json({
        run_id: runID, pipeline: "workflow", status: "completed",
        production: { status: "waiting_confirm", step: "confirm", account_id: "", auto: false },
        stages: [], edges: [],
      });
    }
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

  // 前面的用例会把选中账号写进 localStorage，带着它渲染会走另一条工作流请求
  // 路径，闸门按钮出现时机不同；这里从干净状态开始。
  window.localStorage.clear();
  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />);
  // 闸门按钮要等实验详情 + stages 两轮请求都回来；整套并发跑时 1s 默认超时偶发不够。
  expect(await screen.findByRole("button", { name: "确认开始混剪" }, { timeout: 5000 })).toBeTruthy();
  // 闸门不再自动塞第一个账号：等账号列表回来，由操作员选；选完不切换页面工作流。
  const gateSelect = screen.getByLabelText("混剪账号") as HTMLSelectElement;
  await waitFor(() => expect(gateSelect.querySelector('option[value="acct-9"]')).toBeTruthy());
  expect(gateSelect.value).toBe("");
  fireEvent.change(gateSelect, { target: { value: "acct-9" } });
  expect(api).not.toHaveBeenCalledWith(expect.stringContaining("workflow?account_id=acct-9"), undefined);
  fireEvent.click(screen.getByRole("button", { name: "确认开始混剪" }));
  await waitFor(() => expect(produces).toHaveLength(1));
  expect(produces[0].account_id).toBe("acct-9");
});

test("produce gate defaults to the run's own account and warns when switched", async () => {
  const produces: Array<Record<string, string>> = [];
  const production = { status: "waiting_confirm", step: "confirm", account_id: "acct-2", auto: false };
  const gateExperiment = () => {
    const exp = workbenchExperiment();
    return { ...exp, produce_account_id: "acct-2", runs: exp.runs.map((run) => ({ ...run, production })) };
  };
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === `/api/remix-lab/experiments/${experimentID}`) return json(gateExperiment());
    if (path === `/api/remix-lab/runs/${runID}/stages`) {
      return json({ run_id: runID, pipeline: "workflow", status: "completed", production, stages: [], edges: [] });
    }
    if (path === "/api/accounts") {
      return json([
        { id: "acct-1", name: "云中观局", status: "active" },
        { id: "acct-2", name: "财经漫游", status: "active" },
      ]);
    }
    if (path === `/api/remix-lab/runs/${runID}/produce` && init?.method === "POST") {
      produces.push(JSON.parse(String(init.body)) as Record<string, string>);
      return json({ status: "ok" }, 202);
    }
    if (path === `/api/remix-lab/runs/${runID}` && init?.method === "PATCH") return json({ status: "ok" });
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  window.localStorage.clear();
  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />);
  expect(await screen.findByRole("button", { name: "确认开始混剪" }, { timeout: 5000 })).toBeTruthy();
  const gateSelect = screen.getByLabelText("混剪账号") as HTMLSelectElement;
  await waitFor(() => expect(gateSelect.querySelector('option[value="acct-2"]')).toBeTruthy());
  // 这稿是财经漫游跑出来的，闸门默认就是财经漫游，不沾上一稿选过的账号。
  await waitFor(() => expect(gateSelect.value).toBe("acct-2"));
  expect(screen.queryByText(/确认要进别的号/)).toBeNull();
  fireEvent.change(gateSelect, { target: { value: "acct-1" } });
  expect(screen.getByText(/这稿是按「财经漫游」写的/)).toBeTruthy();
  fireEvent.change(gateSelect, { target: { value: "acct-2" } });
  fireEvent.click(screen.getByRole("button", { name: "确认开始混剪" }));
  await waitFor(() => expect(produces).toHaveLength(1));
  expect(produces[0].account_id).toBe("acct-2");
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

// 智能体面板暂时隐藏（AGENT_PANEL_HIDDEN），恢复面板时把下面四个 skip 撤掉。
test.skip("remix-lab shows agent thinking progress until the reply arrives", async () => {
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

test.skip("remix-lab restores the saved agent conversation", async () => {
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

test.skip("remix-lab sends on Enter and starts a fresh chat", async () => {
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

test.skip("remix-lab recovers last reply after a dropped chat", async () => {
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

test("history rail lists recent experiments and filters them by account", async () => {
  const onNavigate = vi.fn();
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/accounts") {
      return json([
        { id: "acct-a", name: "账号A", status: "active" },
        { id: "acct-b", name: "账号B", status: "active" },
      ]);
    }
    if (path === "/api/remix-lab/experiments") {
      return json([
        {
          id: "exp-waiting", title: "现金为王的时代真的要来了", prompt_stamp: "s",
          status: "completed", workflow: true, account_id: "acct-a",
          created_at: "2026-08-31T08:12:00Z", updated_at: "2026-08-31T08:27:05Z",
        },
        {
          id: "exp-broken", title: "被重启打断的一次", prompt_stamp: "s",
          status: "failed", workflow: true, account_id: "acct-b",
          created_at: "2026-08-31T08:35:09Z", updated_at: "2026-08-31T08:41:00Z",
        },
      ]);
    }
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  render(<RemixLabPage api={api} onNavigate={onNavigate} />);

  // 没选账号（全局默认工作流）：全量展示，标题带条数，全局视图下每条标账号
  expect(await screen.findByText(/内容 2 条/)).toBeTruthy();
  expect(screen.getByText("二创完成")).toBeTruthy();
  expect(screen.getByText("二创失败")).toBeTruthy();
  fireEvent.click(screen.getByText("现金为王的时代真的要来了"));
  expect(onNavigate).toHaveBeenCalledWith("/remix-lab/exp-waiting");

  // 搜索框按标题过滤
  fireEvent.change(screen.getByLabelText("搜索历史"), { target: { value: "重启" } });
  await waitFor(() => expect(screen.queryByText("现金为王的时代真的要来了")).toBeNull());
  expect(screen.getByText("被重启打断的一次")).toBeTruthy();
  fireEvent.change(screen.getByLabelText("搜索历史"), { target: { value: "" } });

  // 切到账号A：只剩A的实验，B的隐藏
  fireEvent.change(screen.getByLabelText("切换账号工作流"), { target: { value: "acct-a" } });
  await waitFor(() => expect(screen.queryByText("被重启打断的一次")).toBeNull());
  expect(screen.getByText("现金为王的时代真的要来了")).toBeTruthy();
});

test("history projects group by account and filter after switching to A", async () => {
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/accounts") return json(labAccounts());
    if (path === "/api/projects") return json(labProjects());
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  // 这些项目没有对应的二创实验 → 列在「独立混剪项目」里，全局视图下每条带账号名
  expect(await screen.findByRole("button", { name: /^A项目一/ })).toBeTruthy();
  expect(screen.getByRole("button", { name: /^A项目二/ })).toBeTruthy();
  expect(screen.getByRole("button", { name: /^B项目/ })).toBeTruthy();
  expect(screen.getByText(/独立混剪项目 3 条（未关联二创记录）/)).toBeTruthy();
  expect(screen.getAllByText("账号A").length).toBeGreaterThan(0);

  fireEvent.change(screen.getByLabelText("切换账号工作流"), { target: { value: accountA } });
  await waitFor(() => {
    expect(screen.queryByRole("button", { name: /^B项目/ })).toBeNull();
  });
  expect(screen.getByRole("button", { name: /^A项目一/ })).toBeTruthy();
  expect(screen.getByRole("button", { name: /^A项目二/ })).toBeTruthy();
});

test("global settings live outside the copy workspace", async () => {
  const onOpenSettings = vi.fn();
  const api = vi.fn(withLabExtras(async (path: string) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    throw new Error(`unexpected ${path}`);
  }));

  render(<RemixLabPage api={api} onNavigate={vi.fn()} onOpenSettings={onOpenSettings} />);
  expect(screen.getByRole("list", { name: "内容生产流程" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "设置" })).toBeNull();
  expect(onOpenSettings).not.toHaveBeenCalled();
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

test("fixed workflow steps save configuration without changing stored graph positions", async () => {
  const puts: Array<Record<string, unknown>> = [];
  const messyWorkflow = {
    ...workflowFixture,
    nodes: workflowFixture.nodes.map((node) =>
      node.id === "hook"
        ? { ...node, x: 777, y: 555 }
        : node.id === "writer"
          ? { ...node, x: 50, y: 900 }
          : node,
    ),
    production: { captions_disabled: true, node_positions: { "produce-montage": { x: 999, y: 888 } } },
  };
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path.startsWith("/api/remix-lab/workflow") && (!init?.method || init.method === "GET")) {
      return json(messyWorkflow);
    }
    if (path.startsWith("/api/remix-lab/workflow") && init?.method === "PUT") {
      const body = JSON.parse(String(init.body)) as Record<string, unknown>;
      puts.push(body);
      return json(body);
    }
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  const { container } = render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: /钩子分析/ }));
  expect(screen.getByRole("navigation", { name: /流程步骤/ })).toBeTruthy();
  expect(container.querySelector(".react-flow")).toBeNull();
  expect(screen.queryByRole("button", { name: "一键整理" })).toBeNull();
  fireEvent.change(screen.getByLabelText("节点系统提示词"), { target: { value: "固定步骤中的新策划规则" } });

  await waitFor(() => {
    const last = puts[puts.length - 1] as
      | { nodes?: Array<{ id: string; x: number; y: number; config: { system_prompt?: string } }>; edges?: unknown; production?: { node_positions?: unknown } }
      | undefined;
    expect(last).toBeTruthy();
    const byID = new Map((last?.nodes ?? []).map((node) => [node.id, node]));
    expect(byID.get("hook")?.config.system_prompt).toBe("固定步骤中的新策划规则");
    expect(last?.nodes?.map(({ id, x, y }) => ({ id, x, y }))).toEqual(
      messyWorkflow.nodes.map(({ id, x, y }) => ({ id, x, y })),
    );
    expect(last?.edges).toEqual(messyWorkflow.edges);
    expect(last?.production).toEqual(messyWorkflow.production);
  }, { timeout: 3000 });
});

test("spoken node lets the operator edit and save the script before narration", async () => {
  const uploads: string[] = [];
  const producedExperiment = () => {
    const exp = workbenchExperiment();
    return {
      ...exp,
      runs: exp.runs.map((run) => ({
        ...run,
        production: {
          status: "completed", step: "done", account_id: "acct-9", auto: false, project_id: "proj-1",
        },
      })),
    };
  };
  const stagesFixture = {
    run_id: runID,
    pipeline: "workflow",
    status: "completed",
    production: { status: "completed", step: "done", account_id: "acct-9", auto: false, project_id: "proj-1" },
    stages: [
      {
        id: "produce-spoken", kind: "produce", title: "口播稿", status: "ok", x: 370, y: 430,
        extra: { production: true, production_step: "spoken", project_id: "proj-1", asset_type: "spoken_script" },
      },
    ],
    edges: [],
  };
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === `/api/remix-lab/experiments/${experimentID}`) return json(producedExperiment());
    if (path === `/api/remix-lab/runs/${runID}/stages`) return json(stagesFixture);
    if (path === "/api/projects/proj-1" && (!init?.method || init.method === "GET")) {
      return json({
        project: { id: "proj-1", stage: "review" },
        assets: { spoken_script: { id: "spoken-1", state: "ready" } },
      });
    }
    if (path === "/api/assets/spoken-1/content") {
      return new Response("手里有钱的都听好了\n翻来覆去睡不着的夜", { status: 200 });
    }
    if (path === "/api/projects/proj-1/assets/spoken_script" && init?.method === "POST") {
      uploads.push(path);
      return json({ status: "ok" });
    }
    if (path === `/api/remix-lab/runs/${runID}` && init?.method === "PATCH") return json({ status: "ok" });
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />);
  fireEvent.click(await within(await screen.findByRole("navigation", { name: "运行步骤" })).findByRole("button", { name: /口播稿/ }));
  expect(await screen.findByText(/手里有钱的都听好了/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "编辑口播稿" }));
  fireEvent.change(screen.getByLabelText("编辑口播稿"), {
    target: { value: "手里有钱的都听好了\n先把手揣兜里" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存口播稿" }));
  await waitFor(() => expect(uploads).toHaveLength(1));
  await waitFor(() => expect(screen.queryByLabelText("编辑口播稿")).toBeNull());
});

test("produce nodes offer redo, export and publish actions on the canvas", async () => {
  const redos: Array<Record<string, string>> = [];
  const publishes: string[] = [];
  const producedExperiment = () => {
    const exp = workbenchExperiment();
    return {
      ...exp,
      runs: exp.runs.map((run) => ({
        ...run,
        production: {
          status: "completed", step: "done", account_id: "acct-9", auto: false, project_id: "proj-1",
        },
      })),
    };
  };
  const stagesFixture = {
    run_id: runID,
    pipeline: "workflow",
    status: "completed",
    production: { status: "completed", step: "done", account_id: "acct-9", auto: false, project_id: "proj-1" },
    stages: [
      { id: "source", kind: "input", title: "对标原文", status: "ok", x: 0, y: 190, output: "原文" },
      { id: "final", kind: "output", title: "定稿与发布包", status: "ok", x: 600, y: 190, output: "{}" },
      {
        id: "produce-montage", kind: "produce", title: "混剪草稿", status: "ok", x: 600, y: 430,
        system_prompt: "混剪提示词",
        extra: { production: true, production_step: "montage", project_id: "proj-1", task_id: "task-m" },
      },
      {
        id: "produce-publish", kind: "produce", title: "发布", status: "waiting", x: 830, y: 430,
        extra: {
          production: true, production_step: "publish", project_id: "proj-1",
          publishing: { descriptions: ["视频描述一"], short_titles: ["板题", "副题"], topics: ["#楼市"] },
        },
      },
    ],
    edges: [["source", "final"], ["final", "produce-montage"], ["produce-montage", "produce-publish"]],
  };
  const api = vi.fn(withLabExtras(async (path: string, init?: RequestInit) => {
    if (path === "/api/remix-lab/defaults") return json(defaults);
    if (path === "/api/remix-lab/experiments") return json([]);
    if (path === `/api/remix-lab/experiments/${experimentID}`) return json(producedExperiment());
    if (path === `/api/remix-lab/runs/${runID}/stages`) return json(stagesFixture);
    if (path === "/api/tasks/task-m") return json({ status: "completed" });
    if (path === "/api/projects/proj-1" && (!init?.method || init.method === "GET")) {
      return json({
        project: { id: "proj-1", stage: "review" },
        assets: {
          mix_draft: { id: "draft-1", state: "ready" },
          narration: { id: "narr-1", state: "ready" },
        },
      });
    }
    if (path === `/api/remix-lab/runs/${runID}/produce/redo` && init?.method === "POST") {
      redos.push(JSON.parse(String(init.body)) as Record<string, string>);
      return json({ status: "ok" }, 202);
    }
    if (path === "/api/projects/proj-1/publish" && init?.method === "POST") {
      publishes.push(path);
      return json({ id: "proj-1", stage: "published" });
    }
    if (path === `/api/remix-lab/runs/${runID}` && init?.method === "PATCH") return json({ status: "ok" });
    throw new Error(`unexpected ${init?.method || "GET"} ${path}`);
  }));

  render(<RemixLabPage api={api} experimentID={experimentID} onNavigate={vi.fn()} />);

  // 混剪草稿节点：导出视频、打开目录、重出草稿
  fireEvent.click(await screen.findByText("混剪草稿"));
  expect(await screen.findByRole("button", { name: "导出视频" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "在电脑上打开剪映目录" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "重出一份剪映草稿（旧的保留）" }));
  await waitFor(() => expect(redos).toHaveLength(1));
  expect(redos[0]).toEqual({ step: "montage" });

  // 发布节点：发布文案复制入口 + 确认已发布
  fireEvent.click(screen.getByText("发布"));
  expect(await screen.findByText("视频描述一")).toBeTruthy();
  expect(screen.getByRole("button", { name: "复制描述 1" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "板题" })).toBeTruthy();
  fireEvent.click(await screen.findByRole("button", { name: "确认已发布" }));
  await waitFor(() => expect(publishes).toHaveLength(1));
});

test('source draft survives account switches and canvas remounts', async () => {
  window.sessionStorage.clear();
  const api = vi.fn(withLabExtras(async (path: string) => {
    if (path === '/api/remix-lab/defaults') return json(defaults);
    if (path === '/api/accounts') return json([{id:'draft-a',name:'草稿账号A',status:'active'},{id:'draft-b',name:'草稿账号B',status:'active'}]);
    if (path === '/api/projects') return json([]);
    if (path.startsWith('/api/remix-lab/experiments')) return json([]);
    throw new Error(`unexpected ${path}`);
  }));
  render(<RemixLabPage api={api} onNavigate={vi.fn()} />);
  await screen.findByRole('option',{name:'草稿账号A'});
  fireEvent.change(screen.getByLabelText('切换账号工作流'),{target:{value:'draft-a'}});
  fireEvent.click(await screen.findByRole('button', { name: /对标原文/ }));
  fireEvent.change(screen.getByLabelText('对标原文'),{target:{value:'账号A尚未提交的原文'}});
  fireEvent.change(screen.getByLabelText('切换账号工作流'),{target:{value:'draft-b'}});
  fireEvent.click(await screen.findByRole('button', { name: /对标原文/ }));
  expect((screen.getByLabelText('对标原文') as HTMLTextAreaElement).value).toBe('');
  fireEvent.change(screen.getByLabelText('切换账号工作流'),{target:{value:'draft-a'}});
  fireEvent.click(await screen.findByRole('button', { name: /对标原文/ }));
  expect((screen.getByLabelText('对标原文') as HTMLTextAreaElement).value).toBe('账号A尚未提交的原文');
  window.sessionStorage.clear();
});
