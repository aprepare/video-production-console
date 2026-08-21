// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ModeHome } from "./ModeHome";
import { parseMontageKind, productionModes, visibleMontageKind, visibleProductionModes } from "./catalog";

afterEach(cleanup);

test("the paused catalog still lists every production line", () => {
  render(<ModeHome modes={productionModes} onNavigate={() => undefined} />);
  expect(screen.getByRole("heading", { name: "选择制作方式", level: 1 })).toBeTruthy();
  expect(screen.getAllByRole("article")).toHaveLength(4);
  expect(productionModes.filter((mode) => mode.paused).map((mode) => mode.id)).toEqual([
    "movie-montage",
    "image-video",
  ]);
});

test("visible modes are only scenic montage and image ZIP", () => {
  expect(visibleProductionModes.map((mode) => mode.id)).toEqual(["scenic", "image"]);
  render(<ModeHome modes={visibleProductionModes} onNavigate={() => undefined} />);
  expect(screen.getAllByRole("article")).toHaveLength(2);
  expect(screen.getByRole("button", { name: "进入风景混剪" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "进入图文制作" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "进入电影混剪" })).toBeNull();
  expect(screen.queryByRole("button", { name: "进入图文视频" })).toBeNull();
});

test("invokes navigation callback with mode-specific addresses", () => {
  const onNavigate = vi.fn();
  render(<ModeHome modes={visibleProductionModes} onNavigate={onNavigate} />);
  fireEvent.click(screen.getByRole("button", { name: "进入风景混剪" }));
  expect(onNavigate).toHaveBeenCalledWith("/projects?mode=scenic");
  fireEvent.click(screen.getByRole("button", { name: "进入图文制作" }));
  expect(onNavigate).toHaveBeenCalledWith("/image-projects");
});

test("parses montage kind from the query string but keeps the UI on scenic", () => {
  expect(parseMontageKind("?mode=movie")).toBe("movie");
  expect(parseMontageKind("mode=image-video")).toBe("image-video");
  expect(parseMontageKind("?mode=scenic")).toBe("scenic");
  expect(parseMontageKind("")).toBe("scenic");
  expect(visibleMontageKind("?mode=movie")).toBe("scenic");
  expect(visibleMontageKind("mode=image-video")).toBe("scenic");
});
