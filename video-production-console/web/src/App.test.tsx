// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import App from "./App";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const publicSettings = {
  listen_addr: "127.0.0.1:8080",
  data_root: "data",
  max_codex_concurrency: 2,
  baokuan_base_url: "",
  baokuan_mcp_executable: "",
  obsidian_vault: "",
  topic_cards_dir: "",
  grok_base_url: "",
  grok_model: "",
  codex_binary_path: "codex",
  media_index_path: "",
  media_root: "",
  jianying_root: "",
  codex_default_model: "gpt-default",
  codex_default_reasoning_effort: "high",
};

function baseFetch(
  handler: (path: string, method: string, init?: RequestInit) => Response | undefined,
) {
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = typeof input === "string" ? input : input.toString();
    const method = (init?.method || "GET").toUpperCase();
    const response = handler(path, method, init);
    if (response) return response;
    if (path === "/api/auth/me") return json({ csrfToken: "csrf" });
    if (path === "/api/accounts")
      return json([{ id: "account-1", name: "认知觉醒" }]);
    if (path === "/api/runtime") return json({ Limit: 4, Running: 0, Queued: 0 });
    if (path === "/api/settings")
      return json({ public: publicSettings, settings_version: 1, secrets: {} });
    throw new Error(`unexpected request: ${method} ${path}`);
  });
}

test("settings show model defaults and use the PUT response as the saved draft", async () => {
  let savedBody: Record<string, unknown> | undefined;
  vi.stubGlobal(
    "fetch",
    baseFetch((path, method, init) => {
      if (path === "/api/projects") return json([]);
      if (path === "/api/settings" && method === "PUT") {
        savedBody = JSON.parse(String(init?.body));
        return json({
          public: {
            ...publicSettings,
            codex_default_model: "gpt-from-server",
            codex_default_reasoning_effort: "ultra",
          },
          settings_version: 2,
          secrets: {},
        });
      }
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "设置" }));

  const model = await screen.findByRole("textbox", { name: "默认模型" });
  const effort = screen.getByRole("combobox", { name: "默认推理强度" });
  expect((model as HTMLInputElement).value).toBe("gpt-default");
  expect((effort as HTMLSelectElement).value).toBe("high");
  expect(
    screen.getByText(
      "默认值只影响之后新建的任务，不会修改运行中任务，也不会改写本机 Codex 全局配置。",
    ),
  ).toBeTruthy();

  fireEvent.change(model, { target: { value: "gpt-edited" } });
  fireEvent.change(effort, { target: { value: "max" } });
  fireEvent.click(screen.getByRole("button", { name: "保存设置" }));

  await waitFor(() => expect((model as HTMLInputElement).value).toBe("gpt-from-server"));
  expect((effort as HTMLSelectElement).value).toBe("ultra");
  expect(savedBody).toMatchObject({
    public: {
      codex_default_model: "gpt-edited",
      codex_default_reasoning_effort: "max",
    },
  });
});

test("a project workflow sends and resets only explicit model overrides", async () => {
  const taskBodies: Record<string, unknown>[] = [];
  const project = {
    id: "project-1",
    account_id: "account-1",
    title: "养老金选题",
    stage: "topic",
  };
  vi.stubGlobal(
    "fetch",
    baseFetch((path, method, init) => {
      if (path === "/api/projects") return json([project]);
      if (path === "/api/projects/project-1")
        return json({ project, assets: {}, missing_assets: [] });
      if (path === "/api/tasks?project_id=project-1") return json([]);
      if (path === "/api/projects/project-1/tasks" && method === "POST") {
        taskBodies.push(JSON.parse(String(init?.body)));
        return json({ id: "task-new" }, 202);
      }
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByText("养老金选题"));
  fireEvent.click(await screen.findByText("模型与推理强度（可选）"));
  fireEvent.change(screen.getByRole("textbox", { name: "临时模型" }), {
    target: { value: "gpt-project" },
  });
  fireEvent.change(screen.getByRole("combobox", { name: "临时推理强度" }), {
    target: { value: "xhigh" },
  });
  expect(screen.getByText("实际将使用：gpt-project · xhigh")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "二创文案" }));

  await waitFor(() => expect(taskBodies).toHaveLength(1));
  expect(taskBodies[0]).toMatchObject({
    type: "remix",
    model: "gpt-project",
    reasoning_effort: "xhigh",
  });
  expect((screen.getByRole("textbox", { name: "临时模型" }) as HTMLInputElement).value).toBe("");
  expect(
    (screen.getByRole("combobox", { name: "临时推理强度" }) as HTMLSelectElement)
      .value,
  ).toBe("");
});

