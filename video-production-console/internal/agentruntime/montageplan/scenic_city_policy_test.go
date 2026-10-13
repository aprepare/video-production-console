package montageplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScenicPolicyVersionsAndSpecificErrors(t *testing.T) {
	for _, version := range []int{1, 2} {
		p := defaultScenicPolicy()
		raw, _ := json.Marshal(map[string]any{"version": version, "china_only": version == 1, "content_scope": "architecture_city_only"})
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		if err := p.validate(); err != nil {
			t.Errorf("v%d: %v", version, err)
		}
		for _, tc := range []struct {
			field string
			value any
		}{
			{"version", 3}, {"orientation", "portrait"}, {"playback_speed", 1.2},
			{"opening_min_s", 2}, {"opening_max_s", 10}, {"opening_max_s", 5},
			{"body_min_s", 2}, {"body_max_s", 13}, {"body_max_s", 5},
			{"dissolve_ratio", -.1}, {"dissolve_ratio", .6}, {"max_source_uses", 0},
			{"max_source_uses", 3}, {"reuse_gap", 2}, {"history_window", -1}, {"history_window", 51},
		} {
			bad := p
			raw, _ := json.Marshal(map[string]any{tc.field: tc.value})
			_ = json.Unmarshal(raw, &bad)
			if err := bad.validate(); err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Errorf("v%d %s=%v: %v", version, tc.field, tc.value, err)
			}
		}
		if version == 1 {
			p.ChinaOnly = false
			if err := p.validate(); err == nil || !strings.Contains(err.Error(), "china_only") {
				t.Errorf("%v", err)
			}
		} else {
			_ = json.Unmarshal([]byte(`{"content_scope":"other"}`), &p)
			if err := p.validate(); err == nil || !strings.Contains(err.Error(), `content_scope "other"`) {
				t.Errorf("%v", err)
			}
		}
	}
}

func TestScenicCityMigrationCheckedBeforeLegacyIndex(t *testing.T) {
	opts := scenicTestOptions(t)
	ctx, err := loadPlanContext(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx.mediaRoot = filepath.Join(t.TempDir(), "AI_Media_Library")
	ctx.mediaIndex = filepath.Join(ctx.mediaRoot, "00_INDEX", "missing-index.json")
	p := defaultScenicPolicy()
	_ = json.Unmarshal([]byte(`{"version":2,"china_only":false,"content_scope":"architecture_city_only"}`), &p)
	if err := writeScenicJSON(filepath.Join(ctx.mediaRoot, "00_INDEX", "scenic_policy.json"), p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ctx.mediaRoot, "00_INDEX", "素材库已迁移.txt"), []byte("\ufeff当前主素材库：C:/隔离测试/新库\r\n旧库仅作存档"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err = buildScenicSemanticPlan(opts, ctx)
	if err == nil || !strings.Contains(err.Error(), "素材库已迁移到 C:/隔离测试/新库") || !strings.Contains(err.Error(), "设置 → 混剪 → 媒体素材目录") {
		t.Fatalf("migration: %v", err)
	}
	if err := ValidateMediaLibraryForPreset(mixPresetScenicSemantic, ctx.mediaIndex, ctx.mediaRoot, ""); err == nil || !strings.Contains(err.Error(), "素材库已迁移") {
		t.Fatalf("preflight: %v", err)
	}
}

func TestScenicChinaPolicyKeepsIndexAndSnapshotPrecedence(t *testing.T) {
	root, indexDir, output := t.TempDir(), t.TempDir(), t.TempDir()
	p := defaultScenicPolicy()
	p.OpeningMin, p.OpeningMax = 4, 5
	if err := writeScenicJSON(filepath.Join(indexDir, "scenic_policy.json"), p); err != nil {
		t.Fatal(err)
	}
	// An unrelated root policy must not replace the China policy beside its index.
	city := defaultScenicPolicy()
	city.Version, city.ChinaOnly, city.ContentScope = 2, false, "architecture_city_only"
	if err := writeScenicJSON(filepath.Join(root, "00_INDEX", "scenic_policy.json"), city); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(output, "production_plan.json")
	got, err := cityLibraryPolicy(filepath.Join(indexDir, "media_index.json"), root, planPath)
	if err != nil || got.Version != 1 || got.OpeningMin != 4 || got.OpeningMax != 5 {
		t.Fatalf("changed China policy precedence: %+v, %v", got, err)
	}
	p.OpeningMin = 3
	if err := writeScenicJSON(filepath.Join(output, "scenic_policy_snapshot.json"), scenicSnapshot{Policy: p, History: map[string]int{"existing": 2}}); err != nil {
		t.Fatal(err)
	}
	if err := writeScenicJSON(filepath.Join(indexDir, "scenic_policy.json"), city); err != nil {
		t.Fatal(err)
	}
	got, err = cityLibraryPolicy(filepath.Join(indexDir, "media_index.json"), root, planPath)
	if err != nil || got.Version != 1 || got.OpeningMin != 3 {
		t.Fatalf("changed frozen China policy: %+v, %v", got, err)
	}
}
