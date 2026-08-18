package timing

import (
	"encoding/json"
	"strings"
)

// Notification is the transport-neutral observable portion of an App Server
// notification or legacy JSONL event. Params is inspected transiently and is
// never copied into a timing record.
type Notification struct {
	Method string
	Params json.RawMessage
}

type Boundary string

const (
	BoundaryStart     Boundary = "start"
	BoundaryComplete  Boundary = "complete"
	BoundaryFail      Boundary = "fail"
	BoundaryCancel    Boundary = "cancel"
	BoundaryInterrupt Boundary = "interrupt"
)

type Classification struct {
	PhaseKey       string
	DisplayName    string
	Boundary       Boundary
	ExternalItemID string
	DetailJSON     string
}

var phaseNames = map[string]string{
	"baokuan_search":       "爆款素材检索",
	"web_research":         "联网研究",
	"obsidian_dedup":       "Obsidian 去重",
	"media_search":         "媒体素材检索",
	"production_plan":      "生产计划校验",
	"plaintext_validation": "混剪草稿校验",
	"codex_execution":      "模型执行",
}

// Classify maps only explicitly recognized, observable protocol evidence.
// Unknown commands deliberately have no phase; in particular the outer
// run_montage_job.py command is excluded because its validated artifact owns
// the internal montage timings.
func Classify(notification Notification) (Classification, bool) {
	method := normalizeMethod(notification.Method)
	boundary, boundaryOK := classifyBoundary(method, notification.Params)
	if !boundaryOK {
		return Classification{}, false
	}
	var payload any
	if len(notification.Params) == 0 || json.Unmarshal(notification.Params, &payload) != nil {
		return Classification{}, false
	}
	if strings.HasPrefix(method, "turn/") {
		id := firstStringAt(payload, []string{"turn", "id"}, []string{"turnId"}, []string{"turn_id"})
		if id == "" {
			return Classification{}, false
		}
		return classified("codex_execution", boundary, id, "turn_boundary"), true
	}

	id := firstStringAt(payload, []string{"item", "id"}, []string{"itemId"}, []string{"item_id"})
	if id == "" {
		return Classification{}, false
	}
	searchable := strings.ToLower(strings.Join(flattenStrings(payload), "\n"))
	key := observablePhase(searchable)
	if key == "" {
		return Classification{}, false
	}
	return classified(key, boundary, id, "observable_tool_event"), true
}

func classified(key string, boundary Boundary, id, safeClass string) Classification {
	return Classification{
		PhaseKey: key, DisplayName: phaseNames[key], Boundary: boundary,
		ExternalItemID: strings.TrimSpace(id),
		DetailJSON:     `{"classification":"` + safeClass + `"}`,
	}
}

func normalizeMethod(method string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(method)), ".", "/")
}

func classifyBoundary(method string, raw json.RawMessage) (Boundary, bool) {
	switch {
	case strings.HasSuffix(method, "item/started"), strings.HasSuffix(method, "turn/started"):
		return BoundaryStart, true
	case strings.HasSuffix(method, "item/completed"), strings.HasSuffix(method, "turn/completed"):
		if hasObservableFailure(raw) {
			return BoundaryFail, true
		}
		return BoundaryComplete, true
	case strings.HasSuffix(method, "item/failed"), strings.HasSuffix(method, "turn/failed"):
		return BoundaryFail, true
	case strings.HasSuffix(method, "item/canceled"), strings.HasSuffix(method, "item/cancelled"), strings.HasSuffix(method, "turn/canceled"), strings.HasSuffix(method, "turn/cancelled"):
		return BoundaryCancel, true
	case strings.HasSuffix(method, "turn/interrupted"), strings.HasSuffix(method, "item/interrupted"):
		return BoundaryInterrupt, true
	default:
		return "", false
	}
}

func observablePhase(s string) string {
	switch {
	case strings.Contains(s, "run_montage_job.py"):
		return ""
	case strings.Contains(s, "baokuan") && strings.Contains(s, "mcp"):
		return "baokuan_search"
	case strings.Contains(s, "grok_search.py"):
		return "web_research"
	case (strings.Contains(s, "file_read") || strings.Contains(s, "read")) && ((strings.Contains(s, "obsidian") && strings.Contains(s, "recent") && strings.Contains(s, "card")) || strings.Contains(s, "recent-cards") || strings.Contains(s, "recent_cards")):
		return "obsidian_dedup"
	case strings.Contains(s, "download_pexels_media.py"), (strings.Contains(s, "media") || strings.Contains(s, "pexels")) && (strings.Contains(s, "index.json") || strings.Contains(s, "index.md")):
		return "media_search"
	case strings.Contains(s, "validate_production_plan.py"):
		return "production_plan"
	case strings.Contains(s, "validate_montage_draft.py"):
		return "plaintext_validation"
	default:
		return ""
	}
}

func hasObservableFailure(raw json.RawMessage) bool {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return false
	}
	if status, _ := payload["status"].(string); strings.EqualFold(strings.TrimSpace(status), "failed") {
		return true
	}
	if observableError(payload["error"]) {
		return true
	}
	if turn, ok := payload["turn"].(map[string]any); ok {
		if status, _ := turn["status"].(string); strings.EqualFold(strings.TrimSpace(status), "failed") {
			return true
		}
		return observableError(turn["error"])
	}
	return false
}

func observableError(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case map[string]any:
		return len(typed) != 0
	default:
		return false
	}
}

func flattenStrings(value any) []string {
	var out []string
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case string:
			out = append(out, typed)
		case []any:
			for _, item := range typed {
				visit(item)
			}
		case map[string]any:
			for key, item := range typed {
				out = append(out, key)
				visit(item)
			}
		}
	}
	visit(value)
	return out
}

func firstStringAt(value any, paths ...[]string) string {
	for _, path := range paths {
		current := value
		for _, key := range path {
			object, ok := current.(map[string]any)
			if !ok {
				current = nil
				break
			}
			current = object[key]
		}
		if text, ok := current.(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}
