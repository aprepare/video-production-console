package openaicompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Read only the new planning contract. Legacy hook analysis remains a historical
// artifact; its replication_guide must not become a new review instruction.
func readWritingPlan(dir string) json.RawMessage {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	for _, name := range []string{"node_output_hook.json", "hook_analysis.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		objects := completeJSONObjects(string(raw))
		for i := len(objects) - 1; i >= 0; i-- {
			object := objects[i]
			var plan map[string]json.RawMessage
			if json.Unmarshal([]byte(object), &plan) != nil {
				continue
			}
			var question string
			if json.Unmarshal(plan["core_question"], &question) != nil || strings.TrimSpace(question) == "" {
				continue
			}
			out := make(map[string]json.RawMessage)
			for _, key := range []string{"core_question", "opening_beats", "body_beats", "comment", "course_bridge", "ending_action"} {
				if value, ok := plan[key]; ok {
					out[key] = value
				}
			}
			encoded, err := json.Marshal(out)
			if err == nil {
				return encoded
			}
		}
	}
	return nil
}

// These are absence hints, never a semantic quality verdict or a blocking gate.
// The reviewer checks whether the question is answerable and the action natural.
func structureAdvisories(script string) []string {
	var hints []string
	commentMarker := false
	for _, marker := range []string{"评论", "留言", "留个言", "回复"} {
		if strings.Contains(script, marker) {
			commentMarker = true
			break
		}
	}
	if !commentMarker {
		hints = append(hints, "评论引导待检查：未发现明确评论/留言信号，请确认正文有一次自然的祝福互动并接回下一段；用户另有要求时按用户要求，不补理财问卷。")
	}
	tail := []rune(script)
	if len(tail) > 700 {
		tail = tail[len(tail)-700:]
	}
	if !strings.Contains(string(tail), "橱窗") {
		hints = append(hints, "课尾购买动作待检查：请确认结尾清楚指向主页橱窗，购买动作后可接自然关注理由；这是字面线索，按完整语意判断。")
	}
	return hints
}
