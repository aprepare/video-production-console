package montageplan

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	cityfixture "video-production-console/internal/testutil/citylib"
)

func cityTestContext(t *testing.T, f *cityfixture.Fixture, seconds float64) *planContext {
	t.Helper()
	var mf manifestFile
	if err := json.Unmarshal([]byte(`{"task_id":"city-test","job_id":"city-test","action":"montage.execute","project":{"id":"project","account_id":"account"}}`), &mf); err != nil {
		t.Fatal(err)
	}
	mf.OutputDir = t.TempDir()
	return &planContext{manifest: mf, mediaRoot: f.Root, duration: seconds}
}

func reserveCityTest(t *testing.T, pc *planContext, id string) *CityLibraryReservation {
	t.Helper()
	plan, err := reserveCityLibrary(t.Context(), pc, defaultScenicPolicy(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func cityReceipt(t *testing.T, plan *CityLibraryReservation) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "citylib_reservation.json")
	if err := writeScenicJSON(path, plan); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCitySelectionTrimsAndHonorsReviewedWindow(t *testing.T) {
	f := cityfixture.New(t)
	f.Add(t, "full", "Housing_Residential")
	f.Add(t, "reviewed", "City_Aerial")
	f.Exec(t, "UPDATE assets SET duration=11,usable_in=4,usable_out=8 WHERE asset_id='reviewed'")
	plan := reserveCityTest(t, cityTestContext(t, f, 9), "console-city-test")
	if len(plan.Clips) != 2 {
		t.Fatalf("clips=%+v", plan.Clips)
	}
	for _, c := range plan.Clips {
		lo, hi := 2.0, 7.0
		if c.AssetID == "reviewed" {
			lo, hi = 4, 8
		}
		if c.SourceIn != lo || c.SourceOut != hi || math.Abs(c.Duration-(c.TimelineOut-c.TimelineIn)) > 1e-9 {
			t.Fatalf("window=%+v", c)
		}
	}
	if plan.Clips[1].TimelineOut != 9 {
		t.Fatal("narration not covered")
	}
}

func TestCitySelectionOpeningMinuteFamiliesAndFilters(t *testing.T) {
	f := cityfixture.New(t)
	for i := 0; i < 12; i++ {
		category := "Housing_Residential"
		if i%2 == 1 {
			category = "City_Aerial"
		}
		id := fmt.Sprintf("opening-%02d", i)
		f.Add(t, id, category)
		f.Add(t, id+"-family", category)
		f.Exec(t, "UPDATE assets SET family=? WHERE asset_id=?", id, id+"-family")
	}
	for _, c := range []string{"Streets", "Architecture_Exterior"} {
		f.Add(t, c, c)
	}
	for _, id := range []string{"portrait", "short", "empty-window", "missing", "pending", "unusable", "used-six", "busy-six", "foreign-category"} {
		f.Add(t, id, "City_Aerial")
	}
	f.Exec(t, "UPDATE assets SET height=2000 WHERE asset_id='portrait'")
	f.Exec(t, "UPDATE assets SET duration=5 WHERE asset_id='short'")
	f.Exec(t, "UPDATE assets SET usable_in=8,usable_out=8 WHERE asset_id='empty-window'")
	f.Exec(t, "UPDATE assets SET status='pending' WHERE asset_id='pending'")
	f.Exec(t, "UPDATE assets SET status='unusable' WHERE asset_id='unusable'")
	f.Exec(t, "UPDATE assets SET category='Rural_Life' WHERE asset_id='foreign-category'")
	if err := os.Remove(filepath.Join(f.Root, "missing.mp4")); err != nil {
		t.Fatal(err)
	}
	f.History(t, "used-six", "other", 6, 0)
	f.History(t, "busy-six", "other", 5, 1)
	plan := reserveCityTest(t, cityTestContext(t, f, 68), "console-city-test")
	seen := map[string]bool{}
	for _, c := range plan.Clips {
		var family string
		if err := f.DB.QueryRow("SELECT family FROM assets WHERE asset_id=?", c.AssetID).Scan(&family); err != nil {
			t.Fatal(err)
		}
		if seen[family] {
			t.Fatalf("duplicate family: %s", family)
		}
		seen[family] = true
		if c.TimelineIn < 60 && !cityOpeningCategory(c.Category) {
			t.Fatalf("opening: %+v", c)
		}
		if c.TimelineIn >= 60 && (c.Category != "Streets" && c.Category != "Architecture_Exterior") {
			t.Fatalf("body: %+v", c)
		}
		if !strings.HasPrefix(c.AssetID, "opening-") && c.Category != "Streets" && c.Category != "Architecture_Exterior" {
			t.Fatalf("ineligible: %+v", c)
		}
	}
	if plan.Clips[len(plan.Clips)-1].Duration != 3 {
		t.Fatal("tail must be shortened")
	}
}

func TestCitySelectionUsageTiersAndPreferenceOrder(t *testing.T) {
	f := cityfixture.New(t)
	for _, id := range []string{"low", "fresh", "account-used", "same-category", "alternate"} {
		f.Add(t, id, "Housing_Residential")
	}
	f.History(t, "fresh", "other", 1, 0)
	f.History(t, "account-used", "account", 1, 0)
	f.History(t, "same-category", "other", 1, 0)
	f.History(t, "alternate", "other", 1, 0)
	f.Exec(t, "UPDATE assets SET category='City_Aerial' WHERE asset_id IN ('account-used','fresh','alternate')")
	f.Exec(t, "UPDATE assets SET origin='generated' WHERE asset_id='alternate'")
	plan := reserveCityTest(t, cityTestContext(t, f, 10), "console-city-test")
	if plan.Clips[0].AssetID != "low" || plan.Clips[1].AssetID != "alternate" {
		t.Fatalf("preferences=%+v", plan.Clips)
	}
}

func TestCityReservationReuseCommitCancelAndSetValidation(t *testing.T) {
	f := cityfixture.New(t)
	f.Add(t, "five", "City_Aerial")
	f.History(t, "five", "other", 5, 0)
	pc := cityTestContext(t, f, 4)
	first := reserveCityTest(t, pc, "console-city-test")
	second := reserveCityTest(t, pc, "console-city-test")
	if !reflect.DeepEqual(first, second) || f.Count(t, "SELECT COUNT(*) FROM reservations") != 1 {
		t.Fatal("retry changed or duplicated reservation")
	}
	if first.Clips[0].PriorUses != 5 {
		t.Fatal("fifth use must still be selectable")
	}
	receipt := cityReceipt(t, first)
	if err := FinalizeCityLibraryReservation(t.Context(), receipt, false); err != nil {
		t.Fatal(err)
	}
	if f.Count(t, "SELECT COUNT(*) FROM reservations") != 0 || f.Count(t, "SELECT COUNT(*) FROM projects WHERE status='cancelled'") != 1 {
		t.Fatal("not released")
	}
	replanned := reserveCityTest(t, pc, "console-city-test")
	if !reflect.DeepEqual(first.Clips, replanned.Clips) || first.RandomSeed != replanned.RandomSeed {
		t.Fatal("cancelled retry not deterministic")
	}
	f.Exec(t, "DELETE FROM reservations WHERE project_id='console-city-test'")
	if err := FinalizeCityLibraryReservation(t.Context(), receipt, true); err == nil {
		t.Fatal("set mismatch accepted")
	}
	if f.Count(t, "SELECT COUNT(*) FROM uses") != 5 {
		t.Fatal("mismatch partially committed")
	}
	f.Exec(t, "INSERT INTO reservations VALUES('console-city-test','five')")
	if err := FinalizeCityLibraryReservation(t.Context(), receipt, true); err != nil {
		t.Fatal(err)
	}
	if err := FinalizeCityLibraryReservation(t.Context(), receipt, true); err != nil {
		t.Fatal("commit must be idempotent", err)
	}
	if f.Count(t, "SELECT COUNT(*) FROM uses") != 6 || f.Count(t, "SELECT COUNT(*) FROM reservations") != 0 || f.Count(t, "SELECT COUNT(*) FROM projects WHERE project_id='console-city-test' AND status='completed'") != 1 {
		t.Fatal("incorrect commit")
	}
	if _, err := reserveCityLibrary(t.Context(), pc, defaultScenicPolicy(), "console-city-test", nil); err == nil || !strings.Contains(err.Error(), "该任务的素材已提交，请新建任务重出草稿") {
		t.Fatalf("completed: %v", err)
	}
}

func TestCityShortageRollsBackWithDiagnostic(t *testing.T) {
	f := cityfixture.New(t)
	for _, id := range []string{"available", "pending", "exhausted", "busy"} {
		f.Add(t, id, "City_Aerial")
	}
	f.Exec(t, "UPDATE assets SET status='pending' WHERE asset_id='pending'")
	f.History(t, "exhausted", "other", 6, 0)
	f.History(t, "busy", "other", 5, 1)
	_, err := reserveCityLibrary(t.Context(), cityTestContext(t, f, 10), defaultScenicPolicy(), "console-city-test", nil)
	for _, part := range []string{"缺少 5.00 秒", "开头一分钟", "待审核 1", "已用满 1", "被预占 1", "等正在制作的项目完成"} {
		if err == nil || !strings.Contains(err.Error(), part) {
			t.Errorf("missing %q: %v", part, err)
		}
	}
	if f.Count(t, "SELECT COUNT(*) FROM projects WHERE project_id='console-city-test'") != 0 || f.Count(t, "SELECT COUNT(*) FROM reservations WHERE project_id='console-city-test'") != 0 {
		t.Fatal("shortage did not roll back")
	}
}

func TestCityConcurrentReservationsCannotExceedSix(t *testing.T) {
	f := cityfixture.New(t)
	f.Add(t, "five", "City_Aerial")
	f.History(t, "five", "other", 5, 0)
	pc := cityTestContext(t, f, 4)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := reserveCityLibrary(context.Background(), pc, defaultScenicPolicy(), fmt.Sprintf("console-concurrent-%d", i), nil)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	succeeded := 0
	for err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 || f.Count(t, "SELECT COUNT(*) FROM reservations") != 1 {
		t.Fatalf("succeeded=%d", succeeded)
	}
}

func TestCityPreflightWithoutLegacyIndex(t *testing.T) {
	f := cityfixture.New(t)
	if err := ValidateMediaLibraryForPreset(mixPresetScenicSemantic, "", f.Root, "missing-profile"); err == nil || !strings.Contains(err.Error(), "eligible") {
		t.Fatalf("empty catalog: %v", err)
	}
	f.Add(t, "ok", "City_Aerial")
	if err := ValidateMediaLibraryForPreset(mixPresetScenicSemantic, "", f.Root, "missing-profile"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMediaLibraryForPreset(mixPresetScenicSemantic, filepath.Join(t.TempDir(), "missing.json"), f.Root, ""); err != nil {
		t.Fatal(err)
	}
}

func TestCityRegistrationRetryCannotExceedUsageLimit(t *testing.T) {
	f := cityfixture.New(t)
	f.Add(t, "clip", "City_Aerial")
	plan := reserveCityTest(t, cityTestContext(t, f, 4), "console-city-test")
	receipt := cityReceipt(t, plan)
	if err := FinalizeCityLibraryReservation(t.Context(), receipt, false); err != nil {
		t.Fatal(err)
	}
	f.History(t, "clip", "other", 6, 0)
	if err := EnsureCityLibraryReservation(t.Context(), receipt); err == nil || !strings.Contains(err.Error(), "已不可预占") {
		t.Fatalf("retry must respect newly consumed uses: %v", err)
	}
	if f.Count(t, "SELECT COUNT(*) FROM reservations") != 0 || f.Count(t, "SELECT COUNT(*) FROM projects WHERE project_id=? AND status='cancelled'", plan.ProjectID) != 1 {
		t.Fatal("failed retry partially restored reservation")
	}
}

func TestCityTransactionCancellationRollsBack(t *testing.T) {
	f := cityfixture.New(t)
	db, err := openCityCatalog(t.Context(), f.Path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err = withCityTransaction(ctx, db, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, "INSERT INTO projects(project_id,status) VALUES('interrupted','reserved')"); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	if err == nil || f.Count(t, "SELECT COUNT(*) FROM projects") != 0 {
		t.Fatalf("cancelled transaction committed: %v", err)
	}
}

func TestCityAccountNameAllowedCategoriesAndBodyShortage(t *testing.T) {
	f := cityfixture.New(t)
	pc := cityTestContext(t, f, 65)
	pc.manifest.NonSecretSettings.DataRoot = t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(pc.manifest.NonSecretSettings.DataRoot, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE accounts(id TEXT,name TEXT); INSERT INTO accounts VALUES('account','测试账号')"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		f.Add(t, fmt.Sprintf("opening-%d", i), "City_Aerial")
	}
	f.Add(t, "street", "Streets")
	p := defaultScenicPolicy()
	p.AllowedCategories = []string{"City_Aerial"}
	if _, err := reserveCityLibrary(t.Context(), pc, p, "console-city-test", nil); err == nil || !strings.Contains(err.Error(), "后段缺少 5.00 秒") || !strings.Contains(err.Error(), "补充素材") {
		t.Fatalf("category/body shortage: %v", err)
	}
	if f.Count(t, "SELECT COUNT(*) FROM projects") != 0 {
		t.Fatal("body shortage did not roll back")
	}
	p.AllowedCategories = append(p.AllowedCategories, "Streets")
	plan, err := reserveCityLibrary(t.Context(), pc, p, "console-city-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Account != "测试账号" || plan.Clips[len(plan.Clips)-1].AssetID != "street" {
		t.Fatalf("account/category selection: %+v", plan)
	}
}
