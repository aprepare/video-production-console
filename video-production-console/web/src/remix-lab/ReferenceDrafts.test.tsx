// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { RunWorkbench } from "./RunWorkbench";
import type { RemixLabRunView } from "./api";
afterEach(cleanup);

test("reference versions, titles and descriptions stay available while editing final copy", async()=>{
  const draft=(id:string,model:string,text:string)=>({id,node_id:"ref",title:"参考稿1",model,created_at:"2026-09-05T07:00:00Z",status:"completed",copy:{continuous_script:text,titles:[`${text}标题`],descriptions:[`${text}描述`],analysis:{opening:"短开头",middle:"祝福互动",ending:"自然承接"}}});
  const run={id:"run",experiment_id:"exp",run_index:1,status:"completed",continuous_script:"最终稿独立保存",package_json:"",comment:"",review_json:"",draft_v1_json:"",titles_json:"[]",adopted_project_id:""} as RemixLabRunView;
  const api=vi.fn(async()=>new Response(JSON.stringify({run_id:"run",status:"completed",stages:[],edges:[],reference_drafts:[draft("old","model-a","旧参考稿"),draft("new","model-b","新参考稿")]})));
  const writeText=vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator,"clipboard",{configurable:true,value:{writeText}});
  render(<RunWorkbench api={api} run={run} onMessage={()=>{}} onChanged={()=>{}} onNavigate={()=>{}} onOpenFlow={()=>{}} />);
  const select=await screen.findByLabelText("选择参考稿版本");
  fireEvent.change(select,{target:{value:"old"}});
  expect((screen.getByLabelText("参考文案") as HTMLTextAreaElement).value).toBe("旧参考稿");
  fireEvent.click(screen.getByRole("button",{name:"标题与视频描述"}));
  expect(screen.getByText("旧参考稿标题")).toBeTruthy();
  expect(screen.getByText("旧参考稿描述")).toBeTruthy();
  fireEvent.click(screen.getByRole("button",{name:"复制视频描述1"}));
  expect(writeText).toHaveBeenCalledWith("旧参考稿描述");
  // Candidate selection must never replace the final draft editor.
  expect(screen.getByDisplayValue("最终稿独立保存")).toBeTruthy();
  fireEvent.change(select,{target:{value:"new"}});
  expect(screen.getByText("新参考稿标题")).toBeTruthy();
});
