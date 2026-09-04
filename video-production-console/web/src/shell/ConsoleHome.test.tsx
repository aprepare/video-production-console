// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import type { Project } from "../types";
import { ConsoleHome } from "./ConsoleHome";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const projects: Project[] = [
  { id: "project-a", account_id: "account-1", title: "空闲项目", stage: "script", updated_at: "2026-08-01T00:00:00Z" },
  { id: "project-b", account_id: "account-1", title: "第二个项目", stage: "script", updated_at: "2026-08-02T00:00:00Z" },
];

function renderHome(
  overrides: Partial<Parameters<typeof ConsoleHome>[0]> = {},
) {
  const props = {
    hidden: false,
    theme: "light" as const,
    onThemeChange: vi.fn(),
    onOpenImageProjects: vi.fn(),
    onOpenRemixLab: vi.fn(),
    modeTitle: "风景混剪",
    runtime: null,
    onOpenSettings: vi.fn(),
    onLogout: vi.fn(),
    accounts: [{ id: "account-1", name: "认知觉醒" }],
    selectedAccountID: "account-1",
    onSelectAccount: vi.fn(),
    accountFormOpen: false,
    onToggleAccountForm: vi.fn(),
    onCreateAccount: vi.fn(),
    newAccountName: "",
    onNewAccountNameChange: vi.fn(),
    accountBackgroundSelected: false,
    onAccountBackgroundChange: vi.fn(),
    newProject: "",
    onNewProjectChange: vi.fn(),
    onCreateProject: vi.fn(),
    message: "",
    onDismissMessage: vi.fn(),
    loading: false,
    projects,
    expandedStages: new Set<Project["stage"]>(),
    onExpandedStagesChange: vi.fn(),
    onOpenProject: vi.fn(),
    onDeleteProjects: vi.fn(),
    ...overrides,
  };
  return { ...render(<ConsoleHome {...props} />), props };
}

test("batch mode toggles cards instead of opening them", () => {
  const { props } = renderHome();
  fireEvent.click(screen.getByRole("button", { name: "批量删除" }));
  fireEvent.click(screen.getByRole("button", { name: "选择项目 空闲项目" }));
  expect(props.onOpenProject).not.toHaveBeenCalled();
  expect(screen.getByRole("button", { name: "选择项目 空闲项目" }).getAttribute("aria-pressed")).toBe("true");
  expect(screen.getByText("已选 1 个")).toBeTruthy();
});

test("delete selected confirms and reports the chosen ids", () => {
  const { props } = renderHome();
  vi.stubGlobal("confirm", vi.fn(() => true));
  fireEvent.click(screen.getByRole("button", { name: "批量删除" }));
  fireEvent.click(screen.getByRole("button", { name: "全选" }));
  fireEvent.click(screen.getByRole("button", { name: "删除所选" }));
  expect(window.confirm).toHaveBeenCalledOnce();
  expect(props.onDeleteProjects).toHaveBeenCalledWith(["project-a", "project-b"]);
});

test("canceling the confirm keeps the projects", () => {
  const { props } = renderHome();
  vi.stubGlobal("confirm", vi.fn(() => false));
  fireEvent.click(screen.getByRole("button", { name: "批量删除" }));
  fireEvent.click(screen.getByRole("button", { name: "选择项目 空闲项目" }));
  fireEvent.click(screen.getByRole("button", { name: "删除所选" }));
  expect(props.onDeleteProjects).not.toHaveBeenCalled();
  expect(screen.getByText("已选 1 个")).toBeTruthy();
});

test("leaving batch mode opens a project again", () => {
  const { props } = renderHome();
  fireEvent.click(screen.getByRole("button", { name: "批量删除" }));
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  fireEvent.click(screen.getByText("空闲项目"));
  expect(props.onOpenProject).toHaveBeenCalledWith(projects[0]);
});

test("header 文案创作台 button opens the remix lab", () => {
  const { props } = renderHome();
  fireEvent.click(screen.getByRole("button", { name: "文案创作台" }));
  expect(props.onOpenRemixLab).toHaveBeenCalledOnce();
});
