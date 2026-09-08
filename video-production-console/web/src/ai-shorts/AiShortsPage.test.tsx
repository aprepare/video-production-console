// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { AiShortsPage } from "./AiShortsPage";
import { defaultVisualSettings, type AiShort } from "./api";
const json = (data: unknown, status = 200) => new Response(JSON.stringify(data), { status });
const short: AiShort = { id: "one", title: "测试短片", mode: "explainer", headline: "", story: "已保存的口播文案", style: "documentary", status: "storyboard", characters: [], shots: [{ index: 0, narration: "第一句", scene: "原画面", motion: "", characters: [], seconds: 3, image_status: "pending", video_status: "pending" }], created_at: "", updated_at: "" };
const base = (path: string) => path === "/api/ai-shorts" ? json({ items: [short] }) : path === "/api/ai-shorts/one" ? json(short) : path === "/api/accounts" ? json([]) : json({ items: [] });
const editorialStyles = [
  { key: "finance_editorial", name: "财经编辑混合", prompt: "", usage: "大多数镜头使用纸张拼贴，概念关系用微缩模型", note: "杂志感、统一克制", preview: "/style-previews/finance_editorial.jpg" },
  { key: "paper_collage", name: "纸张拼贴", prompt: "", usage: "大多数镜头", preview: "/style-previews/paper_collage.jpg" },
  { key: "miniature", name: "微缩模型", prompt: "", usage: "概念关系" },
  { key: "documentary", name: "生活纪实", prompt: "", usage: "旧项目单一画风" },
];
afterEach(() => { cleanup(); sessionStorage.clear(); vi.restoreAllMocks(); });

test("creates new explainers with the finance editorial strategy by default", async () => {
  let sent: Record<string, unknown> = {};
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/ai-shorts/styles") return json({ items: editorialStyles });
    if (path === "/api/ai-shorts" && init?.method === "POST") {
      sent = JSON.parse(String(init.body));
      return json({ ...short, style: sent.style });
    }
    return base(path);
  });
  render(<AiShortsPage api={api} onNavigate={vi.fn()} />);
  expect(await screen.findByLabelText(/^画面风格策略/)).toHaveValue("finance_editorial");
  // 选中画风的用途和判断来自 /api/ai-shorts/styles，参考图卡片可点选。
  expect(screen.getByText(/大多数镜头使用纸张拼贴/)).toBeInTheDocument();
  expect(screen.getByText(/杂志感、统一克制/)).toBeInTheDocument();
  expect(screen.getByRole("radio", { name: "财经编辑混合" })).toHaveAttribute("aria-checked", "true");
  fireEvent.click(screen.getByRole("radio", { name: "纸张拼贴" }));
  expect(screen.getByLabelText(/^画面风格策略/)).toHaveValue("paper_collage");
  fireEvent.click(screen.getByRole("radio", { name: "财经编辑混合" }));
  // 解说模式也有顶部大标题输入框（09-07 之前没有，导致标题一直是空的）；填了要随创建请求发出去。
  fireEvent.change(screen.getByLabelText(/^顶部大标题/), { target: { value: "老百姓的钱开始值钱了" } });
  fireEvent.change(screen.getByLabelText(/口播文案（/), { target: { value: "一家人辛苦攒下来的钱，存款到期之后先把条件问清楚。" } });
  fireEvent.click(screen.getByRole("button", { name: "建短片" }));
  await waitFor(() => expect(sent.style).toBe("finance_editorial"));
  expect(sent.headline).toBe("老百姓的钱开始值钱了");
});

