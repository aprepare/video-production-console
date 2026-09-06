// @vitest-environment jsdom
import {cleanup, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {afterEach, expect, test, vi} from 'vitest';
import {RunWorkbench} from './RunWorkbench';
import {PublishedLibrary} from './PublishedLibrary';
import type {RemixLabRunView} from './api';
afterEach(cleanup);
test('short final saves unchanged without a minimum character gate',async()=>{
 const api=vi.fn(async(_path:string,_init?:RequestInit)=>new Response('{}'));
 render(<RunWorkbench api={api} run={run} onMessage={vi.fn()} onChanged={vi.fn()} onNavigate={vi.fn()} onOpenFlow={vi.fn()}/>);
 fireEvent.change(screen.getByLabelText('运行 1 正文'),{target:{value:'人工定稿。'}});
 fireEvent.click(screen.getByText('保存修改'));
 await waitFor(()=>expect(api.mock.calls.some(([,init])=>init?.method==='PUT')).toBe(true));
 const saved=api.mock.calls.find(([,init])=>init?.method==='PUT');
 expect(JSON.parse(String(saved?.[1]?.body)).continuous_script).toBe('人工定稿。');
});

const run = {id:'r1',run_index:1,comment:'',package_json:JSON.stringify({continuous_script:'原文'.repeat(40),short_titles:['标题']}),production:{status:'waiting_confirm',account_id:'a1'}} as RemixLabRunView;
test('dirty final must save successfully before production',async()=>{
 const produce=vi.fn(); const api=vi.fn(async()=>new Response('{}',{status:500}));
 render(<RunWorkbench api={api} run={run} onMessage={vi.fn()} onChanged={vi.fn()} onNavigate={vi.fn()} onOpenFlow={vi.fn()} onProduce={produce}/>);
 fireEvent.change(screen.getByLabelText('运行 1 正文'),{target:{value:'修改'.repeat(40)}});
 fireEvent.click(screen.getByText('确认开始混剪'));
 await waitFor(()=>expect(api).toHaveBeenCalled());
 expect(produce).not.toHaveBeenCalled();
 expect((screen.getByLabelText('运行 1 正文') as HTMLTextAreaElement).value).toBe('修改'.repeat(40));
});
test('library distinguishes loading and empty',()=>{
 render(<PublishedLibrary api={()=>new Promise(()=>{})} accounts={[]} onClose={vi.fn()} onOpenRun={vi.fn()} onMessage={vi.fn()}/>);
 expect(screen.getByText('正在读取交付文案…')).toBeTruthy();
 expect(screen.queryByText(/还没有出过草稿/)).toBeNull();
});

test('library refresh preserves unsaved metrics and labels delivery status', async()=>{
 const item={run_id:'r1',produced_at:'2026-09-05',account_id:'a1',published:false,title:'标题',metrics:{views:2,likes:0,orders:0,notes:''}};
 const api=vi.fn(async()=>new Response(JSON.stringify({items:[item]})));
 render(<PublishedLibrary api={api} accounts={[]} onClose={vi.fn()} onOpenRun={vi.fn()} onMessage={vi.fn()}/>);
 const views=await screen.findByLabelText('播放量');
 fireEvent.change(views,{target:{value:'99'}});
 fireEvent.click(screen.getByText('刷新'));
 await waitFor(()=>expect(api).toHaveBeenCalledTimes(2));
 expect((await screen.findByLabelText('播放量') as HTMLInputElement).value).toBe('99');
 expect(screen.getByText('待发布')).toBeTruthy();
});
test('production is locked until the request settles',async()=>{
 let finish!:()=>void;
 const produce=vi.fn(()=>new Promise<void>(resolve=>{finish=resolve;}));
 render(<RunWorkbench api={vi.fn()} run={run} onMessage={vi.fn()} onChanged={vi.fn()} onNavigate={vi.fn()} onOpenFlow={vi.fn()} onProduce={produce}/>);
 const button=screen.getByText('确认开始混剪');
 fireEvent.click(button); fireEvent.click(button);
 expect(produce).toHaveBeenCalledTimes(1);
 expect((button as HTMLButtonElement).disabled).toBe(true);
 finish(); await waitFor(()=>expect((button as HTMLButtonElement).disabled).toBe(false));
});

test('failed upload retries the existing project instead of creating another',async()=>{
 let uploads=0;
 const api=vi.fn(async(path:string, init?:RequestInit)=>{
  if(path==='/api/accounts')return new Response(JSON.stringify([{id:'a1',name:'账号一',status:'active'}]));
  if(path==='/api/projects' && init?.method==='POST')return new Response(JSON.stringify({id:'p1'}));
  if(path.includes('/assets/'))return new Response('{}',{status:++uploads===1?500:200});
  return new Response('{}');
 });
 render(<RunWorkbench api={api} run={run} onMessage={vi.fn()} onChanged={vi.fn()} onNavigate={vi.fn()} onOpenFlow={vi.fn()}/>);
 fireEvent.click(screen.getByText('导入混剪（新建项目）'));
 fireEvent.click(await screen.findByText('确认导入'));
 fireEvent.click(await screen.findByText('重试导入已创建项目'));
 await waitFor(()=>expect(uploads).toBe(2));
 expect(api.mock.calls.filter(([path,init])=>path==='/api/projects'&&init?.method==='POST')).toHaveLength(1);
});
