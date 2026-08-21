package narration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
)

// WordTimingDocument is the versioned, serializable evidence from synthesis.
type WordTimingDocument struct {
	SchemaVersion int     `json:"schema_version"`
	Script        string  `json:"script"`
	Provider      string  `json:"provider"`
	Hash          string  `json:"hash,omitempty"`
	ScriptHash    string  `json:"script_hash"`
	Words         []Word  `json:"words"`
	Duration      float64 `json:"duration"`
}

// NewWordTimingDocument validates and copies vendor timings in original order.
func NewWordTimingDocument(script, provider, hash string, words []Word) (WordTimingDocument, error) {
	if len(words) == 0 {
		return WordTimingDocument{}, fmt.Errorf("word timings are empty")
	}
	copyWords := append([]Word(nil), words...)
	prevEnd := 0.0
	for i, w := range copyWords {
		if w.Text == "" {
			return WordTimingDocument{}, fmt.Errorf("word %d has empty text", i)
		}
		if math.IsNaN(w.StartTime) || math.IsNaN(w.EndTime) || math.IsInf(w.StartTime, 0) || math.IsInf(w.EndTime, 0) {
			return WordTimingDocument{}, fmt.Errorf("word %d has non-finite timing", i)
		}
		if w.StartTime < 0 || w.EndTime <= w.StartTime {
			return WordTimingDocument{}, fmt.Errorf("word %d has invalid timing", i)
		}
		if i > 0 && w.StartTime < prevEnd {
			return WordTimingDocument{}, fmt.Errorf("word %d overlaps or is out of order", i)
		}
		prevEnd = w.EndTime
	}
	if provider == "" {
		provider = "unknown"
	}
	sum := sha256.Sum256([]byte(script))
	return WordTimingDocument{SchemaVersion: 1, Script: script, Provider: provider, Hash: hash, ScriptHash: "sha256:" + hex.EncodeToString(sum[:]), Words: copyWords, Duration: prevEnd}, nil
}
