// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { RerunDialog } from "./RerunDialog";

afterEach(cleanup);
test("rerun submits the chosen reference count, models, effort and Fast without changing writer/reviewer", async () => {
  const writer={model:"writer-choice",reasoning_effort:"high",service_tier:"priority"};
  const reviewer={model:"reviewer-choice",reasoning_effort:"medium",service_tier:"default"};
  let posted:unknown;
  const api=vi.fn(async(path:string,init?:RequestInit)=>{
    if(path.endsWith("/rerun-options")) return new Response(JSON.stringify({references:[{model:"first-reference",reasoning_effort:"high",service_tier:"default"}],writer,reviewer}));
    if(path.endsWith("/rerun")) { posted=JSON.parse(String(init?.body)); return new Response(JSON.stringify({run_id:"next-round"})); }
    return new Response("[]");
  });
  const created=vi.fn();
  render(<RerunDialog api={api} runID="old-round" onClose={vi.fn()} onCreated={created}/>);
  await screen.findByLabelText("参考模型1模型");
  fireEvent.click(screen.getByRole("button",{name:"添加参考模型"}));
  fireEvent.click(screen.getByRole("button",{name:"添加参考模型"}));
  fireEvent.change(screen.getByLabelText("参考模型2模型"),{target:{value:"second-reference"}});
  fireEvent.change(screen.getByLabelText("参考模型2推理强度"),{target:{value:"medium"}});
  fireEvent.click(screen.getByRole("button",{name:"参考模型2 Fast"}));
  fireEvent.click(screen.getByRole("button",{name:"移除参考模型3"}));
  fireEvent.click(screen.getByRole("button",{name:"开始新一轮"}));
  await waitFor(()=>expect(created).toHaveBeenCalled());
  expect(posted).toEqual({references:[
    {model:"first-reference",reasoning_effort:"high",service_tier:"default"},
    {model:"second-reference",reasoning_effort:"medium",service_tier:"priority"},
  ],writer,reviewer});
});
