import { expect, test } from "vitest";
import { inheritedTaskEffort, inheritedTaskModel } from "./taskModel";

test("remix inherits remix model first", () => {
  expect(
    inheritedTaskModel(
      { remix_model: "cursor-grok-4.6-xhigh-fast", codex_default_model: "gpt-5.6-sol" },
      "remix",
    ),
  ).toBe("cursor-grok-4.6-xhigh-fast");
});

test("montage inherits Codex default and ignores remix model", () => {
  expect(
    inheritedTaskModel(
      { remix_model: "cursor-grok-4.6-xhigh-fast", codex_default_model: "gpt-5.6-sol" },
      "codex",
    ),
  ).toBe("gpt-5.6-sol");
});

test("remix effort falls back to 不设置 when unset", () => {
  expect(inheritedTaskEffort({ remix_model: "gpt-5.6-sol" }, "remix")).toBe("不设置");
  expect(inheritedTaskEffort({ remix_reasoning_effort: "high" }, "remix")).toBe("high");
});
