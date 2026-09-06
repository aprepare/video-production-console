// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { RoleModelsDialog } from "./RoleModelsDialog";
import type { RemixLabWorkflow } from "./api";

afterEach(cleanup);

test("operator can choose two or three reference models and keep writer/reviewer settings", async () => {
  let saved: RemixLabWorkflow = {version:1,name:"工作流",nodes:[
    {id:"source",type:"input",title:"原文",x:0,y:0,config:{}},
    {id:"writer",type:"writer",title:"写手",x:600,y:0,config:{model:"main",reasoning_effort:"high",prompt_id:"bone_flesh"}},
    {id:"review",type:"reviewer",title:"审稿",x:900,y:0,config:{model:"review",system_prompt:"保留审稿规则"}},
  ],edges:[["source","writer"],["writer","review"]],production:{narration_voice_id:"voice-kept"}};
  const onSave=vi.fn(async (wf:RemixLabWorkflow)=>{saved=wf;});
  const view=render(<RoleModelsDialog workflow={saved} ownerLabel="当前账号" onSave={onSave} onClose={()=>{}} />);
  for(let i=1;i<=3;i++) {
    fireEvent.click(screen.getByRole("button",{name:"添加参考模型"}));
    fireEvent.change(screen.getByLabelText(`参考模型${i}模型`),{target:{value:`reference-${i}`}});
  }
  fireEvent.click(screen.getByRole("button",{name:"移除参考模型3"}));
  fireEvent.click(screen.getByRole("button",{name:"保存并完成"}));
  await waitFor(()=>expect(onSave).toHaveBeenCalledTimes(1));
  const refs=saved.nodes.filter(n=>n.type==="agent");
  expect(refs.map(n=>n.config.model)).toEqual(["reference-1","reference-2"]);
  expect(refs.every(n=>saved.edges.some(([a,b])=>a==="source"&&b===n.id)&&saved.edges.some(([a,b])=>a===n.id&&b==="writer"))).toBe(true);
  expect(saved.nodes.find(n=>n.id==="writer")?.config).toMatchObject({model:"main",reasoning_effort:"high",prompt_id:"bone_flesh"});
  expect(saved.production?.narration_voice_id).toBe("voice-kept");
  view.unmount();
  render(<RoleModelsDialog workflow={saved} ownerLabel="当前账号" onSave={onSave} onClose={()=>{}} />);
  expect((screen.getByLabelText("参考模型2模型") as HTMLInputElement).value).toBe("reference-2");
});
