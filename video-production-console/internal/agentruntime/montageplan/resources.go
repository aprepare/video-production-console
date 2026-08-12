package montageplan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const transitionName = "叠化"

// montageResources holds the verified Jianying asset identities a plan needs.
// Every field has a built-in default, so a machine profile may override only
// the parts it cares about.
type montageResources struct {
	Transition transitionResource
	SFX        []verifiedSFX
	BGM        bgmResource
}

type transitionResource struct {
	Name       string
	EffectID   string
	ResourceID string
	DurationS  float64
}

type bgmResource struct {
	Name         string
	MusicID      string
	ResourceID   string
	CacheKey     string
	LinearVolume float64
	LoopEveryS   float64
	Required     bool
}

// defaultMontageResources returns the resources every plan used before the
// machine profile could override them.
func defaultMontageResources() montageResources {
	return montageResources{
		Transition: transitionResource{
			Name:       transitionName,
			EffectID:   transitionEffectID,
			ResourceID: transitionResID,
			DurationS:  transitionDuration,
		},
		SFX: append([]verifiedSFX(nil), sfxLibrary...),
		BGM: bgmResource{
			Name:         "EXTA$Y+ (Remake)",
			MusicID:      "7223314484093405186",
			ResourceID:   "7223314484093405186",
			CacheKey:     "bgm_extasy_remake",
			LinearVolume: 0.1593,
			LoopEveryS:   bgmLoopSeconds,
			Required:     true,
		},
	}
}

// montageResourcesOverlay is the on-disk shape. Pointers separate "absent"
// from "zero" so each field can fall back on its own.
type montageResourcesOverlay struct {
	Transition *transitionOverlay `json:"transition"`
	SFX        []sfxOverlay       `json:"sfx"`
	BGM        *bgmOverlay        `json:"bgm"`
}

type transitionOverlay struct {
	Name       *string  `json:"name"`
	EffectID   *string  `json:"effect_id"`
	ResourceID *string  `json:"resource_id"`
	DurationS  *float64 `json:"duration_s"`
}

type sfxOverlay struct {
	Name       *string `json:"name"`
	EffectID   *string `json:"effect_id"`
	ResourceID *string `json:"resource_id"`
	CacheKey   *string `json:"cache_key"`
}

type bgmOverlay struct {
	Name         *string  `json:"name"`
	MusicID      *string  `json:"music_id"`
	ResourceID   *string  `json:"resource_id"`
	CacheKey     *string  `json:"cache_key"`
	LinearVolume *float64 `json:"linear_volume"`
	LoopEveryS   *float64 `json:"loop_every_s"`
	Required     *bool    `json:"required"`
}

// montageResourcesFile accepts either a bare resources object or the same
// object wrapped in "montage_resources", so the machine profile and a
// standalone file can share one shape.
type montageResourcesFile struct {
	montageResourcesOverlay
	Nested     *montageResourcesOverlay `json:"montage_resources"`
	NestedPath *string                  `json:"montage_resources_path"`
}

// loadMontageResources resolves plan resources from the machine profile.
// The profile may inline them under "montage_resources" or point at a
// standalone JSON file with "montage_resources_path". A missing or empty
// configuration falls back to the built-in verified resources; malformed JSON
// or an incomplete resource entry is reported instead of being ignored, so a
// broken profile never silently produces a draft with wrong asset IDs.
func loadMontageResources(profilePath string) (montageResources, error) {
	defaults := defaultMontageResources()
	path := strings.TrimSpace(profilePath)
	if path == "" {
		return defaults, nil
	}
	overlay, source, err := readMontageResourcesOverlay(path)
	if err != nil {
		return montageResources{}, err
	}
	if overlay == nil {
		return defaults, nil
	}
	resolved, err := applyMontageResources(defaults, *overlay)
	if err != nil {
		return montageResources{}, fmt.Errorf("%s: %w", source, err)
	}
	return resolved, nil
}

