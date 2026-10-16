package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

func str(desc string) Param    { return Param{Type: "string", Description: desc} }
func reqStr(desc string) Param { return Param{Type: "string", Description: desc, Required: true} }
func pathID(desc string) Param {
	return Param{Type: "string", Description: desc, Required: true, In: "path"}
}
func query(desc string) Param     { return Param{Type: "string", Description: desc, In: "query"} }
func integer(desc string) Param   { return Param{Type: "integer", Description: desc} }
func boolean(desc string) Param   { return Param{Type: "boolean", Description: desc} }
func strList(desc string) Param   { return Param{Type: "array", Items: "string", Description: desc} }
func object(desc string) Param    { return Param{Type: "object", Description: desc} }
func reqObject(desc string) Param { return Param{Type: "object", Description: desc, Required: true} }

const modelBrief = "模型设置对象 {model, reasoning_effort, service_tier}"

// Catalog 是逐个包装好的业务工具；其余接口用 console_api。
func Catalog() []Tool {
	var tools []Tool
	tools = append(tools, accountTools()...)
	tools = append(tools, merchantTools()...)
	tools = append(tools, referenceTools()...)
	tools = append(tools, replicateTools()...)
	tools = append(tools, remixTools()...)
	tools = append(tools, productionTools()...)
	tools = append(tools, projectTools()...)
	tools = append(tools, imageStudioTools()...)
	tools = append(tools, systemTools()...)
	return tools
}

func replicateTools() []Tool {
	return []Tool{
		{
			Name:        "create_replicate_project",
			Description: "从已完成的对标拆解创建 AI 复刻项目；也可用 blank=true 和 title 新建空白项目。",
			Method:      http.MethodPost,
			Path:        "/api/replicate",
			Params: map[string]Param{
				"reference_id":   str("对标素材 id"),
				"analysis_id":    str("已完成且含复刻提示词的拆解 id"),
				"product":        str("用户自己的产品或场景描述"),
				"ratio":          {Type: "string", Description: "画面比例", Enum: []string{"9:16", "16:9", "1:1", "3:4", "4:3"}},
				"video_provider": {Type: "string", Description: "视频模型供应商", Enum: []string{"runninghub_h3", "grok"}},
				"image_model":    str("首帧图片模型；空值使用系统默认模型"),
				"variants":       integer("每个镜头默认生成版本数，1–4"),
				"blank":          boolean("新建空白项目"),
				"title":          str("空白项目标题"),
			},
		},
		{
			Name:        "generate_replicate_shots",
			Description: "为复刻项目的指定镜头并行生成视频版本；shot_ids 为空时生成全部已有首帧的镜头。",
			Method:      http.MethodPost,
			Path:        "/api/replicate/{id}/generate",
			Confirm:     "会调用视频模型（花钱）",
			Params: map[string]Param{
				"id":       pathID("复刻项目 id"),
				"shot_ids": strList("要生成的镜头 id；空数组表示全部"),
				"variants": integer("每个镜头生成版本数，0 使用项目默认值"),
				"provider": {Type: "string", Description: "本次使用的视频模型；空值使用项目默认值", Enum: []string{"runninghub_h3", "grok"}},
			},
		},
		{
			Name:        "stitch_replicate",
			Description: "按镜头顺序异步拼接每个镜头已选中的视频版本。",
			Method:      http.MethodPost,
			Path:        "/api/replicate/{id}/stitch",
			Params: map[string]Param{
				"id":    pathID("复刻项目 id"),
				"audio": {Type: "string", Description: "音频策略；本期只支持 none", Enum: []string{"none"}},
			},
		},
		{
			Name:        "get_replicate_project",
			Description: "读取 AI 复刻项目详情、镜头、生成版本和整体进度。",
			Method:      http.MethodGet,
			Path:        "/api/replicate/{id}",
			Params:      map[string]Param{"id": pathID("复刻项目 id")},
		},
	}
}

