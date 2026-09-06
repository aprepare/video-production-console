package openaicompat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 审稿agent：写手成稿直接把原文、成稿和共同规则
// 清单交给审稿模型终审——只修违规处，不重写、不动写手的口气。首轮在 Run
// 里自动执行；操作员在创作台批注后的「打回重做」调用 ReviewRemixDraft 再走
// 一轮。审稿失败保留进审稿，review.json 记录错误，让操作员处理或重试。

const reviewerSystemPrompt = reviewerRolePrompt

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
	Warnings    []string      `json:"warnings,omitempty"`
	At          string        `json:"at"`
	// Before 是本轮进审稿，Revised 是审稿修订稿（只在 verdict=fixed 时有）。
	// 两版都带正文和全部发布字段：操作员要并排看审稿前后自己定夺采用哪版，
	// 而定稿一旦被手改，修订稿就无处可寻，所以结论里自带两版快照。
	Before  *remixDraft `json:"before,omitempty"`
	Revised *remixDraft `json:"revised,omitempty"`
}

type ReviewOptions struct {
	EditorialRules  *string
	UserTemplate    string
	Client          ChatClient
	BaseURL         string
	APIKey          string
	Model           string
	ReasoningEffort string
	ServiceTier     string
	// SystemPrompt 非空时覆盖内置审稿提示词（创作台「Agent提示词」编辑器）。
	SystemPrompt string
	// Source 是同行原文；DraftJSON 是进审稿（写手 JSON，含正文和发布字段）。
	Source    string
	DraftJSON string
	// FactsJSON 是结构化事实（facts_research.json），供审稿对照事实与数字。
	FactsJSON string
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
	if round == 1 && strings.TrimSpace(opts.OutputDir) != "" {
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
	ctx := loadEditorialContext(opts.OutputDir)
	// 旧运行续审沿用事实与自定义编辑要求，使用现行规则；不修改历史上下文文件。
	if opts.EditorialRules != nil {
		ctx.Policy = *opts.EditorialRules
		ctx.CustomPolicy = true
	} else if !ctx.CustomPolicy {
		ctx.Policy = SharedEditorialPolicy
	}
	if strings.TrimSpace(opts.UserTemplate) != "" {
		ctx.ReviewerUser = opts.UserTemplate
	}
	if _, err := os.Stat(filepath.Join(opts.OutputDir, "editorial_context.json")); err != nil || opts.OutputDir == "" {
		ctx.ReviewerPrompt = override(opts.SystemPrompt, reviewerRolePrompt)
		if strings.TrimSpace(opts.FactsJSON) != "" {
			ctx.Facts = json.RawMessage(normalizeFacts(opts.FactsJSON))
		}
		if err := saveEditorialContext(opts.OutputDir, ctx); err != nil {
			record.Verdict = "error"
			record.Error = "审稿上下文保存失败：" + err.Error()
			return finish("")
		}
	}
	facts := string(ctx.Facts)
	system := withEditorialPolicy(ctx.ReviewerPrompt, ctx.Policy) + "\n\n" + ReviewerJSONContract
	user := reviewUserFromTemplate(ctx.ReviewerUser, opts.Source, string(canonical), annotations, facts, string(ctx.WritingPlan))
	if opts.OutputDir != "" {
		_ = os.WriteFile(filepath.Join(opts.OutputDir, fmt.Sprintf("review_prompt_system_round_%d.txt", round)), []byte(system), 0644)
		_ = os.WriteFile(filepath.Join(opts.OutputDir, fmt.Sprintf("review_prompt_user_round_%d.txt", round)), []byte(user), 0644)
	}

	resp, chatErr := withServiceTier(client, opts.ServiceTier).Chat(ChatRequest{
		Model:           strings.TrimSpace(opts.Model),
		ReasoningEffort: strings.TrimSpace(opts.ReasoningEffort),
		Stream:          true,
		Messages: []Message{
			{Role: "system", Content: system},
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

	if reply.Verdict != "pass" && reply.Verdict != "fixed" {
		record.Verdict = "error"
		record.Error = fmt.Sprintf("非法审稿结论: %s", reply.Verdict)
		return finish("")
	}
	if reply.Verdict == "pass" {
		record.Verdict = reply.Verdict
		return finish("")
	}

	revisedScript := strings.TrimSpace(reply.Revised.ContinuousScript)
	if revisedScript == "" || len(reply.Issues) == 0 {
		record.Verdict = "error"
		record.Error = "fixed 结论缺少完整正文或修改依据。"
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
	if strings.TrimSpace(revised.CTA) == "" {
		revised.CTA = draft.CTA
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
	// Repair from the full reply: malformed quotes can also confuse brace extraction.
	if reply, ok := try(text); ok {
		return reply, nil
	}
	if repaired := repairReviewerJSON(raw); repaired != "" {
		if reply, ok := try(repaired); ok {
			return reply, nil
		}
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
	return reviewerRolePrompt + "\n\n" + SharedEditorialPolicy
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