func readMontageResourcesOverlay(profilePath string) (*montageResourcesOverlay, string, error) {
	profile, ok, err := decodeMontageResourcesFile(profilePath, "machine profile")
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", nil
	}
	if overlay := selectOverlay(profile); overlay != nil {
		return overlay, fmt.Sprintf("machine profile montage_resources (%s)", profilePath), nil
	}
	if profile.NestedPath == nil || strings.TrimSpace(*profile.NestedPath) == "" {
		return nil, "", nil
	}
	sidecar := strings.TrimSpace(*profile.NestedPath)
	if !filepath.IsAbs(sidecar) {
		sidecar = filepath.Join(filepath.Dir(profilePath), sidecar)
	}
	file, ok, err := decodeMontageResourcesFile(sidecar, "montage resources file")
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", nil
	}
	overlay := selectOverlay(file)
	if overlay == nil {
		return nil, "", nil
	}
	return overlay, fmt.Sprintf("montage resources file (%s)", sidecar), nil
}

func selectOverlay(file *montageResourcesFile) *montageResourcesOverlay {
	if file == nil {
		return nil
	}
	if file.Nested != nil {
		return file.Nested
	}
	if file.Transition != nil || len(file.SFX) > 0 || file.BGM != nil {
		overlay := file.montageResourcesOverlay
		return &overlay
	}
	return nil
}

// decodeMontageResourcesFile reports ok=false for an unreadable or blank file
// so callers fall back silently, and an error only for malformed JSON.
func decodeMontageResourcesFile(path, label string) (*montageResourcesFile, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, nil
	}
	raw = stripBOM(raw)
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, false, nil
	}
	var file montageResourcesFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, false, fmt.Errorf("decode %s %s: %w", label, path, err)
	}
	return &file, true, nil
}

func applyMontageResources(base montageResources, overlay montageResourcesOverlay) (montageResources, error) {
	out := base
	if t := overlay.Transition; t != nil {
		applyString(&out.Transition.Name, t.Name)
		applyString(&out.Transition.EffectID, t.EffectID)
		applyString(&out.Transition.ResourceID, t.ResourceID)
		if err := applyPositive(&out.Transition.DurationS, t.DurationS, "transition duration_s"); err != nil {
			return montageResources{}, err
		}
	}
	if len(overlay.SFX) > 0 {
		library := make([]verifiedSFX, 0, len(overlay.SFX))
		for i, item := range overlay.SFX {
			entry := verifiedSFX{
				Name:       trimPointer(item.Name),
				EffectID:   trimPointer(item.EffectID),
				ResourceID: trimPointer(item.ResourceID),
				CacheKey:   trimPointer(item.CacheKey),
			}
			// An SFX entry is a whole verified resource: partial entries would
			// reach the draft with a missing cache or effect mapping.
			if entry.Name == "" || entry.EffectID == "" || entry.ResourceID == "" || entry.CacheKey == "" {
				return montageResources{}, fmt.Errorf("sfx[%d] requires name, effect_id, resource_id and cache_key", i)
			}
			library = append(library, entry)
		}
		out.SFX = library
	}
	if b := overlay.BGM; b != nil {
		applyString(&out.BGM.Name, b.Name)
		applyString(&out.BGM.MusicID, b.MusicID)
		applyString(&out.BGM.ResourceID, b.ResourceID)
		applyString(&out.BGM.CacheKey, b.CacheKey)
		if err := applyPositive(&out.BGM.LinearVolume, b.LinearVolume, "bgm linear_volume"); err != nil {
			return montageResources{}, err
		}
		if err := applyPositive(&out.BGM.LoopEveryS, b.LoopEveryS, "bgm loop_every_s"); err != nil {
			return montageResources{}, err
		}
		if b.Required != nil {
			out.BGM.Required = *b.Required
		}
	}
	return out, nil
}

func applyString(target *string, value *string) {
	if value == nil {
		return
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return
	}
	*target = trimmed
}

func applyPositive(target *float64, value *float64, label string) error {
	if value == nil {
		return nil
	}
	if *value <= 0 {
		return fmt.Errorf("%s must be positive, got %v", label, *value)
	}
	*target = *value
	return nil
}

func trimPointer(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
