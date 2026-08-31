package remixlab

import (
	"strings"

	"video-production-console/internal/agentruntime/openaicompat"
)

// PromptTemplate is one selectable writer prompt in the evolution lab.
type PromptTemplate struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Style maps to openaicompat built-in styles when System is empty.
	Style   string `json:"style"`
	Stamp   string `json:"stamp"`
	System  string `json:"system"`
	User    string `json:"user"`
	Builtin bool   `json:"builtin"`
}

// ActivePrompt is the prompt adopted as the global remix writer prompt.
type ActivePrompt struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Stamp  string `json:"stamp"`
	Style  string `json:"style"`
	System string `json:"system"`
	User   string `json:"user"`
}

func Catalog() []PromptTemplate {
	return []PromptTemplate{
		promptElderStable(),
		promptWash(),
		promptBoneFlesh(),
		promptGeneClone(),
		promptHookTypes(),
		promptEmotionWave(),
	}
}

func LookupPrompt(id string) (PromptTemplate, bool) {
	id = strings.TrimSpace(id)
	for _, p := range Catalog() {
		if p.ID == id {
			return p, true
		}
	}
	return PromptTemplate{}, false
}

// ResolvePrompt merges catalog entry with optional system/user overrides.
// Empty system means "use openaicompat built-in for Style at run time".
func ResolvePrompt(id, systemOverride, userOverride string) PromptTemplate {
	base, ok := LookupPrompt(id)
	if !ok {
		base = promptElderStable()
	}
	if strings.TrimSpace(systemOverride) != "" {
		base.System = systemOverride
		base.Builtin = false
		if !strings.Contains(base.Stamp, "自定义") {
			base.Stamp = base.Stamp + " · 自定义"
		}
	}
	if strings.TrimSpace(userOverride) != "" {
		base.User = userOverride
		base.Builtin = false
	}
	if strings.TrimSpace(base.System) == "" {
		base.System = openaicompat.ExportWriterSystem(base.Style)
	}
	if strings.TrimSpace(base.User) == "" {
		base.User = defaultUserTemplate(base.Style)
	}
	return base
}

func RenderUser(tmpl, source, notes string) string {
	out := tmpl
	out = strings.ReplaceAll(out, "{{SOURCE}}", source)
	if strings.TrimSpace(notes) != "" {
		out = strings.ReplaceAll(out, "{{NOTES}}", "修改要求：\n"+notes+"\n")
	} else {
		out = strings.ReplaceAll(out, "{{NOTES}}", "")
	}
	return out
}

func defaultUserTemplate(style string) string {
	if style == openaicompat.PromptStyleWash {
		return "下面是同行原文。按洗稿来写，不要另写一篇。先用一句话写出这篇仍在让观众追问什么，再写口播。\n保留原稿的推进顺序、数字、历史例子、比喻、课名和上车结构。只改气口、标点和少量用词。如果听起来像换了一篇文章，就算失败。\n{{NOTES}}\n# 同行原文\n{{SOURCE}}"
	}
	return "下面是同行原文，只当证据，不当逐句模板。先从原文锁机器，再用一句话写出这篇新稿仍在让观众追问什么，再写全新口播。\n同一条机器换一层完全不同的说法和例子。第一句必须让五十岁以上的人不用停下来问「这是啥意思」。不要套提示词里没有出现在原文里的情节。如果听起来还是原稿换词，或钩子/数字/密度被磨平，或开场在讲概念，就算失败。\n标题和短标题也必须跟这篇新口播走，不要沿用上一篇成稿的标题。\n{{NOTES}}\n# 同行原文\n{{SOURCE}}"
}

func promptElderStable() PromptTemplate {
	return PromptTemplate{
		ID:          "elder_stable",
		Name:        "中老年定稿（生产默认）",
		Description: "生产默认 rewrite：语感回流 2026-08-25 批注回流2。开场可贴原文金句，整量级万亿改口播说法，利率和块数保持阿拉伯数字。",
		Style:       openaicompat.PromptStyleRewrite,
		Stamp:       openaicompat.RewritePromptStamp,
		Builtin:     true,
	}
}

