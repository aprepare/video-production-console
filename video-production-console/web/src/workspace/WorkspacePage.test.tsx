// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
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
        spoken_body: "改过的口播",
      }));
    }
    if (path.startsWith("/api/workspace/file")) {
      return new Response(JSON.stringify({
        path: "项目/demo/成稿_2026-09-07.md",
        content: "---\n主题: 测\n---\n\n口播正文\n",
        mtime: "2026-09-07T10:00:00Z",
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
  expect(JSON.parse(String(put?.[1]?.body))).toMatchObject({ path: "项目/demo/成稿_2026-09-07.md" });
});
