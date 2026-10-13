package montageplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cityfixture "video-production-console/internal/testutil/citylib"
)

func TestCityOpeningMotionDetectsStaticAndMovingVideo(t *testing.T) {
	for _, tc := range []struct {
		name, filter string
		passed       bool
	}{
		{"static", "color=c=blue:s=128x72:r=12", false},
		{"moving", "testsrc2=s=128x72:r=12", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, ffmpeg := cityfixture.Video(t, tc.filter)
			plan := &CityLibraryReservation{ProjectID: "console-motion", Duration: 4, Clips: []CityLibraryClip{{AssetID: "sample", Path: path, SourceIn: 2, SourceOut: 6, Duration: 4, TimelineIn: 0, TimelineOut: 4}}}
			review, err := reviewCityOpening(t.Context(), ffmpeg, plan)
			if err != nil {
				t.Fatal(err)
			}
			if review.Passed != tc.passed || review.CheckedUntil != 4 || len(review.Evidence) != 1 || len(review.Evidence[0].MeanPixelDifferences) != 2 || review.Evidence[0].Static == tc.passed {
				t.Fatalf("review=%+v", review)
			}
			for _, at := range review.Evidence[0].SampleTimes {
				if at < 2 || at >= 6 {
					t.Fatalf("sample outside source interval: %f", at)
				}
			}
		})
	}
}

func TestCityStaticOpeningReselectionAndStableRetry(t *testing.T) {
	f := cityfixture.New(t)
	still, ffmpeg := cityfixture.Video(t, "color=c=blue:s=128x72:r=12")
	moving, _ := cityfixture.Video(t, "testsrc2=s=128x72:r=12")
	f.Add(t, "static", "Housing_Residential")
	f.Exec(t, "UPDATE assets SET path=? WHERE asset_id='static'", still)
	for _, id := range []string{"moving-a", "moving-b", "moving-c"} {
		f.Add(t, id, "City_Aerial")
		f.Exec(t, "UPDATE assets SET path=? WHERE asset_id=?", moving, id)
		f.History(t, id, "other", 1, 0)
	}
	pc := cityTestContext(t, f, 12)
	pc.manifest.NonSecretSettings.FFmpegPath = ffmpeg
	opts := Options{Context: t.Context(), PlanPath: filepath.Join(pc.manifest.OutputDir, "production_plan.json")}
	shots, _, err := buildScenicSemanticPlan(opts, pc)
	if err != nil {
		t.Fatal(err)
	}
	if shots[0].SourceID == "static" || shots[1].SourceID == "static" || shots[2].SourceID != "static" {
		t.Fatalf("opening exclusion must end at ten seconds: %+v", shots)
	}
	var receipt CityLibraryReservation
	raw, err := os.ReadFile(filepath.Join(pc.manifest.OutputDir, "citylib_reservation.json"))
	if err != nil || json.Unmarshal(raw, &receipt) != nil {
		t.Fatalf("receipt: %v", err)
	}
	if receipt.ProjectID != "console-city-test-r2" || !receipt.OpeningMotionReview.Passed || receipt.OpeningMotionReview.CheckedUntil != 10 {
		t.Fatalf("receipt=%+v", receipt)
	}
	staticEvidence := false
	for _, e := range receipt.OpeningMotionReview.Evidence {
		staticEvidence = staticEvidence || (e.AssetID == "static" && e.Static)
	}
	if !staticEvidence {
		t.Fatal("lost failed motion evidence")
	}
	if f.Count(t, "SELECT COUNT(*) FROM projects WHERE project_id='console-city-test' AND status='cancelled'") != 1 || f.Count(t, "SELECT COUNT(*) FROM reservations WHERE project_id='console-city-test'") != 0 {
		t.Fatal("static attempt leaked")
	}
	again, _, err := buildScenicSemanticPlan(opts, pc)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(shots)
	b, _ := json.Marshal(again)
	if string(a) != string(b) || f.Count(t, "SELECT COUNT(*) FROM reservations") != 3 {
		t.Fatal("retry must reuse latest reservation")
	}
	var qc ScenicSelectionQC
	data, _ := os.ReadFile(filepath.Join(pc.manifest.OutputDir, "scenic_selection_qc.json"))
	if json.Unmarshal(data, &qc) != nil || qc.Basis != "architecture_city_catalog_reservation" || qc.RepeatCount != 0 || len(qc.SourceKeys) != 3 || qc.SourceKeys[0] != shots[0].SourceID {
		t.Fatalf("qc=%+v", qc)
	}
}

