// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { JianyingHealthCard } from "./JianyingHealthCard";

afterEach(cleanup);

const health = {
  jianying_root: "D:/Jianying/com.lveditor.draft", root_exists: true, installed_version: "11.2.0.100",
  version_source: "install_dir", version_note: "", verified_versions: ["11.1"], compatible: false,
  backup_count: 40, backup_bytes: 8 << 20,
};

test("warns on unverified versions and prunes only after a preview", async () => {
  const bodies: Array<Record<string, unknown>> = [];
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/jianying-drafts/health") return new Response(JSON.stringify(health));
    const body = JSON.parse(String(init?.body));
    bodies.push(body);
    return new Response(JSON.stringify({ deleted: [{ name: "root_meta_info.a.bak.json", size: 1024 }], kept: 39, freed_bytes: 1024 }));
  });
  render(<JianyingHealthCard api={api} />);
  expect(await screen.findByText(/未验证（已验证 11.1）/)).toBeTruthy();
  expect(screen.getByText(/草稿结构可能变化/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /预览清理/ }));
  expect(await screen.findByText(/将删除 1 个备份/)).toBeTruthy();
  expect(bodies[0].dry_run).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "确认删除" }));
  await waitFor(() => expect(bodies[1]?.dry_run).toBe(false));
  expect(await screen.findByText(/已删除 1 个备份/)).toBeTruthy();
});