func imageStudioTools() []Tool {
	return []Tool{
		{
			Name:        "generate_image",
			Description: "创建单张出图任务，可生成 1–4 张图片。",
			Method:      http.MethodPost,
			Path:        "/api/image-studio/generations",
			Confirm:     "会调用生图服务（花钱）",
			Params: map[string]Param{
				"prompt":       reqStr("用户原始提示词，1–4000 字"),
				"final_prompt": str("已经扩写或编辑好的最终提示词；传入后跳过扩写"),
				"enhance":      boolean("是否由文本模型扩写提示词"),
				"style":        {Type: "string", Description: "风格键；空表示不套风格，custom 表示自定义"},
				"custom_style": str("style=custom 时的自定义风格，最多 500 字"),
				"model":        str("图片模型；空值使用系统默认模型"),
				"ratio":        {Type: "string", Required: true, Description: "图片比例", Enum: []string{"3:4", "4:3", "9:16", "1:1"}},
				"count":        {Type: "integer", Required: true, Description: "生成张数，1–4"},
				"text_model":   str("扩写提示词使用的文本模型；空值使用系统默认模型"),
			},
		},
		{
			Name:        "list_image_generations",
			Description: "查询单张出图历史。",
			Method:      http.MethodGet,
			Path:        "/api/image-studio/generations",
			Params: map[string]Param{
				"limit":    {Type: "integer", Description: "返回条数，默认 30", In: "query"},
				"before":   query("只返回此创建时间之前的记录（RFC3339）"),
				"favorite": {Type: "integer", Description: "收藏筛选：1 只看收藏，0 只看未收藏", In: "query"},
				"q":        query("按原始提示词搜索"),
			},
		},
	}
}

func accountTools() []Tool {
	return []Tool{
		{Name: "list_accounts", Description: "列出全部视频号账号（id、名称、状态、覆盖设置）。其他工具里的 account_id 从这里拿。", Method: http.MethodGet, Path: "/api/accounts"},
		{
			Name:        "create_account",
			Description: "新增视频号账号。必须给一张本机背景图（PNG/JPEG/WebP，混剪背景板用）。",
			Params: map[string]Param{
				"name":            reqStr("账号名称，例如「认知漫步」"),
				"background_path": reqStr("本机背景图的绝对路径"),
			},
			Run: func(ctx context.Context, s *Server, args map[string]any) toolResult {
				name, path := argString(args, "name"), argString(args, "background_path")
				if name == "" || path == "" {
					return textResult("name 和 background_path 都必填", true)
				}
				return s.doMultipart(ctx, "/api/accounts", map[string]string{"name": name}, "background", path)
			},
		},
		{Name: "rename_account", Description: "账号改名。", Method: http.MethodPatch, Path: "/api/accounts/{id}", Params: map[string]Param{
			"id": pathID("账号 id"), "name": reqStr("新名称"),
		}},
		{
			Name:        "replace_account_background",
			Description: "更换账号的混剪背景图。",
			Params: map[string]Param{
				"id":              reqStr("账号 id"),
				"background_path": reqStr("本机背景图的绝对路径"),
			},
			Run: func(ctx context.Context, s *Server, args map[string]any) toolResult {
				id, path := argString(args, "id"), argString(args, "background_path")
				if id == "" || path == "" {
					return textResult("id 和 background_path 都必填", true)
				}
				return s.doMultipart(ctx, "/api/accounts/"+id+"/background", nil, "background", path)
			},
		},
		{Name: "set_account_overrides", Description: "设置账号级的音色和混剪样式覆盖（domain.AccountOverrides：voice{aurastd_std_voice_id, volc_speech_speaker_id} 及混剪样式字段）。传空对象 {} 清除覆盖。先用 list_accounts 看现有 overrides 再改。", Method: http.MethodPut, Path: "/api/accounts/{id}/overrides", BodyArg: "overrides", Params: map[string]Param{
			"id": pathID("账号 id"), "overrides": reqObject("完整的覆盖对象（整体替换）"),
		}},
		{Name: "deactivate_account", Description: "停用账号（软删除，历史数据保留）。", Method: http.MethodDelete, Path: "/api/accounts/{id}", Confirm: "停用这个账号", Params: map[string]Param{
			"id": pathID("账号 id"),
		}},
	}
}

