// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { WorkspacePage } from "./WorkspacePage";
import { defaultWorkspaceFile, type WorkspaceTree } from "./api";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const tree: WorkspaceTree = {
  root: "二创工作区",
  entries: [
    { path: "agent.md", name: "agent.md", kind: "file", mtime: "2026-09-07T01:00:00Z" },
    {
      path: "项目",
      name: "项目",
      kind: "dir",
      children: [
        {
          path: "项目/demo",
          name: "demo",
          kind: "dir",
          children: [
            {
              path: "项目/demo/成稿_2026-09-07.md",
              name: "成稿_2026-09-07.md",
              kind: "file",
              mtime: "2026-09-07T10:00:00Z",
            },
          ],
        },
      ],
    },
  ],
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((next) => {
    resolve = next;
  });
  return { promise, resolve };
}

function workspaceFile(path: string, content: string, revision: string) {
  return {
    path,
    content,
    mtime: "2026-09-07T10:00:00Z",
    revision,
    spoken_body: content,
  };
}

test("defaultWorkspaceFile prefers the newest draft", () => {
  expect(defaultWorkspaceFile(tree)).toBe("项目/demo/成稿_2026-09-07.md");
});

test("opens a draft, copies spoken body, and writes edits back", async () => {
  const write = vi.fn();
  Object.assign(navigator, { clipboard: { writeText: write } });
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/workspace/tree") return new Response(JSON.stringify(tree));
    if (path.startsWith("/api/workspace/file") && init?.method === "PUT") {
      const body = JSON.parse(String(init.body)) as { content: string };
      return new Response(JSON.stringify({
        path: "项目/demo/成稿_2026-09-07.md",
        content: body.content,
        mtime: "2026-09-07T11:00:00Z",
        revision: "revision-2",
        spoken_body: "改过的口播",
      }));
    }
    if (path.startsWith("/api/workspace/file")) {
      return new Response(JSON.stringify({
        path: "项目/demo/成稿_2026-09-07.md",
        content: "---\n主题: 测\n---\n\n口播正文\n",
        mtime: "2026-09-07T10:00:00Z",
        revision: "revision-1",
        spoken_body: "口播正文",
      }));
    }
    return new Response("missing", { status: 404 });
  });

  render(<WorkspacePage api={api} onNavigate={vi.fn()} />);
  await waitFor(() => expect(screen.getByText("口播正文")).toBeTruthy());
  fireEvent.click(screen.getByRole("button", { name: "复制口播" }));
  await waitFor(() => expect(write).toHaveBeenCalledWith("口播正文"));
  fireEvent.click(screen.getByRole("button", { name: "编辑" }));
  fireEvent.change(screen.getByRole("textbox", { name: "编辑文档" }), { target: { value: "---\n主题: 测\n---\n\n改过的口播\n" } });
  fireEvent.click(screen.getByRole("button", { name: "保存" }));
  await waitFor(() => expect(screen.getByText("已写回工作区文件。")).toBeTruthy());
  const put = api.mock.calls.find(([, init]) => init?.method === "PUT");
  expect(JSON.parse(String(put?.[1]?.body))).toMatchObject({
    path: "项目/demo/成稿_2026-09-07.md",
    expected_revision: "revision-1",
  });
});

test("keeps edits typed while a save is pending and prevents duplicate saves", async () => {
  const pendingSave = deferred<Response>();
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/workspace/tree") return new Response(JSON.stringify(tree));
    if (init?.method === "PUT") return pendingSave.promise;
    return new Response(JSON.stringify(workspaceFile("agent.md", "原稿", "revision-1")));
  });

  render(<WorkspacePage api={api} file="agent.md" onNavigate={vi.fn()} />);
  await waitFor(() => expect(screen.getByText("原稿")).toBeTruthy());
  fireEvent.click(screen.getByRole("button", { name: "编辑" }));
  const editor = screen.getByRole("textbox", { name: "编辑文档" });
  fireEvent.change(editor, { target: { value: "第一次修改" } });
  fireEvent.click(screen.getByRole("button", { name: "保存" }));
  fireEvent.change(editor, { target: { value: "保存期间继续输入" } });
  fireEvent.keyDown(window, { key: "s", ctrlKey: true });

  expect(api.mock.calls.filter(([, init]) => init?.method === "PUT")).toHaveLength(1);
  await act(async () => {
    pendingSave.resolve(new Response(JSON.stringify(workspaceFile("agent.md", "第一次修改", "revision-2"))));
    await pendingSave.promise;
  });
  expect((screen.getByRole("textbox", { name: "编辑文档" }) as HTMLTextAreaElement).value).toBe("保存期间继续输入");
  expect((screen.getByRole("button", { name: "保存" }) as HTMLButtonElement).disabled).toBe(false);
});

test("keeps the draft when the server reports a revision conflict", async () => {
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/workspace/tree") return new Response(JSON.stringify(tree));
    if (init?.method === "PUT") {
      return new Response(JSON.stringify({ message: "文件已被其他程序修改，请刷新后重试。" }), { status: 409 });
    }
    return new Response(JSON.stringify(workspaceFile("agent.md", "原稿", "revision-1")));
  });

  render(<WorkspacePage api={api} file="agent.md" onNavigate={vi.fn()} />);
  await waitFor(() => expect(screen.getByText("原稿")).toBeTruthy());
  fireEvent.click(screen.getByRole("button", { name: "编辑" }));
  fireEvent.change(screen.getByRole("textbox", { name: "编辑文档" }), { target: { value: "不能丢的草稿" } });
  fireEvent.click(screen.getByRole("button", { name: "保存" }));

  await waitFor(() => expect(screen.getByText("文件已被其他程序修改，请刷新后重试。")).toBeTruthy());
  expect((screen.getByRole("textbox", { name: "编辑文档" }) as HTMLTextAreaElement).value).toBe("不能丢的草稿");
  expect((screen.getByRole("button", { name: "保存" }) as HTMLButtonElement).disabled).toBe(false);
});

