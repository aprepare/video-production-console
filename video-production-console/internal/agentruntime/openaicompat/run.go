package openaicompat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"video-production-console/internal/domain"
	"video-production-console/internal/spokenlines"
)

// Options configures the OpenAI-compatible remix/topic runner.
type Options struct {
	ManifestPath      string
	SkillRoot         string
	OutputLastMessage string
	Model             string
	ReasoningEffort   string
	// CheckModel runs the post-draft quality check (overlap, course name,
	// opening/ending, CTA). Empty disables the model pass; local fixes still run.
	CheckModel string
	BaseURL    string
	APIKey            string
	MaxSteps          int
	PythonBinary      string
	Client            ChatClient
}

type manifestLite struct {
	TaskID    string `json:"task_id"`
	Action    string `json:"action"`
	Skill     string `json:"skill"`
	OutputDir string `json:"output_dir"`
	Inputs    []struct {
		Type string `json:"type"`
		Role string `json:"role"`
		Path string `json:"path"`
	} `json:"inputs"`
	NonSecretSettings struct {
		RevisionNotes    string `json:"revision_notes"`
		RemixPromptStyle string `json:"remix_prompt_style"`
	} `json:"non_secret_settings"`
}

const (
	PromptStyleRewrite = "rewrite"

	// RewritePromptStamp 是 rewrite 系统提示词的版本标注。
	// 来源：独立「文案进化台」试写法拍房 / 存款蒸发 / 换锚后的 2026-08-19 中老年定稿；
	// 2026-08-21 增补：篇幅跟原文走、顺序/切口分工说明、短句急停节奏示范、
	// 钩子例子改为示范性质（必须从原文题材现找），并删掉禁词表里的笔误「换毛」。
	// 2026-08-21 爆款回流：复盘账号 5 篇爆款（转发率最高 5.6% 的是「紧急提醒＋具体
	// 日期」开头）后增补【截止日通知感】开头形态与【互动引导】软规则（转发走家庭
	// 责任、评论留许愿口，禁止喊口令式硬引导）。
	// 2026-08-24：课名一律《财富觉醒方法论》，禁止带年份；卖课钩子只收口一次。
	// 2026-08-24 切口定稿：六件套出场顺序可换、开场切口必须换、钩子 2～3 句、
	// 收口最多四句、示范原句进失败标准、截止日只认具体日、拿掉 machine 字段。
	// 只作仓库标注，不发给模型。
	RewritePromptStamp = "文案进化台 2026-08-24 切口收口定稿"
)

func NormalizePromptStyle(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", PromptStyleRewrite:
		return PromptStyleRewrite, nil
	default:
		return "", fmt.Errorf("remix_prompt_style must be rewrite")
	}
}