func merchantTools() []Tool {
	merchantFields := map[string]Param{
		"name": str("商户名"), "category": str("品类，如茶叶/海鲜/餐饮"), "contact": str("联系人"), "phone": str("电话"),
		"address": str("地址"), "selling_points": str("真实、可核验的卖点"), "notes": str("备注"),
		"account_id": str("关联账号 id（可选）"),
		"status":     {Type: "string", Description: "状态", Enum: []string{"prospect", "trial", "active", "paused", "churned"}},
	}
	create := cloneParams(merchantFields)
	create["name"] = reqStr("商户名")
	update := cloneParams(merchantFields)
	update["id"] = pathID("商户 id")
	orderFields := map[string]Param{
		"merchant_id": str("商户 id"), "title": str("订单名"), "package": {Type: "string", Description: "套餐：trial 体验包 / monthly 月费 / premium 精品定制 / custom 其他", Enum: []string{"trial", "monthly", "premium", "custom"}},
		"price_cents": integer("合同额（分）"), "paid_cents": integer("已收（分）"), "video_quota": integer("约定条数"),
		"max_duration_s": integer("单条最长秒数"), "revision_rounds": integer("修改轮次"), "due_date": str("交付日 YYYY-MM-DD"),
		"scope_notes": str("范围说明"), "renewal_of": str("续费自哪个订单 id"),
		"status": {Type: "string", Description: "订单状态", Enum: []string{"draft", "signed", "producing", "delivered", "closed", "cancelled"}},
	}
	orderCreate := cloneParams(orderFields)
	orderCreate["merchant_id"] = reqStr("商户 id")
	orderCreate["title"] = reqStr("订单名")
	orderUpdate := cloneParams(orderFields)
	delete(orderUpdate, "merchant_id")
	orderUpdate["id"] = pathID("订单 id")
	return []Tool{
		{Name: "business_overview", Description: "经营总览：合作商户、合同额、已收、成本、贡献、续费。", Method: http.MethodGet, Path: "/api/business/overview"},
		{Name: "list_merchants", Description: "商户列表。", Method: http.MethodGet, Path: "/api/merchants", Params: map[string]Param{
			"status": query("按状态过滤"), "q": query("按名称/品类/联系人搜索"),
		}},
		{Name: "get_merchant", Description: "商户详情。", Method: http.MethodGet, Path: "/api/merchants/{id}", Params: map[string]Param{"id": pathID("商户 id")}},
		{Name: "create_merchant", Description: "新增商户。卖点要真实可核验，不要编造。", Method: http.MethodPost, Path: "/api/merchants", Params: create},
		{Name: "update_merchant", Description: "修改商户信息（只传要改的字段）。", Method: http.MethodPatch, Path: "/api/merchants/{id}", Params: update},
		{Name: "delete_merchant", Description: "删除商户，连带删除该商户的全部授权素材文件。", Method: http.MethodDelete, Path: "/api/merchants/{id}", Confirm: "删除商户及其全部素材文件，不可恢复", Params: map[string]Param{"id": pathID("商户 id")}},
		{Name: "list_merchant_materials", Description: "商户的授权素材列表。", Method: http.MethodGet, Path: "/api/merchants/{id}/materials", Params: map[string]Param{"id": pathID("商户 id")}},
		{
			Name:        "upload_merchant_material",
			Description: "给商户上传一个授权素材（本机文件，最大 2GB），并写明来源和授权范围。",
			Params: map[string]Param{
				"merchant_id": reqStr("商户 id"), "file_path": reqStr("本机文件绝对路径"),
				"kind": {Type: "string", Description: "素材类型", Enum: []string{"video", "image", "audio", "document", "other"}}, "description": str("内容说明"),
				"license_source": {Type: "string", Description: "授权来源：merchant_shot 商户自拍 / team_shot 团队拍摄 / licensed_stock 授权图库 / ai_generated AI 生成 / unknown", Enum: []string{"merchant_shot", "team_shot", "licensed_stock", "ai_generated", "unknown"}}, "license_scope": str("授权范围"),
				"license_expires_at": str("授权到期 YYYY-MM-DD"), "license_proof": str("授权凭证说明"),
			},
			Run: func(ctx context.Context, s *Server, args map[string]any) toolResult {
				id, path := argString(args, "merchant_id"), argString(args, "file_path")
				if id == "" || path == "" {
					return textResult("merchant_id 和 file_path 都必填", true)
				}
				fields := map[string]string{}
				for _, key := range []string{"kind", "description", "license_source", "license_scope", "license_expires_at", "license_proof"} {
					fields[key] = argString(args, key)
				}
				return s.doMultipart(ctx, "/api/merchants/"+id+"/materials", fields, "file", path)
			},
		},
		{Name: "delete_material", Description: "删除一个商户素材及文件。", Method: http.MethodDelete, Path: "/api/materials/{id}", Confirm: "删除这个素材文件，不可恢复", Params: map[string]Param{"id": pathID("素材 id")}},
		{Name: "list_orders", Description: "订单列表。", Method: http.MethodGet, Path: "/api/orders", Params: map[string]Param{
			"merchant_id": query("只看这个商户"), "status": query("按状态过滤"),
		}},
		{Name: "get_order", Description: "订单详情（含成本、关联项目、贡献汇总）。", Method: http.MethodGet, Path: "/api/orders/{id}", Params: map[string]Param{"id": pathID("订单 id")}},
		{Name: "create_order", Description: "新建订单。金额单位是分（800 元 = 80000）。", Method: http.MethodPost, Path: "/api/orders", Params: orderCreate},
		{Name: "update_order", Description: "修改订单（只传要改的字段）。", Method: http.MethodPatch, Path: "/api/orders/{id}", Params: orderUpdate},
		{Name: "delete_order", Description: "删除订单。", Method: http.MethodDelete, Path: "/api/orders/{id}", Confirm: "删除这个订单", Params: map[string]Param{"id": pathID("订单 id")}},
		{Name: "record_order_cost", Description: "给订单记一笔成本（人工工时、接口费、素材费、返工）。", Method: http.MethodPost, Path: "/api/orders/{id}/costs", Params: map[string]Param{
			"id": pathID("订单 id"), "kind": {Type: "string", Required: true, Description: "成本类型：labor 人工 / api 接口 / material 素材 / revision 返工 / other", Enum: []string{"labor", "api", "material", "revision", "other"}},
			"minutes": integer("人工分钟数"), "amount_cents": integer("金额（分）"), "project_id": str("关联项目 id"),
			"note": str("说明"), "occurred_at": str("发生时间 RFC3339，不填为现在"),
		}},
		{Name: "assign_project_to_order", Description: "把一个制作项目挂到订单下。", Method: http.MethodPost, Path: "/api/orders/{id}/projects", Params: map[string]Param{
			"id": pathID("订单 id"), "project_id": reqStr("项目 id"),
		}},
		{Name: "mark_project_delivered", Description: "把订单下的某个项目标记为已交付。", Method: http.MethodPost, Path: "/api/orders/{id}/projects/{project_id}/deliver", Params: map[string]Param{
			"id": pathID("订单 id"), "project_id": pathID("项目 id"),
		}},
	}
}

