package montageplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildProducesApprovedPlan(t *testing.T) {
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "media")
	clipPath := filepath.Join(mediaRoot, "clip.mp4")
	if err := os.MkdirAll(mediaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clipPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(root, "media_index.json")
	index := []map[string]any{{
		"id": "clip-001", "category": "Nature_Landscape",
		"relative_path": "clip.mp4", "duration_seconds": 20,
	}}
	rawIndex, _ := json.Marshal(index)
	if err := os.WriteFile(indexPath, rawIndex, 0o644); err != nil {
		t.Fatal(err)
	}
	narration := filepath.Join(root, "narration.mp3")
	background := filepath.Join(root, "bg.png")
	script := filepath.Join(root, "script.md")
	srt := filepath.Join(root, "sub.srt")
	for _, path := range []string{narration, background, script, srt} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	taskID := "task-abcdef123456"
	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	manifest := map[string]any{
		"task_id": taskID, "job_id": taskID, "action": "montage.execute", "output_dir": outputDir,
		"inputs": []map[string]string{
			{"role": "narration", "path": narration},
			{"role": "account_background", "path": background},
			{"role": "continuous_script", "path": script},
			{"role": "subtitle_srt", "path": srt},
		},
		"non_secret_settings": map[string]string{
			"media_root": mediaRoot, "media_index_path": indexPath,
			"draft_display_name": "天中观局_房贷困境反思_a4b031",
		},
	}
	rawManifest, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, rawManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(outputDir, "production_plan.json")
	if err := Build(Options{
		ManifestPath: manifestPath,
		PlanPath:     planPath,
		Duration:     func(string) (float64, error) { return 25.0, nil },
	}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if plan["status"] != "approved" || plan["model_role"] != "planner" {
		t.Fatalf("status/role = %v/%v", plan["status"], plan["model_role"])
	}
	if plan["project_duration_s"].(float64) != 25 {
		t.Fatalf("duration = %v", plan["project_duration_s"])
	}
	timeline, ok := plan["timeline"].([]any)
	if !ok || len(timeline) == 0 {
		t.Fatal("timeline missing")
	}
	first := timeline[0].(map[string]any)
	if first["source_path"] != clipPath {
		t.Fatalf("source_path = %v", first["source_path"])
	}
	concurrency := plan["concurrency"].(map[string]any)
	if concurrency["job_id"] != taskID {
		t.Fatalf("job_id = %v", concurrency["job_id"])
	}
	graphics := plan["graphics"].(map[string]any)
	title := graphics["title"].(map[string]any)["text"].(string)
	if runeCount := len([]rune(title)); runeCount < 6 || runeCount > 8 {
		t.Fatalf("title %q has %d runes", title, runeCount)
	}
	if strings.Contains(title, "天中观局") {
		t.Fatalf("on-screen title must not include account name: %q", title)
	}
}

func TestMediaSelectionVariesByTaskAndPreflightRejectsMissing(t *testing.T) {
	root := t.TempDir()
	items := []map[string]any{}
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := os.WriteFile(filepath.Join(root, id+".mp4"), []byte(id), 0o600); err != nil {
			t.Fatal(err)
		}
		items = append(items, map[string]any{"id": id, "category": "Nature_Landscape", "relative_path": id + ".mp4", "duration_seconds": 20})
	}
	indexPath := filepath.Join(root, "index.json")
	writeIndex := func() {
		raw, _ := json.Marshal(items)
		if err := os.WriteFile(indexPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeIndex()
	one, err := sampleMedia(indexPath, root, 4, "task-one", false)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := sampleMedia(indexPath, root, 4, "task-one", false)
	if err != nil {
		t.Fatal(err)
	}
	for i := range one {
		if one[i].ID != retry[i].ID {
			t.Fatalf("same task order changed at %d", i)
		}
	}
	openings := map[string]bool{one[0].ID: true}
	for _, seed := range []string{"task-two", "task-three", "task-four"} {
		clips, err := sampleMedia(indexPath, root, 4, seed, false)
		if err != nil {
			t.Fatal(err)
		}
		openings[clips[0].ID] = true
	}
	if len(openings) < 2 {
		t.Fatalf("openings did not vary: %v", openings)
	}
	items = append(items, map[string]any{"id": "missing", "category": "Nature_Landscape", "relative_path": "missing.mp4", "duration_seconds": 20})
	writeIndex()
	if _, err := sampleMedia(indexPath, root, 4, "task", false); err != nil {
		t.Fatalf("runtime sampling must skip missing entries: %v", err)
	}
	if err := ValidateMediaLibrary(indexPath, root, ""); err == nil {
		t.Fatal("preflight must reject a missing indexed clip")
	}
	if err := os.WriteFile(indexPath, []byte(`[{"id":"broken"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sampleMedia(indexPath, root, 4, "task", false); err == nil {
		t.Fatal("truncated media index must be rejected")
	}
}

func TestFitShotToClipNeverExceedsSourceDuration(t *testing.T) {
	in, out, speed, length := fitShotToClip(8, 12)
	if speed != 1.1 || in != 1 || out <= in || out > 12 || length != 8 {
		t.Fatalf("long clip fit = in=%v out=%v speed=%v length=%v", in, out, speed, length)
	}
	in, out, speed, length = fitShotToClip(8, 8.5)
	if speed != 1.0 || out-in > 8.5+0.001 || length != 8 {
		t.Fatalf("tight clip fit = in=%v out=%v speed=%v length=%v", in, out, speed, length)
	}
	in, out, speed, length = fitShotToClip(8, 6)
	if speed != 1.0 || out > 6+0.001 || length > 6+0.001 || length != out-in {
		t.Fatalf("short clip fit = in=%v out=%v speed=%v length=%v", in, out, speed, length)
	}
}

func TestBuildSFXPlacementsMatchesLongFormValidator(t *testing.T) {
	library := defaultMontageResources().SFX
	short := buildSFXPlacements(25, library)
	if len(short) != 1 {
		t.Fatalf("short sfx count = %d", len(short))
	}
	if short[0]["start_s"].(float64) != 0 || short[0]["cache_key"] != "sfx_opening_hit" {
		t.Fatalf("short opening = %#v", short[0])
	}

	long := buildSFXPlacements(293.832, library)
	if n := len(long); n < 3 || n > 5 {
		t.Fatalf("long sfx count = %d", n)
	}
	if long[0]["start_s"].(float64) != 0 || long[0]["name"] != "综艺开头-咚（空旷）" {
		t.Fatalf("long opening = %#v", long[0])
	}
	prev := -sfxMinGapSeconds
	for i, item := range long {
		start := item["start_s"].(float64)
		if start < 0 || start >= 293.832 {
			t.Fatalf("placement %d start_s=%v out of range", i, start)
		}
		if start-prev < sfxMinGapSeconds {
			t.Fatalf("placement %d gap too small: %v -> %v", i, prev, start)
		}
		if item["db"].(int) != -8 {
			t.Fatalf("placement %d db = %v", i, item["db"])
		}
		prev = start
	}
}

func TestBuildSFXPlacementsStaysValidWithShortConfiguredLibrary(t *testing.T) {
	library := []verifiedSFX{
		{Name: "开场", EffectID: "1", ResourceID: "2", CacheKey: "sfx_open_custom"},
		{Name: "过渡", EffectID: "3", ResourceID: "4", CacheKey: "sfx_mid_custom"},
	}
	long := buildSFXPlacements(293.832, library)
	if n := len(long); n < 3 || n > 5 {
		t.Fatalf("long sfx count = %d", n)
	}
	if long[0]["cache_key"] != "sfx_open_custom" || long[0]["start_s"].(float64) != 0 {
		t.Fatalf("opening = %#v", long[0])
	}
	prev := -sfxMinGapSeconds
	for i, item := range long {
		start := item["start_s"].(float64)
		if start-prev < sfxMinGapSeconds {
			t.Fatalf("placement %d gap too small: %v -> %v", i, prev, start)
		}
		if item["cache_key"] != library[i%len(library)].CacheKey {
			t.Fatalf("placement %d cache_key = %v", i, item["cache_key"])
		}
		prev = start
	}
	if got := buildSFXPlacements(25, nil); len(got) != 1 || got[0]["cache_key"] != "sfx_opening_hit" {
		t.Fatalf("empty library must fall back to built-in: %#v", got)
	}
}

// planFixture writes the inputs Build needs and returns the manifest and plan paths.
func planFixture(t *testing.T, profilePath string) (manifestPath, planPath, clipPath string) {
	t.Helper()
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "media")
	clipPath = filepath.Join(mediaRoot, "clip.mp4")
	if err := os.MkdirAll(mediaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clipPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(root, "media_index.json")
	rawIndex, _ := json.Marshal([]map[string]any{{
		"id": "clip-001", "category": "Nature_Landscape",
		"relative_path": "clip.mp4", "duration_seconds": 20,
	}})
	if err := os.WriteFile(indexPath, rawIndex, 0o644); err != nil {
		t.Fatal(err)
	}
	narration := filepath.Join(root, "narration.mp3")
	background := filepath.Join(root, "bg.png")
	for _, path := range []string{narration, background} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	taskID := "task-abcdef123456"
	settings := map[string]string{
		"media_root": mediaRoot, "media_index_path": indexPath,
		"draft_display_name": "天中观局_房贷困境反思_a4b031",
	}
	if profilePath != "" {
		settings["machine_profile_path"] = profilePath
	}
	manifestPath = filepath.Join(root, "task_manifest.json")
	rawManifest, _ := json.Marshal(map[string]any{
		"task_id": taskID, "job_id": taskID, "action": "montage.execute",
		"output_dir": filepath.Join(root, "output"),
		"inputs": []map[string]string{
			{"role": "narration", "path": narration},
			{"role": "account_background", "path": background},
		},
		"non_secret_settings": settings,
	})
	if err := os.WriteFile(manifestPath, rawManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	return manifestPath, filepath.Join(root, "output", "production_plan.json"), clipPath
}

func buildPlanJSON(t *testing.T, profilePath string) map[string]any {
	t.Helper()
	manifestPath, planPath, _ := planFixture(t, profilePath)
	if err := Build(Options{
		ManifestPath: manifestPath,
		PlanPath:     planPath,
		Duration:     func(string) (float64, error) { return 25.0, nil },
	}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestBuildKeepsBuiltInResourcesWithoutConfig(t *testing.T) {
	for name, profile := range map[string]string{
		"no machine profile":       "",
		"profile without montage":  `{"python_binary":"python"}`,
		"profile with empty block": `{"montage_resources":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			profilePath := ""
			if profile != "" {
				profilePath = writeProfile(t, profile)
			}
			plan := buildPlanJSON(t, profilePath)
			shot := plan["timeline"].([]any)[0].(map[string]any)
			if shot["transition"] != "叠化" || shot["transition_effect_id"] != "322577" ||
				shot["transition_resource_id"] != "6724845717472416269" ||
				shot["transition_duration_s"].(float64) != 0.466666 {
				t.Fatalf("transition = %#v", shot)
			}
			audio := plan["audio"].(map[string]any)
			bgm := audio["bgm"].(map[string]any)
			if bgm["name"] != "EXTA$Y+ (Remake)" || bgm["music_id"] != "7223314484093405186" ||
				bgm["resource_id"] != "7223314484093405186" || bgm["cache_key"] != "bgm_extasy_remake" ||
				bgm["linear_volume"].(float64) != 0.1593 || bgm["loop_every_s"].(float64) != 194.4 ||
				bgm["required"] != true {
				t.Fatalf("bgm = %#v", bgm)
			}
			sfx := audio["sfx"].([]any)
			if len(sfx) != 1 {
				t.Fatalf("sfx count = %d", len(sfx))
			}
			opening := sfx[0].(map[string]any)
			if opening["cache_key"] != "sfx_opening_hit" || opening["effect_id"] != "7132789318354996487" ||
				opening["name"] != "综艺开头-咚（空旷）" || opening["start_s"].(float64) != 0 {
				t.Fatalf("opening sfx = %#v", opening)
			}
		})
	}
}

func TestBuildUsesConfiguredMontageResources(t *testing.T) {
	profilePath := writeProfile(t, `{
      "montage_resources": {
        "transition": {"name": "闪黑", "effect_id": "999", "resource_id": "888", "duration_s": 0.6},
        "sfx": [{"name": "开场", "effect_id": "1", "resource_id": "2", "cache_key": "sfx_open_custom"}],
        "bgm": {"name": "Other Track", "music_id": "111", "resource_id": "222", "cache_key": "bgm_other", "linear_volume": 0.2, "loop_every_s": 120.5, "required": false}
      }
    }`)
	plan := buildPlanJSON(t, profilePath)
	shot := plan["timeline"].([]any)[0].(map[string]any)
	if shot["transition"] != "闪黑" || shot["transition_effect_id"] != "999" ||
		shot["transition_resource_id"] != "888" || shot["transition_duration_s"].(float64) != 0.6 {
		t.Fatalf("transition = %#v", shot)
	}
	audio := plan["audio"].(map[string]any)
	bgm := audio["bgm"].(map[string]any)
	if bgm["name"] != "Other Track" || bgm["cache_key"] != "bgm_other" ||
		bgm["linear_volume"].(float64) != 0.2 || bgm["loop_every_s"].(float64) != 120.5 || bgm["required"] != false {
		t.Fatalf("bgm = %#v", bgm)
	}
	opening := audio["sfx"].([]any)[0].(map[string]any)
	if opening["cache_key"] != "sfx_open_custom" || opening["effect_id"] != "1" || opening["name"] != "开场" {
		t.Fatalf("opening sfx = %#v", opening)
	}
}

func TestBuildRejectsMalformedMontageResources(t *testing.T) {
	profilePath := writeProfile(t, `{"media_root":"x","montage_resources":{"sfx":[{"name":"x","resource_id":"2"}]}}`)
	manifestPath, planPath, _ := planFixture(t, profilePath)
	err := Build(Options{
		ManifestPath: manifestPath,
		PlanPath:     planPath,
		Duration:     func(string) (float64, error) { return 25.0, nil },
	})
	if err == nil {
		t.Fatal("incomplete montage resources must fail the build")
	}
	if !strings.Contains(err.Error(), "sfx[0]") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(planPath); !os.IsNotExist(statErr) {
		t.Fatalf("plan must not be written: %v", statErr)
	}
}

func TestSampleMediaSpreadsCategoriesAndStaysReproducible(t *testing.T) {
	root := t.TempDir()
	items := []map[string]any{}
	for _, category := range []string{"Nature_Landscape", "Architecture", "Scenery"} {
		for i := 0; i < 4; i++ {
			id := category + "-" + string(rune('a'+i))
			if err := os.WriteFile(filepath.Join(root, id+".mp4"), []byte(id), 0o600); err != nil {
				t.Fatal(err)
			}
			items = append(items, map[string]any{
				"id": id, "category": category,
				"relative_path": id + ".mp4", "duration_seconds": 20,
			})
		}
	}
	indexPath := filepath.Join(root, "index.json")
	raw, _ := json.Marshal(items)
	if err := os.WriteFile(indexPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	clips, err := sampleMedia(indexPath, root, 6, "task-spread", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 6 {
		t.Fatalf("clip count = %d", len(clips))
	}
	for i := 1; i < len(clips); i++ {
		if normalizeCategory(clips[i].Category) == normalizeCategory(clips[i-1].Category) {
			t.Fatalf("neighbours %d/%d share category %q", i-1, i, clips[i].Category)
		}
	}
	retry, err := sampleMedia(indexPath, root, 6, "task-spread", false)
	if err != nil {
		t.Fatal(err)
	}
	for i := range clips {
		if clips[i].ID != retry[i].ID {
			t.Fatalf("same seed order changed at %d: %q vs %q", i, clips[i].ID, retry[i].ID)
		}
	}
	if err := ValidateMediaLibrary(indexPath, root, ""); err != nil {
		t.Fatalf("preflight must still accept a complete library: %v", err)
	}
}

func interleaveTestPool(pairs ...string) []mediaItem {
	items := make([]mediaItem, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		items = append(items, mediaItem{ID: pairs[i], Category: pairs[i+1]})
	}
	return items
}

func assertSameMembers(t *testing.T, input, output []mediaItem) {
	t.Helper()
	if len(output) != len(input) {
		t.Fatalf("length = %d, want %d", len(output), len(input))
	}
	counts := map[string]int{}
	for _, item := range input {
		counts[item.ID]++
	}
	for _, item := range output {
		counts[item.ID]--
	}
	for id, delta := range counts {
		if delta != 0 {
			t.Fatalf("id %q count delta = %d", id, delta)
		}
	}
}

func TestInterleaveByCategorySeparatesNeighboursDeterministically(t *testing.T) {
	input := interleaveTestPool(
		"n1", "Nature_Landscape", "n2", "Nature_Landscape", "n3", "Nature_Landscape",
		"a1", "Architecture", "a2", "architecture ", "a3", "ARCHITECTURE",
		"s1", "Scenery", "s2", "Scenery", "s3", "Scenery",
	)
	got := interleaveByCategory(input)
	assertSameMembers(t, input, got)
	for i := 1; i < len(got); i++ {
		if normalizeCategory(got[i].Category) == normalizeCategory(got[i-1].Category) {
			t.Fatalf("neighbours %d/%d share category %q", i-1, i, got[i].Category)
		}
	}
	again := interleaveByCategory(input)
	for i := range got {
		if got[i].ID != again[i].ID {
			t.Fatalf("not reproducible at %d: %q vs %q", i, got[i].ID, again[i].ID)
		}
	}
}

func TestInterleaveByCategoryKeepsSingleCategoryOrder(t *testing.T) {
	input := interleaveTestPool("a", "Nature_Landscape", "b", "nature_landscape", "c", " Nature_Landscape ")
	got := interleaveByCategory(input)
	assertSameMembers(t, input, got)
	for i := range input {
		if got[i].ID != input[i].ID {
			t.Fatalf("single category order changed at %d: %q", i, got[i].ID)
		}
	}
}

func TestInterleaveByCategoryHandlesTinyPools(t *testing.T) {
	if got := interleaveByCategory(nil); len(got) != 0 {
		t.Fatalf("nil pool = %v", got)
	}
	if got := interleaveByCategory([]mediaItem{}); len(got) != 0 {
		t.Fatalf("empty pool = %v", got)
	}
	single := interleaveTestPool("only", "")
	if got := interleaveByCategory(single); len(got) != 1 || got[0].ID != "only" {
		t.Fatalf("single pool = %v", got)
	}
}

func TestInterleaveByCategorySkewedPoolKeepsEveryClip(t *testing.T) {
	input := interleaveTestPool(
		"n1", "Nature_Landscape", "n2", "Nature_Landscape", "n3", "Nature_Landscape",
		"n4", "Nature_Landscape", "n5", "Nature_Landscape", "n6", "Nature_Landscape",
		"c1", "City_Traffic", "a1", "Architecture",
	)
	got := interleaveByCategory(input)
	assertSameMembers(t, input, got)
	// The two rare categories must be spread into the head instead of being
	// starved; the tail is allowed to degrade to consecutive Nature clips.
	head := got[:4]
	distinct := map[string]bool{}
	for _, item := range head {
		distinct[normalizeCategory(item.Category)] = true
	}
	if len(distinct) != 3 {
		t.Fatalf("head categories = %v", distinct)
	}
	for i := 1; i < len(head); i++ {
		if normalizeCategory(head[i].Category) == normalizeCategory(head[i-1].Category) {
			t.Fatalf("head neighbours %d/%d share category", i-1, i)
		}
	}
}

func TestOnScreenTitleSourceStripsAccountAndTaskSuffix(t *testing.T) {
	got := onScreenTitleSource("天中观局_房贷困境反思_a4b031")
	if got != "房贷困境反思" {
		t.Fatalf("got %q", got)
	}
	if got := onScreenTitleSource("房贷困境与时代反思"); got != "房贷困境与时代反思" {
		t.Fatalf("plain label = %q", got)
	}
}
