package montagescript

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cityfixture "video-production-console/internal/testutil/citylib"
)

func writeCityTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func cityRunFixture(t *testing.T, moving, ffmpeg string) (Options, *cityfixture.Fixture, string) {
	t.Helper()
	f := cityfixture.New(t)
	f.Add(t, "moving", "City_Aerial")
	f.Exec(t, "UPDATE assets SET path=?", moving)
	root := t.TempDir()
	script := filepath.Join(root, "skill", "scripts", "run_montage_job.py")
	if err := os.MkdirAll(filepath.Dir(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("# fake"), 0600); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "profile.json")
	writeCityTestJSON(t, profile, map[string]any{"schema_version": "1.0", "media_root": filepath.Join(root, "old"), "media_index_path": filepath.Join(root, "old", "missing.json"), "python_binary": "python", "cache_paths": map[string]string{"test": "keep"}, "extra": "preserved"})
	manifest := filepath.Join(root, "manifest.json")
	output := filepath.Join(root, "output")
	writeCityTestJSON(t, manifest, map[string]any{
		"schema_version": "2.0", "task_id": "city-run", "job_id": "city-run", "skill": "jianying-montage-draft", "action": "montage.execute", "output_dir": output,
		"project":             map[string]string{"id": "project", "account_id": "account"},
		"inputs":              []map[string]string{{"role": "narration", "path": filepath.Join(root, "narration.wav")}, {"role": "account_background", "path": filepath.Join(root, "board.png")}},
		"non_secret_settings": map[string]any{"media_root": f.Root, "mix_preset": "scenic_semantic", "machine_profile_path": profile, "ffmpeg_path": ffmpeg},
	})
	return Options{Context: t.Context(), ManifestPath: manifest, SkillRoot: filepath.Join(root, "skill"), OutputLastMessage: filepath.Join(root, "last.json"), Duration: func(string) (float64, error) { return 4, nil }}, f, output
}

func TestRunCityUsesTaskLocalProfileWithCatalogIndex(t *testing.T) {
	moving, ffmpeg := cityfixture.Video(t, "testsrc2=s=128x72:r=12")
	opts, f, output := cityRunFixture(t, moving, ffmpeg)
	originalManifest, _ := os.ReadFile(opts.ManifestPath)
	originalProfilePath := filepath.Join(filepath.Dir(opts.ManifestPath), "profile.json")
	originalProfile, _ := os.ReadFile(originalProfilePath)
	opts.CommandRunner = func(_ string, args ...string) ([]byte, error) {
		var manifest map[string]any
		raw, err := os.ReadFile(args[3])
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		settings := manifest["non_secret_settings"].(map[string]any)
		path := settings["machine_profile_path"].(string)
		if filepath.Dir(path) != output || path == originalProfilePath {
			t.Fatalf("not a task-local profile: %s", path)
		}
		var profile map[string]any
		raw, err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &profile); err != nil {
			t.Fatal(err)
		}
		if profile["media_root"] != f.Root || profile["media_index_path"] != f.Path || profile["extra"] != "preserved" {
			t.Fatalf("profile=%+v", profile)
		}
		return nil, errors.New("stop after inspecting input adapter")
	}
	if err := Run(opts); err != nil {
		t.Fatal(err)
	}
	afterManifest, _ := os.ReadFile(opts.ManifestPath)
	afterProfile, _ := os.ReadFile(originalProfilePath)
	if string(originalManifest) != string(afterManifest) || string(originalProfile) != string(afterProfile) {
		t.Fatal("original task/profile mutated")
	}
}

func TestRunCityFailuresAndPreviewReleaseReservations(t *testing.T) {
	moving, ffmpeg := cityfixture.Video(t, "testsrc2=s=128x72:r=12")
	for _, kind := range []string{"validate-error", "validate-status", "execute-error", "execute-error-result", "execute-status", "stdout-only", "preview"} {
		t.Run(kind, func(t *testing.T) {
			opts, f, output := cityRunFixture(t, moving, ffmpeg)
			if kind == "preview" {
				raw, _ := os.ReadFile(opts.ManifestPath)
				var manifest map[string]any
				_ = json.Unmarshal(raw, &manifest)
				manifest["action"] = "montage.plan"
				writeCityTestJSON(t, opts.ManifestPath, manifest)
			}
			opts.CommandRunner = func(_ string, args ...string) ([]byte, error) {
				phase := args[1]
				if phase == "validate-plan" {
					if kind == "preview" && f.Count(t, "SELECT COUNT(*) FROM reservations") != 0 {
						t.Fatal("preview held reservations during validation")
					}
					if kind == "validate-error" {
						return nil, errors.New("validation failed")
					}
					if kind == "validate-status" {
						return []byte(`{"schema_version":"2.0","status":"failed"}`), nil
					}
				}
				if phase == "execute" {
					if kind == "preview" || kind == "validate-status" {
						t.Fatal("must stop before execute")
					}
					if kind == "execute-error" {
						return nil, errors.New("execution failed")
					}
					status := "completed"
					if kind == "execute-status" {
						status = "failed"
					}
					result := map[string]any{"schema_version": "2.0", "task_id": "city-run", "action": "montage.execute", "status": status, "warnings": []any{}}
					raw, _ := json.Marshal(result)
					if kind != "stdout-only" {
						writeCityTestJSON(t, filepath.Join(output, "result.json"), result)
					}
					if kind == "execute-error-result" {
						return raw, errors.New("execute returned an error after writing result")
					}
					return raw, nil
				}
				return []byte(`{"schema_version":"2.0","status":"completed"}`), nil
			}
			if err := Run(opts); err != nil {
				t.Fatal(err)
			}
			if f.Count(t, "SELECT COUNT(*) FROM reservations") != 0 || f.Count(t, "SELECT COUNT(*) FROM uses") != 0 || f.Count(t, "SELECT COUNT(*) FROM projects WHERE status='cancelled'") != 1 {
				t.Fatal("failed/preview task leaked reservations or committed uses")
			}
			last, _ := os.ReadFile(opts.OutputLastMessage)
			if (kind == "stdout-only" || kind == "execute-error-result") && !strings.Contains(string(last), `"status":"failed"`) {
				t.Fatalf("unconfirmed execute must fail: %s", last)
			}
		})
	}
}

func TestRunCityPlaintextSuccessWaitsForRegistration(t *testing.T) {
	moving, ffmpeg := cityfixture.Video(t, "testsrc2=s=128x72:r=12")
	opts, f, output := cityRunFixture(t, moving, ffmpeg)
	opts.CommandRunner = func(_ string, args ...string) ([]byte, error) {
		raw := []byte(`{"schema_version":"2.0","task_id":"city-run","action":"montage.execute","status":"completed","warnings":[]}`)
		if args[1] == "execute" {
			if err := os.WriteFile(filepath.Join(output, "result.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		return raw, nil
	}
	if err := Run(opts); err != nil {
		t.Fatal(err)
	}
	if f.Count(t, "SELECT COUNT(*) FROM uses") != 0 || f.Count(t, "SELECT COUNT(*) FROM reservations") != 1 {
		t.Fatal("plaintext success must retain reservation until registered")
	}
}