test("per-shot style is editable on any project and only sent when changed", async () => {
  let mixedPayload: Record<string, unknown> = {};
  const mixed: AiShort = {
    ...short,
    style: "finance_editorial",
    shots: [{ ...short.shots[0], style_key: "paper_collage" }],
  };
  const mixedApi = async (path: string, init?: RequestInit) => {
    if (path === "/api/ai-shorts/styles") return json({ items: editorialStyles });
    if (path.endsWith("/shots/0")) {
      mixedPayload = JSON.parse(String(init?.body));
      return json({ ...mixed, shots: [{ ...mixed.shots[0], ...mixedPayload }] });
    }
    if (path === "/api/ai-shorts/one") return json(mixed);
    return base(path);
  };
  const view = render(<AiShortsPage api={mixedApi} shortID="one" onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "改描述" }));
  const shotStyle = screen.getByLabelText(/^镜头画风/) as HTMLSelectElement;
  expect(shotStyle).toHaveValue("paper_collage");
  // 混合策略本身不是画风，不在单镜可选项里；其余每套都能选（分段画风的手动版）。
  expect(Array.from(shotStyle.options, (option) => option.value)).toEqual(["paper_collage", "miniature", "documentary"]);
  fireEvent.change(shotStyle, { target: { value: "miniature" } });
  fireEvent.click(screen.getByRole("button", { name: "保存" }));
  await waitFor(() => expect(mixedPayload.style_key).toBe("miniature"));

  view.unmount();
  let singlePayload: Record<string, unknown> = {};
  const singleApi = async (path: string, init?: RequestInit) => {
    if (path === "/api/ai-shorts/styles") return json({ items: editorialStyles });
    if (path.endsWith("/shots/0")) {
      singlePayload = JSON.parse(String(init?.body));
      return json(short);
    }
    return base(path);
  };
  const single = render(<AiShortsPage api={singleApi} shortID="one" onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "改描述" }));
  // 没改画风就不发 style_key：后端会把发过画风的镜钉住，不该把没动过的也钉上。
  expect(screen.getByLabelText(/^镜头画风/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "保存" }));
  await waitFor(() => expect(singlePayload).toHaveProperty("scene"));
  expect(singlePayload).not.toHaveProperty("style_key");
  single.unmount();
});

test("unlocks generation after the server normalizes saved fields", async () => {
  let stored = {...short, visual_settings: defaultVisualSettings()};
  const api = async (path: string, init?: RequestInit) => {
    if (path === "/api/ai-shorts/one") {
      if (init?.method === "PATCH") {
        const input = JSON.parse(String(init.body));
        stored = {...stored, ...input, story: input.story.trim(), visual_settings: {...input.visual_settings, video_model: input.visual_settings.video_model.trim()}};
      }
      return json(stored);
    }
    return base(path);
  };
  render(<AiShortsPage api={api} shortID="one" onNavigate={vi.fn()} />);
  fireEvent.change(await screen.findByLabelText("口播文案"), {target: {value: short.story + "\n"}});
  fireEvent.change(screen.getByLabelText("生视频模型"), {target: {value: " model-v1 "}});
  fireEvent.click(screen.getByRole("button", {name: "保存文案"}));
  await waitFor(() => expect(screen.getByRole("button", {name: "生成全部图片"})).toBeEnabled());
  expect(screen.getByLabelText("生视频模型")).toHaveValue("model-v1");
});

test("ignores visual property order and omitted optional defaults in restored drafts", async () => {
  const settings = defaultVisualSettings();
  const reordered = Object.fromEntries(Object.entries(settings).reverse());
  delete reordered.opening_video_seconds;
  sessionStorage.setItem("console:ai-short-draft:one", JSON.stringify({visual: {...reordered, video_model: ""}}));
  render(<AiShortsPage api={async path => path === "/api/ai-shorts/one" ? json({...short, visual_settings: settings}) : base(path)} shortID="one" onNavigate={vi.fn()} />);
  expect(await screen.findByRole("button", {name: "生成全部图片"})).toBeEnabled();
});