// Run asks the model for remix copy only, then the console writes result files.
// Cursor Ask-mode endpoints refuse tools; long non-streaming tool loops also die
// on trycloudflare 120s cutoffs, so this path never sends tools.
func Run(opts Options) error {
	manifestPath := strings.TrimSpace(opts.ManifestPath)
	outPath := strings.TrimSpace(opts.OutputLastMessage)
	if manifestPath == "" || outPath == "" {
		return fmt.Errorf("manifest and output-last-message are required")
	}
	baseURL := strings.TrimSpace(opts.BaseURL)
	apiKey := strings.TrimSpace(opts.APIKey)
	if baseURL == "" || apiKey == "" {
		return writeFailure(outPath, manifestPath, fmt.Errorf("openai base url and api key are required"))
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = "gpt-4o-mini"
	}

	rawManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return writeFailure(outPath, manifestPath, fmt.Errorf("read manifest: %w", err))
	}
	var manifest manifestLite
	if err := json.Unmarshal(stripBOM(rawManifest), &manifest); err != nil {
		return writeFailure(outPath, manifestPath, fmt.Errorf("decode manifest: %w", err))
	}
	if strings.TrimSpace(manifest.OutputDir) == "" {
		return writeFailure(outPath, manifestPath, fmt.Errorf("manifest output_dir is required"))
	}
	if err := os.MkdirAll(manifest.OutputDir, 0o755); err != nil {
		return writeFailure(outPath, manifestPath, err)
	}

	action := strings.TrimSpace(manifest.Action)
	if action == "" {
		action = string(domain.ActionRemixStandard)
	}
	if action == string(domain.ActionCaptionKeywords) {
		return runCaptionKeywords(opts, manifest, outPath)
	}
	source, err := readPrimarySource(manifest)
	if err != nil {
		return writeFailure(outPath, manifestPath, err)
	}
	if action == string(domain.ActionRemixSpokenLines) {
		return runSpokenLines(opts, manifest, source, outPath)
	}
	if _, err := NormalizePromptStyle(manifest.NonSecretSettings.RemixPromptStyle); err != nil {
		return writeFailure(outPath, manifestPath, err)
	}
	system := buildWriterPrompt()
	user := buildWriterUser(manifest, source)

	client := opts.Client
	if client == nil {
		client = &HTTPChatClient{BaseURL: baseURL, APIKey: apiKey}
	}
	resp, err := client.Chat(ChatRequest{
		Model:           model,
		ReasoningEffort: strings.TrimSpace(opts.ReasoningEffort),
		Stream:          true,
		Messages:        []Message{{Role: "system", Content: system}, {Role: "user", Content: user}},
	})
	if err != nil {
		return writeFailure(outPath, manifestPath, err)
	}
	if len(resp.Choices) == 0 {
		return writeFailure(outPath, manifestPath, fmt.Errorf("empty chat choices"))
	}
	content, checkWarnings, checkNote := repairRemixDraft(client, model, strings.TrimSpace(opts.CheckModel), strings.TrimSpace(opts.ReasoningEffort), system, user, source, resp.Choices[0].Message.Content)
	if err := writeRemixDeliverable(manifest.OutputDir, manifest.TaskID, action, content, checkWarnings, checkNote); err != nil {
		return writeFailure(outPath, manifestPath, err)
	}
	resultFile := filepath.Join(manifest.OutputDir, "result.json")
	fileRaw, err := os.ReadFile(resultFile)
	if err != nil {
		return writeFailure(outPath, manifestPath, err)
	}
	if err := writeEnvelope(outPath, fileRaw); err != nil {
		return writeFailure(outPath, manifestPath, err)
	}
	return nil
}

func readPrimarySource(manifest manifestLite) (string, error) {
	var fallback string
	for _, input := range manifest.Inputs {
		switch strings.TrimSpace(input.Type) {
		case "source_script":
			return readTextFile(input.Path)
		case "continuous_script":
			if fallback == "" {
				fallback = input.Path
			}
		}
	}
	if fallback != "" {
		return readTextFile(fallback)
	}
	return "", fmt.Errorf("manifest is missing a source_script input")
}

func readTextFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read source script: %w", err)
	}
	text := strings.TrimSpace(string(stripBOM(raw)))
	if text == "" {
		return "", fmt.Errorf("source script is empty")
	}
	return text, nil
}

func runSpokenLines(opts Options, manifest manifestLite, source, outPath string) error {
	fail := func(err error) error {
		return writeFailure(outPath, opts.ManifestPath, err)
	}
	client := opts.Client
	if client == nil {
		client = &HTTPChatClient{BaseURL: strings.TrimSpace(opts.BaseURL), APIKey: strings.TrimSpace(opts.APIKey)}
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = "gpt-4o-mini"
	}
	resp, err := client.Chat(ChatRequest{
		Model:           model,
		ReasoningEffort: strings.TrimSpace(opts.ReasoningEffort),
		Stream:          true,
		Messages: []Message{
			{Role: "system", Content: spokenlines.SystemPrompt},
			{Role: "user", Content: spokenlines.UserPrompt(source)},
		},
	})
	if err != nil {
		return fail(err)
	}
	if len(resp.Choices) == 0 {
		return fail(fmt.Errorf("empty chat choices"))
	}
	if err := writeSpokenDeliverable(manifest.OutputDir, manifest.TaskID, resp.Choices[0].Message.Content); err != nil {
		return fail(err)
	}
	resultFile := filepath.Join(manifest.OutputDir, "result.json")
	fileRaw, err := os.ReadFile(resultFile)
	if err != nil {
		return fail(err)
	}
	if err := writeEnvelope(outPath, fileRaw); err != nil {
		return fail(err)
	}
	return nil
}

