package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func remixTools() []Tool {
	runWorkflow := map[string]Param{
		"source":     reqStr("对标原文/口播全文（1–20000 字）。从对标库来时用 get_reference 的 transcript"),
		"account_id": str("出稿账号 id：按这个账号的风格、简报和提示词写。强烈建议传"),
		"run_count":  integer("同一模型跑几份 1–3，默认 1"),
		"model":      str("写手模型（不填用账号/全局默认）"),
		"models":     strList("多模型对比，和 model 合计最多 4 个"),
		"reviewer":   object(modelBrief + "，审稿模型（可选）"),
		"auto":       boolean("全自动：定稿后直接进入配音和混剪（要求 account_id 且只跑 1 份）"),
	}
	return []Tool{
		{Name: "list_remix_experiments", Description: "二创历史：每次二创是一个实验，含若干 run（每个 run 是一份稿）。", Method: http.MethodGet, Path: "/api/remix-lab/experiments"},
		{Name: "get_remix_experiment", Description: "二创实验详情：runs[].status（running/completed/failed）、定稿、发布包。开跑后用它轮询。", Method: http.MethodGet, Path: "/api/remix-lab/experiments/{id}", Params: map[string]Param{"id": pathID("实验 id")}},
		{Name: "start_remix", Description: "开始二创：把对标文案交给写手和审稿按账号工作流出稿（异步，返回实验，之后用 get_remix_experiment 轮询，一般 1–5 分钟）。", Method: http.MethodPost, Path: "/api/remix-lab/workflow/run", Confirm: "调用写作/审稿模型（产生模型费用）；auto=true 还会继续配音和混剪", Params: runWorkflow},
		{Name: "import_finished_script", Description: "跳过二创，直接导入一篇已经写好的成稿（停在确认生产的闸门前）。", Method: http.MethodPost, Path: "/api/remix-lab/workflow/import-draft", Params: map[string]Param{
			"continuous_script": reqStr("成稿正文"), "account_id": str("账号 id"),
			"short_titles": strList("短标题（第 1 条做板面主标题）"), "titles": strList("长标题"), "cover_titles": strList("封面两行大字"),
			"descriptions": strList("简介"), "topics": strList("话题"), "cta": str("结尾转化句"), "cover_path": str("封面图路径"),
		}},
		{Name: "get_run_stages", Description: "看一份稿的各工作流节点输入输出、模型和耗时（排查写得不好时用）。", Method: http.MethodGet, Path: "/api/remix-lab/runs/{id}/stages", Params: map[string]Param{"id": pathID("run id")}},
		{Name: "rework_run", Description: "带修改意见让审稿 agent 改稿（异步）。", Method: http.MethodPost, Path: "/api/remix-lab/runs/{id}/rework", Confirm: "调用模型改稿（产生模型费用）", Params: map[string]Param{
			"id": pathID("run id"), "annotations": reqStr("修改意见，≤2000 字"),
		}},
		{Name: "retry_run", Description: "失败的稿整体重试；或指定节点 node_id 重跑（可换 model）。", Method: http.MethodPost, Path: "/api/remix-lab/runs/{id}/retry", Confirm: "调用模型重跑（产生模型费用）", Params: map[string]Param{
			"id": pathID("run id"), "node_id": str("只重跑这个节点（可选）"), "model": str("换用的模型（可选）"),
		}},
		{Name: "rerun_with_models", Description: "用另一组模型把这份稿重新跑一遍（生成新的 run）。先用 console_api GET /api/remix-lab/runs/{id}/rerun-options 看可选项。", Method: http.MethodPost, Path: "/api/remix-lab/runs/{id}/rerun", Confirm: "调用模型重新写稿（产生模型费用）", Params: map[string]Param{
			"id": pathID("run id"), "writer": reqObject(modelBrief), "reviewer": reqObject(modelBrief),
			"planner": object(modelBrief + "（可选）"), "references": {Type: "array", Items: "object", Description: "情报 agent 设置（可选）"},
		}},
		{Name: "get_publish_package", Description: "读一份稿的发布包：定稿正文、短标题、简介、话题、封面大字、封面路径。", Method: http.MethodGet, Path: "/api/remix-lab/runs/{id}/package", Params: map[string]Param{"id": pathID("run id")}},
		{Name: "save_publish_package", Description: "保存修改后的定稿和发布包（整体替换：先 get_publish_package 拿到全部字段，改完全部传回）。视频号短标题规则：一句话、无标点无空格、≤16 字。", Method: http.MethodPut, Path: "/api/remix-lab/runs/{id}/package", Params: map[string]Param{
			"id": pathID("run id"), "continuous_script": reqStr("定稿正文"),
			"short_titles": strList("短标题（第 1 条必填，做板面主标题）"), "titles": strList("长标题"), "cover_titles": strList("封面两行大字"),
			"descriptions": strList("简介"), "topics": strList("话题"), "cta": str("结尾转化句"), "cover_path": str("封面路径"),
		}},
		{Name: "generate_cover", Description: "按账号封面模板本地出封面（免费、立即出，字一定准确）。两行大字各 1–12 字、不带标点。图存到「视频/封面图」并写回发布包。", Method: http.MethodPost, Path: "/api/remix-lab/runs/{id}/cover", Params: map[string]Param{
			"id": pathID("run id"), "line1": reqStr("第一行大字"), "line2": reqStr("第二行大字"),
		}},
		{Name: "generate_ai_cover", Description: "用 RunningHub AI 按账号参考封面整张重画封面（后台出图，平台常排队 3–25 分钟；用 get_ai_cover_status 查进度）。出好后人工看一眼字有没有写错。", Method: http.MethodPost, Path: "/api/remix-lab/runs/{id}/ai-cover", Confirm: "调用 RunningHub 出图，每张约 30 币", Params: map[string]Param{
			"id": pathID("run id"), "line1": reqStr("第一行大字"), "line2": reqStr("第二行大字"),
		}},
		{Name: "get_ai_cover_status", Description: "AI 封面进度：status = none / running / done / failed；done 时 cover.path 是封面库路径。", Method: http.MethodGet, Path: "/api/remix-lab/runs/{id}/ai-cover", Params: map[string]Param{"id": pathID("run id")}},

		// 提示词库与风格
		{Name: "list_prompts", Description: "二创提示词库（每条含 system、user 模板）。", Method: http.MethodGet, Path: "/api/remix-lab/prompts"},
		{Name: "save_prompt", Description: "新增或修改一条提示词库条目（id 已存在就是修改）。user 模板里用 {{SOURCE}} / {{NOTES}} 占位。", Method: http.MethodPost, Path: "/api/remix-lab/prompts", Params: map[string]Param{
			"id": reqStr("条目 id（英文短名，如 finance_v3）"), "name": reqStr("显示名"), "description": str("说明"),
			"style": str("风格标签"), "stamp": str("版本标记"), "system": reqStr("系统提示词全文"), "user": str("用户消息模板"),
		}},
		{Name: "delete_prompt", Description: "删除一条提示词库条目。", Method: http.MethodDelete, Path: "/api/remix-lab/prompts/{id}", Confirm: "删除这条提示词", Params: map[string]Param{"id": pathID("条目 id")}},
		{Name: "get_active_prompt", Description: "当前全局默认使用的写手提示词。", Method: http.MethodGet, Path: "/api/remix-lab/active-prompt"},
		{Name: "set_active_prompt", Description: "设定全局默认写手提示词：传 id（用库里的条目）或直接传 system/user；clear=true 恢复内置默认。账号自己的工作流若已定制写手提示词，以账号的为准。", Method: http.MethodPut, Path: "/api/remix-lab/active-prompt", Params: map[string]Param{
			"id": str("提示词库条目 id"), "system": str("系统提示词"), "user": str("用户模板"), "clear": boolean("恢复默认"),
		}},
		{Name: "list_styles", Description: "二创风格模板（如 视频号爆款二创 v2、财经卖课…）。", Method: http.MethodGet, Path: "/api/remix-lab/styles"},
		{Name: "save_style", Description: "新增或修改自定义风格模板（内置风格不能改，可用 base_id 复制后改）。", Method: http.MethodPost, Path: "/api/remix-lab/styles", Params: map[string]Param{
			"id": reqStr("风格 id"), "base_id": str("基于哪个风格复制"), "name": reqStr("显示名"), "category": str("分类"),
			"description": str("一句话说明"), "best_for": strList("适合场景"), "planner": str("策划节点提示词"), "writer": str("写手系统提示词"),
			"writer_user": str("写手用户模板"), "reviewer": str("审稿提示词"), "rules": str("公共规则"), "finance": boolean("财经卖课模式"), "publish": str("发布规则，如 channels_v2"),
		}},
		{Name: "apply_style_to_account", Description: "把一个风格套到账号的二创工作流上（会覆盖账号当前的写手/审稿提示词）。", Method: http.MethodPut, Path: "/api/remix-lab/workflow/style", Confirm: "覆盖这个账号现有的二创提示词", Params: map[string]Param{
			"account_id": {Type: "string", Required: true, In: "query", Description: "账号 id"},
			"style_id":   reqStr("风格 id（list_styles 里的 id）"), "brief": object("同时更新的内容简报（可选）"),
		}},
		{Name: "update_account_brief", Description: "更新账号的内容简报（行业、受众、卖什么、卖点、转化方式、人设、平台、时长、禁用词）。", Method: http.MethodPut, Path: "/api/remix-lab/workflow/brief", Params: map[string]Param{
			"account_id": {Type: "string", Required: true, In: "query", Description: "账号 id"},
			"industry":   str("行业"), "audience": str("目标受众"), "offer": str("卖什么"), "selling_points": str("卖点"),
			"conversion": str("转化方式"), "persona": str("人设口吻"), "platform": str("平台"), "duration_s": integer("目标时长秒"),
			"banned": str("禁用词/禁区"), "notes": str("其他要求"),
		}},
		{Name: "get_workflow", Description: "读二创工作流（节点、连线、每个节点的模型和提示词、生产段设置）。不传 account_id 读全局。", Method: http.MethodGet, Path: "/api/remix-lab/workflow-prompts", Params: map[string]Param{"account_id": query("账号 id")}},
		{
			Name: "set_node_prompt",
			Description: "修改二创工作流里某个节点的提示词（写手 writer / 审稿 reviewer / 策划 planner 或任意节点 id），只改这一段，其余设置不动。" +
				"不传 account_id 改全局工作流。改前先 get_workflow 看当前内容。",
			Params: map[string]Param{
				"account_id":    str("账号 id；不传改全局"),
				"node":          reqStr("节点 id 或类型（writer / reviewer / planner）"),
				"system_prompt": str("新的系统提示词（不传不改）"),
				"user_template": str("新的用户消息模板，可用 {{source}}、{{node:节点ID}}（不传不改）"),
				"model":         str("改这个节点用的模型（不传不改）"),
			},
			Run: setNodePrompt,
		},
		{Name: "save_workflow", Description: "整体保存二创工作流（高级：节点、连线、生产段设置都在里面）。先 get_workflow 取回完整对象，改完整个传回。只改提示词请用 set_node_prompt。", Method: http.MethodPut, Path: "/api/remix-lab/workflow-prompts", Confirm: "整体覆盖这份工作流", Params: map[string]Param{
			"account_id": query("账号 id；不传存全局"), "workflow": reqObject("完整的 Workflow 对象"),
		}},
		{Name: "get_agent_prompts", Description: "多 agent 管线（钩子、事实核查、弹药、审稿）的系统提示词。", Method: http.MethodGet, Path: "/api/remix-lab/agent-prompts"},
		{Name: "list_published", Description: "已发布文案库及播放/点赞/成交成绩。", Method: http.MethodGet, Path: "/api/remix-lab/published"},
		{Name: "record_published_metrics", Description: "回填已发布视频的成绩。", Method: http.MethodPut, Path: "/api/remix-lab/published/{id}", Params: map[string]Param{
			"id": pathID("已发布条目 id"), "views": integer("播放"), "likes": integer("点赞"), "orders": integer("成交"), "notes": str("备注"),
		}},
	}
}

