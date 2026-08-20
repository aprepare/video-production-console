// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { QuickGenerateForm } from "./QuickGenerateForm";

const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const defaults = {
  defaultRatio: "3:4" as const,
  defaultStyle: "finance_documentary",
  defaultConcurrency: 3,
  defaultTextModel: "gpt-5.6-sol",
  defaultReasoningEffort: "medium" as const,
  defaultImageModel: "gpt-image-2",
  defaultImageAttempts: 2,
};

describe("QuickGenerateForm", () => {
  test("renders supplied defaults without a project-name field", () => {
    render(<QuickGenerateForm api={vi.fn()} {...defaults} onCreated={vi.fn()} onAdvancedMode={vi.fn()} />);
    expect(screen.queryByLabelText(/项目名称/)).toBeNull();
    expect((screen.getByLabelText("思考强度") as HTMLSelectElement).value).toBe("medium");
    expect((screen.getByLabelText("图片比例") as HTMLSelectElement).value).toBe("3:4");
    expect((screen.getByLabelText("视觉风格") as HTMLSelectElement).value).toBe("finance_documentary");
    expect((screen.getByLabelText("项目并发") as HTMLSelectElement).value).toBe("3");
    expect((screen.getByRole("textbox", { name: "图片模型" }) as HTMLInputElement).value).toBe("gpt-image-2");
    expect((screen.getByRole("combobox", { name: "文本模型" }) as HTMLSelectElement).value).toBe("gpt-5.6-sol");
    expect(screen.queryByLabelText("每张图片最多请求次数")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "高级参数" }));
    expect((screen.getByLabelText("每张图片最多请求次数") as HTMLSelectElement).value).toBe("2");
  });

  test("partner allowlist hides gpt-5.6-terra from the text model select", () => {
    render(
      <QuickGenerateForm
        api={vi.fn()}
        {...defaults}
        models={["gpt-5.6-sol"]}
        onCreated={vi.fn()}
        onAdvancedMode={vi.fn()}
      />,
    );
    const options = [...(screen.getByLabelText("文本模型") as HTMLSelectElement).options].map((option) => option.value);
    expect(options).toEqual(["gpt-5.6-sol"]);
    expect(options).not.toContain("gpt-5.6-terra");
  });

  test("lets the user pick a different text model and submits that name", async () => {
    const onCreated = vi.fn();
    let request: RequestInit | undefined;
    const api = vi.fn(async (_path: string, init?: RequestInit) => {
      request = init;
      return json({ project_id: "new-project", run_status: "running" }, 202);
    });
    render(<QuickGenerateForm api={api} {...defaults} defaultTextModel="" onCreated={onCreated} onAdvancedMode={vi.fn()} />);
    const modelInput = screen.getByLabelText("文本模型") as HTMLSelectElement;
    expect(modelInput.value).toBe("gpt-5.6-sol");
    fireEvent.change(modelInput, { target: { value: "grok-4.6" } });
    fireEvent.change(screen.getByLabelText("最终文案"), { target: { value: "完整文案" } });
    fireEvent.click(screen.getByRole("button", { name: "开始生成图片" }));
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith("new-project"));
    expect(JSON.parse(String(request?.body)).text_model).toBe("grok-4.6");
  });

  test("disables submit for blank script or blank custom style", () => {
    render(<QuickGenerateForm api={vi.fn()} {...defaults} onCreated={vi.fn()} onAdvancedMode={vi.fn()} />);
    const submit = screen.getByRole("button", { name: "开始生成图片" }) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("最终文案"), { target: { value: "完整文案" } });
    expect(submit.disabled).toBe(false);
    fireEvent.change(screen.getByLabelText("视觉风格"), { target: { value: "custom" } });
    expect(submit.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("自定义风格"), { target: { value: "纸质账本" } });
    expect(submit.disabled).toBe(false);
  });

  test("submits the exact quick payload and navigates on 202", async () => {
    const onCreated = vi.fn();
    let request: RequestInit | undefined;
    const api = vi.fn(async (path: string, init?: RequestInit) => {
      expect(path).toBe("/api/image-projects/quick-generate");
      request = init;
      return json({ project_id: "new-project", run_status: "running" }, 202);
    });
    render(<QuickGenerateForm api={api} {...defaults} onCreated={onCreated} onAdvancedMode={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("最终文案"), { target: { value: "完整文案" } });
    fireEvent.click(screen.getByRole("button", { name: "开始生成图片" }));
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith("new-project"));
    expect(JSON.parse(String(request?.body))).toEqual({
      script: "完整文案",
      image_count: 0,
      ratio: "3:4",
      style: "finance_documentary",
      custom_style: "",
      concurrency: 3,
      text_model: "gpt-5.6-sol",
      reasoning_effort: "medium",
      image_model: "gpt-image-2",
      image_attempts: 2,
    });
  });

  test("shows the server message with a request-failure label", async () => {
    const api = vi.fn(async () => json({ code: "invalid_image_project", message: "Image project settings are invalid." }, 400));
    render(<QuickGenerateForm api={api} {...defaults} onCreated={vi.fn()} onAdvancedMode={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("最终文案"), { target: { value: "完整文案" } });
    fireEvent.click(screen.getByRole("button", { name: "开始生成图片" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("提交失败");
    expect(alert.textContent).toContain("Image project settings are invalid.");
  });

  test("create view has a single primary action", () => {
    const { container } = render(<QuickGenerateForm api={vi.fn()} {...defaults} onCreated={vi.fn()} onAdvancedMode={vi.fn()} />);
    const primaries = container.querySelectorAll(".primary");
    expect(primaries).toHaveLength(1);
    expect(primaries[0].textContent).toBe("开始生成图片");
  });

  test("advanced mode link calls onAdvancedMode", () => {
    const onAdvancedMode = vi.fn();
    render(<QuickGenerateForm api={vi.fn()} {...defaults} onCreated={vi.fn()} onAdvancedMode={onAdvancedMode} />);
    fireEvent.click(screen.getByRole("button", { name: "高级手动模式" }));
    expect(onAdvancedMode).toHaveBeenCalledTimes(1);
  });
});