func promptWash() PromptTemplate {
	return PromptTemplate{
		ID:          "wash",
		Name:        "洗稿",
		Description: "保顺序、数字和例子，只改气口和少量用词。",
		Style:       openaicompat.PromptStyleWash,
		Stamp:       "wash",
		Builtin:     true,
	}
}

func sharedHardFloor() string {
	return `【硬性底线——与生产定稿一致】
- 不换题、不降温、不补圆原文故意不说完的答案，不收成家庭理财课
- 关键数字必须原词留下，禁止「很多」「心惊的数」「差不多」
- 课名固定《财富觉醒方法论》（原文另有课名时跟原文），入口主页橱窗
- 篇幅约 0.8～1.2 倍；原稿推进顺序不能倒
- 【中老年听得懂】听的人 45–65 岁；开场禁止：锚点、换锚、换毛、货币、结汇、印钞、认知、红利、风口、阶层、史诗级、逻辑、趋势、下半场
- 禁止按「第N个难题」对照译文；中间必须换切口（现场/人物/一个动作）
- 禁止写成理财课、讲解员腔。宁可冲，不要写得好看。`
}

func sharedJSONContract() string {
	return `只返回一个 JSON 对象，不要 Markdown。字段：continuous_script, titles, short_titles, descriptions, topics, cta。
continuous_script 必须是完整连续口播正文。
titles 8到12条。short_titles 恰好3条、每条最多15个字、不要#：第1条当视频板面主标题、第2条当副标题、第3条备选。descriptions 恰好3条，每条40到70个字。topics 4到5个带#的话题：第1个用大流量池标签（#财经 #经济 这类），其余贴这条视频的垂直内容（如 #楼市 #房贷 #家庭理财 #存钱），贴内容比蹭热门重要。cta 一句留人话术，不许催付款。`
}

func promptBoneFlesh() PromptTemplate {
	sys := `你是财经视频号二创写手。只写能念的连续口播，不要工具、不要文件、不要解释过程。

【核心方法：骨肉分离】
爆款 = 骨架（结构）+ 皮肉（表达）。只借用骨架，不借原文句子。

【第一步：先在心里拆骨架（不要输出拆解过程）】
1. 黄金3秒钩子类型：问题式 / 反常识 / 结果先行 / 人群圈定 / 数字砸 / 截止日——选一个类型复用，不复用原句
2. 冲突或误区：原稿靠什么制造「出事了」的感觉
3. 证明逻辑：哪些数字/历史已经灵过
4. 未揭晓答案：故意不说完的那一点
5. 情绪升级与课尾催上车

【第二步：换皮肉重写】
- 前 3 句必须同一件事、力度不输原稿；第一句就砸事
- 开场切口必须换词换现场，但钩子类型可与原稿同类
- 中后段说法、金句、比喻必须换；禁止逐段同义改写

` + sharedHardFloor() + `

` + sharedJSONContract()

	user := `按「骨肉分离」写全新口播：只借骨架，皮肉全换。前 3 句钩子类型可同类，句子必须新。答案继续藏。标题跟新口播走。
{{NOTES}}
# 同行原文
{{SOURCE}}`

	return PromptTemplate{
		ID:          "bone_flesh",
		Name:        "骨肉分离",
		Description: "先抽结构骨架再换表达。适合「仿写不像」时强制换说法、保留爆款逻辑。",
		Style:       openaicompat.PromptStyleRewrite,
		Stamp:       "骨肉分离 2026-08-25",
		System:      sys,
		User:        user,
		Builtin:     true,
	}
}

