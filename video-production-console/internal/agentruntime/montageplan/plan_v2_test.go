package montageplan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
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
	brollCategories := []string{"Nature_Landscape", "Nature_Landscape", "Nature_Landscape", "City_Traffic"}
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
	movieCategories := []string{"Nature_Landscape", "Nature_Landscape", "office"}
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
	for i := 0; i < 10; i++ {
		rel := fmt.Sprintf("images/i%02d.png", i)
		if err := os.WriteFile(filepath.Join(mediaRoot, filepath.FromSlash(rel)), []byte("i"), 0o644); err != nil {
			t.Fatal(err)
		}
		category := "Nature_Landscape"
		if i == 9 {
			category = "ledger"
		}
		index = append(index, map[string]any{
			"id": fmt.Sprintf("image-%02d", i), "kind": "image",
			"category": category, "relative_path": rel,
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

func TestValidateMovieCatalog(t *testing.T) {
	if err := ValidateMovieCatalog(""); err == nil {
		t.Fatal("empty catalog path must fail")
	}
	missing := filepath.Join(t.TempDir(), "missing.db")
	if err := ValidateMovieCatalog(missing); err == nil {
		t.Fatal("missing catalog file must fail")
	}
	dir := t.TempDir()
	if err := ValidateMovieCatalog(dir); err == nil {
		t.Fatal("directory catalog path must fail")
	}
	path := filepath.Join(t.TempDir(), "catalog.db")
	if err := os.WriteFile(path, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMovieCatalog(path); err != nil {
		t.Fatal(err)
	}
}

func TestBuildV2ProducesTypedPlan(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	plan := buildV2PlanJSON(t, v2Options(manifestPath, planPath))

	if plan["plan_version"] != "2.0" || plan["status"] != "approved" || plan["model_role"] != "planner" {
		t.Fatalf("header = %v/%v/%v", plan["plan_version"], plan["status"], plan["model_role"])
	}
	assertSafeExecutionActions(t, plan["execution_actions"])
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
		if shot["source_id"] == "image-09" {
			t.Fatalf("shot %d used ledger still %q", i, shot["source_id"])
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
			if scaleFrom < 1.18 || scaleFrom > 1.30 || scaleTo < 1.18 || scaleTo > 1.30 {
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
			if scaleFrom < 1.18 || scaleFrom > 1.30 || scaleTo < 1.18 || scaleTo > 1.30 {
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
	if captions["mode"] != "off" {
		t.Fatalf("captions mode = %v", captions["mode"])
	}
	items, ok := captions["items"].([]any)
	if !ok {
		t.Fatalf("items must be a list, got %#v", captions["items"])
	}
	if len(items) != 0 {
		t.Fatalf("default plan must not paint spoken captions: %#v", items)
	}
	if _, forbidden := graphics["caption_tracks"]; forbidden {
		t.Fatal("v2 graphics must not carry the v1 caption_tracks marker")
	}
	titleOverlay, ok := graphics["title"].(map[string]any)
	if !ok || titleOverlay["full_duration"] != true || titleOverlay["text"] != "房贷困境反思" {
		t.Fatalf("v2 graphics.title must be the full-duration board title: %#v", graphics["title"])
	}
	subtitleOverlay, ok := graphics["subtitle"].(map[string]any)
	if !ok || subtitleOverlay["full_duration"] != true || subtitleOverlay["text"] != "家庭财务提醒" {
		t.Fatalf("v2 graphics.subtitle must be the full-duration board subtitle: %#v", graphics["subtitle"])
	}
	lines, ok := graphics["boundary_lines"].(map[string]any)
	if !ok || lines["asset_width_px"].(float64) != 1080 || lines["asset_height_px"].(float64) != 6 {
		t.Fatalf("v2 graphics.boundary_lines = %#v", graphics["boundary_lines"])
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

func TestCaptionModeSpokenAddsSRTLines(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	opts := v2Options(manifestPath, planPath)
	opts.CaptionMode = CaptionSpoken
	plan := buildV2PlanJSON(t, opts)
	captions := plan["graphics"].(map[string]any)["captions"].(map[string]any)
	if captions["mode"] != "spoken" {
		t.Fatalf("captions mode = %v", captions["mode"])
	}
	if captions["target_coverage_min"].(float64) != 0 || captions["target_coverage_max"].(float64) != 1 {
		t.Fatalf("spoken mode coverage targets = %#v", captions)
	}
	items, ok := captions["items"].([]any)
	if !ok {
		t.Fatalf("items must be a list, got %#v", captions["items"])
	}
	if len(items) == 0 {
		t.Fatalf("spoken mode must add captions from the SRT: %#v", items)
	}
	first := items[0].(map[string]any)
	if first["kind"] != "spoken" || first["style"] != "spoken_v1" {
		t.Fatalf("spoken caption item = %#v", first)
	}
}

func TestCaptionModeHighlightsOnlySelectsFromSRT(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	opts := v2Options(manifestPath, planPath)
	opts.CaptionMode = CaptionHighlightsOnly
	plan := buildV2PlanJSON(t, opts)
	captions := plan["graphics"].(map[string]any)["captions"].(map[string]any)
	if captions["mode"] != "highlights_only" {
		t.Fatalf("mode = %v", captions["mode"])
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
}

func TestCaptionModeOffHasNoItems(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	opts := v2Options(manifestPath, planPath)
	opts.CaptionMode = CaptionOff
	plan := buildV2PlanJSON(t, opts)
	captions := plan["graphics"].(map[string]any)["captions"].(map[string]any)
	if captions["mode"] != "off" {
		t.Fatalf("mode = %v", captions["mode"])
	}
	items := captions["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("off mode items = %#v", items)
	}
}

func TestSplitSpokenLineBreaksAroundEightRunesAndMergesTails(t *testing.T) {
	pieces := splitSpokenLine("三年前大家挤破头往房子里砸钱还叫投资，", 0)
	got := make([]string, 0, len(pieces))
	for _, piece := range pieces {
		got = append(got, piece.text)
		if n := len([]rune(piece.text)); n > 12 {
			t.Fatalf("spoken line %q is %d runes, want <=12", piece.text, n)
		}
	}
	if len(got) < 2 {
		t.Fatalf("expected wrapped spoken lines, got %#v", got)
	}
	if got[0] != "三年前大家挤破头" {
		t.Fatalf("first line = %q, want 8-rune wrap", got[0])
	}
	for _, line := range got {
		if line == "叫投资，" {
			t.Fatalf("leftover tail was not merged: %#v", got)
		}
	}
}

type fakeLineBreaker struct {
	lines [][]string
	pack  CaptionPack
	err   error
	calls int
}

func (f *fakeLineBreaker) BreakLines(_ context.Context, sentences []string) (CaptionPack, error) {
	f.calls++
	if f.err != nil {
		return CaptionPack{}, f.err
	}
	if len(f.pack.Groups) > 0 {
		if len(f.pack.Groups) != len(sentences) {
			return CaptionPack{}, fmt.Errorf("fixture covers %d sentences, got %d", len(f.pack.Groups), len(sentences))
		}
		return f.pack, nil
	}
	if len(f.lines) != len(sentences) {
		return CaptionPack{}, fmt.Errorf("fixture covers %d sentences, got %d", len(f.lines), len(sentences))
	}
	return captionPackFromPlainLines(f.lines), nil
}

func TestSpokenCaptionsUseModelLinesVerbatim(t *testing.T) {
	sentences := []TimedSentence{{StartMS: 0, EndMS: 6000, Text: "全国法拍房挂牌接近四十万套，同比大涨百分之十九。"}}
	breaker := &fakeLineBreaker{lines: [][]string{{"全国法拍房挂牌", "接近四十万套", "同比大涨", "百分之十九"}}}
	items, _, notes := spokenCaptionsFromSentences(sentences, 371304, breaker, "")
	if breaker.calls != 1 {
		t.Fatalf("line breaker calls = %d", breaker.calls)
	}
	got := make([]string, 0, len(items))
	for _, item := range items {
		got = append(got, item.Text)
	}
	want := []string{"全国法拍房挂牌", "接近四十万套", "同比大涨", "百分之十九"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("captions = %v, want %v (notes %v)", got, want, notes)
	}
	if items[0].StartS != 0 || items[len(items)-1].EndS <= items[0].EndS {
		t.Fatalf("caption window = %#v", items)
	}
}

func TestSpokenCaptionsFallBackWhenModelLinesDoNotRebuildTheSentence(t *testing.T) {
	sentences := []TimedSentence{{StartMS: 0, EndMS: 6000, Text: "全国法拍房挂牌接近四十万套，同比大涨百分之十九。"}}
	breaker := &fakeLineBreaker{lines: [][]string{{"全国法拍房", "改写了内容"}}}
	items, _, notes := spokenCaptionsFromSentences(sentences, 371304, breaker, "")
	if len(items) == 0 {
		t.Fatal("a rejected model answer must fall back to the deterministic splitter")
	}
	for _, item := range items {
		if item.Text == "改写了内容" {
			t.Fatalf("model text that does not rebuild the sentence was used: %#v", items)
		}
	}
	if !strings.Contains(strings.Join(notes, "|"), "caption_lines_rejected") {
		t.Fatalf("planner notes must record the rejection: %v", notes)
	}
}

func TestSpokenCaptionLinesAreCachedPerSubtitleDigest(t *testing.T) {
	if !CaptionLLMLineBreakerEnabled {
		t.Skip("caption LLM cache is off while the line breaker is disabled")
	}
	sentences := []TimedSentence{{StartMS: 0, EndMS: 6000, Text: "全国法拍房挂牌接近四十万套，同比大涨百分之十九。"}}
	cache := filepath.Join(t.TempDir(), "caption_lines.json")
	first := &fakeLineBreaker{lines: [][]string{{"全国法拍房挂牌", "接近四十万套", "同比大涨", "百分之十九"}}}
	if _, _, notes := spokenCaptionsFromSentences(sentences, 371304, first, cache); len(notes) > 0 {
		t.Fatalf("unexpected notes: %v", notes)
	}
	offline := &fakeLineBreaker{err: errors.New("model unavailable")}
	items, _, notes := spokenCaptionsFromSentences(sentences, 371304, offline, cache)
	if offline.calls != 0 {
		t.Fatalf("cached lines must not call the model again, calls = %d", offline.calls)
	}
	if len(items) != 4 || items[0].Text != "全国法拍房挂牌" {
		t.Fatalf("cached captions = %#v notes=%v", items, notes)
	}
}

func TestSpokenCaptionsPaintModelKeywords(t *testing.T) {
	sentences := []TimedSentence{{StartMS: 0, EndMS: 6000, Text: "三年前大家挤破头往房子里砸钱还叫投资。"}}
	breaker := &fakeLineBreaker{pack: CaptionPack{Groups: [][]CaptionLine{{
		{Text: "三年前大家挤破头", Keywords: []string{"挤破头"}},
		{Text: "往房子里砸钱", Keywords: []string{"砸钱"}},
		{Text: "还叫投资", Keywords: []string{"投资"}},
	}}}}
	items, pack, notes := spokenCaptionsFromSentences(sentences, 371304, breaker, "")
	if len(notes) > 0 {
		t.Fatalf("notes = %v", notes)
	}
	if pack.Groups[0][2].Keywords[0] != "投资" {
		t.Fatalf("pack keywords = %#v", pack.Groups)
	}
	marked := map[string]bool{}
	for _, item := range items {
		runes := []rune(item.Text)
		for _, span := range item.Spans {
			marked[string(runes[span.Start:span.End])] = true
		}
	}
	for _, want := range []string{"挤破头", "砸钱", "投资"} {
		if !marked[want] {
			t.Fatalf("model keyword %q was not painted, got %v texts=%v", want, marked, captionTexts(items))
		}
	}
}

func captionTexts(items []CaptionItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Text)
	}
	return out
}

func TestSpokenLinesDropPunctuationAndKeepWordsWhole(t *testing.T) {
	sentences := []TimedSentence{
		{StartMS: 0, EndMS: 6982, Text: "三年前大家挤破头往房子里砸钱还叫投资，三年后想脱手才发现自己成了接盘的那一个。"},
		{StartMS: 7082, EndMS: 16920, Text: "今年前五个月，全国法拍房挂牌接近四十万套，日均上新超过两千六百套，每一套背后都是一个被逼到墙角的家庭。"},
		{StartMS: 17020, EndMS: 30806, Text: "国家统计局的口径里，全国常住人口城镇化率到了百分之六十七，消化周期超过三十六个月的城市要暂停卖地。"},
	}
	items, _, warnings := spokenCaptionsFromSentences(sentences, 371304, nil, "")
	if len(items) == 0 {
		t.Fatalf("no spoken captions: %v", warnings)
	}
	texts := make([]string, 0, len(items))
	for _, item := range items {
		texts = append(texts, item.Text)
		if strings.ContainsAny(item.Text, "，。、；：！？…,;:") {
			t.Fatalf("caption keeps punctuation: %q", item.Text)
		}
		if n := len([]rune(item.Text)); n > spokenLineMaxRunes || n < 2 {
			t.Fatalf("caption %q is %d runes, want at most %d", item.Text, n, spokenLineMaxRunes)
		}
	}
	joined := strings.Join(texts, "|")
	for _, word := range []string{"接近", "平米", "百分之", "个月", "法拍房", "城镇化率", "四十万", "超过"} {
		for cut := 1; cut < len([]rune(word)); cut++ {
			runes := []rune(word)
			broken := string(runes[:cut]) + "|" + string(runes[cut:])
			if strings.Contains(joined, broken) {
				t.Fatalf("word %q was split across lines as %q", word, broken)
			}
		}
	}
}

func TestSpokenCaptionSpansMarkNumbersAndFinanceTerms(t *testing.T) {
	sentences := []TimedSentence{{
		StartMS: 0,
		EndMS:   9000,
		Text:    "全国法拍房挂牌接近四十万套，同比大涨百分之十九。",
	}}
	items, _, _ := spokenCaptionsFromSentences(sentences, 371304, nil, "")
	marked := map[string]bool{}
	for _, item := range items {
		runes := []rune(item.Text)
		prevEnd := 0
		for _, span := range item.Spans {
			if span.Start < prevEnd || span.End <= span.Start || span.End > len(runes) {
				t.Fatalf("caption %q has invalid span %#v", item.Text, span)
			}
			if span.Style != spokenKeywordStyle {
				t.Fatalf("span style = %q", span.Style)
			}
			marked[string(runes[span.Start:span.End])] = true
			prevEnd = span.End
		}
	}
	for _, want := range []string{"法拍房", "四十万套", "百分之十九"} {
		if !marked[want] {
			t.Fatalf("keyword %q was not marked, got %v", want, marked)
		}
	}
}

func TestBoardTitleKeepsFullShortTitleAndShrinksToFit(t *testing.T) {
	title, subtitle := titlePair("接盘之后五个要命难题", "法拍房快堆到四十万")
	if title != "接盘之后五个要命难题" || subtitle != "法拍房快堆到四十万" {
		t.Fatalf("board copy was truncated: %q / %q", title, subtitle)
	}
	if size := boardTextSize(len([]rune(title)), boardTitleSize); size >= boardTitleSize || size < 9 {
		t.Fatalf("10-rune title size = %v, want between 9 and %v", size, boardTitleSize)
	}
	if size := boardTextSize(8, boardTitleSize); size != boardTitleSize {
		t.Fatalf("8-rune title must keep the QC size, got %v", size)
	}
	if _, over := titlePair(strings.Repeat("长", 20), ""); over == "" {
		t.Fatal("subtitle fallback must stay non-empty")
	}
	if long, _ := titlePair(strings.Repeat("长", 20), ""); len([]rune(long)) != 15 {
		t.Fatalf("over-long title = %d runes, want the 15 cap", len([]rune(long)))
	}
}

func TestBoardTitlesPreferFrozenManifestFieldsWithoutPackageFile(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	settings := manifest["non_secret_settings"].(map[string]any)
	settings["board_title"] = "接盘之后五个要命"
	settings["board_subtitle"] = "法拍房快堆到四十"
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	plan := buildV2PlanJSON(t, v2Options(manifestPath, planPath))
	graphics := plan["graphics"].(map[string]any)
	if graphics["title"].(map[string]any)["text"] != "接盘之后五个要命" {
		t.Fatalf("title = %#v", graphics["title"])
	}
	if graphics["subtitle"].(map[string]any)["text"] != "法拍房快堆到四十" {
		t.Fatalf("subtitle = %#v", graphics["subtitle"])
	}
}

func TestSpokenCaptionSplitsDoNotOverlapAfterMicrosecondRounding(t *testing.T) {
	sentences := []TimedSentence{{
		StartMS: 0,
		EndMS:   6982,
		Text:    "三年前大家挤破头往房子里砸钱还叫投资，三年后想脱手才发现自己成了接盘的那一个。",
	}}
	items, _, warnings := spokenCaptionsFromSentences(sentences, 371304, nil, "")
	if len(items) < 2 {
		t.Fatalf("items=%#v warnings=%v", items, warnings)
	}
	var prevEnd int64
	for i, item := range items {
		start := int64(math.Round(item.StartS * 1e6))
		end := start + int64(math.Round((item.EndS-item.StartS)*1e6))
		if start < prevEnd {
			t.Fatalf("item %d %q overlaps previous: start=%d prevEnd=%d", i, item.Text, start, prevEnd)
		}
		if end <= start {
			t.Fatalf("item %d %q has empty range %d-%d", i, item.Text, start, end)
		}
		prevEnd = end
	}
}

func TestBoardTitlesPreferPublishingShortTitles(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	root := filepath.Dir(manifestPath)
	projectID := "11111111-1111-1111-1111-111111111111"
	packageDir := filepath.Join(root, "projects", projectID)
	if err := os.MkdirAll(packageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "publishing_package.json"), []byte(`{"short_titles":["接盘之后五个要命难题","法拍房快堆到四十万"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["project"] = map[string]string{"id": projectID, "account_id": "22222222-2222-2222-2222-222222222222"}
	settings := manifest["non_secret_settings"].(map[string]any)
	settings["data_root"] = root
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	plan := buildV2PlanJSON(t, v2Options(manifestPath, planPath))
	graphics := plan["graphics"].(map[string]any)
	title := graphics["title"].(map[string]any)
	if title["text"] != "接盘之后五个要命难题" {
		t.Fatalf("title = %#v", title)
	}
	if size := title["size_min"].(float64); size >= boardTitleSize {
		t.Fatalf("a 10-rune title must shrink to fit, size_min = %v", size)
	}
	if graphics["subtitle"].(map[string]any)["text"] != "法拍房快堆到四十万" {
		t.Fatalf("subtitle = %#v", graphics["subtitle"])
	}
}

func TestBoardTitlesUseCaptionModelWhenPackageMissing(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	opts := v2Options(manifestPath, planPath)
	opts.CaptionMode = CaptionSpoken
	opts.LineBreaker = &fakeLineBreaker{pack: CaptionPack{
		BoardTitle:    "接盘之后五个要命难题",
		BoardSubtitle: "法拍房快堆到四十万",
		Groups: [][]CaptionLine{
			{{Text: "所以家庭现金流"}, {Text: "必须留出安全垫"}},
			{{Text: "但是很多人忽视了"}, {Text: "利率变化"}},
			{{Text: "所以要提前"}, {Text: "核对每月的账单"}},
			{{Text: "但是收入并不总是"}, {Text: "稳定的"}},
			{{Text: "所以记住不要压上"}, {Text: "全部积蓄"}},
			{{Text: "但是市场情绪常常"}, {Text: "放大风险"}},
			{{Text: "所以最后要设置"}, {Text: "止损的底线"}},
			{{Text: "但是真正的耐心"}, {Text: "最难做到"}},
		},
	}}
	plan := buildV2PlanJSON(t, opts)
	graphics := plan["graphics"].(map[string]any)
	if graphics["title"].(map[string]any)["text"] != "接盘之后五个要命难题" {
		t.Fatalf("title = %#v", graphics["title"])
	}
	if graphics["subtitle"].(map[string]any)["text"] != "法拍房快堆到四十万" {
		t.Fatalf("subtitle = %#v", graphics["subtitle"])
	}
}

func TestBuildV2FailsWhenCaptionLineBreakerIsUnreachable(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	opts := v2Options(manifestPath, planPath)
	opts.CaptionMode = CaptionSpoken
	opts.LineBreaker = &fakeLineBreaker{err: errors.New("connection reset")}
	err := BuildV2(opts)
	if err == nil || !strings.Contains(err.Error(), "caption_lines_unavailable") {
		t.Fatalf("unreachable caption model must fail the plan, err=%v", err)
	}
}

func TestV2PlanOmitsWindowOpeningTitle(t *testing.T) {
	manifestPath, planPath := v2Fixture(t)
	plan := buildV2PlanJSON(t, v2Options(manifestPath, planPath))
	graphics := plan["graphics"].(map[string]any)
	if _, present := graphics["opening_title"]; present {
		t.Fatalf("v2 plan must not declare a window opening title: %#v", graphics["opening_title"])
	}
	if graphics["title"].(map[string]any)["text"] == "" || graphics["subtitle"].(map[string]any)["text"] == "" {
		t.Fatalf("board title/subtitle missing: %#v", graphics)
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

func TestV2SourceWindowUsesPlaybackSpeed(t *testing.T) {
	item := mediaItem{
		Kind: mediaKindBroll, DurationSeconds: 20,
		SourceInSeconds: 2, SourceOutSeconds: 14, ShotID: "shot-speed",
	}
	in, out := v2SourceWindow(item, 8, 0)
	if math.Abs((out-in)-8*v2PlaybackSpeed) > 0.05 {
		t.Fatalf("window %v..%v does not consume %.1fx of an 8s slot", in, out, v2PlaybackSpeed)
	}
	if in < 2-1e-9 || out > 14+1e-9 {
		t.Fatalf("window %v..%v left the shot range 2..14", in, out)
	}
}

func TestV2SourceWindowShiftsOnReuse(t *testing.T) {
	item := mediaItem{Kind: mediaKindBroll, DurationSeconds: 40}
	firstIn, firstOut := v2SourceWindow(item, 8, 0)
	secondIn, secondOut := v2SourceWindow(item, 8, 1)
	need := 8 * v2PlaybackSpeed
	if math.Abs((firstOut-firstIn)-need) > 0.05 || math.Abs((secondOut-secondIn)-need) > 0.05 {
		t.Fatalf("reuse windows %v..%v and %v..%v must each consume %.1fx of an 8s slot",
			firstIn, firstOut, secondIn, secondOut, v2PlaybackSpeed)
	}
	if secondIn <= firstIn+1e-9 {
		t.Fatalf("reuse must start later than first cut %v, got %v", firstIn, secondIn)
	}
	if math.Abs(secondIn-(firstIn+need)) > 0.05 {
		t.Fatalf("second cut %v should start one source window after first cut %v", secondIn, firstIn)
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
		if slot <= 0 {
			t.Fatalf("shot %d has empty timeline slot", i)
		}
		speed := (out - in) / slot
		if math.Abs(speed-v2PlaybackSpeed) > 0.02 {
			t.Fatalf("shot %d playback speed = %.3f from window %v..%v / slot %v, want %.2f",
				i, speed, in, out, slot, v2PlaybackSpeed)
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
	// The fixture keeps 9 landscape stills (one ledger poison pill is
	// filtered out). The preset must still dominate: landscape stills
	// get used before any degradation onto video kinds.
	imageShots := 0
	for _, rawShot := range plan["timeline"].([]any) {
		if rawShot.(map[string]any)["media_kind"] == "image" {
			imageShots++
		}
	}
	if imageShots < 8 {
		t.Fatalf("image_video must consume the landscape still library first, used %d", imageShots)
	}
}

func TestIsLandscapeItemAcceptsScenicCityAndFinance(t *testing.T) {
	keep := []mediaItem{
		{Category: "Nature_Landscape"},
		{Category: "Weather_Water_Fire"},
		{Category: "Space_Cosmos"},
		{Category: "City_Traffic"},
		{Category: "Finance_Business"},
		{Category: "office"},
		{Category: "风景"},
		{Category: "城市景观"},
		{RelativePath: "14_Pexels/landscape/lake.mp4"},
		{RelativePath: "03_Weather_Water_Fire/storm.mp4"},
		{RelativePath: "02_City_Traffic/skyline.mp4"},
		{Tags: []string{"风景"}},
		{Tags: []string{"车流"}},
		{Tags: []string{"财经"}},
	}
	drop := []mediaItem{
		{Category: "Family_Life"},
		{Category: "ledger"},
		{Category: "Food_Drink"},
		{RelativePath: "movies/bank.mp4", Tags: []string{"银行柜台"}},
	}
	for _, item := range keep {
		if !isLandscapeItem(item) {
			t.Fatalf("should keep %#v", item)
		}
	}
	for _, item := range drop {
		if isLandscapeItem(item) {
			t.Fatalf("should drop %#v", item)
		}
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
