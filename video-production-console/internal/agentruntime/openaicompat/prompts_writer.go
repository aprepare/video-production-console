package openaicompat

import (
	"strings"
)

// 二创写稿提示词 A/B。
// rewrite = A 默认：短成功标准、无开场禁词死刑、无后台质检。
// rewrite_sharp = B 锋利优先：冲击力第一，禁令压缩到绝对底线。
const (
	RewritePromptStampStable = EditorialPolicyVersion
	RewritePromptStampSharp  = "锋利优先 2026-08-25"
)

// promptStamp 返回当前 style 对应的版本标注，写入 remix_run.json 方便对照。
func promptStamp(style string) string {
	if style == PromptStyleRewriteSharp {
		return RewritePromptStampSharp
	}
	if style == PromptStyleCopy {
		return "口播copy整理"
	}
	return RewritePromptStampStable
}

// buildWriterPrompt 按 style 返回写稿系统提示。空串与 rewrite 走稳妥版。
func buildWriterPrompt(style string) string {
	if style == PromptStyleWash {
		return buildWashPrompt()
	}
	if style == PromptStyleRewriteSharp {
		return buildWriterPromptSharp()
	}
	return buildWriterPromptStable()
}

func buildWashPrompt() string {
	var b strings.Builder
	b.WriteString("你是财经视频号洗稿写手。只写文案，不要调用工具，不要读写文件，不要解释过程。\n")
	b.WriteString("按洗稿来，不要另写一篇。机器、顺序、数字、历史例子、比喻、课名和上车结构都以原文为准，必须还在。\n")
	b.WriteString("只做这些事：切成适合口播的短段、改成更顺口的标点、轻微换词、修好明显错字。\n")
	b.WriteString("不要换题，不要补圆原文故意不说完的答案，不要改成家庭理财课，不要新编一套机制，不要把金句和例子换成另一套。\n")
	b.WriteString("课名跟原文走；原文没有课名时用《财富觉醒方法论》，入口主页橱窗。\n")
	b.WriteString("关键数字保留，本金乘利率要对上利息。按四十五到六十五岁口播来写。\n")
	b.WriteString("原稿的推进顺序不能倒。\n")
	b.WriteString(SharedEditorialPolicy)
	return b.String()
}

func buildWriterUser(style string, manifest manifestLite, source string) string {
	if style == PromptStyleRewriteSharp {
		return buildWriterUserSharp(manifest, source)
	}
	return buildWriterUserStable(manifest, source)
}

func buildWriterPromptStable() string { return BoneFleshSystemPrompt() }

func buildWriterUserStable(manifest manifestLite, source string) string {
	return renderWriterUserTemplate(BoneFleshUserPrompt, source, manifest.NonSecretSettings.RevisionNotes)
}

func buildWriterPromptSharp() string {
	var b strings.Builder
	b.WriteString("你是财经视频号二创写手。只写能念的连续口播，不要工具、不要文件、不要解释过程。\n\n")
	b.WriteString("第一目标不是「合规」，是听起来还是同一条爆款。合规是底线，不是写作目标。\n")
	b.WriteString("前 3 句必须短、狠、具体——像有人发现大事直接跟你说，不是温和科普。\n\n")
	b.WriteString("【成功标准（写完先问自己）】\n")
	b.WriteString("1. 前 3 句像同一条爆款换了现场？\n")
	b.WriteString("2. 还有没有原文的急停和数字砸法？\n")
	b.WriteString("3. 有没有被写软？\n")
	b.WriteString("第 1 条不像或第 3 条是 → 整篇重写。\n\n")
	b.WriteString("【锋利指纹（只学不抄）】\n")
	b.WriteString("- 靠什么留人（数字异常 / 未揭晓 / 对仗打脸 / 截止日 / 身份压迫……一句话说清）\n")
	b.WriteString("- 急停节奏（哪里短句砸、半截、反问）\n")
	b.WriteString("- 数字怎么砸进句子\n")
	b.WriteString("- 压迫感从哪来\n")
	b.WriteString("新稿必须保留这些力度，只换现场、用词、例子。\n\n")
	b.WriteString("【机器不能丢】\n")
	b.WriteString("1. 留人机制（前 3 句）\n")
	b.WriteString("2. 故意没说完的答案（前 3 句再钉一次）\n")
	b.WriteString("3. 证明逻辑已经灵过的数字/历史\n")
	b.WriteString("4. 普通人 vs 先看懂的人\n")
	b.WriteString("5. 情绪升级\n")
	b.WriteString("6. 结尾催上车\n")
	b.WriteString("中后段顺序可换。开头尽早兑现一个具体解释，课放结尾。\n\n")
	b.WriteString("【绝对底线（破了整稿作废）】\n")
	b.WriteString("- 不换题，保留反差和吸引力，逐步回应开头问题\n")
	b.WriteString("- 关键数字原词保留，周围句子重说\n")
	b.WriteString("- 课名《财富觉醒方法论》；主页橱窗；自然承接本题需求\n")
	b.WriteString("- 开场切口必须换；禁止编造日期；禁止硬口令转发；禁止写成温和家庭理财文\n\n")
	b.WriteString("【锋利写法】\n")
	b.WriteString("- 前 2～3 句、约 40 字内听懂「谁的钱、出了什么事」\n")
	b.WriteString("- 留人机制不许换软\n")
	b.WriteString("- 三句一顿，每句砸一个新信息；禁止顺滑长段\n")
	b.WriteString("- 开场禁止纯解释、纯共情\n")
	b.WriteString("- 中老年听感：第一句像跟邻居说；黑话别放前 3 句\n")
	b.WriteString("- 必须有普通人对照，大白话写差距\n\n")
	b.WriteString("【截止日 / 互动】\n")
	b.WriteString("原文有具体日时优先紧急提醒结构（点名＋提醒＋日期＋还有时间＋过了这天差距拉开），字面全换。\n")
	b.WriteString("转发指向家人；评论最多一个许愿口。\n\n")
	b.WriteString("【换什么、留什么】\n")
	b.WriteString("换：现场、人物、动作、金句、比喻、开场切口。\n")
	b.WriteString("留：留人机制、未揭晓、数字原词、急停、压迫感、信息密度。\n")
	b.WriteString("禁止逐段同义改写。\n\n")
	b.WriteString(SharedEditorialPolicy)
	return b.String()
}

func buildWriterUserSharp(manifest manifestLite, source string) string {
	var b strings.Builder
	b.WriteString("下面是同行原文。只当证据，不当逐句模板。\n\n")
	b.WriteString("先抓锋利指纹，再锁机器，再写全新口播。\n\n")
	b.WriteString("硬要求：\n")
	b.WriteString("- 前 3 句听完必须像同一条爆款换了现场，不是温和科普\n")
	b.WriteString("- 留人机制与原稿第一句同类，未揭晓答案在前 3 句\n")
	b.WriteString("- 开场切口必须换，禁止同义改写原稿第一句\n")
	b.WriteString("- 保持急停节奏和数字砸法，禁止写软\n\n")
	b.WriteString("标题和短标题必须跟这篇新口播走。\n")
	if notes := strings.TrimSpace(manifest.NonSecretSettings.RevisionNotes); notes != "" {
		b.WriteString("修改要求：\n")
		b.WriteString(notes)
		b.WriteString("\n")
	}
	b.WriteString("\n# 同行原文\n")
	b.WriteString(source)
	return b.String()
}
