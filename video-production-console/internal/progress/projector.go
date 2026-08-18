package progress

import (
	"encoding/json"
	"strings"

	"video-production-console/internal/domain"
	phasetiming "video-production-console/internal/timing"
)

// Input is the minimal, transport-neutral data used to turn low-level Codex
// events into short messages that can be rendered in the console UI.
type Input struct {
	TaskID                              string
	Action                              domain.TaskAction
	Method, RawJSON, LegacyKind, Status string
}

// ProjectTiming delegates both App Server notifications and legacy JSONL to
// the same closed classifier. RawJSON is used transiently as evidence and is
// never included in the returned safe timing detail.
func ProjectTiming(in Input) (phasetiming.Classification, bool) {
	method := in.Method
	var envelope struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(in.RawJSON), &envelope) == nil && strings.TrimSpace(envelope.Type) != "" {
		method = envelope.Type
	}
	return phasetiming.Classify(phasetiming.Notification{Method: method, Params: json.RawMessage(in.RawJSON)})
}

type Event struct {
	Kind                            domain.SemanticEventKind
	Phase, DisplayText, CoalesceKey string
	Visible                         bool
}

// Project deliberately recognizes stable protocol words rather than trying to
// expose raw agent diagnostics. It is safe for both legacy JSONL and App
// Server notifications and keeps the normal progress view readable.
func isRemixAction(action domain.TaskAction) bool {
	switch action {
	case domain.ActionRemixStandard, domain.ActionRemixEnhanced, domain.ActionRemixFromTopic, domain.ActionRemixReview:
		return true
	default:
		return false
	}
}

func Project(in Input) Event {
	method := strings.ToLower(strings.TrimSpace(in.Method))
	raw := strings.ToLower(in.RawJSON)
	phase := "processing"
	text := ""
	switch {
	case in.Status == string(domain.TaskFailed) || strings.Contains(method, "failed"):
		return Event{Kind: domain.SemanticFailure, Phase: "failed", DisplayText: "任务未能完成", Visible: true}
	case method == "turn/completed" || in.Status == string(domain.TaskCompleted):
		if isRemixAction(in.Action) {
			return Event{Kind: domain.SemanticTurnCompleted, Phase: "completed", DisplayText: "二创文案已写完", Visible: true}
		}
		return Event{Kind: domain.SemanticTurnCompleted, Phase: "completed", DisplayText: "处理已结束", Visible: true}
	case isRemixAction(in.Action):
		return Event{Kind: domain.SemanticPhaseProgress, Phase: "processing", DisplayText: "正在写二创文案", CoalesceKey: in.TaskID + ":processing:" + string(domain.SemanticPhaseProgress), Visible: true}
	case strings.Contains(raw, "grok_search.py") || strings.Contains(raw, "grok_search"):
		return Event{Kind: domain.SemanticPhaseProgress, Phase: "research", DisplayText: "正在使用 Grok 联网核对最新信息", CoalesceKey: in.TaskID + ":research:" + string(domain.SemanticPhaseProgress), Visible: true}
	case strings.Contains(method, "item") && (strings.Contains(raw, "tool") || strings.Contains(raw, "command") || strings.Contains(raw, "function")):
		return Event{Kind: domain.SemanticToolActivity, Phase: "tool", DisplayText: "正在调用工具", Visible: true}
	case strings.Contains(method, "item") && (strings.Contains(raw, "agent_message") || strings.Contains(raw, "assistant_message")):
		return Event{Kind: domain.SemanticAssistantMessage, Phase: "assistant", DisplayText: "模型正在写稿", Visible: true}
	case strings.Contains(method, "question") || strings.Contains(raw, "question"):
		return Event{Kind: domain.SemanticQuestion, Phase: "input", DisplayText: "正在等待你的回复", Visible: true}
	case strings.Contains(raw, "baokuan") || strings.Contains(raw, "爆款"):
		phase, text = "research", "正在检索爆款库素材"
	case strings.Contains(raw, "jianying") || strings.Contains(raw, "剪映"):
		phase, text = "register", "正在登记剪映草稿"
	case in.Action == domain.ActionMontageExecute || strings.Contains(string(in.Action), "montage"):
		phase, text = "montage", "正在生成混剪草稿"
	case strings.Contains(method, "started") || strings.Contains(method, "progress") || strings.Contains(method, "updated"):
		phase, text = "processing", "正在处理任务"
	default:
		return Event{}
	}
	return Event{Kind: domain.SemanticPhaseProgress, Phase: phase, DisplayText: text, CoalesceKey: in.TaskID + ":" + phase + ":" + string(domain.SemanticPhaseProgress), Visible: true}
}

// RedactedRaw intentionally declines to return an event payload. Raw protocol
// messages can contain prompts, file paths, or tool arguments; diagnostics use
// the projected event and explicit task error fields instead.
func RedactedRaw(raw string) string {
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return ""
	}
	return ""
}
