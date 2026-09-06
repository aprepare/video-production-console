// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { MediaSettingsPanel } from "./MediaSettingsPanel";
afterEach(cleanup);
test("saves separate URLs, keys and concurrency without persisting keys in browser storage",async()=>{
  const initial={image:{base_url:"",has_api_key:false},video:{base_url:"",has_api_key:false},image_concurrency:20,video_concurrency:6};
  const api=vi.fn(async(_path:string,init?:RequestInit)=>new Response(JSON.stringify(init?.method==="PUT"?{...initial,image:{base_url:"https://image.example/v1",has_api_key:true},video:{base_url:"https://video.example/v1",has_api_key:true}}:initial)));
  render(<MediaSettingsPanel api={api}/>);
  await waitFor(()=>expect(screen.getByLabelText("图片接口URL")).toBeEnabled());
  for(const [label,value] of [["图片接口URL","https://image.example/v1"],["视频接口URL","https://video.example/v1"],["图片API Key","image-key"],["视频API Key","video-key"],["图片并发","8"],["视频并发","4"]]) fireEvent.change(screen.getByLabelText(label),{target:{value}});
  fireEvent.click(screen.getByRole("button",{name:"保存图片／视频接口"}));
  await screen.findByText("接口已保存，后续生成使用新配置。");
  const call=api.mock.calls.find(([,init])=>init?.method==="PUT");
  expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({image:{base_url:"https://image.example/v1",api_key:"image-key"},video:{base_url:"https://video.example/v1",api_key:"video-key"},image_concurrency:8,video_concurrency:4});
  expect(screen.getByLabelText("图片API Key")).toHaveValue("");
  expect(screen.getByLabelText("视频API Key")).toHaveValue("");
});
