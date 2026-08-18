package montageplan

import (
	"os"
	"path/filepath"
	"testing"
)

func writeProfile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machine_profile.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertDefaultResources(t *testing.T, got montageResources) {
	t.Helper()
	if got.Transition.Name != "叠化" || got.Transition.EffectID != "322577" ||
		got.Transition.ResourceID != "6724845717472416269" || got.Transition.DurationS != 0.466666 {
		t.Fatalf("transition = %#v", got.Transition)
	}
	want := []verifiedSFX{
		{Name: "综艺开头-咚（空旷）", EffectID: "7132789318354996487", ResourceID: "7132789318354996487", CacheKey: "sfx_opening_hit"},
		{Name: "水滴", EffectID: "6924829910603287822", ResourceID: "6924829910603287822", CacheKey: "sfx_water_drop"},
		{Name: "“呼”的转场音效", EffectID: "6896679799100656904", ResourceID: "6896679799100656904", CacheKey: "sfx_whoosh"},
		{Name: "综艺咚", EffectID: "7072236855973924103", ResourceID: "7072236855973924103", CacheKey: "sfx_conclusion_hit"},
	}
	if len(got.SFX) != len(want) {
		t.Fatalf("sfx count = %d, want %d", len(got.SFX), len(want))
	}
	for i := range want {
		if got.SFX[i] != want[i] {
			t.Fatalf("sfx[%d] = %#v, want %#v", i, got.SFX[i], want[i])
		}
	}
	wantBGM := bgmResource{
		Name: "やわらかs'xな光", MusicID: "7555333028841670665", ResourceID: "7555333028841670665",
		CacheKey: "bgm_yawaraka_hikari", LinearVolume: 0.2512, LoopEveryS: 313.7,
		UsableHeadS: 313.7, ClimaxStartS: 67.3, ClimaxDurationS: 57.633333, Required: true,
	}
	if got.BGM != wantBGM {
		t.Fatalf("bgm = %#v, want %#v", got.BGM, wantBGM)
	}
}

func TestLoadMontageResourcesWithoutConfigMatchesBuiltInIDs(t *testing.T) {
	got, err := loadMontageResources("")
	if err != nil {
		t.Fatalf("loadMontageResources: %v", err)
	}
	assertDefaultResources(t, got)
}

