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

func buildWriterPrompt(skillMD, style string) string {
	if style == PromptStyleWash {
		return buildWashPrompt()
	}
	var b strings.Builder
	b.WriteString("你是财经视频号二创写手。只写文案，不要调用工具，不要读写文件，不要解释过程。\n")
	b.WriteString("先锁爆款机器，再写新稿。机器必须还在：又一批人要发财 → 人民币第三次换锚 → 第一波美元/外贸 → 第二波土地/房子 → 旧锚死了 → 利率和一百七十万亿存款 → 第三个锚故意不说完 → 现在上车。\n")
	b.WriteString("不换题，不降温，不补圆第三个锚，不收成家庭理财课。课程名固定《财富觉醒方法论》，入口主页橱窗。\n")
	b.WriteString("禁止逐段同义改写。禁止照抄或轻微改写原稿金句和比喻，尤其是：河的上游、先漫过再流到下游、歪打正着踩中时代、连瓶水都买不起、短信费赚不回来、方便面被外卖抢走。这些画面必须换成新的。\n")
	b.WriteString("中后段也不能留原稿原句。尤其禁止：中国经济正式进入下半场；零三年北京三环、零八年电商、一六年短视频这组原样例子。提前占位的比喻必须另写。\n")
	b.WriteString("关键数字保留，例子必须自洽：本金乘利率要对上利息，不要一万块对出九百五。按四十五到六十五岁口播来写，少用戏眼、命码、硬切换这类书面词。\n")
	b.WriteString("第一句必须接住发财、换锚、八月窗口。禁止用熬夜、站位、人生感悟开场。\n")
	b.WriteString("开场句式和比喻可以换，机器顺序不能倒：先发财换锚，再两次历史证明，旧锚死了之后才讲利率和一百七十万亿。利率提前，观众会听成别把钱放银行。写成能念的连续口播，不要讲解员作文。\n")
	b.WriteString(writerJSONContract())
	if excerpt := skillExcerpt(skillMD); excerpt != "" {
		b.WriteString("\n# 补充约束\n")
		b.WriteString(excerpt)
	}
	return b.String()
}

func buildWashPrompt() string {
	var b strings.Builder
	b.WriteString("你是财经视频号洗稿写手。只写文案，不要调用工具，不要读写文件，不要解释过程。\n")
	b.WriteString("按洗稿来，不要另写一篇。机器、顺序、数字、历史例子、比喻、课名和上车结构都必须还在。\n")
	b.WriteString("只做这些事：切成适合口播的短段、改成更顺口的标点、轻微换词、修好明显错字。可以「又有一批人」改成「又一批人」，「换毛」改成「换锚」。\n")
	b.WriteString("不要换题，不要补圆第三个锚，不要改成家庭理财课，不要新编一套机制，不要把金句和例子换成另一套。\n")
	b.WriteString("课名跟原文走；原文没有课名时用《财富觉醒方法论》，入口主页橱窗。\n")
	b.WriteString("关键数字保留，本金乘利率要对上利息。按四十五到六十五岁口播来写。\n")
	b.WriteString("机器顺序不能倒：先发财换锚，再两次历史证明，旧锚死了之后才讲利率和一百七十万亿。\n")
	b.WriteString(writerJSONContract())
	return b.String()
}

func writerJSONContract() string {
	return "只返回一个 JSON 对象，不要 Markdown。字段：continuous_script, titles, short_titles, descriptions, topics, cta。\n" +
		"continuous_script 必须是完整连续口播正文。titles 8到12条。short_titles 恰好5条、每条6到16个字、不要#。descriptions 恰好3条。话题只能从这些热门标签里选3到4个：#经济 #思维认知 #认知 #宏观趋势 #思维 #干货分享 #认知觉醒。三条描述末尾都带这同一组标签，topics 也只用这组，不要自造其他#。cta 一句催促上车。\n"
}

func buildWriterUser(manifest manifestLite, source, style string) string {
	var b strings.Builder
	if style == PromptStyleWash {
		b.WriteString("下面是同行原文。按洗稿来写，不要另写一篇。先用一句话写出这篇仍在让观众追问什么，再写口播。\n")
		b.WriteString("保留原稿的推进顺序、数字、历史例子、比喻、课名和上车结构。只改气口、标点和少量用词。如果听起来像换了一篇文章，就算失败。\n")
	} else {
		b.WriteString("下面是同行原文，只当证据，不当逐句模板。先用一句话写出这篇新稿仍在让观众追问什么，再写全新口播。\n")
		b.WriteString("同一条机器换一层完全不同的说法和例子。如果听起来还是原稿换词，就算失败。\n")
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
