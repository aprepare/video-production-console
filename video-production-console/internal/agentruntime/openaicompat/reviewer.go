package openaicompat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// 审稿agent：写手成稿通过机械自检（10字连抄+篇幅）后，把原文、成稿和规范
// 清单交给审稿模型终审——只修违规处，不重写、不动写手的口气。首轮在 Run
// 里自动执行；操作员在创作台批注后的「打回重做」调用 ReviewRemixDraft 再走
// 一轮。审稿任何一步失败都不拦交付：保留进审稿原样交付，review.json 记录
// skipped/error，让操作员在界面上看到审稿没生效的原因。

const reviewerSystemPrompt = `你是财经口播文案的终审编辑。写手已经交稿，你只干一件事：按规范清单逐条检查这份稿子，只修有问题的地方。不重写、不润色、不改写手的口气和结构，没毛病的句子一个字都不许动。

【规范清单】
1. 开头（前三句）：必须直接砸钩子——二选一逼问、数字砸脸、宣布大事这类；不许自我介绍、不许讲概念铺垫。第一句就要让刷手机的人停下手指。
2. 数字口语化：小数最多留一位（"户均3.02人"要说成"一户平均三口人"这类）；连着报数字要给喘气和参照物。政策数字、利率、金额这些事实数字不许改值，只许改讲法。
3. 事实与日期精度：不许出现原文和情报里都没依据的精确日期或数值；拿不准的一律降级成"今年""最近"这类说法。发现疑似编造的精确信息，降级处理并记入 issues。
4. 互动段位置跟原文走：先看原文的点赞/评论/收藏/转发那一段落在哪——落在开头（讲正题之前）就必须留在开头，落在中段就留在中段；成稿把位置挪了的，挪回原文的位置。原文要了几个动作（点赞、收藏、转给家里管钱的人、评论四字吉利话）成稿一个不许少，尤其"转给家里管钱的人"这类转发指向是分发杠杆，删了就补回。评论暗号必须是四字祝福语（顺风顺水一类），不是口号；动作的理由一句话说清即可（原文"给自己找个心理支点"这种），成稿把一句理由解释成三句（做个记号、留个书签、以后好找）的，压回一句。只有原文没有互动段时，才在中段合适的气口补两三句。全篇只此一处。
5. 课尾转化段：全文最末连续逼单，字数硬上限480、下限380（从读心开场那句起算到最后一句），完整口语句、不是电报体。超过480的必须压回来：先删复述同一意思的句子、再删不带新信息的排比，不许动原文带来的签名句。课名《财富觉醒方法论》1～2次、主页橱窗1～3次、五块钱3～5次，都只许出现在课尾；必须有橱窗动作句（点开主页橱窗一类），其后允许一句陪跑式关注（新老朋友点个关注+持续价值理由），不能用判断金句结尾。
   课尾以原文为准、清单只兜底：原文课尾自己就有完整逼单结构的（有动作阶梯、有价格锚、有筛人、有反问、有收尾口号），成稿必须按原文的顺序和动作改写，只补原文缺的要件；禁止把原文课尾拆掉、换成"读心→课名→价格→筛人→反问→橱窗"这套固定顺序。原文课尾里的具体动作阶梯（关掉朋友圈、卸载游戏、点开橱窗这类"三件事"）、收尾口号句（上车要趁早我在车里等你）、点破式金句（你差的不是吃苦的本事是站的位置不对）是这篇的签名，成稿丢了的一律补回对应那一句，用写手的口气重说但机制和顺序不变。反过来，签名句也不许整句照抄：除照搬的开头前两三句外，成稿任何一句与原文连续相同超过十个字（去掉标点数），就地换词重说——机制、动作词、课名不动，句式和连接词换掉（例：「上车要趁早，我在车里等你」→「车马上开，你上不上？我在车上等着你」）。评论暗号原文有就必须用原文那四个字，成稿换成别的四字词（顺风顺水→一顺百顺）的改回去。只有原文课尾偏弱（一句"课在橱窗里"带过）时，才按读心开场、价格锚定、拆犹豫、收尾动作句这些要件补齐。
   禁止改成「下一条讲xxx，关注我」；禁止补「不承诺赚钱只保证听懂」及其变体（"就干一件事：让你听懂""不念文件不堆大词只让你听懂""听懂……听懂……听懂……"三连），撞上就删掉换成原文课尾的说法；禁止收益承诺和玄学转运。修辞性小数字（第六次、十个里八个、亏十倍、五块钱）要写成汉字，只有政策事实数字用阿拉伯数字，写反了就改。讲解员腔词（计量口径、对价、标的、顶层规则、先决条件、兑付、闭环、赋能、抓手、底层逻辑）换成大白话；写稿人的词（"正文没来得及展开""这条内容""这篇""上面讲的"）换成口播人的词（"刚才说的""前面那几步"）；不像人话的生硬搭配换回常用说法；年份、百分比、金额与原文逐个核对，写错的改回原文。原文里倍数、次数、时长、序数类的修辞性数字（翻一倍、亏十倍、三个月、五次、第六次、每八年、一套房一辆车）在成稿里漏掉的，补回对应那一句；成稿新编的量级（"好几倍""翻了十倍"这类原文没有的倍数）删掉或改回原文的说法。开头单独查一条：原文第一句是宣布大事、数字砸脸、截止日或反问逼问这类强钩子，而成稿把开头改写成了数文件、报机构、讲背景的罗列式开头，或者去掉了紧接开头的情绪重锤句、命定留存句（比你上一辈子班挣的还多、你心里什么滋味自己清楚、这条视频只会在你人生好转的节点推到你眼前），一律改回照搬原文前两三句并保留重锤句，其余不动。
6. AI味清除：删换"首先/其次/总而言之/值得注意的是/不难发现/让我们"这类书面串词；讲解员腔、连环排比、空喊口号改成街坊聊天的说法。模板腔清除：写手提示词里举过的例句（"十个人里八个……划走""你缺的是这五块钱还是缺一个把事看透的脑子""走到这里的你已经不是观众"）不是素材，原文没有这句而成稿硬套进来的，删掉或换成原文里对应位置的说法；"十个人里八个"全篇最多一次。原文里被磨平的狠话对照着查一遍：原文的实感说法（钱趴在账上睡大觉、被通胀一口口吃掉、规则彻底焊死、真话说出来你可能都不太信）被成稿换成抽象说法（资金流出、购买力下降、规则定型、这个说法是表面）的，改回原文那种实感，词可以换、狠劲不许降。
7. 发布字段与正文一致：titles 留空；short_titles 恰好3条、每条6到15个字、三种钩子各一（数字或日期砸脸 / 反问或反常识 / 人群圈定或结果），出现「窗口不会等人」「普通人的新窗口」「钱会流向哪里」这类通用口号或截原文首句凑数的，用正文里的具体物重写；descriptions 2到3条、每条一句不超过40字、各带一个具体钩子，出现「看懂的人先拿位置」「答案先留着」这类没有具体物的总结句，用正文的日期、数字、反问重写；topics 3到4个：第1个是 #财经/#经济/#理财 之一，其余必须是正文出现过的名词，看到 #认知 #思维认知 #干货分享 #认知觉醒 #宏观趋势 就换成正文里的具体名词；发布字段的修辞性小数字用汉字；哪个字段跑题或超格就修哪个字段。

【修改纪律】
- 每处修改必须对应一条 issue：where 引用原句片段（不超过30字），problem 写违反哪条规范，fix 写改成了什么。
- 操作员批注优先级最高：逐条落实，每条落实情况单独记一条 issue（problem 填"操作员批注"）。
- 没有任何问题时 verdict 填 "pass"，issues 为空数组，revised 原样返回进审稿。
- 有修改时 verdict 填 "fixed"，revised 是修订后的完整结果：所有字段都带上，没动的字段原样带回。
- 禁止整篇重写：修订稿与进审稿的差异只能落在 issues 列出的位置。

只返回一个 JSON 对象，不要 Markdown。字段：
- verdict: "pass" 或 "fixed"
- summary: 一句话说明审了什么、改了几处
- issues: [{where, problem, fix}]
- revised: {continuous_script, titles, short_titles, descriptions, topics, cta}`

