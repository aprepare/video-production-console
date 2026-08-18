package openaicompat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Options configures the OpenAI-compatible remix/topic runner.
type Options struct {
	ManifestPath      string
	SkillRoot         string
	OutputLastMessage string
	Model             string
	ReasoningEffort   string
	BaseURL           string
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
	PromptStyleWash    = "wash"

	// RewritePromptStamp 是 rewrite 系统提示词的版本标注。
	// 来源：独立「文案进化台」试写法拍房 / 存款蒸发 / 换锚后的 2026-08-19 中老年定稿。
	// 只作仓库标注，不发给模型。wash 路径未改。
	RewritePromptStamp = "文案进化台 2026-08-19 中老年定稿"
)

func NormalizePromptStyle(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", PromptStyleRewrite:
		return PromptStyleRewrite, nil
	case PromptStyleWash:
		return PromptStyleWash, nil
	default:
		return "", fmt.Errorf("remix_prompt_style must be rewrite or wash")
	}
}

// Run asks the model for remix copy only, then the console writes result files.
// Cursor Ask-mode endpoints refuse tools; long non-streaming tool loops also die
// on trycloudflare 120s cutoffs, so this path never sends tools.
func Run(opts Options) error {
	manifestPath := strings.TrimSpace(opts.ManifestPath)
	skillRoot := strings.TrimSpace(opts.SkillRoot)
	outPath := strings.TrimSpace(opts.OutputLastMessage)
	if manifestPath == "" || skillRoot == "" || outPath == "" {
		return fmt.Errorf("manifest, skill-root, and output-last-message are required")
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

	source, err := readPrimarySource(manifest)
	if err != nil {
		return writeFailure(outPath, manifestPath, err)
	}
	skillMD, _ := os.ReadFile(filepath.Join(skillRoot, "SKILL.md"))
	style, err := NormalizePromptStyle(manifest.NonSecretSettings.RemixPromptStyle)
	if err != nil {
		return writeFailure(outPath, manifestPath, err)
	}
	system := buildWriterPrompt(string(skillMD), style)
	user := buildWriterUser(manifest, source, style)

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
	action := manifest.Action
	if action == "" {
		action = "remix.standard"
	}
	if err := writeRemixDeliverable(manifest.OutputDir, manifest.TaskID, action, resp.Choices[0].Message.Content); err != nil {
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

// buildWriterPrompt 拼系统提示词。
//
// rewrite（默认）= RewritePromptStamp（文案进化台 2026-08-19 中老年定稿）。
// 相对控制台旧版加了这些刀：
//   - 先锁六件套爆款机器（钩子 / 未揭晓 / 证明 / 差距 / 情绪 / 收口）
//   - 关键数字必须原词，禁止「很多」「心惊的数」
//   - 对仗钩子不能写成更软的解释句
//   - 【中老年听得懂】开场禁锚点/换锚/货币/认知等黑话
//   - 禁止按「第N个难题」对照译文，中间必须换切口
//   - 密度失败标准写死：钩子听不懂 / 数字糊了 / 写顺了 / 对照没了 / 降温成课
//
// wash 路径未改。落盘 JSON 仍走 writerJSONContract（标题/描述/话题/cta）。
// 模型可额外返回 machine；parseRemixDraft 忽略未知字段，控制台仍以 continuous_script 为准。
func buildWriterPrompt(skillMD, style string) string {
	if style == PromptStyleWash {
		return buildWashPrompt()
	}
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
	b.WriteString("写稿时这六条一条都不能丢。\n")
	b.WriteString("\n【硬性保留】\n")
	b.WriteString("- 不换题，不降温，不补圆原文故意不说完的答案，不收成家庭理财课\n")
	b.WriteString("- 课程名固定《财富觉醒方法论》，入口主页橱窗；原文另有课名时跟原文\n")
	b.WriteString("- 关键数字必须原词留下（套数、日均、比例、年限、单价、金额、城数）。禁止改成「很多」「心惊的数」「差不多」这类形容词或约数\n")
	b.WriteString("- 例子必须自洽：本金乘利率要对上利息\n")
	b.WriteString("- 原稿的推进顺序不能倒\n")
	b.WriteString("- 第一句必须接住原稿钩子力度；原稿若是对仗打脸，新稿第一句必须还是对仗打脸，两边都得是大白话，用新词，不许写成更软的解释句或中介口吻\n")
	b.WriteString("- 禁止用熬夜、站位、人生感悟开场\n")
	b.WriteString("\n【中老年听得懂——钩子先过这一关】\n")
	b.WriteString("听的人是四十五到六十五岁，第一句必须像跟邻居说话，一听就懂，不用停下来问「这是啥意思」。\n")
	b.WriteString("- 钩子用具体事：存折上的钱少了、利息不够买早饭、房子挂出去没人问、银行柜台上利率条换了\n")
	b.WriteString("- 开场禁止：锚点、换锚、换毛、货币、结汇、印钞、认知、红利、风口、阶层、史诗级、逻辑、趋势、下半场\n")
	b.WriteString("- 对仗可以，但两边都得是他们生活里的词。能说「三年前抢着买叫投资，现在想卖没人要」，不能说「第一次锚定美元，第二次锚定房地产」\n")
	b.WriteString("- 黑话如果原文中段才出现，后文用大白话解释一次再往下走，不准扔在第一句\n")
	b.WriteString("\n【允许换、但禁止洗没】\n")
	b.WriteString("- 禁止逐段同义改写，禁止按「第N个难题」对照译文\n")
	b.WriteString("- 中间论证必须换切口（现场、人物、一个动作），不能是原稿换词\n")
	b.WriteString("- 禁止照抄或轻微改写原稿金句、比喻和专属例子，这些画面必须换成新的\n")
	b.WriteString("- 中后段也不能留原稿原句\n")
	b.WriteString("- 开场句式和比喻可以换，但钩子类型、信息密度、短句急停节奏不能被磨平\n")
	b.WriteString("\n【密度失败标准——出现任一条即整稿失败】\n")
	b.WriteString("- 前3秒没有明确钩子，或钩子要解释才能懂\n")
	b.WriteString("- 开场出现锚点、换锚、货币、认知等中老年听着费劲的词\n")
	b.WriteString("- 关键数字被删、被改糊或被形容词替代\n")
	b.WriteString("- 短句急停被改成顺滑长段，信息密度明显下降\n")
	b.WriteString("- 普通人对照/阶层差距被删软或删掉\n")
	b.WriteString("- 听起来像换了一篇更温和的家庭理财文\n")
	b.WriteString("\n按四十五到六十五岁口播来写。少用书面词。句子短，像当面说话。写成能念的连续口播，不要讲解员作文。\n")
	b.WriteString(writerJSONContract())
	b.WriteString("rewrite 还必须带 machine：对象，含 hook / unanswered / proof / gap / emotion / cta 六句（锁机器）。控制台落盘仍以 continuous_script 为准。\n")
	if excerpt := skillExcerpt(skillMD); excerpt != "" {
		b.WriteString("\n# 补充约束\n")
		b.WriteString(excerpt)
	}
	return b.String()
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
	b.WriteString(writerJSONContract())
	return b.String()
}

func writerJSONContract() string {
	return "只返回一个 JSON 对象，不要 Markdown。字段：continuous_script, titles, short_titles, descriptions, topics, cta。\n" +
		"continuous_script 必须是完整连续口播正文。\n" +
		"titles、short_titles、descriptions 必须从这篇口播长出来，讲的是同一件事。禁止拿别的成稿标题来凑数，也不要用提示词里没有出现在原文里的情节做标题。\n" +
		"titles 8到12条。short_titles 恰好5条、每条6到16个字、不要#。descriptions 恰好3条。话题只能从这些热门标签里选3到4个：#经济 #思维认知 #认知 #宏观趋势 #思维 #干货分享 #认知觉醒。三条描述末尾都带这同一组标签，topics 也只用这组，不要自造其他#。cta 一句催促上车。\n"
}

func buildWriterUser(manifest manifestLite, source, style string) string {
	var b strings.Builder
	if style == PromptStyleWash {
		b.WriteString("下面是同行原文。按洗稿来写，不要另写一篇。先用一句话写出这篇仍在让观众追问什么，再写口播。\n")
		b.WriteString("保留原稿的推进顺序、数字、历史例子、比喻、课名和上车结构。只改气口、标点和少量用词。如果听起来像换了一篇文章，就算失败。\n")
	} else {
		// 文案进化台 2026-08-19 用户侧：先锁机器，再换皮；钩子必须五十岁以上一听就懂。
		b.WriteString("下面是同行原文，只当证据，不当逐句模板。先从原文锁机器，再用一句话写出这篇新稿仍在让观众追问什么，再写全新口播。\n")
		b.WriteString("同一条机器换一层完全不同的说法和例子。第一句必须让五十岁以上的人不用停下来问「这是啥意思」。不要套提示词里没有出现在原文里的情节。如果听起来还是原稿换词，或钩子/数字/密度被磨平，或开场在讲概念，就算失败。\n")
		b.WriteString("标题和短标题也必须跟这篇新口播走，不要沿用上一篇成稿的标题。\n")
	}
	if notes := strings.TrimSpace(manifest.NonSecretSettings.RevisionNotes); notes != "" {
		b.WriteString("修改要求：\n")
		b.WriteString(notes)
		b.WriteString("\n")
	}
	b.WriteString("\n# 同行原文\n")
	b.WriteString(source)
	return b.String()
}

func skillExcerpt(skillMD string) string {
	text := strings.TrimSpace(skillMD)
	if text == "" {
		return ""
	}
	const max = 1800
	if len([]rune(text)) <= max {
		return text
	}
	return string([]rune(text)[:max]) + "…"
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