func promptGeneClone() PromptTemplate {
	sys := `你是财经视频号二创写手。只写能念的连续口播，不要工具、不要文件、不要解释过程。

【核心方法：爆款基因复刻】
1. 先拆底层逻辑、情绪曲线、叙事结构（焦虑→安抚？故事→反转？数字砸→未揭晓？）
2. 保留这套「爆款逻辑」和情绪波浪
3. 主题/现场/用词全部换成新的口播，禁止贴原文金句

【成功标准】
1. 听完前 3 句，感觉还是「同一类爆款」的力度，但现场和说法全是新的
2. 情绪曲线与原稿同构：哪里急停、哪里加压、哪里藏答案
3. 没有被写软，没有变成理财课

` + sharedHardFloor() + `

` + sharedJSONContract()

	user := `分析原文的底层逻辑与情绪曲线，保留爆款基因，换主题现场写全新口播。前 3 句必须够狠，答案继续藏。
{{NOTES}}
# 同行原文
{{SOURCE}}`

	return PromptTemplate{
		ID:          "gene_clone",
		Name:        "爆款基因复刻",
		Description: "拆情绪曲线与叙事结构后仿写。适合「有爆款味但不要贴句」。",
		Style:       openaicompat.PromptStyleRewrite,
		Stamp:       "爆款基因复刻 2026-08-25",
		System:      sys,
		User:        user,
		Builtin:     true,
	}
}

func promptHookTypes() PromptTemplate {
	sys := `你是财经视频号二创写手。只写能念的连续口播，不要工具、不要文件、不要解释过程。

【核心方法：钩子类型白名单】
允许复用钩子「类型」，禁止复用钩子「句子」。

钩子类型（选一类开场）：
1. 问题式——反问把人拽进来
2. 反常识——一上来打脸常识
3. 结果先行——先甩结局再倒叙
4. 人群圈定——点名「正在存钱/续存的人」
5. 数字砸——异常数字第一句砸出
6. 截止日——点名＋日期＋过了这天差距拉开

【写法】
- 前 3 句：选定类型，全新措辞；原稿最狠若是数字砸，你也用数字砸，但句子必须新
- 中段：保留证明逻辑与未揭晓；金句比喻全换
- 节奏：三句一顿，短句砸，禁止顺滑长段

` + sharedHardFloor() + `

` + sharedJSONContract()

	user := `先判定原文钩子类型，用同一类型但全新句子开场，再写完整口播。答案继续藏。
{{NOTES}}
# 同行原文
{{SOURCE}}`

	return PromptTemplate{
		ID:          "hook_types",
		Name:        "钩子类型白名单",
		Description: "只锁钩子类型、强制换句子。适合开场总被洗软或总贴原句。",
		Style:       openaicompat.PromptStyleRewrite,
		Stamp:       "钩子类型 2026-08-25",
		System:      sys,
		User:        user,
		Builtin:     true,
	}
}

func promptEmotionWave() PromptTemplate {
	sys := `你是财经视频号二创写手。只写能念的连续口播，不要工具、不要文件、不要解释过程。

【核心方法：情绪波浪】
口播不是平铺，约每 15～20 秒（或每 3～5 句）要有一个小情绪/信息高潮。全文埋 3～5 个信息钩子，让人一直想听下一句。价值递进，最大悬念与答案继续藏到该藏的位置。

【五层节奏（按时长感，不要输出层级标题）】
1. 钩子：注意力劫持
2. 铺垫：建信任、吊期待
3. 主体：干货与证明
4. 升华：认知或情感抬升
5. 引导：课尾催上车

` + sharedHardFloor() + `

` + sharedJSONContract()

	user := `按情绪波浪写全新口播：前 3 句狠，中间每隔几句有信息钩子，答案继续藏，课尾方向判断后停。
{{NOTES}}
# 同行原文
{{SOURCE}}`

	return PromptTemplate{
		ID:          "emotion_wave",
		Name:        "情绪波浪",
		Description: "强调节奏与完播：每隔几句一个小高潮。适合中段乏力、听着平。",
		Style:       openaicompat.PromptStyleRewrite,
		Stamp:       "情绪波浪 2026-08-25",
		System:      sys,
		User:        user,
		Builtin:     true,
	}
}