func referenceTools() []Tool {
	return []Tool{
		{Name: "list_references", Description: "对标素材库列表（采集过的抖音/视频号/小红书作品）。", Method: http.MethodGet, Path: "/api/references", Params: map[string]Param{
			"q": query("搜索标题、作者、口播"), "platform": query("douyin / channels / xhs"), "merchant_id": query("只看某商户"), "status": query("状态"), "limit": query("条数"),
		}},
		{Name: "get_reference", Description: "对标详情，transcript 字段是口播全文。", Method: http.MethodGet, Path: "/api/references/{id}", Params: map[string]Param{"id": pathID("对标 id")}},
		{Name: "collect_reference", Description: "贴抖音（v.douyin.com 短链）或视频号（weixin.qq.com/sph）分享链接采集对标，原视频自动存到本机。transcribe=true 同时做口播转写（通义听悟，按时长计费）。", Method: http.MethodPost, Path: "/api/references", Params: map[string]Param{
			"link": reqStr("分享链接或整段分享文字"), "transcribe": boolean("同时转写口播（会产生转写费用）"),
			"keyword": str("采集时的关键词备注"), "merchant_id": str("归属商户 id（可选）"),
		}},
		{Name: "search_references", Description: "按关键词在抖音或小红书搜视频（只搜索，不入库）。", Method: http.MethodGet, Path: "/api/references/search", Params: map[string]Param{
			"platform": {Type: "string", Required: true, In: "query", Enum: []string{"douyin", "xhs"}, Description: "平台"},
			"keyword":  {Type: "string", Required: true, In: "query", Description: "关键词 1–50 字"},
			"cursor":   query("翻页游标"), "search_id": query("翻页用"), "session_id": query("翻页用"),
		}},
		{Name: "transcribe_reference", Description: "对已采集的对标做口播转写（异步，约 20–60 秒；用 get_reference 看 transcript）。", Method: http.MethodPost, Path: "/api/references/{id}/transcribe", Confirm: "调用通义听悟转写，按视频时长计费", Params: map[string]Param{"id": pathID("对标 id")}},
		{Name: "update_reference", Description: "修改对标的口播稿、备注或所属商户。", Method: http.MethodPatch, Path: "/api/references/{id}", Params: map[string]Param{
			"id": pathID("对标 id"), "transcript": str("口播全文"), "notes": str("备注"), "merchant_id": str("商户 id"),
		}},
		{Name: "delete_reference", Description: "删除一条对标。", Method: http.MethodDelete, Path: "/api/references/{id}", Confirm: "删除这条对标", Params: map[string]Param{"id": pathID("对标 id")}},
		{Name: "analyze_reference", Description: "对对标视频做拆解分析（抽关键帧给视觉模型，异步），并生成逐镜头首帧、运动和文生视频复刻提示词。要求原视频已缓存。", Method: http.MethodPost, Path: "/api/reference-analyses", Confirm: "调用视觉/拆解模型（产生模型费用）", Params: map[string]Param{
			"reference_id": reqStr("对标 id"), "model": str("模型（可选）"), "focus": str("重点关注什么（可选）"), "product": str("要替换进复刻提示词的用户产品或场景描述，最多 300 字（可选）"),
		}},
		{Name: "list_reference_analyses", Description: "某条对标的拆解分析列表与结果。", Method: http.MethodGet, Path: "/api/reference-analyses", Params: map[string]Param{"reference_id": query("对标 id")}},
		{Name: "check_video_quality", Description: "审核已下载的对标视频，检查抽样帧中的手指、肢体、面部、商品、文字、穿模、水印和清晰度（异步）。image_video_job 暂不支持直接审核，请先导出视频并通过上传接口提交。", Method: http.MethodPost, Path: "/api/video-qc", Confirm: "会调用看图模型（花钱）", Params: map[string]Param{
			"source":    {Type: "string", Required: true, Description: "视频来源", Enum: []string{"reference", "image_video_job"}},
			"source_id": reqStr("来源记录 id（对标 id 或图生视频任务 id）"),
			"product":   str("商品外观和包装描述，供模型核对一致性（可选）"),
			"model":     str("看图模型（可选，空值使用系统配置）"),
		}},
	}
}

func argString(args map[string]any, key string) string {
	value, ok := args[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func cloneParams(in map[string]Param) map[string]Param {
	out := make(map[string]Param, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