test("explains why editing a shot prevents generation", async () => {
  render(<AiShortsPage api={async path => base(path)} shortID="one" onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", {name: "改描述"}));
  expect(screen.getByRole("button", {name: "生成全部图片"})).toBeDisabled();
  expect(screen.getByText("第 1 镜正在编辑，请先保存或取消该分镜的修改。" )).toBeVisible();
});

test("shows polling failures and unlocks resume when the task finishes", async () => {
  let poll!: () => Promise<void>;
  vi.spyOn(window, "setInterval").mockImplementation(((callback: () => Promise<void>) => { poll = callback; return 1; }) as typeof window.setInterval);
  let calls = 0;
  const partial = {...short, shots: [{...short.shots[0], image_status: "done", image_path: "still.png"}, {...short.shots[0], index: 1}]};
  const api = async (path: string) => {
    if (path === "/api/ai-shorts/one") {
      calls++;
      if (calls === 2) throw new Error("offline");
      return json({...partial, status: calls === 1 ? "generating" : "failed"});
    }
    return base(path);
  };
  render(<AiShortsPage api={api} shortID="one" onNavigate={vi.fn()} />);
  expect(await screen.findByRole("button", {name: "生成中…"})).toBeDisabled();
  await act(async () => { await poll(); });
  expect(screen.getByRole("alert")).toHaveTextContent("任务状态刷新失败");
  await act(async () => { await poll(); });
  expect(screen.queryByRole("alert")).toBeNull();
  expect(screen.getByRole("button", {name: "续跑未完成的"})).toBeEnabled();
});

test("enables opening video and saves its model before offering video regeneration", async () => {
  let stored: AiShort={...short,visual_settings:defaultVisualSettings(),shots:[{...short.shots[0],start_s:0,end_s:4,image_status:"done",image_path:"still.png"}]};
  const api=vi.fn(async(path:string,init?:RequestInit)=>{
    if(path==="/api/ai-shorts/styles") return json({items:[],default_video_model:"grok-imagine-video-1.5"});
    if(path==="/api/ai-shorts/one") {
      if(init?.method==="PATCH") stored={...stored,...JSON.parse(String(init.body))};
      return json(stored);
    }
    return base(path);
  });
  render(<AiShortsPage api={api} shortID="one" onNavigate={vi.fn()} />);
  fireEvent.change(await screen.findByLabelText("AI 视频用法"),{target:{value:"opening"}});
  fireEvent.change(screen.getByLabelText("开场时长"),{target:{value:"60"}});
  const model=screen.getByLabelText("生视频模型");
  await waitFor(()=>expect(model).toHaveAttribute("placeholder","默认：grok-imagine-video-1.5"));
  expect(screen.getByRole("button",{name:"重生图"})).toBeDisabled();
  fireEvent.change(model,{target:{value:"grok-imagine-video-1.5"}});
  fireEvent.click(screen.getByRole("button",{name:"保存文案"}));
  expect(await screen.findByRole("button",{name:"只重生视频"})).toBeEnabled();
  expect(stored.visual_settings).toMatchObject({video_plan:"opening",opening_video_seconds:60,video_model:"grok-imagine-video-1.5"});
  expect(stored.shots[0].image_path).toBe("still.png");
  expect(api.mock.calls.some(([path])=>path.endsWith("/generate")||path.endsWith("/storyboard"))).toBe(false);
});

test("shows the runtime image default and submits the chosen model on creation", async () => {
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/ai-shorts/styles") return json({ items: [], default_image_model: "relay-image-default" });
    if (path === "/api/ai-shorts" && init?.method === "POST") return json(short);
    return base(path);
  });
  const view = render(<AiShortsPage api={api} onNavigate={vi.fn()} />);
  const model = await screen.findByLabelText("生图模型");
  await waitFor(() => expect(model).toHaveAttribute("placeholder", "默认：relay-image-default"));
  fireEvent.change(model, { target: { value: "custom-image-v1" } });
  view.unmount();
  render(<AiShortsPage api={api} onNavigate={vi.fn()} />);
  expect(screen.getByLabelText("生图模型")).toHaveValue("custom-image-v1");
  fireEvent.change(screen.getByLabelText(/口播文案（/), { target: { value: "一家人辛苦攒下来的钱，存款到期之后先把条件问清楚。" } });
  fireEvent.click(screen.getByRole("button", { name: "建短片" }));
  await waitFor(() => expect(api.mock.calls.some(([path, init]) => path === "/api/ai-shorts" && init?.method === "POST" && JSON.parse(String(init.body)).image_model === "custom-image-v1")).toBe(true));
});