test("the topic composer has an independent temporary model override", async () => {
  let messageBody: Record<string, unknown> | undefined;
  vi.stubGlobal(
    "fetch",
    baseFetch((path, method, init) => {
      if (path === "/api/projects") return json([]);
      if (path === "/api/ideas" && method === "GET") return json([]);
      if (path === "/api/ideas" && method === "POST")
        return json({
          id: "idea-new",
          account_id: "account-1",
          title: "新选题规划",
          status: "planning",
        });
      if (path === "/api/ideas/idea-new/messages" && method === "POST") {
        messageBody = JSON.parse(String(init?.body));
        return json({ task_id: "task-new" }, 202);
      }
      if (path === "/api/ideas/idea-new")
        return json({
          session: {
            id: "idea-new",
            account_id: "account-1",
            title: "新选题规划",
            status: "planning",
          },
          messages: [],
          candidates: [],
        });
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "给我选题" }));
  fireEvent.click(await screen.findByText("模型与推理强度（可选）"));
  fireEvent.change(screen.getByRole("textbox", { name: "选题临时模型" }), {
    target: { value: "gpt-idea" },
  });
  fireEvent.change(screen.getByPlaceholderText("输入你的想法或追问"), {
    target: { value: "给我一个养老选题" },
  });
  fireEvent.click(screen.getByRole("button", { name: "发送" }));

  await waitFor(() => expect(messageBody).toBeDefined());
  expect(messageBody).toMatchObject({ content: "给我一个养老选题", model: "gpt-idea" });
  expect(messageBody).not.toHaveProperty("reasoning_effort");
  expect(
    (screen.getByRole("textbox", { name: "选题临时模型" }) as HTMLInputElement)
      .value,
  ).toBe("");
});

test("topic planner lets the user choose the account used for confirmation", async () => {
  let confirmationBody: Record<string, unknown> | undefined;
  const session = {
    id: "idea-1",
    account_id: "account-2",
    title: "养老规划",
    status: "planning",
  };
  vi.stubGlobal(
    "fetch",
    baseFetch((path, method, init) => {
      if (path === "/api/accounts")
        return json([
          { id: "account-1", name: "账号一" },
          { id: "account-2", name: "账号二" },
        ]);
      if (path === "/api/projects") return json([]);
      if (path === "/api/ideas" && method === "GET") return json([session]);
      if (path === "/api/ideas/idea-1")
        return json({
          session,
          messages: [],
          candidates: [
            { id: "candidate-1", title: "存款到期", summary: "摘要" },
          ],
        });
      if (path === "/api/ideas/idea-1/select" && method === "POST") {
        confirmationBody = JSON.parse(String(init?.body));
        return json({
          project: {
            id: "project-new",
            account_id: "account-2",
            title: "存款到期",
            stage: "topic",
          },
        }, 201);
      }
      if (path === "/api/projects/project-new")
        return json({
          project: {
            id: "project-new",
            account_id: "account-2",
            title: "存款到期",
            stage: "topic",
          },
          assets: {},
          missing_assets: [],
        });
      if (path === "/api/tasks?project_id=project-new") return json([]);
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "账号二" }));
  fireEvent.click(screen.getByRole("button", { name: "给我选题" }));
  const selector = await screen.findByRole("combobox", { name: "选题账号" });
  fireEvent.change(selector, { target: { value: "account-2" } });
  fireEvent.click(await screen.findByRole("button", { name: "确认并建项目" }));

  await waitFor(() => expect(confirmationBody).toBeDefined());
  expect(confirmationBody).toMatchObject({
    candidate_id: "candidate-1",
    account_id: "account-2",
  });
});

test("topic planner keeps the sidebar account instead of restoring another account session", async () => {
  const accountOneSession = {
    id: "idea-account-one",
    account_id: "account-1",
    title: "账号一的旧选题",
    status: "planning",
  };
  vi.stubGlobal(
    "fetch",
    baseFetch((path, method) => {
      if (path === "/api/accounts")
        return json([
          { id: "account-1", name: "账号一" },
          { id: "account-2", name: "账号二" },
        ]);
      if (path === "/api/projects") return json([]);
      if (path === "/api/ideas" && method === "GET") return json([accountOneSession]);
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "账号二" }));
  fireEvent.click(screen.getByRole("button", { name: "给我选题" }));

  const selector = await screen.findByRole("combobox", { name: "选题账号" });
  expect((selector as HTMLSelectElement).value).toBe("account-2");
  expect(screen.getByRole("heading", { name: "新选题规划" })).toBeTruthy();
  expect(screen.queryByRole("heading", { name: "账号一的旧选题" })).toBeNull();
});