// reviewRevisedMinShrinkRatio 防审稿越权：修订稿正文低于进审稿的这个比例视为
// 整篇重写/删稿，弃用修订、保留进审稿。
const reviewRevisedMinShrinkRatio = 0.70

type ReviewIssue struct {
	Where   string `json:"where"`
	Problem string `json:"problem"`
	Fix     string `json:"fix"`
}

// ReviewRecord 是写进 review.json 与运行记录的审稿结论。
type ReviewRecord struct {
	Verdict     string        `json:"verdict"` // pass / fixed / skipped / error
	Round       int           `json:"round"`
	Summary     string        `json:"summary"`
	Issues      []ReviewIssue `json:"issues,omitempty"`
	Annotations string        `json:"annotations,omitempty"`
	Error       string        `json:"error,omitempty"`
	At          string        `json:"at"`
	// Before 是本轮进审稿，Revised 是审稿修订稿（只在 verdict=fixed 时有）。
	// 两版都带正文和全部发布字段：操作员要并排看审稿前后自己定夺采用哪版，
	// 而定稿一旦被手改，修订稿就无处可寻，所以结论里自带两版快照。
	Before  *remixDraft `json:"before,omitempty"`
	Revised *remixDraft `json:"revised,omitempty"`
}

type ReviewOptions struct {
	Client          ChatClient
	BaseURL         string
	APIKey          string
	Model           string
	ReasoningEffort string
	// SystemPrompt 非空时覆盖内置审稿提示词（创作台「Agent提示词」编辑器）。
	SystemPrompt string
	// Source 是同行原文；DraftJSON 是进审稿（写手 JSON，含正文和发布字段）。
	Source    string
	DraftJSON string
	// Annotations 为空时是发稿前的自动首轮；非空时是操作员批注打回。
	Annotations string
	OutputDir   string
	Round       int
}

