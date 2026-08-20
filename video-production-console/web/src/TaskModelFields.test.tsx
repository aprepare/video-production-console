// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { TaskModelFields } from "./TaskModelFields";

afterEach(cleanup);

function optionValues(label: string) {
  const select = screen.getByLabelText(label) as HTMLSelectElement;
  return [...select.options].map((option) => option.value);
}

test("partner allowlist hides gpt-5.6-terra from TaskModelFields", () => {
  render(
    <TaskModelFields
      value={{ model: "", reasoningEffort: "" }}
      onChange={() => undefined}
      models={["gpt-5.6-sol"]}
      efforts={["medium", "high"]}
    />,
  );
  fireEvent.click(screen.getByText("模型与推理强度（可选）"));
  expect(optionValues("临时模型")).toEqual(["", "gpt-5.6-sol"]);
  expect(optionValues("临时模型")).not.toContain("gpt-5.6-terra");
  expect(optionValues("临时推理强度")).toEqual(["", "medium", "high"]);
});

test("omitting models keeps the hardcoded selectable list including gpt-5.6-terra", () => {
  render(
    <TaskModelFields
      value={{ model: "", reasoningEffort: "" }}
      onChange={() => undefined}
    />,
  );
  fireEvent.click(screen.getByText("模型与推理强度（可选）"));
  expect(optionValues("临时模型")).toContain("gpt-5.6-terra");
});
