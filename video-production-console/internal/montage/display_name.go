package montage

import (
	"strings"
	"time"
	"unicode"
)

const windowsReservedDraftCharacters = `<>:"/\|?*`

// BuildDraftDisplayName returns presentation metadata for a Jianying draft.
// It does not determine the task workspace or registered directory name.
// The suffix is the local creation time (MMDD-HHMM) so drafts sort readably
// in the Jianying list.
func BuildDraftDisplayName(account, project, shortTitle string, createdAt time.Time) string {
	label := strings.TrimSpace(shortTitle)
	if label == "" {
		label = strings.TrimSpace(project)
	}
	account = sanitizeDraftLabel(account, 24)
	label = sanitizeDraftLabel(label, 36)
	return strings.Join([]string{
		fallbackDraftLabel(account, "未命名账号"),
		fallbackDraftLabel(label, "未命名项目"),
		createdAt.Local().Format("0102-1504"),
	}, "_")
}

func sanitizeDraftLabel(value string, maxRunes int) string {
	var sanitized []rune
	previousUnderscore := false
	for _, current := range value {
		if current < 0x20 || strings.ContainsRune(windowsReservedDraftCharacters, current) {
			continue
		}
		if current == '_' {
			if previousUnderscore {
				continue
			}
			previousUnderscore = true
		} else {
			previousUnderscore = false
		}
		sanitized = append(sanitized, current)
	}
	sanitized = trimDraftLabel(sanitized)
	if len(sanitized) > maxRunes {
		sanitized = trimDraftLabel(sanitized[:maxRunes])
	}
	return string(sanitized)
}

func trimDraftLabel(value []rune) []rune {
	start, end := 0, len(value)
	for start < end && (value[start] == '.' || unicode.IsSpace(value[start])) {
		start++
	}
	for end > start && (value[end-1] == '.' || unicode.IsSpace(value[end-1])) {
		end--
	}
	return value[start:end]
}

func fallbackDraftLabel(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