type ReviewOutcome struct {
	Record ReviewRecord
	// RevisedJSON 是修订后的完整写手 JSON；verdict 非 fixed 时为空，调用方沿用进审稿。
	RevisedJSON string
}

type reviewerReply struct {
	Verdict string        `json:"verdict"`
	Summary string        `json:"summary"`
	Issues  []ReviewIssue `json:"issues"`
	Revised remixDraft    `json:"revised"`
}

// ReviewRemixDraft 执行一轮审稿。失败不返回 error：结论（含失败原因）都落在
// Outcome.Record 里，由调用方决定展示；进审稿永远是保底交付物。
func ReviewRemixDraft(opts ReviewOptions) ReviewOutcome {
	round := opts.Round
	if round < 1 {
		round = 1
	}
	record := ReviewRecord{
		Verdict:     "skipped",
		Round:       round,
		Annotations: strings.TrimSpace(opts.Annotations),
		At:          time.Now().Format(time.RFC3339),
	}
	finish := func(revised string) ReviewOutcome {
		writeReviewArtifacts(opts.OutputDir, record)
		appendRemixRunLog(opts.OutputDir, map[string]any{
			"event": "review", "round": record.Round, "verdict": record.Verdict,
			"issues": len(record.Issues), "error": record.Error,
		})
		return ReviewOutcome{Record: record, RevisedJSON: revised}
	}

	draft, err := parseRemixDraft(opts.DraftJSON)
	if err != nil || strings.TrimSpace(draft.ContinuousScript) == "" {
		record.Error = "进审稿无法解析，跳过审稿。"
		return finish("")
	}
	canonical, err := json.Marshal(draft)
	if err != nil {
		record.Error = "进审稿序列化失败，跳过审稿。"
		return finish("")
	}
	before := draft
	record.Before = &before
	if round == 1 {
		// 写手原稿只在首轮留档一次，后续打回不覆盖，界面上永远能对照初稿。
		_ = os.WriteFile(filepath.Join(opts.OutputDir, "draft_v1.json"), canonical, 0o644)
	}

	client := opts.Client
	if client == nil {
		base := strings.TrimSpace(opts.BaseURL)
		key := strings.TrimSpace(opts.APIKey)
		if base == "" || key == "" {
			record.Error = "审稿通道未配置。"
			return finish("")
		}
		client = &HTTPChatClient{BaseURL: base, APIKey: key}
	}

	annotations := strings.TrimSpace(opts.Annotations)
	if annotations == "" {
		annotations = "（无，本轮为发稿前自动终审）"
	}
	user := "按系统提示的规范清单逐条终审下面这份稿子。\n\n【操作员批注（优先级最高，逐条落实）】\n" + annotations +
		"\n\n# 同行原文\n" + opts.Source +
		"\n\n# 待审成稿（写手 JSON）\n" + string(canonical)

	resp, chatErr := client.Chat(ChatRequest{
		Model:           strings.TrimSpace(opts.Model),
		ReasoningEffort: strings.TrimSpace(opts.ReasoningEffort),
		Stream:          true,
		Messages: []Message{
			{Role: "system", Content: override(opts.SystemPrompt, reviewerSystemPrompt)},
			{Role: "user", Content: user},
		},
	})
	if chatErr != nil {
		record.Verdict = "error"
		record.Error = chatErr.Error()
		return finish("")
	}
	if len(resp.Choices) == 0 {
		record.Verdict = "error"
		record.Error = "审稿模型返回空结果。"
		return finish("")
	}
	reply, parseErr := parseReviewerReply(resp.Choices[0].Message.Content)
	if parseErr != nil {
		record.Verdict = "error"
		record.Error = "审稿结论无法解析：" + parseErr.Error()
		// 原始回复落盘：不留现场就永远查不出是哪种烂法（截断/裸换行/别的结构）。
		if strings.TrimSpace(opts.OutputDir) != "" {
			_ = os.MkdirAll(opts.OutputDir, 0o755)
			if os.WriteFile(filepath.Join(opts.OutputDir, "review_reply_raw.txt"), []byte(resp.Choices[0].Message.Content), 0o644) == nil {
				record.Error += "（原始回复已存运行目录 review_reply_raw.txt）"
			}
		}
		return finish("")
	}
	record.Summary = strings.TrimSpace(reply.Summary)
	record.Issues = reply.Issues

	revisedScript := strings.TrimSpace(reply.Revised.ContinuousScript)
	if reply.Verdict != "fixed" || revisedScript == "" || revisedScript == strings.TrimSpace(draft.ContinuousScript) && len(reply.Issues) == 0 {
		record.Verdict = "pass"
		return finish("")
	}
	inRunes := utf8.RuneCountInString(draft.ContinuousScript)
	outRunes := utf8.RuneCountInString(revisedScript)
	if inRunes > 0 && float64(outRunes) < float64(inRunes)*reviewRevisedMinShrinkRatio {
		record.Verdict = "error"
		record.Error = fmt.Sprintf("修订稿只剩进审稿的 %.0f%%，疑似整篇重写，弃用修订。", float64(outRunes)/float64(inRunes)*100)
		return finish("")
	}
	revised := reply.Revised
	if len(revised.Titles) == 0 {
		revised.Titles = draft.Titles
	}
	if len(revised.ShortTitles) == 0 {
		revised.ShortTitles = draft.ShortTitles
	}
	if len(revised.Descriptions) == 0 {
		revised.Descriptions = draft.Descriptions
	}
	if len(revised.Topics) == 0 {
		revised.Topics = draft.Topics
	}
	revisedJSON, marshalErr := json.Marshal(revised)
	if marshalErr != nil {
		record.Verdict = "error"
		record.Error = "修订稿序列化失败，弃用修订。"
		return finish("")
	}
	record.Verdict = "fixed"
	record.Revised = &revised
	return finish(string(revisedJSON))
}