// runCaptionKeywords asks the model which terms of each 口播稿 line deserve
// on-screen emphasis and writes the caption_keywords.json asset.
func runCaptionKeywords(opts Options, manifest manifestLite, outPath string) error {
	fail := func(err error) error {
		return writeFailure(outPath, opts.ManifestPath, err)
	}
	var spokenPath string
	for _, input := range manifest.Inputs {
		if strings.TrimSpace(input.Type) == "spoken_script" {
			spokenPath = input.Path
			break
		}
	}
	if spokenPath == "" {
		return fail(fmt.Errorf("manifest is missing a spoken_script input"))
	}
	raw, err := readTextFile(spokenPath)
	if err != nil {
		return fail(err)
	}
	formatted, err := spokenlines.Format(raw)
	if err != nil {
		return fail(err)
	}
	lines := spokenlines.Lines(formatted)
	client := opts.Client
	if client == nil {
		client = &HTTPChatClient{BaseURL: strings.TrimSpace(opts.BaseURL), APIKey: strings.TrimSpace(opts.APIKey)}
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = "gpt-4o-mini"
	}
	resp, err := client.Chat(ChatRequest{
		Model:           model,
		ReasoningEffort: strings.TrimSpace(opts.ReasoningEffort),
		Stream:          true,
		Messages: []Message{
			{Role: "system", Content: spokenlines.KeywordSystemPrompt},
			{Role: "user", Content: spokenlines.KeywordUserPrompt(lines)},
		},
	})
	if err != nil {
		return fail(err)
	}
	if len(resp.Choices) == 0 {
		return fail(fmt.Errorf("empty chat choices"))
	}
	if err := writeKeywordsDeliverable(manifest.OutputDir, manifest.TaskID, resp.Choices[0].Message.Content, lines); err != nil {
		return fail(err)
	}
	resultFile := filepath.Join(manifest.OutputDir, "result.json")
	fileRaw, err := os.ReadFile(resultFile)
	if err != nil {
		return fail(err)
	}
	if err := writeEnvelope(outPath, fileRaw); err != nil {
		return fail(err)
	}
	return nil
}