// setNodePrompt 读出工作流，只换一个节点的提示词再写回，免得 AI 手工拼整个工作流出错。
func setNodePrompt(ctx context.Context, s *Server, args map[string]any) toolResult {
	node := argString(args, "node")
	if node == "" {
		return textResult("node 必填", true)
	}
	q := url.Values{}
	if id := argString(args, "account_id"); id != "" {
		q.Set("account_id", id)
	}
	current := s.do(ctx, http.MethodGet, "/api/remix-lab/workflow-prompts", q, nil)
	if current.IsError {
		return current
	}
	raw := strings.TrimPrefix(current.Content[0]["text"].(string), "HTTP 200\n")
	var view map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		return textResult("读工作流失败："+err.Error(), true)
	}
	workflowRaw, ok := view["workflow"]
	if !ok {
		workflowRaw = []byte(raw)
	}
	var workflow map[string]any
	if err := json.Unmarshal(workflowRaw, &workflow); err != nil {
		return textResult("工作流格式无法识别："+err.Error(), true)
	}
	nodes, _ := workflow["nodes"].([]any)
	var target map[string]any
	for _, item := range nodes {
		n, _ := item.(map[string]any)
		if n == nil {
			continue
		}
		if n["id"] == node {
			target = n
			break
		}
		if target == nil && n["type"] == node {
			target = n
		}
	}
	if target == nil {
		var ids []string
		for _, item := range nodes {
			if n, _ := item.(map[string]any); n != nil {
				ids = append(ids, fmt.Sprintf("%v(%v)", n["id"], n["type"]))
			}
		}
		return textResult("找不到节点 "+node+"；现有节点："+strings.Join(ids, "、"), true)
	}
	config, _ := target["config"].(map[string]any)
	if config == nil {
		config = map[string]any{}
	}
	changed := false
	for arg, field := range map[string]string{"system_prompt": "system_prompt", "user_template": "user_template", "model": "model"} {
		if value, ok := args[arg]; ok && value != nil {
			config[field] = value
			changed = true
		}
	}
	if !changed {
		return textResult("没有要改的内容：system_prompt / user_template / model 至少传一个", true)
	}
	// 用户直接写了正文，就不再跟随提示词库条目，否则保存后又被库内容盖回去。
	if _, ok := args["system_prompt"]; ok {
		delete(config, "prompt_id")
		delete(config, "frozen_prompt_id")
	}
	target["config"] = config
	return s.do(ctx, http.MethodPut, "/api/remix-lab/workflow-prompts", q, map[string]any{"workflow": workflow})
}