test("ignores a save response after another file has been selected", async () => {
  const pendingSave = deferred<Response>();
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/workspace/tree") return new Response(JSON.stringify(tree));
    if (init?.method === "PUT") return pendingSave.promise;
    if (path.includes("agent.md")) return new Response(JSON.stringify(workspaceFile("agent.md", "原稿", "revision-1")));
    return new Response(JSON.stringify(workspaceFile("项目/demo/成稿_2026-09-07.md", "新文件", "revision-new")));
  });
  const view = render(<WorkspacePage api={api} file="agent.md" onNavigate={vi.fn()} />);
  await waitFor(() => expect(screen.getByText("原稿")).toBeTruthy());
  fireEvent.click(screen.getByRole("button", { name: "编辑" }));
  fireEvent.change(screen.getByRole("textbox", { name: "编辑文档" }), { target: { value: "旧文件修改" } });
  fireEvent.click(screen.getByRole("button", { name: "保存" }));

  view.rerender(<WorkspacePage api={api} file="项目/demo/成稿_2026-09-07.md" onNavigate={vi.fn()} />);
  await waitFor(() => expect(screen.getByText("新文件")).toBeTruthy());
  await act(async () => {
    pendingSave.resolve(new Response(JSON.stringify(workspaceFile("agent.md", "旧文件修改", "revision-2"))));
    await pendingSave.promise;
  });

  expect(screen.queryByText("旧文件修改")).toBeNull();
  expect(screen.getByText("新文件")).toBeTruthy();
  expect(screen.getByRole("heading", { name: "项目/demo/成稿_2026-09-07.md" })).toBeTruthy();
});

test("ignores an older file response after a newer file has loaded", async () => {
  const oldRead = deferred<Response>();
  const api = vi.fn(async (path: string) => {
    if (path === "/api/workspace/tree") return new Response(JSON.stringify(tree));
    if (path.includes("agent.md")) return oldRead.promise;
    return new Response(JSON.stringify(workspaceFile("项目/demo/成稿_2026-09-07.md", "新文件", "revision-new")));
  });
  const view = render(<WorkspacePage api={api} file="agent.md" onNavigate={vi.fn()} />);
  await waitFor(() => expect(api.mock.calls.some(([path]) => String(path).includes("agent.md"))).toBe(true));
  view.rerender(<WorkspacePage api={api} file="项目/demo/成稿_2026-09-07.md" onNavigate={vi.fn()} />);
  await waitFor(() => expect(screen.getByText("新文件")).toBeTruthy());

  await act(async () => {
    oldRead.resolve(new Response(JSON.stringify(workspaceFile("agent.md", "旧文件迟到", "revision-old"))));
    await oldRead.promise;
  });
  expect(screen.queryByText("旧文件迟到")).toBeNull();
  expect(screen.getByText("新文件")).toBeTruthy();
});

test("focus cannot supersede a route file that is still loading", async () => {
  const routeRead = deferred<Response>();
  let routeReads = 0;
  const api = vi.fn(async (path: string) => {
    if (path === "/api/workspace/tree") return new Response(JSON.stringify(tree));
    const requestedFile = new URL(path, "http://workspace.test").searchParams.get("path");
    if (requestedFile === "项目/demo/成稿_2026-09-07.md") {
      routeReads += 1;
      return routeRead.promise;
    }
    return new Response(JSON.stringify(workspaceFile("agent.md", "文件 A", "revision-a")));
  });
  const view = render(<WorkspacePage api={api} file="agent.md" onNavigate={vi.fn()} />);
  await waitFor(() => expect(screen.getByText("文件 A")).toBeTruthy());

  view.rerender(<WorkspacePage api={api} file="项目/demo/成稿_2026-09-07.md" onNavigate={vi.fn()} />);
  await waitFor(() => expect(routeReads).toBe(1));
  fireEvent(window, new Event("focus"));
  await act(async () => {
    routeRead.resolve(new Response(JSON.stringify(workspaceFile("项目/demo/成稿_2026-09-07.md", "文件 B", "revision-b"))));
    await routeRead.promise;
  });

  expect(screen.getByRole("heading", { name: "项目/demo/成稿_2026-09-07.md" })).toBeTruthy();
  expect(screen.getByText("文件 B")).toBeTruthy();
});

test("focus refresh cannot overwrite input started while its read is pending", async () => {
  const focusRead = deferred<Response>();
  let reads = 0;
  const api = vi.fn(async (path: string) => {
    if (path === "/api/workspace/tree") return new Response(JSON.stringify(tree));
    reads += 1;
    if (reads === 1) return new Response(JSON.stringify(workspaceFile("agent.md", "原稿", "revision-1")));
    return focusRead.promise;
  });

  render(<WorkspacePage api={api} file="agent.md" onNavigate={vi.fn()} />);
  await waitFor(() => expect(screen.getByText("原稿")).toBeTruthy());
  fireEvent(window, new Event("focus"));
  await waitFor(() => expect(reads).toBe(2));
  fireEvent.click(screen.getByRole("button", { name: "编辑" }));
  fireEvent.change(screen.getByRole("textbox", { name: "编辑文档" }), { target: { value: "刷新期间输入" } });

  await act(async () => {
    focusRead.resolve(new Response(JSON.stringify(workspaceFile("agent.md", "外部新稿", "revision-2"))));
    await focusRead.promise;
  });
  expect((screen.getByRole("textbox", { name: "编辑文档" }) as HTMLTextAreaElement).value).toBe("刷新期间输入");
});