// buildWriterPrompt 拼系统提示词。
//
// rewrite（默认）= RewritePromptStamp（文案进化台 2026-08-24 切口收口定稿）。
// 相对上一版加了这些刀：
//   - 六件套都要在，出场顺序可以换；禁止提前揭答案、把课收到开头
//   - 开场切口必须换，原稿第一句的同义改写整稿失败
//   - 钩子 2～3 句内完成，大约 40 字内听懂谁的钱出了什么事
//   - 卖课收口最多四句；课名、橱窗全文各一次
//   - 示范只留结构，示范原句写进失败标准
//   - 截止日只认具体日（X月X号 / 会议日），年份和大年不算
//   - 开场仍禁「阶层」，后文必须有普通人对照，用大白话
//   - 不再要求模型返回 machine 字段
//
// 落盘 JSON 仍走 writerJSONContract（标题/描述/话题/cta）。
func buildWriterPrompt() string {
	var b strings.Builder
	b.WriteString("你是财经视频号二创写手。只写文案，不要调用工具，不要读写文件，不要解释过程。\n")
	b.WriteString("\n【先锁爆款机器】\n")
	b.WriteString("先从同行原文锁住爆款机器，再写新稿。机器以原文为准，不要用提示词里的现成情节去套。\n")
	b.WriteString("必须能用一句话分别指回原稿：\n")
	b.WriteString("1）第一句靠什么留人（钩子类型）\n")
	b.WriteString("2）观众最想知道、且原稿故意还没说完的答案\n")
	b.WriteString("3）历史或数字怎样证明这套逻辑已经灵过\n")
	b.WriteString("4）普通人与先看懂的人之间的差距\n")
	b.WriteString("5）情绪怎么升级\n")
	b.WriteString("6）结尾靠什么催促上车\n")
	b.WriteString("写稿时这六条一条都不能丢。六条都要在，出场顺序可以换：允许先算家里的账再落到全国数字，允许先讲历史对照再落到这次为什么来。禁止把后半段答案提前揭开，禁止把课收到开头。\n")
	b.WriteString("\n【硬性保留】\n")
	b.WriteString("- 不换题，不降温，不补圆原文故意不说完的答案，不收成家庭理财课\n")
	b.WriteString("- 篇幅跟原文走：正文字数控制在原文的 0.8～1.2 倍。不许缩成摘要，也不许注水拉长\n")
	b.WriteString("- 课程名固定写成《财富觉醒方法论》，入口主页橱窗。禁止带年份、禁止写成「2026财富觉醒方法论」或「叫2026财富觉醒方法论」。原文课名即使带年份，口播也必须改成《财富觉醒方法论》。全文只出现一次课程名、一次「主页橱窗」\n")
	b.WriteString("- 卖课钩子只在全文最末收口，最多四句：点开主页橱窗 → 五块钱 → 方向判断（该出手还是该按住）→ 停。禁止开头、中段、结尾各讲一遍；禁止同一段里把课名、五块钱、橱窗再重复一遍；禁止把收口写成一段小课\n")
	b.WriteString("- 关键数字必须原词留下（套数、日均、比例、年限、单价、金额、城数）。禁止改成「很多」「心惊的数」「差不多」这类形容词或约数\n")
	b.WriteString("- 数字原词不等于整句照搬：带数字的数据句同样必须换说法重讲，只有数字本身一个不动。数据句原样照搬按留原句判失败\n")
	b.WriteString("- 例子必须自洽：本金乘利率要对上利息\n")
	b.WriteString("- 第一句必须接住原稿钩子力度，但开场切口必须换。原稿若是对仗打脸，新稿第一句必须还是对仗打脸，两边都得是大白话，用新词。禁止写成更软的解释句或中介口吻。禁止用原稿第一句的同义改写开场\n")
	b.WriteString("- 钩子必须在前 2～3 句内完成，大约 40 字内让人听懂「谁的钱、出了什么事」。禁止用「这不是网上传的」「报表已经写死了」这类解释段当第一句。「给我三分钟」「比过去三年更值钱」全篇最多一次，且不能顶在钩子前面\n")
	b.WriteString("- 禁止用熬夜、站位、人生感悟开场\n")
	b.WriteString("\n【中老年听得懂——钩子先过这一关】\n")
	b.WriteString("听的人是四十五到六十五岁，第一句必须像跟邻居说话，一听就懂，不用停下来问「这是啥意思」。\n")
	b.WriteString("- 钩子用具体事，说的必须是观众自家能摸到的东西。具体用哪件事必须从这篇原文的题材里现找\n")
	b.WriteString("- 开场禁止：锚点、换锚、货币、结汇、印钞、认知、红利、风口、阶层、史诗级、逻辑、趋势、下半场\n")
	b.WriteString("- 后文必须有普通人对照，用大白话写先看懂的人和后知道的人差在哪，不要用「阶层」这个词\n")
	b.WriteString("- 对仗可以，但两边都得是他们生活里的词\n")
	b.WriteString("- 黑话如果原文中段才出现，后文用大白话解释一次再往下走，不准扔在第一句\n")
	b.WriteString("\n【截止日通知感——只有具体日子才用】\n")
	b.WriteString("只有原文出现具体日（X月X号、某会议日、某政策生效日）时，第一句才优先写成紧急提醒。年份、季度、「集中到期大年」不算截止日，不许写成「过了这天」。\n")
	b.WriteString("- 通知节奏：点名人群＋提醒口吻＋原文里的具体日期＋你还有时间准备＋过了这天差距当场拉开。只学结构，字面必须换\n")
	b.WriteString("- 禁止编造日期、挪动日期或把模糊时间说成具体日子；原文没有具体日就不用这个开头\n")
	b.WriteString("- 通知感开头也要接住原稿钩子力度，提醒的是观众自家的钱和日子，不是播报新闻\n")
	b.WriteString("\n【互动引导——写成内容，不写成口号】\n")
	b.WriteString("- 转发走家庭责任：收口处把这件事指向观众的家人，让观众自己觉得该转给老伴、转进家庭群。只留结构，不要套现成句子\n")
	b.WriteString("- 评论留许愿口：全篇最多一个，放在讲完上一轮谁富了、或点明这回轮到谁之后。只留口子，不逼着评论\n")
	b.WriteString("- 禁止喊口令：不许出现「快转发」「转发给几个群」「评论扣1」「接接接」「见者发财」「不转不是」这类硬引导，一出现即整稿失败\n")
	b.WriteString("- 转发引导和评论口子各最多一处，不能打断正文推进，收口的主任务仍是催上车，不是催转发\n")
	b.WriteString("\n【允许换、但禁止洗没】\n")
	b.WriteString("六个机器零件都要在，段落顺序可以换。必须换的是每个论点下面的例子、画面、人物、现场，以及开场切口。\n")
	b.WriteString("- 禁止逐段同义改写，禁止按「第N个难题」对照译文\n")
	b.WriteString("- 中间论证必须换切口（现场、人物、一个动作），不能是原稿换词\n")
	b.WriteString("- 禁止照抄或轻微改写原稿金句、比喻和专属例子，这些画面必须换成新的\n")
	b.WriteString("- 中后段也不能留原稿原句\n")
	b.WriteString("- 开场句式和比喻必须换，钩子类型、信息密度、短句急停节奏不能被磨平\n")
	b.WriteString("- 短句急停只学节奏：三句一顿，每句砸进一个新信息，不许并成一个长句。提示词里的示范原句一律不准写进成稿\n")
	b.WriteString("\n【密度失败标准——出现任一条即整稿失败】\n")
	b.WriteString("- 前 2～3 句没有明确钩子，或钩子要解释才能懂，或钩子超过大约 40 字还没让人听懂出事的是谁的钱\n")
	b.WriteString("- 开场仍是原稿第一句的同义改写\n")
	b.WriteString("- 开场出现锚点、换锚、货币、认知、阶层等中老年听着费劲的词\n")
	b.WriteString("- 关键数字被删、被改糊或被形容词替代\n")
	b.WriteString("- 短句急停被改成顺滑长段，信息密度明显下降\n")
	b.WriteString("- 普通人对照被删软或删掉\n")
	b.WriteString("- 听起来像换了一篇更温和的家庭理财文\n")
	b.WriteString("- 正文比原文短了两成以上，或明显注水变长\n")
	b.WriteString("- 出现「快转发」「评论扣1」「接接接」这类喊口令式互动引导，或编造了原文里没有的截止日期\n")
	b.WriteString("- 课程名带了年份，或写成「2026财富觉醒方法论」\n")
	b.WriteString("- 卖课钩子在全文出现两次及以上，或收口超过四句，或课名、橱窗在全文出现超过一次\n")
	b.WriteString("- 成稿里出现提示词示范原句，包括但不限于：「存折上的钱少了、利息不够买早饭」「房子卖不动了。不是降价卖不动。是白送都没人接。」「还把钱死死捂在银行卡里的，我紧急提一句」「上一轮你踩没踩中，评论区说一句」「一家人里至少要有一个人听懂」\n")
	b.WriteString("\n按四十五到六十五岁口播来写。少用书面词。句子短，像当面说话。写成能念的连续口播，不要讲解员作文。\n")
	b.WriteString(writerJSONContract())
	return b.String()
}