test("saves image model changes before generation without requiring a new storyboard", async () => {
  let stored = { ...short, image_model: "old-image-model" };
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/ai-shorts/one") {
      if (init?.method === "PATCH") stored = { ...stored, ...JSON.parse(String(init.body)) };
      return json(stored);
    }
    return base(path);
  });
  render(<AiShortsPage api={api} shortID="one" onNavigate={vi.fn()} />);
  const model = await screen.findByLabelText("生图模型");
  expect(model).toHaveValue("old-image-model");
  fireEvent.change(model, { target: { value: "new-image-model" } });
  expect(screen.getByRole("button", { name: "生成全部图片" })).toBeDisabled();
  expect(screen.getByText("生图模型有未保存修改，请先保存文案。保存后用于后续生图，无需重新拆分镜。")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "保存文案" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "生成全部图片" })).toBeEnabled());
  expect(stored.image_model).toBe("new-image-model");
  expect(screen.getByRole("button", { name: "重生图" })).toBeEnabled();
  expect(api.mock.calls.some(([path]) => path.endsWith("/storyboard") || path.endsWith("/generate"))).toBe(false);
  fireEvent.change(model, { target: { value: "" } });
  fireEvent.click(screen.getByRole("button", { name: "保存文案" }));
  await waitFor(() => expect(stored.image_model).toBe(""));
});
test("keeps a failed route distinct from new creation and allows retry", async () => {
  let failed = true;
  const api = vi.fn(async (path: string) => path === "/api/ai-shorts/one" && failed ? json({ message: "连接暂时不可用" }, 503) : base(path));
  render(<AiShortsPage api={api} shortID="one" onNavigate={vi.fn()} />);
  expect(await screen.findByRole("alert")).toHaveTextContent("连接暂时不可用");
  expect(screen.queryByText("新建短片")).toBeNull();
  failed = false;
  fireEvent.click(screen.getByRole("button", { name: "重试读取短片" }));
  expect(await screen.findByRole("button", { name: "改描述" })).toBeEnabled();
});
test("locks shot save while pending and keeps edits after failure", async () => {
  let finish!: (response: Response) => void;
  const api = vi.fn(async (path: string) => path.endsWith("/shots/0") ? new Promise<Response>((resolve) => { finish = resolve; }) : base(path));
  render(<AiShortsPage api={api} shortID="one" onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "改描述" }));
  fireEvent.change(screen.getByLabelText("画面"), { target: { value: "编辑后的画面" } });
  fireEvent.click(screen.getByRole("button", { name: "保存" }));
  expect(screen.getByRole("button", { name: "保存中…" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "取消" })).toBeDisabled();
  expect(screen.getByLabelText("画面")).toBeDisabled();
  expect(screen.getByRole("button", { name: "返回列表" })).toBeDisabled();
  finish(json({ message: "保存失败，请重试" }, 500));
  await waitFor(() => expect(screen.getByRole("button", { name: "保存" })).toBeEnabled());
  expect(screen.getByLabelText("画面")).toHaveValue("编辑后的画面");
});
test("does not start generation against unsaved story edits", async () => {
  render(<AiShortsPage api={async (path) => base(path)} shortID="one" onNavigate={vi.fn()} />);
  await screen.findByRole("button", { name: "改描述" });
  fireEvent.change(screen.getByLabelText("口播文案"), { target: { value: "刚改过的文案" } });
  expect(screen.getByRole("button", { name: "生成全部图片" })).toBeDisabled();
  expect(screen.getByText("文案有未保存修改，请保存并重新拆分镜后生成。 ".trim())).toBeVisible();
});


test("restores story and shot edits after global navigation unmounts the page", async () => {
  const api = async (path: string) => base(path);
  const view = render(<AiShortsPage api={api} shortID="one" onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "改描述" }));
  fireEvent.change(screen.getByLabelText("画面"), { target: { value: "未保存分镜" } });
  fireEvent.change(screen.getByLabelText("口播文案"), { target: { value: "未保存文案" } });
  view.unmount();
  render(<AiShortsPage api={api} shortID="one" onNavigate={vi.fn()} />);
  expect(await screen.findByLabelText("画面")).toHaveValue("未保存分镜");
  expect(screen.getByLabelText("口播文案")).toHaveValue("未保存文案");
});
test("restores the new short form after leaving and returning", async () => {
  const api = async (path: string) => base(path);
  const view = render(<AiShortsPage api={api} onNavigate={vi.fn()} />);
  fireEvent.change(screen.getByLabelText(/口播文案（/), { target: { value: "新的创作草稿" } });
  view.unmount();
  render(<AiShortsPage api={api} onNavigate={vi.fn()} />);
  expect(screen.getByLabelText(/口播文案（/)).toHaveValue("新的创作草稿");
});
test("ignores an old short polling response after switching to another short", async () => {
  let poll!: () => Promise<void>;
  vi.spyOn(window, "setInterval").mockImplementation(((callback: () => Promise<void>) => { poll = callback; return 1; }) as typeof window.setInterval);
  let calls = 0;
  let finish!: (response: Response) => void;
  const api = async (path: string) => {
    if (path === "/api/ai-shorts/one") return ++calls === 1 ? json({ ...short, status: "generating" }) : new Promise<Response>((resolve) => { finish = resolve; });
    if (path === "/api/ai-shorts/two") return json({ ...short, id: "two", story: "第二条文案" });
    return base(path);
  };
  const view = render(<AiShortsPage api={api} shortID="one" onNavigate={vi.fn()} />);
  await screen.findByLabelText("口播文案");
  let pending!: Promise<void>;
  act(() => { pending = poll(); });
  view.rerender(<AiShortsPage api={api} shortID="two" onNavigate={vi.fn()} />);
  await waitFor(() => expect(screen.getByLabelText("口播文案")).toHaveValue("第二条文案"));
  await act(async () => { finish(json({ ...short, title: "旧短片轮询返回", status: "ready" })); await pending; });
  expect(screen.queryByText("旧短片轮询返回")).toBeNull();
  expect(screen.getByRole("button", { name: "生成全部图片" })).toBeEnabled();
});
test("clears persisted edits after a successful save", async () => {
  let stored = short;
  const api = async (path: string, init?: RequestInit) => {
    if (path === "/api/ai-shorts/one") {
      if (init?.method === "PATCH") stored = { ...stored, ...JSON.parse(String(init.body)) };
      return json(stored);
    }
    return base(path);
  };
  const view = render(<AiShortsPage api={api} shortID="one" onNavigate={vi.fn()} />);
  await screen.findByLabelText("口播文案");
  fireEvent.change(screen.getByLabelText("口播文案"), { target: { value: "保存的新内容" } });
  fireEvent.click(screen.getByRole("button", { name: "保存文案" }));
  await screen.findByText("已保存。改了旁白记得重新拆分镜。");
  expect(sessionStorage.getItem("console:ai-short-draft:one")).toBeNull();
  view.unmount();
  stored = { ...stored, story: "更新后的服务端文案" };
  render(<AiShortsPage api={api} shortID="one" onNavigate={vi.fn()} />);
  expect(await screen.findByLabelText("口播文案")).toHaveValue("更新后的服务端文案");
});

