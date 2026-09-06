// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { WorkflowCanvas } from "./WorkflowCanvas";
import type { RemixLabExperiment } from "./api";

class ResizeObserverStub { observe() {} unobserve() {} disconnect() {} }
globalThis.ResizeObserver = ResizeObserverStub;
afterEach(() => { cleanup(); window.sessionStorage.clear(); });
const json = (value: unknown, status=200) => new Response(JSON.stringify(value), { status, headers:{"Content-Type":"application/json"} });
const experiment = {
 id:"same-project", source_text:"现有原文", slots:[{id:"old-slot",model:"old-writer"}],
 runs:[{id:"old-run",slot_id:"old-slot",run_index:1,status:"completed"}],
} as RemixLabExperiment;

test("switching to a draft without production clears the previous account gate", async () => {
 const previous={...experiment,runs:[{...experiment.runs[0],production:{account_id:"research",status:"waiting_confirm",step:"confirm"}}]} as RemixLabExperiment;
 const api=vi.fn(async(path:string)=>{
  if(path.includes("/stages")) return new Promise<Response>(()=>{});
  if(path.includes("/workflow")) return json({version:1,name:"工作流",nodes:[],edges:[]});
  if(path==="/api/accounts") return json([{id:"research",name:"认知研习",status:"active"}]);
  return json([]);
 });
 const props={api,prompts:[],onMessage:vi.fn(),onEditAgentPrompts:vi.fn(),runWorkflow:vi.fn(),refreshToken:0};
 const view=render(<WorkflowCanvas {...props} resumeExperiment={previous} />);
 await screen.findByRole("button",{name:"确认开始混剪"});
 view.rerender(<WorkflowCanvas {...props} resumeExperiment={{...experiment,id:"another-project",runs:[{...experiment.runs[0],id:"another-run"}]}} />);
 expect(screen.queryByRole("button",{name:"确认开始混剪"})).toBeNull();
 expect(screen.queryByLabelText("混剪账号")).toBeNull();
});

test("rerun selects both models and parameters, stays in the existing project and selects the new version", async () => {
 const next = {...experiment,slots:[...experiment.slots,{id:"new-slot",model:"claude-writer"}],runs:[...experiment.runs,{id:"new-run",slot_id:"new-slot",run_index:1,status:"queued"}]};
 let posted: unknown;
 const api=vi.fn(async(path:string,init?:RequestInit)=>{
   if(path.endsWith("/rerun-options")) return json({planner:{model:"old-planner",reasoning_effort:"high",service_tier:"priority"},writer:{model:"old-writer",reasoning_effort:"high",service_tier:"default"},reviewer:{model:"old-reviewer",reasoning_effort:"medium",service_tier:"default"}});
  if(path.endsWith("/rerun")) {posted=JSON.parse(String(init?.body));return json({experiment:next,run_id:"new-run"},202);}
  if(path.includes("/stages")) return json({run_id:path.includes("new-run")?"new-run":"old-run",status:"completed",stages:[],edges:[]});
  if(path.includes("/workflow")) return json({version:1,name:"工作流",nodes:[],edges:[]});
  return json([]);
 });
 const view=render(<WorkflowCanvas api={api} prompts={[]} onMessage={vi.fn()} onEditAgentPrompts={vi.fn()} runWorkflow={vi.fn()} refreshToken={0} resumeExperiment={experiment} />);
 fireEvent.click(await screen.findByRole("button",{name:"重新生成文案"}));
  fireEvent.change(await screen.findByLabelText("重跑写手模型"),{target:{value:"claude-writer"}});
  fireEvent.change(screen.getByLabelText("重跑二创策划模型"),{target:{value:"claude-planner"}});
  fireEvent.change(screen.getByLabelText("二创策划推理强度"),{target:{value:"medium"}});
  fireEvent.click(screen.getByRole("button",{name:"二创策划 Fast"}));
 fireEvent.change(screen.getByLabelText("重跑审稿模型"),{target:{value:"claude-reviewer"}});
 fireEvent.change(screen.getByLabelText("写手推理强度"),{target:{value:"low"}});
 fireEvent.click(screen.getByRole("button",{name:"审稿 Fast"}));
 fireEvent.click(screen.getByRole("button",{name:"开始新一轮"}));
  await waitFor(()=>expect(posted).toEqual({planner:{model:"claude-planner",reasoning_effort:"medium",service_tier:"default"},writer:{model:"claude-writer",reasoning_effort:"low",service_tier:"default"},reviewer:{model:"claude-reviewer",reasoning_effort:"medium",service_tier:"priority"}}));
 await waitFor(()=>expect(api.mock.calls.some(([path])=>path==="/api/remix-lab/runs/new-run/stages")).toBe(true));
 expect(screen.getByRole("button",{name:/old-writer/})).toBeTruthy();
 expect(screen.getByRole("button",{name:/claude-writer/})).toBeTruthy();
 expect(api.mock.calls.some(([path])=>path==="/api/remix-lab/workflow/run")).toBe(false);
 view.unmount();
 api.mockClear();
 render(<WorkflowCanvas api={api} prompts={[]} onMessage={vi.fn()} onEditAgentPrompts={vi.fn()} runWorkflow={vi.fn()} refreshToken={0} resumeExperiment={next as RemixLabExperiment} />);
 await waitFor(()=>expect(api.mock.calls.some(([path])=>path==="/api/remix-lab/runs/new-run/stages")).toBe(true));
 expect(api.mock.calls.some(([path])=>path==="/api/remix-lab/runs/old-run/stages")).toBe(false);
});
