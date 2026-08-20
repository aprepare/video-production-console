// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { PartnerGate } from "./PartnerGate";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  window.localStorage.clear();
  window.sessionStorage.clear();
});

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function stubPartnerFetch(
  handler: (path: string, method: string, init?: RequestInit) => Response | Promise<Response> | undefined,
) {
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = typeof input === "string" ? input : input.toString();
    const method = (init?.method || "GET").toUpperCase();
    const response = await handler(path, method, init);
    if (response) return response;
    throw new Error(`unexpected request: ${method} ${path}`);
  });
}

test("shows only activation until the partner is ready", async () => {
  vi.stubGlobal(
    "fetch",
    stubPartnerFetch((path) => {
      if (path === "/api/partner/status") return json({ state: "needs_activation" });
    }),
  );

  render(
    <PartnerGate>
      <div>business-ui</div>
    </PartnerGate>,
  );

  expect(await screen.findByLabelText("伙伴密钥")).toBeTruthy();
  expect(screen.queryByText("business-ui")).toBeNull();
});

test("shows a verifying spinner and hides children", async () => {
  vi.stubGlobal(
    "fetch",
    stubPartnerFetch((path) => {
      if (path === "/api/partner/status") return json({ state: "verifying" });
    }),
  );

  render(
    <PartnerGate>
      <div>business-ui</div>
    </PartnerGate>,
  );

  expect(await screen.findByText("正在验证伙伴授权…")).toBeTruthy();
  expect(screen.queryByText("business-ui")).toBeNull();
  expect(screen.queryByLabelText("伙伴密钥")).toBeNull();
});

test("shows the setup wizard after authorization when setup is incomplete", async () => {
  vi.stubGlobal(
    "fetch",
    stubPartnerFetch((path) => {
      if (path === "/api/partner/status") return json({ state: "ready" });
      if (path === "/api/partner/setup") return json({ complete: false, detected_jianying_root: "" });
    }),
  );

  render(
    <PartnerGate>
      <div>business-ui</div>
    </PartnerGate>,
  );

  expect(await screen.findByLabelText("剪映草稿目录")).toBeTruthy();
  expect(screen.queryByText("business-ui")).toBeNull();
});

test("renders children when the partner is ready", async () => {
  vi.stubGlobal(
    "fetch",
    stubPartnerFetch((path) => {
      if (path === "/api/partner/status") {
        return json({
          state: "ready",
          capabilities: { features: ["scenery_montage", "image_text"] },
        });
      }
      if (path === "/api/partner/setup") return json({ complete: true, detected_jianying_root: "" });
    }),
  );

  render(
    <PartnerGate>
      <div>business-ui</div>
    </PartnerGate>,
  );

  expect(await screen.findByText("business-ui")).toBeTruthy();
  expect(screen.queryByLabelText("伙伴密钥")).toBeNull();
});

test("keeps the activation form when partner status cannot be loaded", async () => {
  vi.stubGlobal(
    "fetch",
    stubPartnerFetch((path) => {
      if (path === "/api/partner/status") return json({ message: "unauthorized" }, 401);
    }),
  );

  render(
    <PartnerGate>
      <div>business-ui</div>
    </PartnerGate>,
  );

  expect(await screen.findByLabelText("伙伴密钥")).toBeTruthy();
  expect(await screen.findByText("授权服务暂时不可用，请稍后重试。")).toBeTruthy();
  expect(screen.queryByText("当前无法使用")).toBeNull();
  expect(screen.queryByText("business-ui")).toBeNull();
});

test("shows a locked error with retry that refetches partner status", async () => {
  let snapshot: { state: string; error_code?: string } = {
    state: "locked",
    error_code: "partner_disabled",
  };
  const statusCalls: string[] = [];
  vi.stubGlobal(
    "fetch",
    stubPartnerFetch((path) => {
      if (path === "/api/partner/status") {
        statusCalls.push(path);
        return json(snapshot);
      }
      if (path === "/api/partner/setup") return json({ complete: true, detected_jianying_root: "" });
    }),
  );

  render(
    <PartnerGate>
      <div>business-ui</div>
    </PartnerGate>,
  );

  expect(await screen.findByText("当前账号已停用。")).toBeTruthy();
  expect(screen.queryByText("business-ui")).toBeNull();

  snapshot = { state: "ready" };
  fireEvent.click(screen.getByRole("button", { name: "重试" }));

  expect(await screen.findByText("business-ui")).toBeTruthy();
  expect(statusCalls.length).toBeGreaterThanOrEqual(2);
});

