package timing

import (
	"encoding/json"
	"testing"
)

func TestClassifyObservableToolEvidence(t *testing.T) {
	tests := []struct {
		name, method, params, key string
	}{
		{"baokuan MCP", "item/started", `{"item":{"id":"mcp-1","type":"mcp_tool_call","server":"baokuan","tool":"search"}}`, "baokuan_search"},
		{"grok command", "item/started", `{"item":{"id":"cmd-1","type":"command_execution","command":"python grok_search.py --query secret"}}`, "web_research"},
		{"Obsidian recent cards", "item/started", `{"item":{"id":"read-1","type":"file_read","path":"vault/recent-cards.json"}}`, "obsidian_dedup"},
		{"Pexels downloader", "item/started", `{"item":{"id":"media-1","type":"command_execution","command":"python download_pexels_media.py"}}`, "media_search"},
		{"media index read", "item/started", `{"item":{"id":"media-2","type":"file_read","path":"output/media-index.json"}}`, "media_search"},
		{"production plan validator", "item/started", `{"item":{"id":"plan-1","command":"python validate_production_plan.py output/production_plan.json"}}`, "production_plan"},
		{"draft validator", "item/started", `{"item":{"id":"draft-1","command":"python validate_montage_draft.py --input draft.txt"}}`, "plaintext_validation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Classify(Notification{Method: tt.method, Params: json.RawMessage(tt.params)})
			if !ok || got.PhaseKey != tt.key || got.Boundary != BoundaryStart || got.ExternalItemID == "" {
				t.Fatalf("classification=%+v ok=%t", got, ok)
			}
			if got.DetailJSON != `{"classification":"observable_tool_event"}` {
				t.Fatalf("unsafe or unstable detail_json=%q", got.DetailJSON)
			}
		})
	}
}

func TestClassifyRejectsUnknownAndOuterMontageCommand(t *testing.T) {
	for _, params := range []string{
		`{"item":{"id":"unknown","command":"python something_else.py --api-key top-secret"}}`,
		`{"item":{"id":"outer","command":"python run_montage_job.py --prompt full-secret-prompt"}}`,
		`{"item":{"id":"prompt-only","type":"agent_message","text":"please check Obsidian recent cards"}}`,
	} {
		if got, ok := Classify(Notification{Method: "item/started", Params: json.RawMessage(params)}); ok || got.PhaseKey != "" {
			t.Fatalf("unexpected classification=%+v ok=%t", got, ok)
		}
	}
}

func TestClassifyTurnBoundariesAndTerminalState(t *testing.T) {
	tests := []struct {
		method, params string
		boundary       Boundary
	}{
		{"turn/started", `{"turn":{"id":"turn-1"}}`, BoundaryStart},
		{"turn/completed", `{"turn":{"id":"turn-1"}}`, BoundaryComplete},
		{"turn/completed", `{"turn":{"id":"turn-1","error":null}}`, BoundaryComplete},
		{"turn/completed", `{"turn":{"id":"turn-1","error":{"message":"failed"}}}`, BoundaryFail},
		{"turn/canceled", `{"turn":{"id":"turn-1"}}`, BoundaryCancel},
	}
	for _, tt := range tests {
		got, ok := Classify(Notification{Method: tt.method, Params: json.RawMessage(tt.params)})
		if !ok || got.PhaseKey != "codex_execution" || got.Boundary != tt.boundary || got.ExternalItemID != "turn-1" {
			t.Fatalf("%s classification=%+v ok=%t", tt.method, got, ok)
		}
	}
}

func TestClassifyCompletionUsesSameExternalItemIdentity(t *testing.T) {
	params := json.RawMessage(`{"item":{"id":"cmd-7","type":"command_execution","command":"python grok_search.py"}}`)
	started, ok := Classify(Notification{Method: "item/started", Params: params})
	if !ok {
		t.Fatal("start was not classified")
	}
	completed, ok := Classify(Notification{Method: "item/completed", Params: params})
	if !ok || completed.Boundary != BoundaryComplete || completed.ExternalItemID != started.ExternalItemID || completed.PhaseKey != started.PhaseKey {
		t.Fatalf("start=%+v completion=%+v ok=%t", started, completed, ok)
	}
}
