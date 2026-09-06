package openaicompat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	ServiceTier       string
	// DefaultModel / DefaultEffort 是设置页/模型档默认，给审稿等节点在
	// 自身配置留空时用。不能回落到 Model（那是写手槽位，改写手不应改审稿）。
	DefaultModel  string
	DefaultEffort string
	// CheckModel is retained for older callers; mechanical review is retired.
	CheckModel   string
	BaseURL      string
	APIKey       string
	CopyBaseURL  string
	CopyAPIKey   string
	MaxSteps     int
	PythonBinary string
	Client       ChatClient
	CopyClient   CopyClient
	// Pipeline 为 PipelineMultiAgent 时，写手动笔前先做一次二创策划。
	// 产出注入写手上下文。仅 rewrite 生效。
	Pipeline string
	// ReviewerEnabled 打开后，写手成稿直接交给审稿agent终审：
	// 只修违规处并留 draft_v1.json / review.json 双版本产物。仅 rewrite 生效。
	ReviewerEnabled bool
	// 各路agent系统提示词的覆盖文本；为空用内置默认。由创作台的
	// 「Agent提示词」编辑器提供，让操作员不改代码就能调agent行为。
	HookSystemPrompt         string
	FactsSearchSystemPrompt  string
	FactsOfflineSystemPrompt string
	AmmoSystemPrompt         string
	ReviewerSystemPrompt     string
	// WorkflowJSON 非空时按工作流快照执行（节点图驱动情报agent与审稿），
	// 优先于 Pipeline/ReviewerEnabled 的固定管线开关。仅 rewrite 生效。
	WorkflowJSON string
	// Search* 是事实核查agent用的联网搜索通道（OpenAI 兼容端点，如 Grok）。
	// 未配置时事实agent降级为离线盘点，不编造新数据。
	SearchBaseURL string
	SearchAPIKey  string
	SearchModel   string
	SearchClient  ChatClient
	// IntelFileDir 是工作流 agent 节点 {{file:名字}} 占位符的取文件目录
	// （素材库等运行时从磁盘读最新内容，不用改提示词）。为空则占位符不解析。
	IntelFileDir string
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
		RevisionNotes     string `json:"revision_notes"`
		RemixPromptStyle  string `json:"remix_prompt_style"`
		RemixSystemPrompt string `json:"remix_system_prompt"`
		RemixUserPrompt   string `json:"remix_user_prompt"`
		RemixPromptStamp  string `json:"remix_prompt_stamp"`
	} `json:"non_secret_settings"`
}

