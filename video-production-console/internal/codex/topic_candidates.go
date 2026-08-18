package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"video-production-console/internal/domain"
)

type topicCandidatesArtifact struct {
	SchemaVersion string                   `json:"schema_version"`
	TaskID        string                   `json:"task_id"`
	SessionID     string                   `json:"session_id"`
	Candidates    []topicCandidateArtifact `json:"candidates"`
}

type topicCandidateArtifact struct {
	ID             string              `json:"id"`
	Topic          string              `json:"topic"`
	MotherTheme    string              `json:"mother_theme"`
	FamilyConflict string              `json:"family_conflict"`
	AnomalyFraming string              `json:"anomaly_framing"`
	NarrativeEntry string              `json:"narrative_entry"`
	Score          topicCandidateScore `json:"score"`
	SourceRefs     []string            `json:"source_refs"`
	FragmentRefs   []string            `json:"fragment_refs"`
}

type topicCandidateScore struct {
	Audience  float64 `json:"audience"`
	Evidence  float64 `json:"evidence"`
	Freshness float64 `json:"freshness"`
	Distance  float64 `json:"distance"`
	CourseFit float64 `json:"course_fit"`
	Total     float64 `json:"total"`
}

func loadTopicCandidates(path, taskID string) (string, []domain.IdeaCandidate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("read topic candidates: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var artifact topicCandidatesArtifact
	if err := decoder.Decode(&artifact); err != nil {
		return "", nil, fmt.Errorf("decode topic candidates: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return "", nil, fmt.Errorf("topic candidates contain trailing JSON")
	}
	if artifact.SchemaVersion != "2.0" || artifact.TaskID != taskID || strings.TrimSpace(artifact.SessionID) == "" {
		return "", nil, fmt.Errorf("topic candidates have invalid task or session metadata")
	}
	if len(artifact.Candidates) < 3 || len(artifact.Candidates) > 5 {
		return "", nil, fmt.Errorf("topic candidates count must be between 3 and 5")
	}
	out := make([]domain.IdeaCandidate, 0, len(artifact.Candidates))
	for i, candidate := range artifact.Candidates {
		if strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.Topic) == "" {
			return "", nil, fmt.Errorf("candidate %d is missing id or topic", i)
		}
		for field, value := range map[string]string{
			"topic": candidate.Topic, "mother_theme": candidate.MotherTheme,
			"family_conflict": candidate.FamilyConflict, "anomaly_framing": candidate.AnomalyFraming,
			"narrative_entry": candidate.NarrativeEntry,
		} {
			if containsEncodingCorruption(value) {
				return "", nil, fmt.Errorf("candidate %d field %s contains encoding corruption", i, field)
			}
		}
		summary := strings.TrimSpace(candidate.NarrativeEntry)
		if summary == "" {
			summary = strings.TrimSpace(strings.Join([]string{candidate.MotherTheme, candidate.FamilyConflict, candidate.AnomalyFraming}, "；"))
		}
		out = append(out, domain.IdeaCandidate{
			ID: candidate.ID, SessionID: artifact.SessionID, Position: i + 1,
			Title: candidate.Topic, Summary: summary, MotherTheme: candidate.MotherTheme,
			FamilyConflict: candidate.FamilyConflict, AnomalyFraming: candidate.AnomalyFraming,
			NarrativeEntry: candidate.NarrativeEntry, SourceRefs: append([]string(nil), candidate.SourceRefs...),
			FragmentRefs: append([]string(nil), candidate.FragmentRefs...), Score: candidate.Score.Total,
			Source: strings.Join(candidate.SourceRefs, ","),
		})
	}
	return artifact.SessionID, out, nil
}

func containsEncodingCorruption(value string) bool {
	return strings.Contains(value, "???") || strings.ContainsRune(value, '\uFFFD')
}
