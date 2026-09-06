// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { WorkflowCanvas } from "./WorkflowCanvas";
import type { RemixLabWorkflow } from "./api";

class ResizeObserverStub { observe() {} unobserve() {} disconnect() {} }
globalThis.ResizeObserver = ResizeObserverStub;
afterEach(() => { cleanup(); window.localStorage.clear(); });
const json = (value: unknown) => new Response(JSON.stringify(value));

test("role settings save independent models to the current account and survive reopening without altering prompts", async () => {
  let stored: RemixLabWorkflow = { version: 1, name: "账号工作流", nodes: [
    {id:"source",type:"input",title:"原文",x:0,y:0,config:{}},
    {id:"hook",type:"agent",title:"二创策划",x:100,y:0,config:{model:"planner-old",reasoning_effort:"high",service_tier:"priority",channel:"search",system_prompt:"策划规则"}},
    {id:"writer",type:"writer",title:"写手",x:200,y:0,config:{model:"writer-old",reasoning_effort:"low",service_tier:"priority",prompt_id:"bone_flesh"}},
    {id:"review",type:"reviewer",title:"审稿",x:300,y:0,config:{model:"review-old",reasoning_effort:"medium",system_prompt:"审稿规则"}},
  ],edges:[["source","hook"],["hook","writer"],["writer","review"]],production:{narration_voice_id:"keep-voice"} };
  const original = structuredClone(stored);
  let savedPath = "";
  const api = vi.fn(async (path:string, init?:RequestInit) => {
    if(path.startsWith("/api/remix-lab/workflow")) {
      if(init?.method === "PUT") { stored = JSON.parse(String(init.body)); savedPath = path; }
      return json(stored);
    }
    return json([]);
  });
  const props = {api,prompts:[],onMessage:vi.fn(),onEditAgentPrompts:vi.fn(),runWorkflow:vi.fn(),refreshToken:0,accountID:"cloud"};
  const page = render(<WorkflowCanvas {...props} />);
  fireEvent.click(await screen.findByRole("button", {name:"模型配置"}));
  fireEvent.change(await screen.findByLabelText("二创策划模型"), {target:{value:"planner-new"}});
  fireEvent.click(within(screen.getByRole("group", {name:"二创策划"})).getByRole("button", {name:"改回默认模型"}));
  expect((screen.getByLabelText("二创策划模型") as HTMLInputElement).value).toBe("planner-old");
  fireEvent.change(screen.getByLabelText("二创策划模型"), {target:{value:"planner-new"}});
  fireEvent.change(screen.getByLabelText("写手模型"), {target:{value:"writer-new"}});
  fireEvent.change(screen.getByLabelText("审稿模型"), {target:{value:"review-new"}});
  fireEvent.change(screen.getByLabelText("二创策划推理强度"), {target:{value:"medium"}});
  fireEvent.click(screen.getByRole("button", {name:"二创策划 Fast"}));
  fireEvent.click(screen.getByRole("button", {name:"审稿 Fast"}));
  fireEvent.click(screen.getByRole("button", {name:"保存并完成"}));
  await waitFor(() => expect(screen.queryByRole("dialog",{name:"模型配置"})).toBeNull());
  expect(savedPath).toBe("/api/remix-lab/workflow?account_id=cloud");
  expect(stored.nodes[1].config).toEqual({...original.nodes[1].config,model:"planner-new",reasoning_effort:"medium",service_tier:"default"});
  expect(stored.nodes[2].config).toEqual({...original.nodes[2].config,model:"writer-new"});
  expect(stored.nodes[3].config).toEqual({...original.nodes[3].config,model:"review-new",service_tier:"priority"});
  expect(stored.production).toEqual(original.production);
  expect(stored.edges).toEqual(original.edges);
  page.unmount();
  render(<WorkflowCanvas {...props} />);
  fireEvent.click(await screen.findByRole("button",{name:"模型配置"}));
  expect((await screen.findByLabelText("二创策划模型") as HTMLInputElement).value).toBe("planner-new");
  expect((screen.getByLabelText("写手模型") as HTMLInputElement).value).toBe("writer-new");
  expect(screen.getByRole("button",{name:"审稿 Fast"}).getAttribute("aria-pressed")).toBe("true");
});
