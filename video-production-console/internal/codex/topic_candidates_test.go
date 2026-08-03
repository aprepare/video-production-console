package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTopicCandidatesMapsArtifactToIdeaCandidates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topic_candidates.json")
	data := `{"schema_version":"2.0","task_id":"task-1","session_id":"session-1","candidates":[{"id":"c1","topic":"主题一","mother_theme":"母题","family_conflict":"冲突","anomaly_framing":"反常","narrative_entry":"叙事入口","score":{"audience":1,"evidence":2,"freshness":3,"distance":4,"course_fit":5,"total":15},"source_refs":["source-a"],"fragment_refs":[]},{"id":"c2","topic":"主题二","mother_theme":"母题","family_conflict":"冲突","anomaly_framing":"反常","narrative_entry":"入口二","score":{"audience":1,"evidence":2,"freshness":3,"distance":4,"course_fit":5,"total":14},"source_refs":[],"fragment_refs":[]},{"id":"c3","topic":"主题三","mother_theme":"母题","family_conflict":"冲突","anomaly_framing":"反常","narrative_entry":"入口三","score":{"audience":1,"evidence":2,"freshness":3,"distance":4,"course_fit":5,"total":13},"source_refs":["source-c"],"fragment_refs":[]}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	session, candidates, err := loadTopicCandidates(path, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if session != "session-1" || len(candidates) != 3 || candidates[0].Title != "主题一" || candidates[0].Score != 15 || candidates[0].Source != "source-a" {
		t.Fatalf("session=%q candidates=%+v", session, candidates)
	}
}

func TestLoadTopicCandidatesRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topic_candidates.json")
	data := `{"schema_version":"2.0","task_id":"task-1","session_id":"session-1","extra":true,"candidates":[]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadTopicCandidates(path, "task-1"); err == nil {
		t.Fatal("expected strict decoder to reject unknown fields")
	}
}
