package montagescript

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"video-production-console/internal/agentruntime/montageplan"
	cityfixture "video-production-console/internal/testutil/citylib"
)

// Opt in to the installed executor's contract without loading a production
// profile or registering a draft. All media, databases and output are temporary.
func TestCityPythonV2CatalogContract(t *testing.T) {
	skillRoot := os.Getenv("MONTAGE_TEST_SKILL_ROOT")
	if skillRoot == "" {
		t.Skip("set MONTAGE_TEST_SKILL_ROOT to a read-only Jianying montage skill")
	}
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not on PATH")
	}
	moving, ffmpeg := cityfixture.Video(t, "testsrc2=s=128x72:r=12")
	opts, f, output := cityRunFixture(t, moving, ffmpeg)
	video, err := os.ReadFile(moving)
	if err != nil {
		t.Fatal(err)
	}
	localVideo := filepath.Join(f.Root, "moving.mp4")
	if err := os.WriteFile(localVideo, video, 0600); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, "UPDATE assets SET path=?", localVideo)
	root := filepath.Dir(opts.ManifestPath)
	narration := filepath.Join(root, "narration.wav")
	if out, err := exec.CommandContext(t.Context(), ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono", "-t", "4", narration).CombinedOutput(); err != nil {
		t.Fatalf("create narration: %v: %s", err, out)
	}
	inputs := []map[string]any{{"role": "narration", "path": narration, "required": true}}
	for role, name := range map[string]string{"continuous_script": "script.txt", "subtitle_srt": "subtitle.srt", "account_background": "board.png"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, map[string]any{"role": role, "path": path, "required": true})
	}
	raw, _ := os.ReadFile(opts.ManifestPath)
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["task_id"], manifest["job_id"] = "11111111-1111-4111-8111-111111111111", "11111111-1111-4111-8111-111111111111"
	manifest["approval_mode"], manifest["inputs"] = "auto_after_valid_plan", inputs
	writeCityTestJSON(t, opts.ManifestPath, manifest)
	profilePath := filepath.Join(root, "profile.json")
	raw, _ = os.ReadFile(profilePath)
	var profile map[string]any
	if err := json.Unmarshal(raw, &profile); err != nil {
		t.Fatal(err)
	}
	profile["jianying_root"] = t.TempDir()
	cache := map[string]string{}
	for _, key := range []string{"bgm_yawaraka_hikari", "sfx_opening_hit", "sfx_water_drop", "sfx_whoosh", "sfx_conclusion_hit", "transition_cross_dissolve"} {
		cache[key] = narration
	}
	profile["cache_paths"] = cache
	writeCityTestJSON(t, profilePath, profile)
	planPath := filepath.Join(output, "production_plan.json")
	if err := montageplan.BuildV2(montageplan.Options{Context: t.Context(), ManifestPath: opts.ManifestPath, PlanPath: planPath, MixPreset: "scenic_semantic", Duration: opts.Duration}); err != nil {
		t.Fatal(err)
	}
	derived, err := prepareCityScriptManifest(opts.ManifestPath, planPath)
	if err != nil {
		t.Fatal(err)
	}
	// The incomplete draft intentionally produces layout findings. The contract
	// under test is real v2 plan validation and dispatch, including binary SQLite
	// as the index path; the legacy JSON loader must never be reached.
	const check = `import json, sys
from pathlib import Path
sys.path.insert(0, str(Path(sys.argv[1]) / 'scripts'))
import run_montage_job as job
ctx = job.load_context(sys.argv[2])
assert ctx.machine_profile.media_index_path.suffix == '.sqlite3'
assert not job.validate_inputs(ctx), job.validate_inputs(ctx)
plan, validation = job._validate_plan_for_version(ctx, Path(sys.argv[3]), require_approval=True)
assert not [f for f in validation['findings'] if f['level'] == 'error'], validation
ctx.workspace.mkdir(parents=True)
(ctx.workspace / 'draft_content.json').write_text(json.dumps({'duration':4000000,'tracks':[],'materials':{}}), encoding='utf-8')
report = job.DeterministicDraftBackend().validate(ctx, plan, ctx.workspace)
assert report.get('plan_version') == '2.0', report
assert report['findings'], 'incomplete draft fixture should produce layout findings'
assert not [f for f in report['findings'] if 'media index' in f['message'].lower()], report
print('v2 catalog/profile/plan/draft-dispatch contract passed')
`
	cmd := exec.CommandContext(t.Context(), python, "-B", "-c", check, skillRoot, derived, planPath)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Python city contract: %v: %s", err, out)
	} else {
		t.Log(string(out))
	}
}