test("a created project stays visible when its detail request fails", async () => {
  const requests: Array<{ path: string; method: string }> = [];
  const session = {
    id: "idea-1",
    account_id: "account-1",
    title: "养老规划",
    status: "planning",
  };
  const project = {
    id: "project-created",
    account_id: "account-1",
    title: "存款到期新变化",
    stage: "topic",
  };
  vi.stubGlobal(
    "fetch",
    baseFetch((path, method) => {
      requests.push({ path, method });
      if (path === "/api/projects") return json([]);
      if (path === "/api/ideas" && method === "GET") return json([session]);
      if (path === "/api/ideas/idea-1")
        return json({
          session,
          messages: [],
          candidates: [{ id: "candidate-1", title: project.title, summary: "摘要" }],
        });
      if (path === "/api/ideas/idea-1/select" && method === "POST")
        return json({ project }, 201);
      if (path === "/api/projects/project-created")
        return json({ message: "temporary failure" }, 500);
      if (path === "/api/tasks?project_id=project-created") return json([]);
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "给我选题" }));
  fireEvent.click(await screen.findByRole("button", { name: "确认并建项目" }));

  expect(await screen.findByRole("heading", { name: project.title })).toBeTruthy();
  expect(await screen.findByText("项目详情暂时无法读取")).toBeTruthy();
  expect(screen.getByRole("button", { name: "重试读取详情" })).toBeTruthy();
  expect(requests.some((item) => item.method === "DELETE")).toBe(false);
});

