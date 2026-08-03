package codex

import (
	"encoding/json"
	"testing"
)

func TestParseLineMapsKnownEventsAndResults(t *testing.T) {
	e, err := ParseLine([]byte(`{"type":"thread.started","thread_id":"s1"}`))
	if err != nil || e.Kind != "thread_started" || e.SessionID != "s1" {
		t.Fatalf("event=%+v err=%v", e, err)
	}
	e, err = ParseLine([]byte(`{"type":"item.completed","item":{"type":"agent_message","text":"hello"}}`))
	if err != nil || e.Kind != "agent_message" || e.DisplayText != "hello" || e.AgentMessageText != "hello" || e.Level != "info" {
		t.Fatalf("event=%+v err=%v", e, err)
	}
	e, err = ParseLine([]byte(`{"type":"turn.completed","result":{"status":"completed","summary":"done","artifacts":[]}}`))
	if err != nil || e.Kind != "turn_completed" || e.FinalResult == nil || e.FinalResult.Status != "completed" {
		t.Fatalf("event=%+v err=%v", e, err)
	}
}

func TestParseLinePreservesExactRawAgentMessage(t *testing.T) {
	raw := []byte("  {\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"candidate\"}}  ")
	e, err := ParseLine(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(e.RawJSON) != string(raw) {
		t.Fatalf("raw=%q want=%q", e.RawJSON, raw)
	}
	if e.DisplayText != "candidate" || e.AgentMessageText != "candidate" {
		t.Fatalf("event=%+v", e)
	}
}

func TestParseLinePreservesUnknownAndWarnsMalformedJSON(t *testing.T) {
	e, err := ParseLine([]byte(`{"type":"new.event","x":1}`))
	if err != nil || e.Kind != "raw_event" || string(e.RawJSON) != `{"type":"new.event","x":1}` {
		t.Fatalf("event=%+v err=%v", e, err)
	}
	e, err = ParseLine([]byte(`not json`))
	if err != nil || e.Kind != "parse_warning" || e.Level != "warning" {
		t.Fatalf("event=%+v err=%v", e, err)
	}
}

func TestResultDecodesQuestionAndArtifacts(t *testing.T) {
	e, err := ParseLine([]byte(`{"type":"turn.completed","result":{"status":"needs_input","summary":"choose","question":{"text":"pick","options":["a"]},"artifacts":[{"type":"spoken_script","path":"x.txt","description":"x"}]}}`))
	if err != nil || e.FinalResult == nil || e.FinalResult.Question == nil || len(e.FinalResult.Artifacts) != 1 {
		t.Fatalf("event=%+v err=%v", e, err)
	}
	if _, err := json.Marshal(e.FinalResult); err != nil {
		t.Fatal(err)
	}
}