func productionTools() []Tool {
	return []Tool{
		{Name: "start_production", Description: "确认二创稿、开始生产：建项目 → 口播稿 → 字幕 → 配音 → 混剪，产出剪映草稿（异步，几分钟到十几分钟；用 get_run_stages 或 get_production_status 看进度）。失败后再调一次会从断点续跑。", Method: http.MethodPost, Path: "/api/remix-lab/runs/{id}/produce", Confirm: "调用口播模型和配音（TTS 按字数计费），并新建剪映草稿", Params: map[string]Param{
			"id": pathID("run id（get_remix_experiment 的 runs[].id，必须是已完成的稿）"), "account_id": str("出片账号 id（不传沿用二创时的账号）"),
		}},
		{Name: "redo_production_step", Description: "从某一步起重做生产：spoken 口播稿（连带重配音、重出草稿）/ captions 字幕关键词 / narration 配音 / montage 混剪（只出新草稿）。", Method: http.MethodPost, Path: "/api/remix-lab/runs/{id}/produce/redo", Confirm: "重新调用模型/配音并生成新的剪映草稿", Params: map[string]Param{
			"id": pathID("run id"), "step": {Type: "string", Required: true, Enum: []string{"spoken", "captions", "narration", "montage"}, Description: "从哪一步重做"},
		}},
		{Name: "get_production_status", Description: "按项目查生产进度（status/step/error）。", Method: http.MethodGet, Path: "/api/remix-lab/productions/by-project/{project_id}", Params: map[string]Param{"project_id": pathID("项目 id")}},
		{Name: "list_tasks", Description: "后台任务（口播、配音、混剪、选题…）列表。center=1 只看任务中心里的。", Method: http.MethodGet, Path: "/api/tasks", Params: map[string]Param{
			"project_id": query("只看某项目"), "status": query("queued/running/completed/failed…"), "center": query("填 1 只看任务中心"),
		}},
		{Name: "get_task", Description: "任务详情（事件、消息、错误）。", Method: http.MethodGet, Path: "/api/tasks/{id}", Params: map[string]Param{"id": pathID("任务 id")}},
		{Name: "get_task_result", Description: "任务产物（连续稿、发布包、混剪注册信息等）。", Method: http.MethodGet, Path: "/api/tasks/{id}/result", Params: map[string]Param{"id": pathID("任务 id")}},
		{Name: "answer_task", Description: "回答任务里模型提出的问题，让它继续。", Method: http.MethodPost, Path: "/api/tasks/{id}/answer", Params: map[string]Param{
			"id": pathID("任务 id"), "answer": reqStr("回答内容"),
		}},
		{Name: "cancel_task", Description: "取消正在运行的任务。", Method: http.MethodPost, Path: "/api/tasks/{id}/cancel", Confirm: "中止这个任务", Params: map[string]Param{"id": pathID("任务 id")}},
		{Name: "dismiss_tasks", Description: "把已结束的任务从任务中心移除（记录仍保留）。", Method: http.MethodPost, Path: "/api/tasks/dismiss", Params: map[string]Param{"ids": {Type: "array", Items: "string", Required: true, Description: "任务 id 列表"}}},
		{Name: "list_jianying_drafts", Description: "剪映草稿列表。", Method: http.MethodGet, Path: "/api/jianying-drafts"},
	}
}