test("tasks show their actual model and awaiting replies keep it read-only", async () => {
  const project = {
    id: "project-1",
    account_id: "account-1",
    title: "养老金选题",
    stage: "topic",
  };
  const task = {
    id: "task-1",
    project_id: "project-1",
    type: "remix",
    skill_name: "二创",
    status: "awaiting_input",
    created_at: "2026-08-04T00:00:00Z",
    model: "gpt-actual",
    reasoning_effort: "max",
  };
  vi.stubGlobal(
    "fetch",
    baseFetch((path) => {
      if (path === "/api/projects") return json([project]);
      if (path === "/api/projects/project-1")
        return json({ project, assets: {}, missing_assets: [] });
      if (path === "/api/tasks?project_id=project-1") return json([task]);
      if (path === "/api/tasks/task-1") return json(task);
      if (path === "/api/tasks/task-1/semantic-events?limit=20")
        return json({ events: [] });
      if (path === "/api/tasks/task-1/result") return json({});
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByText("养老金选题"));

  expect(await screen.findByText("gpt-actual · max")).toBeTruthy();
  const continueHint = screen.getByText("继续使用：gpt-actual · max");
  expect(continueHint).toBeTruthy();
  expect(continueHint.parentElement?.querySelector("input, select")).toBeNull();
});

test("a running task exposes a stop action and sends the cancellation request", async () => {
  const requests: Array<{ path: string; method: string }> = [];
  const project = {
    id: "project-1",
    account_id: "account-1",
    title: "养老金选题",
    stage: "script",
  };
  const task = {
    id: "task-running",
    project_id: "project-1",
    type: "remix",
    skill_name: "finance-viral-remix",
    status: "running",
    created_at: "2026-08-07T00:00:00Z",
    model: "gpt-5.6-sol",
    reasoning_effort: "medium",
  };
  vi.stubGlobal("confirm", vi.fn(() => true));
  vi.stubGlobal(
    "fetch",
    baseFetch((path, method) => {
      requests.push({ path, method });
      if (path === "/api/projects") return json([project]);
      if (path === "/api/projects/project-1")
        return json({ project, assets: {}, missing_assets: [] });
      if (path === "/api/tasks?project_id=project-1") return json([task]);
      if (path === "/api/tasks/task-running") return json(task);
      if (path === "/api/tasks/task-running/semantic-events?limit=20")
        return json({ events: [] });
      if (path === "/api/tasks/task-running/cancel" && method === "POST")
        return json({ ...task, status: "cancelled" });
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByText("养老金选题"));
  fireEvent.click(await screen.findByRole("button", { name: "停止任务" }));

  await waitFor(() =>
    expect(requests).toContainEqual({
      path: "/api/tasks/task-running/cancel",
      method: "POST",
    }),
  );
});

test("starting a second remix asks before creating a new script version", async () => {
  const taskRequests: string[] = [];
  const project = {
    id: "project-1",
    account_id: "account-1",
    title: "养老金选题",
    stage: "script",
  };
  vi.stubGlobal("confirm", vi.fn(() => false));
  vi.stubGlobal(
    "fetch",
    baseFetch((path, method) => {
      if (path === "/api/projects") return json([project]);
      if (path === "/api/projects/project-1")
        return json({
          project,
          assets: {
            continuous_script: {
              id: "script-1",
              type: "continuous_script",
              filename: "continuous_script.txt",
              size: 1024,
              version: 1,
            },
          },
          missing_assets: [],
        });
      if (path === "/api/tasks?project_id=project-1") return json([]);
      if (path === "/api/projects/project-1/tasks" && method === "POST") {
        taskRequests.push(path);
        return json({ id: "task-new" }, 201);
      }
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByText("养老金选题"));
  fireEvent.click(await screen.findByRole("button", { name: "二创文案" }));

  await waitFor(() => expect(confirm).toHaveBeenCalled());
  expect(taskRequests).toHaveLength(0);
});

test("the topic planner lazily creates and can delete conversations", async () => {
  const requests: Array<{ path: string; method: string }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = typeof input === "string" ? input : input.toString();
      const method = (init?.method || "GET").toUpperCase();
      requests.push({ path, method });
      if (path === "/api/auth/me") return json({ csrfToken: "csrf" });
      if (path === "/api/accounts")
        return json([{ id: "account-1", name: "认知觉醒" }]);
      if (path === "/api/projects") return json([]);
      if (path === "/api/runtime") return json({ Limit: 4, Running: 0, Queued: 0 });
      if (path === "/api/settings")
        return json({ public: publicSettings, settings_version: 1, secrets: {} });
      if (path === "/api/ideas" && method === "GET")
        return json([
          {
            id: "idea-1",
            account_id: "account-1",
            title: "养老规划",
            status: "planning",
          },
          {
            id: "idea-2",
            account_id: "account-1",
            title: "存款到期",
            status: "planning",
          },
        ]);
      if (path === "/api/ideas/idea-1")
        return json({
          session: {
            id: "idea-1",
            account_id: "account-1",
            title: "养老规划",
            status: "planning",
          },
          messages: [],
          candidates: [],
        });
      if (path === "/api/ideas" && method === "POST")
        return json({
          id: "idea-new",
          account_id: "account-1",
          title: "新选题规划",
          status: "planning",
        });
      if (path === "/api/ideas/idea-new/messages" && method === "POST")
        return json({ task_id: "task-new" }, 202);
      if (path === "/api/ideas/idea-2" && method === "DELETE")
        return new Response(null, { status: 204 });
      if (path === "/api/ideas/idea-new")
        return json({
          session: {
            id: "idea-new",
            account_id: "account-1",
            title: "新选题规划",
            status: "planning",
          },
          messages: [],
          candidates: [],
        });
      throw new Error(`unexpected request: ${method} ${path}`);
    }),
  );

  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "给我选题" }));

  expect(await screen.findByRole("button", { name: "新建对话" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "养老规划规划中" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "存款到期规划中" })).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "新建对话" }));
  expect(await screen.findByRole("heading", { name: "新选题规划" })).toBeTruthy();
  expect(requests).not.toContainEqual({ path: "/api/ideas", method: "POST" });

  fireEvent.change(screen.getByPlaceholderText("输入你的想法或追问"), {
    target: { value: "给我一个养老选题" },
  });
  fireEvent.click(screen.getByRole("button", { name: "发送" }));
  await waitFor(() =>
    expect(requests).toContainEqual({ path: "/api/ideas", method: "POST" }),
  );
  expect(requests).toContainEqual({
    path: "/api/ideas/idea-new/messages",
    method: "POST",
  });

  vi.spyOn(window, "confirm").mockReturnValue(true);
  fireEvent.click(screen.getByRole("button", { name: "删除对话 存款到期" }));
  await waitFor(() =>
    expect(requests).toContainEqual({ path: "/api/ideas/idea-2", method: "DELETE" }),
  );
});
