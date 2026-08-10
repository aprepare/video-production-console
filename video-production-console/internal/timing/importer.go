package timing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"video-production-console/internal/domain"
)

const SkillTimingSchemaVersion = "1.0"

var skillTimingNames = map[string]string{
	"input_validation":     "输入校验",
	"media_search":         "素材检索",
	"production_plan":      "生产方案",
	"draft_build":          "草稿构建",
	"plaintext_validation": "明文草稿校验",
}

type ImportExpectation struct {
	TaskID          string
	SkillSnapshotID string
	Attempt         int
	NotBefore       time.Time
	NotAfter        time.Time
}

type timingArtifact struct {
	SchemaVersion   string        `json:"schema_version"`
	TaskID          string        `json:"task_id"`
	SkillSnapshotID string        `json:"skill_snapshot_id"`
	Attempt         int           `json:"attempt"`
	Phases          []timingPhase `json:"phases"`
}

type timingPhase struct {
	PhaseKey    string  `json:"phase_key"`
	DisplayName string  `json:"display_name"`
	Source      string  `json:"source"`
	State       string  `json:"state"`
	StartedAt   string  `json:"started_at"`
	FinishedAt  string  `json:"finished_at"`
	DurationMS  int64   `json:"duration_ms"`
	ExternalID  string  `json:"external_id"`
	Error       *string `json:"error"`
}

func LoadSkillTimings(path, expectedSHA256 string, expectation ImportExpectation) ([]domain.SkillTimingRun, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read execution timings: %w", err)
	}
	digest := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), strings.TrimSpace(expectedSHA256)) {
		return nil, fmt.Errorf("execution timings sha256 does not match file")
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var artifact timingArtifact
	if err := decoder.Decode(&artifact); err != nil {
		return nil, fmt.Errorf("decode execution timings: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode execution timings: multiple JSON values are not allowed")
		}
		return nil, fmt.Errorf("decode execution timings trailing data: %w", err)
	}
	if artifact.SchemaVersion != SkillTimingSchemaVersion {
		return nil, fmt.Errorf("execution timings schema_version must equal %q", SkillTimingSchemaVersion)
	}
	if artifact.TaskID != expectation.TaskID || artifact.SkillSnapshotID != expectation.SkillSnapshotID || artifact.Attempt != expectation.Attempt || artifact.Attempt <= 0 {
		return nil, fmt.Errorf("execution timings task, skill snapshot, or attempt identity mismatch")
	}
	if expectation.NotBefore.IsZero() || expectation.NotAfter.IsZero() || expectation.NotAfter.Before(expectation.NotBefore) {
		return nil, fmt.Errorf("valid task timing bounds are required")
	}
	seen := map[string]bool{}
	runs := make([]domain.SkillTimingRun, 0, len(artifact.Phases))
	for i, phase := range artifact.Phases {
		displayName, allowed := skillTimingNames[phase.PhaseKey]
		if !allowed || phase.DisplayName != displayName || phase.Source != string(domain.PhaseSourceSkill) {
			return nil, fmt.Errorf("execution timings phase %d has unsupported phase identity", i)
		}
		state := domain.TaskPhaseState(phase.State)
		if state != domain.PhaseCompleted && state != domain.PhaseFailed {
			return nil, fmt.Errorf("execution timings phase %d has invalid terminal state", i)
		}
		if strings.TrimSpace(phase.ExternalID) == "" || seen[phase.ExternalID] {
			return nil, fmt.Errorf("execution timings phase %d has empty or duplicate external_id", i)
		}
		seen[phase.ExternalID] = true
		started, err := time.Parse(time.RFC3339Nano, phase.StartedAt)
		if err != nil {
			return nil, fmt.Errorf("execution timings phase %d started_at: %w", i, err)
		}
		finished, err := time.Parse(time.RFC3339Nano, phase.FinishedAt)
		if err != nil {
			return nil, fmt.Errorf("execution timings phase %d finished_at: %w", i, err)
		}
		started, finished = started.UTC(), finished.UTC()
		if finished.Before(started) || started.Before(expectation.NotBefore) || finished.After(expectation.NotAfter) {
			return nil, fmt.Errorf("execution timings phase %d is outside task bounds", i)
		}
		duration := finished.Sub(started).Milliseconds()
		if phase.DurationMS < 0 || absoluteMillis(duration-phase.DurationMS) > 1000 {
			return nil, fmt.Errorf("execution timings phase %d duration is invalid", i)
		}
		if state == domain.PhaseCompleted && phase.Error != nil || state == domain.PhaseFailed && (phase.Error == nil || strings.TrimSpace(*phase.Error) == "") {
			return nil, fmt.Errorf("execution timings phase %d error does not match state", i)
		}
		detail := `{}`
		if phase.Error != nil {
			encoded, _ := json.Marshal(map[string]string{"error": *phase.Error})
			detail = string(encoded)
		}
		runs = append(runs, domain.SkillTimingRun{
			TaskID: artifact.TaskID, SkillSnapshotID: artifact.SkillSnapshotID, Attempt: artifact.Attempt,
			PhaseKey: phase.PhaseKey, DisplayName: phase.DisplayName, ExternalID: phase.ExternalID,
			DetailJSON: detail, State: state, StartedAt: started, FinishedAt: finished, DurationMS: duration,
		})
	}
	return runs, nil
}

func absoluteMillis(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return fmt.Errorf("execution timings JSON: %w", err)
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return fmt.Errorf("duplicate or invalid object key %q", key)
			}
			seen[key] = true
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected delimiter %q", delim)
	}
}
