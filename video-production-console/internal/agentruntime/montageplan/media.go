package montageplan

import (
	"fmt"
	"path/filepath"
	"strings"
)

// mediaKind is the planner-facing top-level media type. Catalog subtypes such
// as chart or generated images are normalized onto mediaKindImage and keep
// their origin in Subtype/Origin, so duration quotas have a single unit.
type mediaKind string

const (
	mediaKindBroll mediaKind = "broll"
	mediaKindMovie mediaKind = "movie"
	mediaKindImage mediaKind = "image"
)

// minVideoSourceSeconds is the floor for video-like rows: an 8s timeline shot
// at 1.1x needs 8.8s of source plus a 1s lead-in margin. Images have no
// intrinsic duration and are exempt.
const minVideoSourceSeconds = 10

type mediaItem struct {
	ID               string    `json:"id"`
	Kind             mediaKind `json:"kind,omitempty"`
	Category         string    `json:"category"`
	RelativePath     string    `json:"relative_path"`
	DurationSeconds  float64   `json:"duration_seconds"`
	SourceInSeconds  float64   `json:"source_in_seconds,omitempty"`
	SourceOutSeconds float64   `json:"source_out_seconds,omitempty"`
	ShotID           string    `json:"shot_id,omitempty"`
	Tags             []string  `json:"tags,omitempty"`
	Summary          string    `json:"summary,omitempty"`
	Mood             string    `json:"mood,omitempty"`
	Subtype          string    `json:"subtype,omitempty"`
	Origin           string    `json:"origin,omitempty"`
	AbsPath          string    `json:"-"`
}

// hasShotRange reports whether the row addresses a sub-range of its source.
func (m mediaItem) hasShotRange() bool {
	return m.SourceOutSeconds > m.SourceInSeconds && m.SourceOutSeconds > 0
}

// availableSeconds is the source span a timeline shot may consume.
func (m mediaItem) availableSeconds() float64 {
	if m.hasShotRange() {
		return m.SourceOutSeconds - m.SourceInSeconds
	}
	return m.DurationSeconds
}

// shotKey identifies a never-repeat unit: an explicit shot when present,
// otherwise the indexed row itself.
func (m mediaItem) shotKey() string {
	if shot := strings.TrimSpace(m.ShotID); shot != "" {
		return "shot\x00" + shot
	}
	return "item\x00" + strings.TrimSpace(m.ID)
}

// sourceKey identifies the underlying file for reuse limits, so several shots
// cut from one movie count against the same source.
func (m mediaItem) sourceKey() string {
	return filepath.Clean(filepath.FromSlash(m.RelativePath))
}

// normalizeMediaItem canonicalizes the kind of a decoded index row.
// Legacy rows without a kind stay broll; catalog image subtypes fold into
// kind=image while keeping their subtype; anything else is rejected.
func normalizeMediaItem(item *mediaItem) error {
	kind := mediaKind(strings.ToLower(strings.TrimSpace(string(item.Kind))))
	switch kind {
	case "":
		kind = mediaKindBroll
	case mediaKindBroll, mediaKindMovie, mediaKindImage:
	case "chart":
		kind = mediaKindImage
		if strings.TrimSpace(item.Subtype) == "" {
			item.Subtype = "chart"
		}
	case "generated_image":
		kind = mediaKindImage
		if strings.TrimSpace(item.Subtype) == "" {
			item.Subtype = "generated"
		}
	default:
		return fmt.Errorf("media index row %q has unknown kind %q", item.ID, item.Kind)
	}
	item.Kind = kind
	if kind != mediaKindImage && item.hasShotRange() {
		if item.SourceInSeconds < 0 || item.SourceOutSeconds <= item.SourceInSeconds ||
			(item.DurationSeconds > 0 && item.SourceOutSeconds > item.DurationSeconds+0.0001) {
			return fmt.Errorf("media index row %q has invalid shot range [%v, %v] for duration %v",
				item.ID, item.SourceInSeconds, item.SourceOutSeconds, item.DurationSeconds)
		}
	}
	return nil
}

// resolveMediaPath joins a declared relative path with the canonical media
// root and rejects absolute paths and any traversal outside the root.
func isLandscapeItem(item mediaItem) bool {
	raw := strings.TrimSpace(item.Category)
	key := strings.ToLower(strings.ReplaceAll(raw, " ", "_"))
	switch key {
	case "nature_landscape", "scenery", "landscape", "nature":
		return true
	}
	if strings.Contains(raw, "风景") || strings.Contains(raw, "景观") {
		return true
	}
	path := strings.ToLower(filepath.ToSlash(item.RelativePath + " " + item.AbsPath))
	for _, token := range []string{"nature_landscape", "/scenery/", "/landscape/", "风景", "景观"} {
		if strings.Contains(path, token) {
			return true
		}
	}
	for _, tag := range item.Tags {
		tag = strings.TrimSpace(tag)
		lower := strings.ToLower(tag)
		if lower == "landscape" || lower == "scenery" || strings.Contains(tag, "风景") || strings.Contains(tag, "景观") {
			return true
		}
	}
	return false
}

func filterLandscapeCandidates(candidates []rankedCandidate) []rankedCandidate {
	out := make([]rankedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if isLandscapeItem(candidate.Item) {
			out = append(out, candidate)
		}
	}
	return out
}

func rankedFromIndex(clips []mediaItem) []rankedCandidate {
	out := make([]rankedCandidate, 0, len(clips))
	for _, clip := range clips {
		out = append(out, rankedCandidate{Item: clip})
	}
	return out
}

func mergeRankedCandidates(primary, extra []rankedCandidate) []rankedCandidate {
	seen := make(map[string]bool, len(primary)+len(extra))
	out := make([]rankedCandidate, 0, len(primary)+len(extra))
	add := func(candidate rankedCandidate) {
		if strings.TrimSpace(candidate.Item.ID) == "" {
			return
		}
		key := candidate.Item.shotKey()
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, candidate)
	}
	for _, candidate := range primary {
		add(candidate)
	}
	for _, candidate := range extra {
		add(candidate)
	}
	return out
}

func resolveMediaPath(mediaRoot, relative string) (string, error) {
	cleaned := filepath.FromSlash(strings.TrimSpace(relative))
	if filepath.IsAbs(cleaned) || filepath.VolumeName(cleaned) != "" ||
		strings.HasPrefix(cleaned, string(filepath.Separator)) {
		return "", fmt.Errorf("media index relative_path must not be absolute: %s", relative)
	}
	root := filepath.Clean(mediaRoot)
	abs := filepath.Join(root, cleaned)
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("media index relative_path escapes media root: %s", relative)
	}
	return abs, nil
}