const (
	PromptStyleRewrite      = "rewrite"
	PromptStyleRewriteSharp = "rewrite_sharp"
	PromptStyleCopy         = "copy"
	PromptStyleWash         = "wash"

	// RewritePromptStamp 是默认 rewrite 系统提示词的版本标注。
	// 默认 rewrite：短成功标准、无开场禁词死刑、无后台质检。
	// 实际 stamp 文案在 prompts_writer.go。
	//   rewrite       = 短成功标准、无开场禁词死刑、无后台质检
	//   rewrite_sharp = B 锋利优先（冲击力第一、禁令压缩）
	//   copy          = 口播copy整理（先打 hooks/scripts）
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

// captureWriterPrompts persists the exact system/user messages sent to the model
// so operators can inspect and iterate prompts from the task detail UI.
func captureWriterPrompts(outputDir, system, user string) {
	if strings.TrimSpace(outputDir) == "" {
		return
	}
	_ = os.MkdirAll(outputDir, 0o755)
	_ = os.WriteFile(filepath.Join(outputDir, "prompt_system.txt"), []byte(system), 0o644)
	_ = os.WriteFile(filepath.Join(outputDir, "prompt_user.txt"), []byte(user), 0o644)
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
	stamp := promptStamp(style)
	if override := strings.TrimSpace(manifest.NonSecretSettings.RemixPromptStamp); override != "" {
		stamp = override
	}
	var system, user string
	if override := strings.TrimSpace(manifest.NonSecretSettings.RemixSystemPrompt); override != "" {
		system = override
		if tmpl := strings.TrimSpace(manifest.NonSecretSettings.RemixUserPrompt); tmpl != "" {
			user = renderWriterUserTemplate(tmpl, source, manifest.NonSecretSettings.RevisionNotes)
		} else {
			user = buildWriterUser(style, manifest, source)
		}
	} else if style == PromptStyleCopy {
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
	pipeline := strings.ToLower(strings.TrimSpace(opts.Pipeline))
	spec, hasSpec := parseFlowSpec(opts.WorkflowJSON)
	if hasSpec {
		if node := spec.writer(); node != nil {
			if strings.TrimSpace(node.Config.SystemPrompt) != "" {
				system = node.Config.SystemPrompt
			}
			if strings.TrimSpace(node.Config.UserTemplate) != "" {
				user = renderWriterUserTemplate(node.Config.UserTemplate, source, manifest.NonSecretSettings.RevisionNotes)
			}
		}
	}
	if style == PromptStyleRewrite {
		if hasSpec {
			// 工作流快照驱动：agent 节点按图分波并行，输出按连线注入写手。
			if intel := runFlowAgents(client, opts, source, manifest.OutputDir, spec); intel != "" {
				user = injectIntel(user, intel)
			}
		} else if pipeline == PipelineMultiAgent {
			if intel := runIntelPhase(client, opts, source, manifest.OutputDir); intel != "" {
				user = injectIntel(user, intel)
			}
		}
	}
	if style == PromptStyleRewrite {
		ctx := currentEditorialContext(manifest.OutputDir)
		if hasSpec && spec.EditorialRules != nil {
			ctx.Policy = *spec.EditorialRules
			ctx.CustomPolicy = true
		}
		if node := spec.reviewer(); hasSpec && node != nil {
			ctx.ReviewerUser = node.Config.UserTemplate
		}
		_, _, _, reviewerPrompt := resolveReviewerSettings(opts, specOrNil(spec, hasSpec), model, opts.ReasoningEffort)
		ctx.ReviewerPrompt = override(reviewerPrompt, reviewerRolePrompt)
		if err := saveEditorialContext(manifest.OutputDir, ctx); err != nil {
			return writeFailure(outPath, manifestPath, err)
		}
		system = withEditorialPolicy(system, ctx.Policy)
		system += "\n\n" + WriterJSONContract
	}
	captureWriterPrompts(manifest.OutputDir, system, user)
	appendRemixRunLog(manifest.OutputDir, map[string]any{
		"event": "prompt_selected", "prompt_style": style, "prompt_stamp": stamp,
		"pipeline": pipeline, "system_bytes": len(system), "user_bytes": len(user),
	})

	baseClient := client
	client = withServiceTier(baseClient, opts.ServiceTier)
	writerStarted := time.Now()
	resp, err := client.Chat(ChatRequest{
		Model:           model,
		ReasoningEffort: strings.TrimSpace(opts.ReasoningEffort),
		Stream:          true,
		Messages:        []Message{{Role: "system", Content: system}, {Role: "user", Content: user}},
	})
	writerMillis := time.Since(writerStarted).Milliseconds()
	if err != nil {
		appendRemixRunLog(manifest.OutputDir, map[string]any{
			"event": "model_error", "model": model, "prompt_style": style, "prompt_stamp": stamp, "error": err.Error(), "ms": writerMillis,
		})
		return writeFailure(outPath, manifestPath, err)
	}
	if len(resp.Choices) == 0 {
		appendRemixRunLog(manifest.OutputDir, map[string]any{
			"event": "empty_choices", "model": model, "prompt_style": style, "prompt_stamp": stamp, "ms": writerMillis,
		})
		return writeFailure(outPath, manifestPath, fmt.Errorf("empty chat choices"))
	}
	// 写手首稿耗时，运行视图据此显示用时。
	appendRemixRunLog(manifest.OutputDir, map[string]any{
		"event": "writer", "model": model, "ms": writerMillis, "effort": strings.TrimSpace(opts.ReasoningEffort),
		"requested_service_tier": opts.ServiceTier,
	})
	rawReply := resp.Choices[0].Message.Content
	captureRemixModelReply(manifest.OutputDir, model, style, stamp, rawReply)
	// 不按篇幅、重合率、数字锁词或发布字段触发返工；保留写手原始输出。
	content := rawReply
	if style == PromptStyleRewrite {
		// 审稿模型直接接收写手首稿，保留修改前后供人工定稿。
		// 工作流快照存在时由快照决定审稿节点有无与提示词；否则看固定开关。
		// 审稿通道或结构解析失败时保留待审稿，允许人工处理或重试。
		runReview, reviewerModel, reviewerEffort, reviewerPrompt := resolveReviewerSettings(opts, specOrNil(spec, hasSpec), model, opts.ReasoningEffort)
		if runReview {
			outcome := ReviewRemixDraft(ReviewOptions{
				Client:          baseClient,
				ServiceTier:     reviewerServiceTier(opts, specOrNil(spec, hasSpec)),
				Model:           reviewerModel,
				ReasoningEffort: reviewerEffort,
				SystemPrompt:    reviewerPrompt,
				EditorialRules:  spec.EditorialRules,
				Source:          source,
				DraftJSON:       content,
				OutputDir:       manifest.OutputDir,
				Round:           1,
			})
			if outcome.RevisedJSON != "" {
				content = outcome.RevisedJSON
			}
			if outcome.Record.Error != "" {
				preserveDraft(manifest.OutputDir, content)
				return writeFailure(outPath, manifestPath, fmt.Errorf("终审未完成，已保留待审稿：%s", outcome.Record.Error))
			}
		}
	}
	if err := writeRemixDeliverable(manifest.OutputDir, manifest.TaskID, action, content, nil, ""); err != nil {
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

func specOrNil(spec flowSpec, has bool) *flowSpec {
	if !has {
		return nil
	}
	return &spec
}

// resolveReviewerSettings 工作流审稿：节点自己的模型/强度 > 默认模型档；
// 不回落到写手槽位。旧固定管线（无快照）仍跟写手同一模型。
func resolveReviewerSettings(opts Options, spec *flowSpec, writerModel, writerEffort string) (run bool, model, effort, prompt string) {
	prompt = opts.ReviewerSystemPrompt
	if spec != nil {
		node := spec.reviewer()
		if node == nil {
			return false, "", "", ""
		}
		if p := strings.TrimSpace(node.Config.SystemPrompt); p != "" {
			prompt = p
		}
		model = strings.TrimSpace(node.Config.Model)
		if model == "" {
			model = strings.TrimSpace(opts.DefaultModel)
		}
		effort = strings.TrimSpace(node.Config.ReasoningEffort)
		if effort == "" {
			effort = strings.TrimSpace(opts.DefaultEffort)
		}
		return true, model, effort, prompt
	}
	if !opts.ReviewerEnabled {
		return false, "", "", ""
	}
	return true, writerModel, strings.TrimSpace(writerEffort), prompt
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
	system := spokenlines.SystemPrompt
	captureWriterPrompts(manifest.OutputDir, system, spokenlines.UserPrompt(source))
	// 切句只看局部上下文：长文案按句子边界切块并发请求，按序拼回。
	// 任何一块失败整体失败，不拼半篇稿子。
	chunks := spokenlines.SplitForParallel(source)
	if len(chunks) == 0 {
		return fail(fmt.Errorf("spoken source is empty"))
	}
	results := make([]string, len(chunks))
	errs := make([]error, len(chunks))
	chunkMillis := make([]int64, len(chunks))
	spokenStarted := time.Now()
	var wg sync.WaitGroup
	for i, chunk := range chunks {
		wg.Add(1)
		go func(i int, chunk string) {
			defer wg.Done()
			started := time.Now()
			defer func() { chunkMillis[i] = time.Since(started).Milliseconds() }()
			resp, err := client.Chat(ChatRequest{
				Model:           model,
				ReasoningEffort: strings.TrimSpace(opts.ReasoningEffort),
				Stream:          true,
				Messages: []Message{
					{Role: "system", Content: system},
					{Role: "user", Content: spokenlines.UserPrompt(chunk)},
				},
			})
			if err != nil {
				errs[i] = err
				return
			}
			if len(resp.Choices) == 0 {
				errs[i] = fmt.Errorf("empty chat choices")
				return
			}
			results[i] = strings.TrimSpace(resp.Choices[0].Message.Content)
		}(i, chunk)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return fail(fmt.Errorf("spoken chunk %d/%d: %w", i+1, len(chunks), err))
		}
	}
	// 每块各自耗时 vs 总耗时：并行生效时总耗时≈最慢那块，而不是各块之和。
	appendRemixRunLog(manifest.OutputDir, map[string]any{
		"event": "spoken_lines", "chunks": len(chunks), "model": model,
		"effort":   strings.TrimSpace(opts.ReasoningEffort),
		"chunk_ms": chunkMillis, "total_ms": time.Since(spokenStarted).Milliseconds(),
	})
	if err := writeSpokenDeliverable(manifest.OutputDir, manifest.TaskID, strings.Join(results, "\n")); err != nil {
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
	system := spokenlines.KeywordSystemPrompt
	user := spokenlines.KeywordUserPrompt(lines)
	captureWriterPrompts(manifest.OutputDir, system, user)
	resp, err := client.Chat(ChatRequest{
		Model:           model,
		ReasoningEffort: strings.TrimSpace(opts.ReasoningEffort),
		Stream:          true,
		Messages: []Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
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
		"titles 留空数组。short_titles 恰好3条、每条6到15个字、不要#，三条用三种不同钩子：第1条数字或日期砸脸（能当画面主标题，如「9月1日起你的数据能换钱了」）、第2条反常识或反问（如「五次机会你抓住过几回」）、第3条人群圈定或结果（如「这次不用本金也能进场」）；禁止通用口号（「普通人的新窗口」「窗口不会等人」「财富密码」），禁止截原文首句。descriptions 2到3条，每条一句话不超过40个字，各带一个具体钩子（日期、数字、反问、对号入座），三条钩子不重样，不复述正文，不写#话题（系统会追加）；禁止总结式空话（「看懂的人先拿位置」「答案先留着」）。topics 3到4个带#：第1个从 #财经 #经济 #理财 里选一个，其余必须是正文里真正出现过的名词（如 #数据资产 #存款利率 #楼市 #房贷），禁止 #认知 #思维认知 #干货分享 #认知觉醒 #宏观趋势 这类空泛词。发布字段里修辞性小数字用汉字（第六次、十个里八个、五块钱），年份日期金额用阿拉伯数字。cta 必须留空字符串。发布文案不要写课程名、主页橱窗、上车、推广期、几块钱。口播正文仍可按硬性保留收口到课程，但 titles / short_titles / descriptions / cta 一律不写推广。\n"
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

func renderWriterUserTemplate(tmpl, source, notes string) string {
	noteText := ""
	if strings.TrimSpace(notes) != "" {
		noteText = "修改要求：\n" + notes + "\n"
	}
	out := strings.NewReplacer("{{SOURCE}}", source, "{{NOTES}}", noteText).Replace(tmpl)
	if !strings.Contains(tmpl, "{{SOURCE}}") {
		out += "\n\n# 同行原文\n" + source
	}
	if !strings.Contains(tmpl, "{{NOTES}}") && noteText != "" {
		out += "\n\n" + noteText
	}
	return out
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
	envelope := map[string]any{
		"schema_version": "2.0",
		"task_id":        taskID,
		"action":         action,
		"status":         "failed",
		"summary":        cause.Error(),
		"questions":      []any{},
		"artifacts":      failureArtifacts(action, outputDir),
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

func failureArtifacts(action, outputDir string) []map[string]string {
	switch strings.TrimSpace(action) {
	case string(domain.ActionRemixStandard), string(domain.ActionRemixEnhanced), string(domain.ActionRemixFromTopic), string(domain.ActionRemixReview):
		artifacts := remixDeliverableArtifacts(outputDir, nil)
		if artifacts == nil {
			return []map[string]string{}
		}
		return artifacts
	default:
		return []map[string]string{}
	}
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
