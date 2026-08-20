// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SetupWizard } from "./SetupWizard";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

it("requires Jianying and scenery roots before opening the app", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => new Promise<Response>(() => {})),
  );
  render(<SetupWizard status={{ complete: false, detected_jianying_root: "" }} onComplete={vi.fn()} />);
  fireEvent.change(screen.getByLabelText("剪映草稿目录"), { target: { value: "D:\\剪映草稿" } });
  fireEvent.change(screen.getByLabelText("风景素材目录"), { target: { value: "E:\\风景素材" } });
  fireEvent.click(screen.getByRole("button", { name: "检查并继续" }));
  expect(await screen.findByText("正在建立本机素材索引")).toBeVisible();
});