func writerJSONContract() string {
	return "只返回一个 JSON 对象，不要 Markdown。字段：continuous_script, titles, short_titles, descriptions, topics, cta。\n" +
		"continuous_script 必须是完整连续口播正文。\n" +
		"titles、short_titles、descriptions 必须从这篇口播长出来，讲的是同一件事。禁止拿别的成稿标题来凑数，也不要用提示词里没有出现在原文里的情节做标题。\n" +
		"titles 8到12条。short_titles 恰好5条、每条最多15个字、不要#。descriptions 恰好3条，每条只用一到两句话概括这条视频、不超过40个字，不要复述正文段落。话题只能从这些热门标签里选3到4个：#经济 #思维认知 #认知 #宏观趋势 #思维 #干货分享 #认知觉醒。三条描述末尾都带这同一组标签，topics 也只用这组，不要自造其他#。cta 必须留空字符串。发布文案不要写课程名、主页橱窗、上车、推广期、几块钱。口播正文仍可按硬性保留收口到课程，但 titles / short_titles / descriptions / cta 一律不写推广。\n"
}

func buildWriterUser(manifest manifestLite, source string) string {
	var b strings.Builder
	b.WriteString("下面是同行原文，只当证据，不当逐句模板。先从原文锁机器，再写全新口播。\n")
	b.WriteString("开场切口必须和原稿第一句不同。不要套提示词里的示范原句。如果听起来还是原稿换词，或钩子/数字/密度被磨平，或开场在讲概念，就算失败。\n")
	b.WriteString("标题和短标题必须跟这篇新口播走，不要沿用上一篇成稿的标题。\n")
	if notes := strings.TrimSpace(manifest.NonSecretSettings.RevisionNotes); notes != "" {
		b.WriteString("修改要求：\n")
		b.WriteString(notes)
		b.WriteString("\n")
	}
	b.WriteString("\n# 同行原文\n")
	b.WriteString(source)
	return b.String()
}