test("posts the activation key only to /api/partner/activate and clears the input after settle", async () => {
  let resolveActivate!: (response: Response) => void;
  const activatePending = new Promise<Response>((resolve) => {
    resolveActivate = resolve;
  });
  const calls: Array<{ path: string; method: string; body?: string }> = [];
  vi.stubGlobal(
    "fetch",
    stubPartnerFetch((path, method, init) => {
      calls.push({
        path,
        method,
        body: typeof init?.body === "string" ? init.body : undefined,
      });
      if (path === "/api/partner/status") return json({ state: "needs_activation" });
      if (path === "/api/partner/setup") return json({ complete: true, detected_jianying_root: "" });
      if (path === "/api/partner/activate" && method === "POST") return activatePending;
    }),
  );

  render(
    <PartnerGate>
      <div>business-ui</div>
    </PartnerGate>,
  );

  const input = (await screen.findByLabelText("伙伴密钥")) as HTMLInputElement;
  fireEvent.change(input, { target: { value: "one-time-secret" } });
  fireEvent.click(screen.getByRole("button", { name: "激活" }));

  await waitFor(() =>
    expect(calls).toContainEqual({
      path: "/api/partner/activate",
      method: "POST",
      body: JSON.stringify({ activation_key: "one-time-secret" }),
    }),
  );
  expect(input.value).toBe("one-time-secret");

  resolveActivate(json({ state: "ready", capabilities: { features: ["text"] } }));

  expect(await screen.findByText("business-ui")).toBeTruthy();
  expect(screen.queryByLabelText("伙伴密钥")).toBeNull();
  expect(window.localStorage.length).toBe(0);
  expect(window.sessionStorage.length).toBe(0);
  expect(window.location.href).not.toContain("one-time-secret");
});

test("clears the activation input after a failed request settles", async () => {
  vi.stubGlobal(
    "fetch",
    stubPartnerFetch((path, method) => {
      if (path === "/api/partner/status") return json({ state: "needs_activation" });
      if (path === "/api/partner/activate" && method === "POST") {
        return json({ code: "invalid_credential", message: "dial 23.138.12.112" }, 401);
      }
    }),
  );

  render(
    <PartnerGate>
      <div>business-ui</div>
    </PartnerGate>,
  );

  fireEvent.change(await screen.findByLabelText("伙伴密钥"), {
    target: { value: "bad-secret" },
  });
  fireEvent.click(screen.getByRole("button", { name: "激活" }));

  expect(await screen.findByText("伙伴密钥不正确。")).toBeTruthy();
  expect((screen.getByLabelText("伙伴密钥") as HTMLInputElement).value).toBe("");
  expect(screen.queryByText("business-ui")).toBeNull();
});

test("shows only sanitized activation errors and never raw bodies or gateway URLs", async () => {
  vi.stubGlobal(
    "fetch",
    stubPartnerFetch((path, method) => {
      if (path === "/api/partner/status") return json({ state: "needs_activation" });
      if (path === "/api/partner/activate" && method === "POST") {
        return json(
          {
            code: "device_mismatch",
            message: "dial 23.138.12.112:2443 with secret leaked-key",
          },
          401,
        );
      }
    }),
  );

  render(
    <PartnerGate>
      <div>business-ui</div>
    </PartnerGate>,
  );

  fireEvent.change(await screen.findByLabelText("伙伴密钥"), {
    target: { value: "leaked-key" },
  });
  fireEvent.click(screen.getByRole("button", { name: "激活" }));

  expect(await screen.findByText("该密钥已绑定其他电脑。")).toBeTruthy();
  expect(screen.queryByText(/23\.138\.12\.112/)).toBeNull();
  expect(screen.queryByText(/2443/)).toBeNull();
  expect(screen.queryByText(/leaked-key/)).toBeNull();
  expect(screen.queryByText(/dial/)).toBeNull();
  expect(screen.queryByText(/https?:\/\//)).toBeNull();
});

test("maps remaining sanitized partner error codes", async () => {
  vi.stubGlobal(
    "fetch",
    stubPartnerFetch((path) => {
      if (path === "/api/partner/status") {
        return json({ state: "locked", error_code: "client_too_old" });
      }
    }),
  );

  render(
    <PartnerGate>
      <div>business-ui</div>
    </PartnerGate>,
  );

  expect(await screen.findByText("软件版本过低，请获取新版。")).toBeTruthy();
});