func projectTools() []Tool {
	return []Tool{
		{Name: "list_projects", Description: "制作项目列表（一条视频一个项目）。", Method: http.MethodGet, Path: "/api/projects", Params: map[string]Param{
			"account_id": query("只看某账号"), "stage": query("script/assets/mixing/review/published/archived"), "q": query("搜索标题"),
		}},
		{Name: "get_project", Description: "项目详情（各类资产的当前版本：连续稿、口播稿、配音、字幕、剪映草稿…）。", Method: http.MethodGet, Path: "/api/projects/{id}", Params: map[string]Param{"id": pathID("项目 id")}},
		{Name: "create_project", Description: "新建一个空的制作项目（一般不用：start_production 会自动建项目）。", Method: http.MethodPost, Path: "/api/projects", Params: map[string]Param{
			"account_id": reqStr("账号 id"), "title": reqStr("项目标题"),
		}},
		{Name: "move_project_stage", Description: "切换项目阶段。", Method: http.MethodPost, Path: "/api/projects/{id}/move", Params: map[string]Param{
			"id": pathID("项目 id"), "stage": {Type: "string", Required: true, Enum: []string{"script", "assets", "mixing", "review", "published", "archived"}, Description: "目标阶段"},
		}},
		{Name: "delete_project", Description: "删除项目及其全部文件（有任务在跑时会拒绝）。", Method: http.MethodDelete, Path: "/api/projects/{id}", Confirm: "删除项目及全部文件，不可恢复", Params: map[string]Param{"id": pathID("项目 id")}},
	}
}