func stripBOM(raw []byte) []byte {
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		return raw[3:]
	}
	return raw
}

func writeEnvelope(path string, raw []byte) error {
	trimmed := strings.TrimSpace(string(stripBOM(raw)))
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end <= start {
		return fmt.Errorf("result is not JSON")
	}
	candidate := []byte(trimmed[start : end+1])
	var probe map[string]any
	if err := json.Unmarshal(candidate, &probe); err != nil {
		return err
	}
	if _, ok := probe["schema_version"]; !ok {
		return fmt.Errorf("result missing schema_version")
	}
	normalizePaths(probe)
	encoded, err := json.Marshal(probe)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}

func normalizePaths(probe map[string]any) {
	strip := func(value string) string {
		trimmed := strings.TrimSpace(value)
		if strings.HasPrefix(trimmed, `\\?\`) {
			return trimmed[4:]
		}
		return trimmed
	}
	for _, key := range []string{"artifacts", "asset_outputs"} {
		items, ok := probe[key].([]any)
		if !ok {
			continue
		}
		for _, item := range items {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if path, ok := obj["path"].(string); ok {
				obj["path"] = strip(path)
			}
		}
	}
}

func writeFailure(outPath, manifestPath string, cause error) error {
	taskID, action := identityFromManifest(manifestPath)
	envelope := map[string]any{
		"schema_version": "2.0",
		"task_id":        taskID,
		"action":         action,
		"status":         "failed",
		"summary":        cause.Error(),
		"questions":      []any{},
		"artifacts":      []any{},
		"asset_outputs":  []any{},
		"warnings":       []any{},
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return cause
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("%v; also failed to create output dir: %w", cause, err)
	}
	if err := os.WriteFile(outPath, raw, 0o644); err != nil {
		return fmt.Errorf("%v; also failed to write envelope: %w", cause, err)
	}
	return nil
}

func identityFromManifest(path string) (taskID, action string) {
	action = "remix.standard"
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", action
	}
	var manifest manifestLite
	if json.Unmarshal(stripBOM(raw), &manifest) != nil {
		return "", action
	}
	if manifest.Action != "" {
		action = manifest.Action
	}
	return manifest.TaskID, action
}
