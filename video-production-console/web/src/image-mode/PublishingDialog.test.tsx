// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { PublishingDialog } from "./PublishingDialog";

const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
const hashtags = "#存款 #财富管理 #思维提升";
const candidates = Array.from({ length: 5 }, (_, i) => ({
  position: i + 1,
  title: `标题${i + 1}`,
  description: `描述内容${i + 1}。${hashtags}`,
}));

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe("PublishingDialog", () => {
  test("renders five candidates and edits the selected one", () => {
    render(<PublishingDialog api={vi.fn()} projectID="p" candidates={candidates} selected={2} onClose={vi.fn()} onSaved={vi.fn()} />);
    expect(screen.getAllByRole("radio")).toHaveLength(5);
    expect((screen.getByLabelText("标题") as HTMLTextAreaElement).value).toBe("标题2");
    fireEvent.click(screen.getByRole("radio", { name: /候选 4/ }));
    expect((screen.getByLabelText("标题") as HTMLTextAreaElement).value).toBe("标题4");
    expect((screen.getByLabelText("描述") as HTMLTextAreaElement).value).toContain(hashtags);
  });

  test("disables save at 23 title runes or 1001 description runes", () => {
    const longTitle = [...candidates];
    longTitle[0] = { ...candidates[0], title: "😀".repeat(23) };
    const view = render(<PublishingDialog api={vi.fn()} projectID="p" candidates={longTitle} selected={1} onClose={vi.fn()} onSaved={vi.fn()} />);
    expect((screen.getByRole("button", { name: /保存当前候选/ }) as HTMLButtonElement).disabled).toBe(true);
    const longDescription = [...candidates];
    longDescription[0] = { ...candidates[0], title: "合法标题", description: `${"字".repeat(1001)}${hashtags}` };
    view.rerender(<PublishingDialog api={vi.fn()} projectID="p" candidates={longDescription} selected={1} onClose={vi.fn()} onSaved={vi.fn()} />);
    expect((screen.getByRole("button", { name: /保存当前候选/ }) as HTMLButtonElement).disabled).toBe(true);
    const valid = [...candidates];
    valid[0] = { ...candidates[0], title: "😀".repeat(22), description: `${"字".repeat(10)} ${hashtags}` };
    view.rerender(<PublishingDialog api={vi.fn()} projectID="p" candidates={valid} selected={1} onClose={vi.fn()} onSaved={vi.fn()} />);
    expect((screen.getByRole("button", { name: /保存当前候选/ }) as HTMLButtonElement).disabled).toBe(false);
  });

  test("copies title, description, and all separately", async () => {
    const writeText = vi.fn(async () => undefined);
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    render(<PublishingDialog api={vi.fn()} projectID="p" candidates={candidates} selected={1} onClose={vi.fn()} onSaved={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: /复制标题/ }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("标题1"));
    fireEvent.click(screen.getByRole("button", { name: /复制描述/ }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(candidates[0].description));
    fireEvent.click(screen.getByRole("button", { name: /复制全部/ }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(`标题1\n${candidates[0].description}`));
  });

  test("saves with PATCH then select POST and keeps the selected position", async () => {
    const onSaved = vi.fn();
    const api = vi.fn(async (_path: string, _init?: RequestInit) => json({}));
    render(<PublishingDialog api={api} projectID="p" candidates={candidates} selected={3} onClose={vi.fn()} onSaved={onSaved} />);
    fireEvent.click(screen.getByRole("button", { name: /保存当前候选/ }));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(api.mock.calls.some(([p, i]) => String(p).includes("publishing-candidates/3") && i?.method === "PATCH")).toBe(true);
    expect(api.mock.calls.some(([p, i]) => String(p).endsWith("publishing-candidates/select") && i?.method === "POST" && JSON.parse(String(i.body)).position === 3)).toBe(true);
    expect(onSaved.mock.calls[0][1]).toBe(3);
  });

  test("Escape closes the dialog and close control is focused", () => {
    const onClose = vi.fn();
    render(<PublishingDialog api={vi.fn()} projectID="p" candidates={candidates} selected={1} onClose={onClose} onSaved={vi.fn()} />);
    expect(document.activeElement).toBe(screen.getByRole("button", { name: /关闭/ }));
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  test("empty state shows publishing error and generate action", async () => {
    const api = vi.fn(async (_path: string, _init?: RequestInit) => json({ publishing_candidates: candidates }));
    render(<PublishingDialog api={api} projectID="p" candidates={[]} selected={1} publishingError="话题数量不足" onClose={vi.fn()} onSaved={vi.fn()} />);
    expect(screen.getByText("话题数量不足")).toBeTruthy();
    expect(api.mock.calls.length).toBe(0);
    fireEvent.click(screen.getByRole("button", { name: "重新生成发布文案" }));
    await waitFor(() => expect(api.mock.calls.some(([p, i]) => String(p).includes("publishing-candidates/generate") && i?.method === "POST")).toBe(true));
  });

  test("regenerate accepts the project-detail payload shape", async () => {
    const api = vi.fn(async () => json({ project: { publishing_candidates: candidates }, items: [] }));
    render(<PublishingDialog api={api} projectID="p" candidates={[]} selected={1} onClose={vi.fn()} onSaved={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "重新生成发布文案" }));
    await screen.findByRole("radio", { name: /候选 1/ });
    expect(screen.getAllByRole("radio")).toHaveLength(5);
  });
});