func systemTools() []Tool {
	return []Tool{
		{Name: "setup_status", Description: "配置体检：模型、配音、剪映、素材库等是否就绪，哪一项没配好。", Method: http.MethodGet, Path: "/api/setup/status"},
		{Name: "get_settings", Description: "读控制台设置（公开项；密钥只显示是否已配置）。", Method: http.MethodGet, Path: "/api/settings"},
		{Name: "update_settings", Description: "修改控制台设置：public 传要改的公开项（局部合并），secrets 传要更新的密钥。改前先 get_settings。", Method: http.MethodPut, Path: "/api/settings", Confirm: "修改系统设置", Params: map[string]Param{
			"public": object("要改的公开设置项（键名见 get_settings）"), "secrets": object("要更新的密钥 {键: 值}"),
		}},
		{Name: "restart_console", Description: "重启控制台进程（重新编译程序后让新版生效用；正在跑的任务会中断，重启后续跑）。", Method: http.MethodPost, Path: "/api/system/restart", Confirm: "重启控制台，正在运行的任务会被打断", Params: map[string]Param{}},
		{Name: "test_dependency", Description: "检测一项依赖是否连得上：baokuan / baokuan-mcp / baokuan_http / codex / obsidian / media / jianying / grok。", Method: http.MethodPost, Path: "/api/settings/test/{dependency}", Params: map[string]Param{
			"dependency": {Type: "string", Required: true, In: "path", Enum: []string{"baokuan", "baokuan-mcp", "baokuan_http", "codex", "obsidian", "media", "jianying", "grok"}, Description: "依赖名"},
		}},
	}
}