test("generates a cover from the saved headline", async () => {
  const withHeadline: AiShort = { ...short, headline: "老百姓的钱开始值钱了" };
  const api = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/ai-shorts/one/cover" && init?.method === "POST") return json({ status: "ok" }, 202);
    return path === "/api/ai-shorts/one" ? json(withHeadline) : base(path);
  });
  render(<AiShortsPage api={api} shortID="one" onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "生成封面" }));
  await waitFor(() => expect(api.mock.calls.some(([path, init]) => path === "/api/ai-shorts/one/cover" && init?.method === "POST")).toBe(true));
  expect(await screen.findByRole("button", { name: "封面出图中…" })).toBeDisabled();
});

test("saves visual settings without invoking storyboard or generation", async () => {
  let stored: AiShort={...short,visual_settings:defaultVisualSettings()};
  const api=vi.fn(async(path:string,init?:RequestInit)=>{
    if(path==="/api/ai-shorts/one"){
      if(init?.method==="PATCH") stored={...stored,...JSON.parse(String(init.body))};
      return json(stored);
    }
    return base(path);
  });
  render(<AiShortsPage api={api} shortID="one" onNavigate={vi.fn()} />);
  const position=await screen.findByLabelText("字幕位置");
  fireEvent.change(position,{target:{value:"middle"}});
  fireEvent.change(screen.getByLabelText("运动强度"),{target:{value:"none"}});
  expect(screen.getByRole("button",{name:"生成全部图片"})).toBeDisabled();
  fireEvent.click(screen.getByRole("button",{name:"保存文案"}));
  await screen.findByText("已保存。改了旁白记得重新拆分镜。");
  expect(stored.visual_settings?.caption_position).toBe("middle");
  expect(stored.visual_settings?.caption_enabled).toBe(true);
  expect(stored.visual_settings?.motion_strength).toBe("none");
  expect(api.mock.calls.some(([path])=>path.endsWith("/generate")||path.endsWith("/storyboard"))).toBe(false);
});

test("shot editor persists intent movement annotation and intentionally empty keywords", async () => {
  let sent: Record<string,unknown>={};
  const stored:AiShort={...short,visual_settings:defaultVisualSettings(),shots:[{...short.shots[0],narration:"利率0.95%，先看风险",keywords:[{text:"0.95%",kind:"number"}]}]};
  const api=async(path:string,init?:RequestInit)=>{
    if(path.endsWith("/shots/0")){sent=JSON.parse(String(init?.body));return json({...stored,shots:[{...stored.shots[0],...sent}]});}
    return path==="/api/ai-shorts/one"?json(stored):base(path);
  };
  render(<AiShortsPage api={api} shortID="one" onNavigate={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button",{name:"改描述"}));
  fireEvent.change(screen.getByLabelText("配图意图"),{target:{value:"用两份资料表现条件对比"}});
  fireEvent.change(screen.getByLabelText("图片运动"),{target:{value:"still"}});
  fireEvent.change(screen.getByLabelText("重点标注（可空）"),{target:{value:"先看清条件"}});
  fireEvent.click(screen.getByRole("button",{name:"移除关键词1"}));
  fireEvent.click(screen.getByRole("button",{name:"保存"}));
  await screen.findByRole("button",{name:"改描述"});
  expect(sent).toMatchObject({visual_intent:"用两份资料表现条件对比",camera_move:"still",annotation:"先看清条件",keywords:[]});
});