func TestCityMotionToolFailureReleasesReservation(t *testing.T) {
	f := cityfixture.New(t)
	f.Add(t, "sample", "City_Aerial")
	pc := cityTestContext(t, f, 4)
	pc.manifest.NonSecretSettings.FFmpegPath = filepath.Join(t.TempDir(), "absent-ffmpeg")
	_, _, err := buildScenicSemanticPlan(Options{PlanPath: filepath.Join(pc.manifest.OutputDir, "production_plan.json")}, pc)
	if err == nil || !strings.Contains(err.Error(), "开头运动检测") {
		t.Fatalf("%v", err)
	}
	if f.Count(t, "SELECT COUNT(*) FROM reservations") != 0 {
		t.Fatal("motion failure leaked reservation")
	}
}

func TestCityMotionEvidenceSurvivesInterruptedRevision(t *testing.T) {
	f := cityfixture.New(t)
	f.Add(t, "static", "Housing_Residential")
	moving, ffmpeg := cityfixture.Video(t, "testsrc2=s=128x72:r=12")
	f.Add(t, "moving", "City_Aerial")
	f.Exec(t, "UPDATE assets SET path=? WHERE asset_id='moving'", moving)
	f.History(t, "moving", "other", 1, 0)
	pc := cityTestContext(t, f, 4)
	pc.manifest.NonSecretSettings.FFmpegPath = ffmpeg
	opts := Options{Context: t.Context(), PlanPath: filepath.Join(pc.manifest.OutputDir, "production_plan.json")}
	first, err := reserveCityLibrary(t.Context(), pc, defaultScenicPolicy(), cityProjectID(pc.manifest.TaskID, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	first.OpeningMotionReview = CityOpeningMotionReview{CheckedUntil: 4, Evidence: []CityOpeningMotionEvidence{{ProjectID: first.ProjectID, AssetID: "static", Static: true}}}
	if err := saveCityReview(t.Context(), first, filepath.Join(pc.manifest.OutputDir, "citylib_reservation.json")); err != nil {
		t.Fatal(err)
	}
	if err := releaseCityPlan(first); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after reserving r2 but before its review/receipt was saved.
	if _, err := reserveCityLibrary(t.Context(), pc, defaultScenicPolicy(), cityProjectID(pc.manifest.TaskID, 2), []string{"static"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := buildScenicSemanticPlan(opts, pc); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(pc.manifest.OutputDir, "citylib_reservation.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt CityLibraryReservation
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.OpeningMotionReview.Passed || len(receipt.OpeningMotionReview.Evidence) != 2 || !receipt.OpeningMotionReview.Evidence[0].Static {
		t.Fatalf("lost previous revision evidence: %+v", receipt.OpeningMotionReview)
	}
}

func TestCityBuildV2PreviewReleasesWithoutIndexOrModelSelection(t *testing.T) {
	f := cityfixture.New(t)
	moving, ffmpeg := cityfixture.Video(t, "testsrc2=s=128x72:r=12")
	f.Add(t, "moving", "City_Aerial")
	f.Exec(t, "UPDATE assets SET path=?", moving)
	manifest, planPath := v2Fixture(t)
	raw, _ := os.ReadFile(manifest)
	var mf map[string]any
	_ = json.Unmarshal(raw, &mf)
	mf["action"] = "montage.plan"
	mf["project"] = map[string]any{"id": "project", "account_id": "account"}
	settings := mf["non_secret_settings"].(map[string]any)
	settings["media_root"] = f.Root
	settings["media_index_path"] = ""
	settings["ffmpeg_path"] = ffmpeg
	if err := writeScenicJSON(manifest, mf); err != nil {
		t.Fatal(err)
	}
	if err := BuildV2(Options{ManifestPath: manifest, PlanPath: planPath, MixPreset: mixPresetScenicSemantic, Duration: func(string) (float64, error) { return 4, nil }}); err != nil {
		t.Fatal(err)
	}
	if f.Count(t, "SELECT COUNT(*) FROM reservations") != 0 || f.Count(t, "SELECT COUNT(*) FROM uses") != 0 || f.Count(t, "SELECT COUNT(*) FROM projects WHERE status='cancelled'") != 1 {
		t.Fatal("preview occupies uses")
	}
	var plan ProductionPlanV2
	raw, _ = os.ReadFile(planPath)
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Inputs.MediaIndex != f.Path || len(plan.Timeline) != 1 || plan.Timeline[0].MediaKind != "broll" {
		t.Fatalf("plan=%+v", plan)
	}
}