func parseReviewerReply(raw string) (reviewerReply, error) {
	text := strings.TrimSpace(stripCodeFence(raw))
	if text == "" {
		return reviewerReply{}, fmt.Errorf("空回复")
	}
	try := func(candidate string) (reviewerReply, bool) {
		var reply reviewerReply
		if err := json.Unmarshal([]byte(candidate), &reply); err == nil && reply.Verdict != "" {
			return reply, true
		}
		return reviewerReply{}, false
	}
	candidates := []string{text}
	if extracted := extractJSONObject(text); extracted != "" && extracted != text {
		candidates = append(candidates, extracted)
	}
	for _, candidate := range candidates {
		if reply, ok := try(candidate); ok {
			return reply, nil
		}
		// 长正文回复最常见的两种死法：字符串里有裸换行、有未转义引号。
		// 先修控制字符再修引号（引号启发式依赖行结构，换行修好后更准）。
		if fixed := escapeControlCharsInJSONStrings(candidate); fixed != candidate {
			if reply, ok := try(fixed); ok {
				return reply, nil
			}
			if repaired := repairUnescapedJSONQuotes(fixed); repaired != fixed {
				if reply, ok := try(repaired); ok {
					return reply, nil
				}
			}
		}
		if repaired := repairUnescapedJSONQuotes(candidate); repaired != candidate {
			if reply, ok := try(repaired); ok {
				return reply, nil
			}
		}
	}
	return reviewerReply{}, fmt.Errorf("回复不是规定的 JSON 结构")
}

// DefaultReviewerPrompt 暴露内置审稿提示词，供创作台编辑器展示默认值。
func DefaultReviewerPrompt() string {
	return reviewerSystemPrompt
}

// WriteReviewedFiles 把修订稿写回运行目录（continuous_script.txt 与规范化的
// publishing_package.json），返回口播正文。创作台打回重做完成后调用。
func WriteReviewedFiles(outputDir, draftJSON string) (string, error) {
	draft, err := parseRemixDraft(draftJSON)
	if err != nil {
		return "", err
	}
	script := strings.TrimSpace(draft.ContinuousScript)
	if script == "" {
		return "", fmt.Errorf("revised draft has no script")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "continuous_script.txt"), []byte(script), 0o644); err != nil {
		return "", err
	}
	if err := writeJSONFile(filepath.Join(outputDir, "publishing_package.json"), publishingPackageFromDraft(draft, script)); err != nil {
		return "", err
	}
	return script, nil
}

// writeReviewArtifacts 落 review.json（最新一轮）和 review_round_N.json（留痕）。
func writeReviewArtifacts(outputDir string, record ReviewRecord) {
	if strings.TrimSpace(outputDir) == "" {
		return
	}
	_ = writeJSONFile(filepath.Join(outputDir, "review.json"), record)
	_ = writeJSONFile(filepath.Join(outputDir, fmt.Sprintf("review_round_%d.json", record.Round)), record)
}
