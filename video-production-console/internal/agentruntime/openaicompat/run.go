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
	APIKey     string
	CopyBaseURL string
	CopyAPIKey  string
	MaxSteps     int
	PythonBinary string
	Client       ChatClient
	CopyClient   CopyClient
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
	PromptStyleRewrite      = "rewrite"
	PromptStyleRewriteSharp = "rewrite_sharp"
	PromptStyleCopy         = "copy"

	// RewritePromptStamp 是默认 rewrite（语感回流 A）系统提示词的版本标注。
	// 写稿正文已迁到 prompts_writer.go：
	//   rewrite       = A 语感回流（默认，含语感指纹 + 正向验收）
	//   rewrite_sharp = B 锋利优先（更少禁令、冲击力优先）
	//   copy          = 口播copy整理（先打 hooks/scripts）
	// 2026-08-24 A/B 分版：补语感指纹与正向验收；B 版压缩禁令、抬高锋利目标。
	RewritePromptStamp = RewritePromptStampStable
)

func NormalizePromptStyle(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", PromptStyleRewrite:
		return PromptStyleRewrite, nil
	case PromptStyleRewriteSharp:
		return PromptStyleRewriteSharp, nil
	case PromptStyleCopy:
		return PromptStyleCopy, nil
	default:
		return "", fmt.Errorf("remix_prompt_style must be rewrite, rewrite_sharp, or copy")
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
	style, err := NormalizePromptStyle(manifest.NonSecretSettings.RemixPromptStyle)
	if err != nil {
		return writeFailure(outPath, manifestPath, err)
	}
	var system, user string
	if style == PromptStyleCopy {
		copyClient := opts.CopyClient
		if copyClient == nil {
			copyClient = &HTTPCopyClient{BaseURL: strings.TrimSpace(opts.CopyBaseURL), APIKey: strings.TrimSpace(opts.CopyAPIKey)}
		}
		hooks, scripts, err := fetchCopyMaterials(copyClient, source)
		if err != nil {
			return writeFailure(outPath, manifestPath, err)
		}
		system = buildAssemblePrompt()
		user = buildAssembleUser(manifest, source, hooks, scripts)
	} else {
		// rewrite（默认 A 语感回流）或 rewrite_sharp（B 锋利优先）
		system = buildWriterPrompt(style)
		user = buildWriterUser(style, manifest, source)
	}

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
		appendRemixRunLog(manifest.OutputDir, map[string]any{"event": "model_error", "model": model, "prompt_style": style, "error": err.Error()})
		return writeFailure(outPath, manifestPath, err)
	}
	if len(resp.Choices) == 0 {
		appendRemixRunLog(manifest.OutputDir, map[string]any{"event": "empty_choices", "model": model, "prompt_style": style})
		return writeFailure(outPath, manifestPath, fmt.Errorf("empty chat choices"))
	}
	rawReply := resp.Choices[0].Message.Content
	captureRemixModelReply(manifest.OutputDir, model, style, rawReply)
	content, checkWarnings, checkNote, err := repairRemixDraft(client, model, strings.TrimSpace(opts.CheckModel), strings.TrimSpace(opts.ReasoningEffort), system, user, source, rawReply)
	if err != nil {
		appendRemixRunLog(manifest.OutputDir, map[string]any{"event": "quality_failed", "error": err.Error(), "note": checkNote, "warnings": checkWarnings})
		if strings.TrimSpace(content) != "" && content != rawReply {
			if draft, parseErr := parseRemixDraft(content); parseErr == nil && strings.TrimSpace(draft.ContinuousScript) != "" {
				_ = os.WriteFile(filepath.Join(manifest.OutputDir, "continuous_script.txt"), []byte(strings.TrimSpace(draft.ContinuousScript)), 0o644)
			}
		}
		return writeFailure(outPath, manifestPath, err)
	}
	appendRemixRunLog(manifest.OutputDir, map[string]any{"event": "quality_passed", "note": checkNote, "warnings": checkWarnings})
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

// writerJSONContract is shared by rewrite A/B and copy assemble prompts.
func writerJSONContract() string {
	return "只返回一个 JSON 对象，不要 Markdown。字段：continuous_script, titles, short_titles, descriptions, topics, cta。\n" +
		"continuous_script 必须是完整连续口播正文。\n" +
		"titles、short_titles、descriptions 必须从这篇口播长出来，讲的是同一件事。禁止拿别的成稿标题来凑数，也不要用提示词里没有出现在原文里的情节做标题。\n" +
		"titles 8到12条。short_titles 恰好5条、每条最多15个字、不要#。descriptions 恰好3条，每条只用一到两句话概括这条视频、不超过40个字，不要复述正文段落。话题只能从这些热门标签里选3到4个：#经济 #思维认知 #认知 #宏观趋势 #思维 #干货分享 #认知觉醒。三条描述末尾都带这同一组标签，topics 也只用这组，不要自造其他#。cta 必须留空字符串。发布文案不要写课程名、主页橱窗、上车、推广期、几块钱。口播正文仍可按硬性保留收口到课程，但 titles / short_titles / descriptions / cta 一律不写推广。\n"
}

