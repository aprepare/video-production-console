package montageplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func patchManifestSettings(t *testing.T, manifestPath string, extra map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	settings := manifest["non_secret_settings"].(map[string]any)
	for key, value := range extra {
		settings[key] = value
	}
	patched, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, patched, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildV2DefaultsKeepHistoricalStyleAndBuiltinBGM(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	plan := buildV2PlanJSON(t, v2Options(manifestPath, planPath))

	overrides := plan["style_overrides"].(map[string]any)
	spoken := overrides["spoken_v1"].(map[string]any)
	if spoken["size"].(float64) != 20 || spoken["y"].(float64) != 0 || spoken["font"] != "新青年体" {
		t.Fatalf("default spoken_v1 override drifted: %#v", spoken)
	}
	keyword := overrides["spoken_keyword_v1"].(map[string]any)
	if keyword["size"].(float64) != 23 {
		t.Fatalf("default keyword size drifted: %#v", keyword)
	}
	graphics := plan["graphics"].(map[string]any)
	title := graphics["title"].(map[string]any)
	if title["enabled"] != true || title["y"].(float64) != 0.6 {
		t.Fatalf("default title drifted: %#v", title)
	}
	bgm := plan["audio"].(map[string]any)["bgm"].(map[string]any)
	if bgm["custom"] != nil || bgm["music_id"] != "7555333028841670665" || bgm["linear_volume"].(float64) != 0.2512 {
		t.Fatalf("default bgm must stay builtin: %#v", bgm)
	}
}

func TestBuildV2AppliesMontageStyleAndCustomBGM(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	bgmFile := filepath.Join(t.TempDir(), "custom.mp3")
	if err := os.WriteFile(bgmFile, []byte("mp3"), 0o644); err != nil {
		t.Fatal(err)
	}
	patchManifestSettings(t, manifestPath, map[string]any{
		"montage_style": map[string]any{
			"caption_size": 26, "caption_color": "#FFFFFF", "caption_position": "bottom",
			"caption_font": "俪金黑", "plain_size": 18, "keyword_size": 30, "keyword_color": "#FF0000",
			"title_hidden": true, "subtitle_color": "#00FF00", "subtitle_y": 0.5,
			"bgm_id": "abcd1234", "bgm_volume": 0.5,
		},
		"montage_bgm": map[string]any{
			"name": "custom.mp3", "file_path": bgmFile, "duration_s": 200.0,
			"usable_head_s": 180.0, "climax_start_s": 60.0, "climax_duration_s": 50.0,
			"volume": 0.5,
		},
	})
	plan := buildV2PlanJSON(t, v2Options(manifestPath, planPath))

	overrides := plan["style_overrides"].(map[string]any)
	spoken := overrides["spoken_v1"].(map[string]any)
	if spoken["size"].(float64) != 26 || spoken["y"].(float64) != -0.3 || spoken["font"] != "俪金黑" {
		t.Fatalf("spoken_v1 override wrong: %#v", spoken)
	}
	color := spoken["color"].([]any)
	if color[0].(float64) != 1 || color[1].(float64) != 1 || color[2].(float64) != 1 {
		t.Fatalf("caption color wrong: %#v", color)
	}
	plain := overrides["spoken_plain_v1"].(map[string]any)
	if plain["size"].(float64) != 18 || plain["y"].(float64) != -0.3 {
		t.Fatalf("plain override wrong: %#v", plain)
	}
	keyword := overrides["spoken_keyword_v1"].(map[string]any)
	kwColor := keyword["color"].([]any)
	if keyword["size"].(float64) != 30 || kwColor[0].(float64) != 1 || kwColor[1].(float64) != 0 {
		t.Fatalf("keyword override wrong: %#v", keyword)
	}
	warning := overrides["spoken_warning_v1"].(map[string]any)
	if warning["size"].(float64) != 30 {
		t.Fatalf("warning override wrong: %#v", warning)
	}

	graphics := plan["graphics"].(map[string]any)
	title := graphics["title"].(map[string]any)
	if title["enabled"] != false {
		t.Fatalf("hidden title must be disabled: %#v", title)
	}
	subtitle := graphics["subtitle"].(map[string]any)
	subColor := subtitle["color"].([]any)
	if subtitle["enabled"] != true || subtitle["y"].(float64) != 0.5 || subColor[1].(float64) != 1 || subColor[0].(float64) != 0 {
		t.Fatalf("subtitle style wrong: %#v", subtitle)
	}

	bgm := plan["audio"].(map[string]any)["bgm"].(map[string]any)
	if bgm["custom"] != true || bgm["file_path"] != bgmFile {
		t.Fatalf("custom bgm missing: %#v", bgm)
	}
	if bgm["usable_head_s"].(float64) != 180 || bgm["climax_start_s"].(float64) != 60 ||
		bgm["climax_duration_s"].(float64) != 50 || bgm["loop_every_s"].(float64) != 180 {
		t.Fatalf("custom bgm windows wrong: %#v", bgm)
	}
	if bgm["linear_volume"].(float64) != 0.5 || bgm["required"] != true {
		t.Fatalf("custom bgm volume wrong: %#v", bgm)
	}
	if _, hasIdentity := bgm["music_id"]; hasIdentity {
		t.Fatalf("custom bgm must not carry verified identity: %#v", bgm)
	}
}

func TestBuildV2BuiltinBGMHonorsVolumeOverride(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	patchManifestSettings(t, manifestPath, map[string]any{
		"montage_style": map[string]any{"bgm_volume": 0.1},
	})
	plan := buildV2PlanJSON(t, v2Options(manifestPath, planPath))
	bgm := plan["audio"].(map[string]any)["bgm"].(map[string]any)
	if bgm["music_id"] != "7555333028841670665" || bgm["linear_volume"].(float64) != 0.1 {
		t.Fatalf("builtin bgm volume override lost: %#v", bgm)
	}
}

func TestHexToRGB(t *testing.T) {
	rgb := hexToRGB("#FF1515")
	if rgb[0] != 1 || rgb[1] != 0.0824 || rgb[2] != 0.0824 {
		t.Fatalf("hexToRGB(#FF1515) = %v", rgb)
	}
	fallback := hexToRGB("red")
	if fallback[0] != 1 || fallback[1] != 1 || fallback[2] != 1 {
		t.Fatalf("invalid hex must fall back to white: %v", fallback)
	}
}
