// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import {render,screen,cleanup,fireEvent} from "@testing-library/react";
import {afterEach,test,expect,vi} from "vitest";
import {AssemblyStatus,ReasoningSelect} from "./WorkflowControls";
import type {AiShort} from "./api";
afterEach(cleanup);
test("reasoning selection exposes high effort",()=>{const change=vi.fn();render(<ReasoningSelect value="low" onChange={change}/>);fireEvent.change(screen.getByLabelText("分镜思考强度"),{target:{value:"high"}});expect(change).toHaveBeenCalledWith("high")});
test("assembly progress shows live stage, failure and confirmed success",()=>{
 const s={status:"assembling",assembly_progress:{stage:3,message:"导入剪映",started_at:new Date().toISOString(),updated_at:new Date().toISOString()}} as AiShort;
 const view=render(<AssemblyStatus short={s}/>);
 expect(screen.getByRole("progressbar")).toHaveAttribute("value","3");
 expect(screen.getByText("进行中 · 导入剪映")).toBeVisible();
 view.rerender(<AssemblyStatus short={{...s,status:"failed",error:"导入失败"}}/>);expect(screen.getByText("草稿导出失败，可重试")).toBeVisible();
 view.rerender(<AssemblyStatus short={{...s,status:"assembled",draft_name:"新草稿"}}/>);expect(screen.getByRole("progressbar")).toHaveAttribute("value","4");expect(screen.getByText("草稿已成功导入剪映")).toBeVisible();
});
