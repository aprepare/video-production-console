package taskmodel

import (
	"errors"
	"regexp"
	"strings"
)

const (
	DefaultModel           = "gpt-5.6-sol"
	DefaultReasoningEffort = "medium"
)

var (
	errInvalidModel           = errors.New("invalid task model")
	errInvalidReasoningEffort = errors.New("invalid task reasoning effort")
	modelPattern              = regexp.MustCompile(`^[A-Za-z0-9_./:][A-Za-z0-9_.:/-]{0,127}$`)
	validReasoningEfforts     = map[string]struct{}{
		"low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {}, "ultra": {},
	}
)

type Selection struct {
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
}

func Normalize(selection Selection) (Selection, error) {
	selection.Model = strings.TrimSpace(selection.Model)
	selection.ReasoningEffort = strings.ToLower(strings.TrimSpace(selection.ReasoningEffort))
	if !modelPattern.MatchString(selection.Model) {
		return Selection{}, errInvalidModel
	}
	if _, ok := validReasoningEfforts[selection.ReasoningEffort]; !ok {
		return Selection{}, errInvalidReasoningEffort
	}
	return selection, nil
}

func Resolve(defaults, override Selection) (Selection, error) {
	if strings.TrimSpace(override.Model) != "" {
		defaults.Model = override.Model
	}
	if strings.TrimSpace(override.ReasoningEffort) != "" {
		defaults.ReasoningEffort = override.ReasoningEffort
	}
	return Normalize(defaults)
}