func TestLoadMontageResourcesFallsBackWhenMissingOrEmpty(t *testing.T) {
	cases := map[string]string{
		"empty file":         "",
		"blank file":         "   \r\n",
		"profile without it": `{"media_root":"C:\\media","media_index_path":"C:\\media\\index.json"}`,
		"empty object":       `{"montage_resources":{}}`,
		"empty sfx list":     `{"montage_resources":{"sfx":[]}}`,
		"blank overrides":    `{"montage_resources":{"transition":{"effect_id":"  ","name":""},"bgm":{"cache_key":""}}}`,
		"blank sidecar path": `{"montage_resources_path":"   "}`,
		"missing sidecar":    `{"montage_resources_path":"nope.json"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := loadMontageResources(writeProfile(t, body))
			if err != nil {
				t.Fatalf("loadMontageResources: %v", err)
			}
			assertDefaultResources(t, got)
		})
	}
	got, err := loadMontageResources(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("absent profile: %v", err)
	}
	assertDefaultResources(t, got)
}

func TestLoadMontageResourcesAppliesPerFieldFallback(t *testing.T) {
	profile := writeProfile(t, `{
      "media_root": "C:\\media",
      "montage_resources": {
        "bgm": {"name": "Other Track", "music_id": "111", "resource_id": "222", "cache_key": "bgm_other", "loop_every_s": 120.5}
      }
    }`)
	got, err := loadMontageResources(profile)
	if err != nil {
		t.Fatalf("loadMontageResources: %v", err)
	}
	wantBGM := bgmResource{
		Name: "Other Track", MusicID: "111", ResourceID: "222", CacheKey: "bgm_other",
		LinearVolume: 0.2512, LoopEveryS: 120.5, UsableHeadS: 313.7,
		ClimaxStartS: 67.3, ClimaxDurationS: 57.633333, Required: true,
	}
	if got.BGM != wantBGM {
		t.Fatalf("bgm = %#v, want %#v", got.BGM, wantBGM)
	}
	defaults := defaultMontageResources()
	if got.Transition != defaults.Transition {
		t.Fatalf("transition must stay built-in: %#v", got.Transition)
	}
	if len(got.SFX) != len(defaults.SFX) {
		t.Fatalf("sfx must stay built-in: %#v", got.SFX)
	}
	for i := range defaults.SFX {
		if got.SFX[i] != defaults.SFX[i] {
			t.Fatalf("sfx[%d] = %#v", i, got.SFX[i])
		}
	}
}

func TestLoadMontageResourcesOverridesTransitionAndSFX(t *testing.T) {
	profile := writeProfile(t, `{
      "montage_resources": {
        "transition": {"name": "闪黑", "effect_id": "999", "resource_id": "888", "duration_s": 0.6},
        "sfx": [
          {"name": "开场", "effect_id": "1", "resource_id": "2", "cache_key": "sfx_open_custom"},
          {"name": "过渡", "effect_id": "3", "resource_id": "4", "cache_key": "sfx_mid_custom"}
        ]
      }
    }`)
	got, err := loadMontageResources(profile)
	if err != nil {
		t.Fatalf("loadMontageResources: %v", err)
	}
	wantTransition := transitionResource{Name: "闪黑", EffectID: "999", ResourceID: "888", DurationS: 0.6}
	if got.Transition != wantTransition {
		t.Fatalf("transition = %#v, want %#v", got.Transition, wantTransition)
	}
	if len(got.SFX) != 2 || got.SFX[0].CacheKey != "sfx_open_custom" || got.SFX[1].CacheKey != "sfx_mid_custom" {
		t.Fatalf("sfx = %#v", got.SFX)
	}
	if got.BGM != defaultMontageResources().BGM {
		t.Fatalf("bgm must stay built-in: %#v", got.BGM)
	}
}

func TestLoadMontageResourcesReadsSidecarFile(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "machine_profile.json")
	if err := os.WriteFile(profile, []byte(`{"montage_resources_path":"resources.json"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(root, "resources.json")
	if err := os.WriteFile(sidecar, []byte(`{"transition":{"effect_id":"777"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadMontageResources(profile)
	if err != nil {
		t.Fatalf("loadMontageResources: %v", err)
	}
	if got.Transition.EffectID != "777" || got.Transition.ResourceID != "6724845717472416269" {
		t.Fatalf("transition = %#v", got.Transition)
	}

	// A wrapped sidecar body is accepted too, so one file shape fits both slots.
	if err := os.WriteFile(sidecar, []byte(`{"montage_resources":{"transition":{"effect_id":"666"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = loadMontageResources(profile)
	if err != nil {
		t.Fatalf("loadMontageResources: %v", err)
	}
	if got.Transition.EffectID != "666" {
		t.Fatalf("wrapped sidecar transition = %#v", got.Transition)
	}

	// Inline resources win over the sidecar pointer.
	if err := os.WriteFile(profile, []byte(`{"montage_resources":{"transition":{"effect_id":"555"}},"montage_resources_path":"resources.json"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = loadMontageResources(profile)
	if err != nil {
		t.Fatalf("loadMontageResources: %v", err)
	}
	if got.Transition.EffectID != "555" {
		t.Fatalf("inline transition = %#v", got.Transition)
	}
}

func TestLoadMontageResourcesRejectsBrokenConfig(t *testing.T) {
	if _, err := loadMontageResources(writeProfile(t, `{"montage_resources":`)); err == nil {
		t.Fatal("malformed machine profile JSON must be reported")
	}
	root := t.TempDir()
	profile := filepath.Join(root, "machine_profile.json")
	if err := os.WriteFile(profile, []byte(`{"montage_resources_path":"resources.json"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "resources.json"), []byte(`{"sfx":[}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMontageResources(profile); err == nil {
		t.Fatal("malformed sidecar JSON must be reported")
	}
	for name, body := range map[string]string{
		"sfx missing effect_id": `{"montage_resources":{"sfx":[{"name":"x","resource_id":"2","cache_key":"k"}]}}`,
		"sfx missing cache_key": `{"montage_resources":{"sfx":[{"name":"x","effect_id":"1","resource_id":"2"}]}}`,
		"negative duration":     `{"montage_resources":{"transition":{"duration_s":0}}}`,
		"negative volume":       `{"montage_resources":{"bgm":{"linear_volume":-1}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadMontageResources(writeProfile(t, body)); err == nil {
				t.Fatalf("incomplete config must be reported: %s", body)
			}
		})
	}
}
