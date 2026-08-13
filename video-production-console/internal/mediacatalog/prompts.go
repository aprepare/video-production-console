package mediacatalog

import (
	"fmt"
	"strings"
)

// AnalysisVersion pins the prompt wording and response schema. Re-running the
// same source hash with the same version must be idempotent (zero network
// requests), so any prompt change requires bumping this constant.
const AnalysisVersion = "vision-v1"

// visionSystemPrompt is fixed for AnalysisVersion. It demands visible facts
// only: no person identification, no sensitive-attribute inference, and no
// transcription of on-screen text.
const visionSystemPrompt = `You analyze 2-3 low-resolution keyframes that all belong to one single video shot.
Describe only facts that are visible in the frames.
Never identify, name, or guess the identity of any person.
Never infer sensitive attributes such as ethnicity, religion, health, political views, or sexual orientation.
Never transcribe, quote, or repeat any text visible inside the frames; only report its existence through has_text.
Respond with exactly one strict JSON object and nothing else, with exactly these fields:
{"summary":string,"mood":string,"setting":string,"people_count":integer,"motion_level":"static"|"low"|"medium"|"high","has_text":boolean,"tags":[{"value":string,"confidence":number}]}`

// visionUserInstruction accompanies the keyframe images in the user message.
const visionUserInstruction = "Analyze the attached keyframes of this single shot and return the JSON object."

// embeddingInputTemplate is the fixed template that turns one ShotAnalysis
// into the text embedded for semantic retrieval.
const embeddingInputTemplate = "summary: %s\ntags: %s\nmood: %s\nsetting: %s"

// EmbeddingInput renders the deterministic embedding text for one analysis.
// Tag order is preserved as returned by the analyzer so the same analysis
// always produces byte-identical input.
func EmbeddingInput(analysis ShotAnalysis) string {
	values := make([]string, 0, len(analysis.Tags))
	for _, tag := range analysis.Tags {
		values = append(values, tag.Value)
	}
	return fmt.Sprintf(embeddingInputTemplate,
		analysis.Summary, strings.Join(values, ", "), analysis.Mood, analysis.Setting)
}