func buildAssemblePrompt() string {
	var b strings.Builder
	b.WriteString("你是财经视频号口播二创员。只写口播，不要调用工具，不要解释过程。\n")
	b.WriteString("对标文留下题材、钩子类型、关键数字、未揭晓的答案、课名收口。换词换说法。中段出场顺序可以跟对标文走，也可以重排，不要因为顺序相同判失败。\n")
	b.WriteString("钩子候选只用来锁开场力度和损失类型，不要整段贴进去。分镜脚本只当段落提纲：取信息点，丢掉镜头、括号、音效、时间轴，更不要把分镜里的口播原句念出来。\n")
	b.WriteString("每一句都要换词换说法。机构名、具体利率、单月少了多少这些数字原词保留，周围的句子必须重说。连续 8 个字和原文一样就计入字面重合，整篇必须低于 40%。\n")
	b.WriteString("禁止照搬金句、比喻和专属口头禅，例如后背发凉、无声迁徙、舔瓶盖、集体叛逃、当燃料、财富警觉这类现成表达，必须换成新的说法。\n")
	b.WriteString("开场切口必须和原稿前 80 字不同。禁止再问「2万亿/20500亿去了哪儿」，也禁止「不是买房不是炒股黄金没接住，那钱去哪了」这套切入口。钩子类型不许换成更软的损失。\n")
	b.WriteString("禁止写「50到77万亿」「50万亿到77万亿」「50–75万亿定存到期」这类到期总盘估算。可以说到期规模很大、分批出来，不要报这个区间。\n")
	b.WriteString("损失场景、悬念、适度焦虑必须留下。冲击句、共情句按对标文需要保留，不要当成禁写项删掉。\n")
	b.WriteString("课程名固定写成《财富觉醒方法论》，禁止带年份。全文课名一次、主页橱窗一次。卖课只在最末最多四句。\n")
	b.WriteString("cta 必须空字符串。发布外壳不要写课名、橱窗、几块钱。\n")
	b.WriteString(writerJSONContract())
	return b.String()
}

func buildAssembleUser(manifest manifestLite, source, hooks, scripts string) string {
	var b strings.Builder
	b.WriteString("按对标文写一篇全新口播：锁钩子类型，换开场切口，再逐句换词换说法。中段顺序可以跟对标文走。不要顺着分镜原句往下念，也不要同义改写原文第一句。\n")
	b.WriteString("标题和短标题必须跟这篇新口播走。\n")
	if notes := strings.TrimSpace(manifest.NonSecretSettings.RevisionNotes); notes != "" {
		b.WriteString("修改要求：\n")
		b.WriteString(notes)
		b.WriteString("\n")
	}
	b.WriteString("\n# 钩子候选\n")
	b.WriteString(strings.TrimSpace(hooks))
	b.WriteString("\n\n# 分镜脚本\n")
	b.WriteString(strings.TrimSpace(scripts))
	b.WriteString("\n\n# 同行原文（只作核对，不当逐句模板）\n")
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
	taskID, action, outputDir := identityFromManifest(manifestPath)
	if strings.TrimSpace(outputDir) != "" {
		appendRemixRunLog(outputDir, map[string]any{"event": "failed", "error": cause.Error()})
	}
	artifacts := remixDeliverableArtifacts(outputDir, nil)
	if artifacts == nil {
		artifacts = []map[string]string{}
	}
	envelope := map[string]any{
		"schema_version": "2.0",
		"task_id":        taskID,
		"action":         action,
		"status":         "failed",
		"summary":        cause.Error(),
		"questions":      []any{},
		"artifacts":      artifacts,
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

func identityFromManifest(path string) (taskID, action, outputDir string) {
	action = "remix.standard"
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", action, ""
	}
	var manifest manifestLite
	if json.Unmarshal(stripBOM(raw), &manifest) != nil {
		return "", action, ""
	}
	if manifest.Action != "" {
		action = manifest.Action
	}
	return manifest.TaskID, action, strings.TrimSpace(manifest.OutputDir)
}
