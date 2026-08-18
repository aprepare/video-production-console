package publishing

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var ErrRemixJSONMissingScript = errors.New("remix json missing continuous_script")

type remixDraft struct {
	ContinuousScript string   `json:"continuous_script"`
	Titles           []string `json:"titles"`
	ShortTitles      []string `json:"short_titles"`
	Descriptions     []string `json:"descriptions"`
	Topics           []string `json:"topics"`
	CTA              string   `json:"cta"`
}

// UnwrapContinuousScript turns a pasted remix JSON object into the spoken
// continuous script plus an optional publishing package. Plain prose is
// returned unchanged.
func UnwrapContinuousScript(raw string) (string, *Package, bool, error) {
	text := strings.TrimSpace(stripCodeFence(raw))
	if text == "" {
		return "", nil, false, nil
	}
	if !strings.HasPrefix(text, "{") {
		return text, nil, false, nil
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return "", nil, false, ErrRemixJSONMissingScript
	}
	var draft remixDraft
	if err := json.Unmarshal([]byte(text[start:end+1]), &draft); err != nil {
		return "", nil, false, ErrRemixJSONMissingScript
	}
	script := strings.TrimSpace(draft.ContinuousScript)
	if script == "" {
		return "", nil, false, ErrRemixJSONMissingScript
	}
	pkg := packageFromDraft(draft)
	if pkg.empty() {
		return script, nil, true, nil
	}
	return script, &pkg, true, nil
}

func packageFromDraft(draft remixDraft) Package {
	descriptions := trimFilled(draft.Descriptions)
	description := ""
	if len(descriptions) > 0 {
		description = descriptions[0]
	}
	titles := trimFilled(draft.Titles)
	top := make([]TitleRecommendation, 0, 3)
	for i := 0; i < 3 && i < len(titles); i++ {
		top = append(top, TitleRecommendation{Rank: i + 1, Title: titles[i], Reason: "导入的发布标题。"})
	}
	return Package{
		Titles:       titles,
		TopTitles:    top,
		ShortTitles:  trimFilled(draft.ShortTitles),
		Descriptions: descriptions,
		Description:  description,
		Topics:       trimFilled(draft.Topics),
		CTA:          strings.TrimSpace(draft.CTA),
	}
}

func (p Package) empty() bool {
	return len(p.Titles) == 0 && len(p.ShortTitles) == 0 && len(p.Descriptions) == 0 &&
		len(p.Topics) == 0 && strings.TrimSpace(p.CTA) == "" && strings.TrimSpace(p.Description) == ""
}

func trimFilled(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func stripCodeFence(raw string) string {
	text := strings.TrimSpace(raw)
	if !strings.HasPrefix(text, "```") {
		return text
	}
	text = strings.TrimPrefix(text, "```")
	if nl := strings.Index(text, "\n"); nl >= 0 {
		text = text[nl+1:]
	}
	text = strings.TrimSuffix(text, "```")
	return strings.TrimSpace(text)
}

func WriteFile(path string, pkg Package) error {
	raw, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}
