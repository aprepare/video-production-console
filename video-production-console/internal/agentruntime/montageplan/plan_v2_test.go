package montageplan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// v2Fixture builds a rich three-kind media library, a word-timed SRT and a
// manifest, and returns the manifest path plus the plan output path.
func v2Fixture(t *testing.T) (manifestPath, planPath string) {
	t.Helper()
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "media")
	for _, dir := range []string{"broll", "movies", "images"} {
		if err := os.MkdirAll(filepath.Join(mediaRoot, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	index := []map[string]any{}
	brollCategories := []string{"Nature_Landscape", "Architecture", "City_Traffic", "Family_Life"}
	for i := 0; i < 12; i++ {
		rel := fmt.Sprintf("broll/b%02d.mp4", i)
		if err := os.WriteFile(filepath.Join(mediaRoot, filepath.FromSlash(rel)), []byte("b"), 0o644); err != nil {
			t.Fatal(err)
		}
		index = append(index, map[string]any{
			"id": fmt.Sprintf("broll-%02d", i), "kind": "broll",
			"category": brollCategories[i%len(brollCategories)], "relative_path": rel,
			"duration_seconds": 30,
		})
	}
	movieCategories := []string{"office", "street", "home"}
	for s := 0; s < 6; s++ {
		rel := fmt.Sprintf("movies/m%02d.mp4", s)
		if err := os.WriteFile(filepath.Join(mediaRoot, filepath.FromSlash(rel)), []byte("m"), 0o644); err != nil {
			t.Fatal(err)
		}
		for k := 0; k < 2; k++ {
			in := float64(40 + k*100)
			index = append(index, map[string]any{
				"id": fmt.Sprintf("movie-%02d-%d", s, k), "kind": "movie",
				"category": movieCategories[s%len(movieCategories)], "relative_path": rel,
				"duration_seconds": 600, "source_in_seconds": in, "source_out_seconds": in + 6,
				"shot_id": fmt.Sprintf("shot-%02d-%d", s, k),
			})
		}
	}
	imageCategories := []string{"ledger", "chart", "people"}
	for i := 0; i < 8; i++ {
		rel := fmt.Sprintf("images/i%02d.png", i)
		if err := os.WriteFile(filepath.Join(mediaRoot, filepath.FromSlash(rel)), []byte("i"), 0o644); err != nil {
			t.Fatal(err)
		}
		index = append(index, map[string]any{
			"id": fmt.Sprintf("image-%02d", i), "kind": "image",
			"category": imageCategories[i%len(imageCategories)], "relative_path": rel,
			"duration_seconds": 0,
		})
	}
	indexPath := filepath.Join(root, "media_index.json")
	rawIndex, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, rawIndex, 0o644); err != nil {
		t.Fatal(err)
	}

	srtPath := filepath.Join(root, "sub.srt")
	if err := os.WriteFile(srtPath, []byte(v2FixtureSRT()), 0o644); err != nil {
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
	manifestPath = filepath.Join(root, "task_manifest.json")
	rawManifest, err := json.Marshal(map[string]any{
		"task_id": taskID, "job_id": taskID, "action": "montage.execute",
		"output_dir": filepath.Join(root, "output"),
		"inputs": []map[string]string{
			{"role": "narration", "path": narration},
			{"role": "account_background", "path": background},
			{"role": "subtitle_srt", "path": srtPath},
		},
		"non_secret_settings": map[string]string{
			"media_root": mediaRoot, "media_index_path": indexPath,
			"draft_display_name": "天中观局_房贷困境反思_a4b031",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, rawManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	return manifestPath, filepath.Join(root, "output", "production_plan.json")
}

// v2FixtureSRT provides 8 highlight sentences of 3.5s each between 6s and 94s
// so the 120s narration can reach the 15%-25% caption coverage window.
func v2FixtureSRT() string {
	stamp := func(ms int) string {
		return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
	}
	var buf bytes.Buffer
	texts := []string{
		"所以家庭现金流必须留出安全垫。",
		"但是很多人忽视了利率变化。",
		"所以要提前核对每月的账单。",
		"但是收入并不总是稳定的。",
		"所以记住不要压上全部积蓄。",
		"但是市场情绪常常放大风险。",
		"所以最后要设置止损的底线。",
		"但是真正的耐心最难做到。",
	}
	for i, text := range texts {
		start := 6000 + i*12000
		fmt.Fprintf(&buf, "%d\n%s --> %s\n%s\n\n", i+1, stamp(start), stamp(start+3500), text)
	}
	return buf.String()
}

func buildV2PlanJSON(t *testing.T, opts Options) map[string]any {
	t.Helper()
	if err := BuildV2(opts); err != nil {
		t.Fatalf("BuildV2: %v", err)
	}
	raw, err := os.ReadFile(opts.PlanPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func v2Options(manifestPath, planPath string) Options {
	return Options{
		ManifestPath: manifestPath,
		PlanPath:     planPath,
		Duration:     func(string) (float64, error) { return 120.0, nil },
	}
}

func TestBuildV2ProducesTypedPlan(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	plan := buildV2PlanJSON(t, v2Options(manifestPath, planPath))

	if plan["plan_version"] != "2.0" || plan["status"] != "approved" || plan["model_role"] != "planner" {
		t.Fatalf("header = %v/%v/%v", plan["plan_version"], plan["status"], plan["model_role"])
	}
	if plan["project_duration_s"].(float64) != 120 {
		t.Fatalf("duration = %v", plan["project_duration_s"])
	}
	mix := plan["media_mix_policy"].(map[string]any)
	if mix["preset"] != "movie_mix" || mix["basis"] != "timeline_duration" {
		t.Fatalf("mix policy = %#v", mix)
	}
	targets := mix["targets"].(map[string]any)
	for _, kind := range []string{"broll", "movie", "image"} {
		entry, ok := targets[kind].(map[string]any)
		if !ok {
			t.Fatalf("targets missing %s: %#v", kind, targets)
		}
		if _, ok := entry["min"].(float64); !ok {
			t.Fatalf("targets.%s.min missing", kind)
		}
	}
	if mix["max_source_uses"].(float64) != 2 || mix["min_segments_between_reuse"].(float64) != 5 {
		t.Fatalf("mix reuse rules = %#v", mix)
	}

	timeline := plan["timeline"].([]any)
	if len(timeline) == 0 {
		t.Fatal("timeline is empty")
	}
	kinds := map[string]bool{}
	cursor := 0.0
	for i, rawShot := range timeline {
		shot := rawShot.(map[string]any)
		if got := shot["shot_no"].(float64); got != float64(i+1) {
			t.Fatalf("shot %d shot_no = %v", i, got)
		}
		if start := shot["start_s"].(float64); start != cursor {
			t.Fatalf("shot %d start_s = %v, want %v", i, start, cursor)
		}
		cursor = shot["end_s"].(float64)
		kind := shot["media_kind"].(string)
		kinds[kind] = true
		if shot["source_id"].(string) == "" || shot["source_path"].(string) == "" {
			t.Fatalf("shot %d missing source identity: %#v", i, shot)
		}
		if shot["opacity"].(float64) != 1.0 {
			t.Fatalf("shot %d opacity = %v", i, shot["opacity"])
		}
		if shot["source_audio_muted"] != true {
			t.Fatalf("shot %d must mute source audio", i)
		}
		if _, present := shot["emphasis_effect"]; !present || shot["emphasis_effect"] != nil {
			t.Fatalf("shot %d emphasis_effect must be an explicit null: %#v", i, shot["emphasis_effect"])
		}
		transition := shot["transition"].(map[string]any)
		if transition["preset"] != "cross_dissolve" || transition["duration_s"].(float64) != 0.466666 {
			t.Fatalf("shot %d transition = %#v", i, transition)
		}
		match := shot["match"].(map[string]any)
		if match["level"] != "neutral" || match["score"].(float64) != 0 {
			t.Fatalf("shot %d match = %#v", i, match)
		}
		if match["intent_id"].(string) == "" || match["reason"].(string) == "" {
			t.Fatalf("shot %d match evidence incomplete: %#v", i, match)
		}
		motion := shot["motion"].(map[string]any)
		preset := motion["preset"].(string)
		scaleFrom := motion["scale_from"].(float64)
		scaleTo := motion["scale_to"].(float64)
		for _, axis := range []string{"x_from", "x_to", "y_from", "y_to"} {
			if pan := motion[axis].(float64); pan < -0.03 || pan > 0.03 {
				t.Fatalf("shot %d motion %s = %v exceeds 3%% pan", i, axis, pan)
			}
		}
		_, hasIn := shot["source_in_s"]
		_, hasOut := shot["source_out_s"]
		switch kind {
		case "image":
			if hasIn || hasOut {
				t.Fatalf("image shot %d must not declare source_in_s/source_out_s: %#v", i, shot)
			}
			if preset != "kenburns_in" && preset != "kenburns_out" {
				t.Fatalf("image shot %d motion preset = %q", i, preset)
			}
			if scaleFrom < 1.0 || scaleFrom > 1.06 || scaleTo < 1.0 || scaleTo > 1.06 {
				t.Fatalf("image shot %d scale = %v..%v", i, scaleFrom, scaleTo)
			}
			if preset == "kenburns_in" && scaleFrom >= scaleTo {
				t.Fatalf("image shot %d kenburns_in requires scale_from < scale_to", i)
			}
			if preset == "kenburns_out" && scaleFrom <= scaleTo {
				t.Fatalf("image shot %d kenburns_out requires scale_from > scale_to", i)
			}
		case "movie", "broll":
			if !hasIn || !hasOut {
				t.Fatalf("%s shot %d requires source_in_s/source_out_s: %#v", kind, i, shot)
			}
			in := shot["source_in_s"].(float64)
			out := shot["source_out_s"].(float64)
			if in < 0 || in >= out {
				t.Fatalf("%s shot %d source window = %v..%v", kind, i, in, out)
			}
			if preset != "steady" && preset != "push" && preset != "pull" {
				t.Fatalf("%s shot %d motion preset = %q", kind, i, preset)
			}
			maxScale := 1.06
			if kind == "movie" {
				maxScale = 1.05
			}
			if scaleFrom < 1.0 || scaleFrom > maxScale || scaleTo < 1.0 || scaleTo > maxScale {
				t.Fatalf("%s shot %d scale = %v..%v", kind, i, scaleFrom, scaleTo)
			}
			if preset == "steady" && scaleFrom != scaleTo {
				t.Fatalf("%s shot %d steady preset requires equal scales", kind, i)
			}
		default:
			t.Fatalf("shot %d unknown media_kind %q", i, kind)
		}
	}
	if cursor != 120 {
		t.Fatalf("timeline ends at %v, want 120", cursor)
	}
	for _, kind := range []string{"broll", "movie", "image"} {
		if !kinds[kind] {
			t.Fatalf("rich library must select every kind, got %v", kinds)
		}
	}

	graphics := plan["graphics"].(map[string]any)
	captions := graphics["captions"].(map[string]any)
	if captions["mode"] != "highlights_only" {
		t.Fatalf("captions mode = %v", captions["mode"])
	}
	if captions["target_coverage_min"].(float64) != 0.15 || captions["target_coverage_max"].(float64) != 0.25 {
		t.Fatalf("caption targets = %#v", captions)
	}
	items := captions["items"].([]any)
	if len(items) == 0 {
		t.Fatal("highlights_only must select caption items from the SRT")
	}
	for i, rawItem := range items {
		item := rawItem.(map[string]any)
		kind := item["kind"].(string)
		if kind != "number" && kind != "turning_point" && kind != "conclusion" {
			t.Fatalf("caption %d kind = %q", i, kind)
		}
		if item["style"] != "highlight_v1" {
			t.Fatalf("caption %d style = %v", i, item["style"])
		}
	}
	if _, forbidden := graphics["caption_tracks"]; forbidden {
		t.Fatal("v2 graphics must not carry the v1 caption_tracks marker")
	}
	if _, forbidden := graphics["subtitle"]; forbidden {
		t.Fatal("v2 graphics must not carry the v1 full-length subtitle")
	}
	labels, ok := graphics["chapter_labels"].([]any)
	if !ok {
		t.Fatalf("chapter_labels must be a list: %#v", graphics["chapter_labels"])
	}
	_ = labels

	qc := plan["qc_expectations"].(map[string]any)
	if qc["max_obvious_effects_per_30s"].(float64) != 1 || qc["full_caption_track_forbidden"] != true {
		t.Fatalf("qc_expectations = %#v", qc)
	}
	audio := plan["audio"].(map[string]any)
	if audio["bgm"] == nil || audio["narration"] == nil {
		t.Fatalf("audio = %#v", audio)
	}
	concurrency := plan["concurrency"].(map[string]any)
	if concurrency["job_id"] != "task-abcdef123456" || concurrency["media_index_lock"] != "media-index-write" {
		t.Fatalf("concurrency = %#v", concurrency)
	}
}

func TestCaptionModeOffProducesNoCaptionItems(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	opts := v2Options(manifestPath, planPath)
	opts.CaptionMode = CaptionOff
	plan := buildV2PlanJSON(t, opts)
	captions := plan["graphics"].(map[string]any)["captions"].(map[string]any)
	if captions["mode"] != "off" {
		t.Fatalf("mode = %v", captions["mode"])
	}
	if captions["target_coverage_min"].(float64) != 0 || captions["target_coverage_max"].(float64) != 0 {
		t.Fatalf("off mode coverage targets = %#v", captions)
	}
	items, ok := captions["items"].([]any)
	if !ok {
		t.Fatalf("items must be an empty list, got %#v", captions["items"])
	}
	if len(items) != 0 {
		t.Fatalf("off mode items = %#v", items)
	}
}

func TestOpeningTitleEndsWithinFiveSeconds(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	plan := buildV2PlanJSON(t, v2Options(manifestPath, planPath))
	graphics := plan["graphics"].(map[string]any)
	title := graphics["opening_title"].(map[string]any)
	if title["start_s"].(float64) != 0 {
		t.Fatalf("opening title start = %v", title["start_s"])
	}
	end := title["end_s"].(float64)
	if end < 3 || end > 5 {
		t.Fatalf("opening title end = %v, want 3-5s", end)
	}
	if title["style"] != "opening_title_v1" || title["text"].(string) == "" {
		t.Fatalf("opening title = %#v", title)
	}
	for i, rawItem := range graphics["captions"].(map[string]any)["items"].([]any) {
		item := rawItem.(map[string]any)
		if item["start_s"].(float64) < end {
			t.Fatalf("caption %d overlaps the opening title: %#v", i, item)
		}
	}
}

func TestObviousEffectsAreAtLeastTwentySecondsApart(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	plan := buildV2PlanJSON(t, v2Options(manifestPath, planPath))
	starts := []float64{}
	for _, rawShot := range plan["timeline"].([]any) {
		shot := rawShot.(map[string]any)
		if shot["emphasis_effect"] != nil {
			starts = append(starts, shot["start_s"].(float64))
		}
	}
	// The conservative whitelist has no verified emphasis effect, so the plan
	// must declare none at all; the spacing rule is then trivially satisfied.
	if len(starts) != 0 {
		t.Fatalf("no emphasis effect is verified yet, got %v", starts)
	}
	for i := 1; i < len(starts); i++ {
		if starts[i]-starts[i-1] < 20 {
			t.Fatalf("effects at %v and %v are closer than 20s", starts[i-1], starts[i])
		}
	}
}

func TestBuildV2SlotLengthsFollowKindRanges(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	plan := buildV2PlanJSON(t, v2Options(manifestPath, planPath))
	timeline := plan["timeline"].([]any)
	for i, rawShot := range timeline {
		shot := rawShot.(map[string]any)
		start := shot["start_s"].(float64)
		length := shot["end_s"].(float64) - start
		if i == len(timeline)-1 {
			// The final slot absorbs the tail; it only has to stay positive
			// and bounded.
			if length <= 0 || length > 12 {
				t.Fatalf("final slot length = %v", length)
			}
			continue
		}
		lo, hi := 4.0, 7.0
		if start >= 30 {
			switch shot["media_kind"].(string) {
			case "movie":
				lo, hi = 3.0, 6.0
			case "broll":
				lo, hi = 5.0, 9.0
			default:
				lo, hi = 4.0, 7.0
			}
		}
		if length < lo-1e-9 || length > hi+1e-9 {
			t.Fatalf("shot %d (%s at %vs) length = %v, want [%v, %v]",
				i, shot["media_kind"], start, length, lo, hi)
		}
	}
}

func TestBuildV2MovieAndBrollStayInsideSourceWindows(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	plan := buildV2PlanJSON(t, v2Options(manifestPath, planPath))
	for i, rawShot := range plan["timeline"].([]any) {
		shot := rawShot.(map[string]any)
		kind := shot["media_kind"].(string)
		if kind == "image" {
			continue
		}
		in := shot["source_in_s"].(float64)
		out := shot["source_out_s"].(float64)
		slot := shot["end_s"].(float64) - shot["start_s"].(float64)
		if diff := (out - in) - slot; diff > 0.002 || diff < -0.002 {
			t.Fatalf("shot %d source window %v..%v does not match slot %v", i, in, out, slot)
		}
		if kind == "movie" {
			// Fixture movie shots expose windows like [40,46] or [140,146].
			if out > in+6+1e-9 {
				t.Fatalf("movie shot %d exceeds its 6s shot window: %v..%v", i, in, out)
			}
		}
	}
}

func TestBuildV2IsByteStableForSameInput(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	opts := v2Options(manifestPath, planPath)
	if err := BuildV2(opts); err != nil {
		t.Fatalf("BuildV2 first: %v", err)
	}
	first, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := BuildV2(opts); err != nil {
		t.Fatalf("BuildV2 second: %v", err)
	}
	second, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("same input and job ID must produce byte-identical v2 plans")
	}
}

func TestBuildV2RejectsUnknownMixPreset(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	opts := v2Options(manifestPath, planPath)
	opts.MixPreset = "party_mix"
	if err := BuildV2(opts); err == nil {
		t.Fatal("unknown mix preset must fail, not silently use the default")
	}
	if _, err := os.Stat(planPath); !os.IsNotExist(err) {
		t.Fatalf("plan must not be written on preset error: %v", err)
	}
}

func TestBuildV2ImageVideoPresetWritesPresetName(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	opts := v2Options(manifestPath, planPath)
	opts.MixPreset = "image_video"
	plan := buildV2PlanJSON(t, opts)
	mix := plan["media_mix_policy"].(map[string]any)
	if mix["preset"] != "image_video" {
		t.Fatalf("preset = %v", mix["preset"])
	}
	movie := mix["targets"].(map[string]any)["movie"].(map[string]any)
	if movie["min"].(float64) != 0 || movie["max"].(float64) != 0 {
		t.Fatalf("image_video movie target = %#v", movie)
	}
	// The fixture has only 8 stills, so the substitution matrix may degrade
	// onto video kinds; the preset must still dominate the selection order:
	// every still gets used before any degradation happens.
	imageShots := 0
	for _, rawShot := range plan["timeline"].([]any) {
		if rawShot.(map[string]any)["media_kind"] == "image" {
			imageShots++
		}
	}
	if imageShots < 8 {
		t.Fatalf("image_video must consume the whole still library first, used %d of 8", imageShots)
	}
}

func TestBuildV2BrollMotionPresetsNeverRepeatMoreThanTwice(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	plan := buildV2PlanJSON(t, v2Options(manifestPath, planPath))
	run := 0
	prev := ""
	for _, rawShot := range plan["timeline"].([]any) {
		shot := rawShot.(map[string]any)
		if shot["media_kind"] != "broll" {
			continue
		}
		preset := shot["motion"].(map[string]any)["preset"].(string)
		if preset == prev {
			run++
		} else {
			prev, run = preset, 1
		}
		if run > 2 {
			t.Fatalf("broll motion preset %q repeated more than twice in a row", preset)
		}
	}
}
