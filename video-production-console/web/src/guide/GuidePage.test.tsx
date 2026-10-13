// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { GuidePage, loadGuideProgress } from "./GuidePage";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

function fakeApi(overrides: Record<string, unknown> = {}) {
  const responses: Record<string, unknown> = {
    "/api/setup/status": { ready: true, items: [] },
    "/api/business/overview": { merchants_by_status: { active: 2 }, orders_by_status: { signed: 1, delivered: 1 } },
    "/api/references?limit=300": [{ transcript_status: "completed" }, { transcript_status: "running" }],
    "/api/remix-lab/experiments": [],
    "/api/projects": [],
    ...overrides,
  };
  return vi.fn(async (path: string) => path in responses
    ? new Response(JSON.stringify(responses[path]))
    : new Response("{}", { status: 404 }));
}

test("progress is read from real data, and a failed call counts as nothing done", async () => {
  const progress = await loadGuideProgress(fakeApi({ "/api/remix-lab/experiments": undefined }) as never);
  expect(progress).toMatchObject({ setupReady: true, merchants: 2, orders: 2, references: 2, transcribed: 1, delivered: 1 });
  const broken = await loadGuideProgress(vi.fn(async () => new Response("{}", { status: 500 })) as never);
  expect(broken).toMatchObject({ setupReady: false, merchants: 0, experiments: 0, delivered: 0 });
});

test("the first unfinished step is marked as next and its button leads there", async () => {
  const navigate = vi.fn();
  render(<GuidePage api={fakeApi() as never} onNavigate={navigate} />);
  expect(await screen.findByText("已完成 4/6 步")).toBeTruthy();
  const next = screen.getByText("下一步").closest("li")!;
  expect(next.textContent).toContain("二创写文案");
  fireEvent.click(screen.getAllByRole("button", { name: /去文案与混剪/ })[0]);
  expect(navigate).toHaveBeenCalledWith("/");
});

test("the guide names the buttons exactly as the pages show them", async () => {
  render(<GuidePage api={fakeApi() as never} onNavigate={vi.fn()} />);
  await screen.findByText("已完成 4/6 步");
  for (const label of ["「新增商户」", "「上传授权素材」", "「新建订单」", "「送进二创」", "「风格与简报」", "「确认二创」", "「确认开始混剪」", "「归单」", "「标记已交付」", "「记一笔」"]) {
    expect(document.body.textContent).toContain(label);
  }
});
