package montage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
	cityfixture "video-production-console/internal/testutil/citylib"
)

func TestCoordinatorCityUsageFollowsRegistrationResult(t *testing.T) {
	for _, kind := range []string{"success", "failed", "commit-warning", "retry-cancelled"} {
		t.Run(kind, func(t *testing.T) {
			registered := newRegistrationFixture(t, false, true)
			taskID := registered.request.TaskID
			db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			now := time.Now().UTC()
			for _, stmt := range []string{
				`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('11111111-1111-4111-8111-111111111111','Account','#fff','active',?,?)`,
				`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES('22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111','p','mixing',?,?)`,
			} {
				if _, err := db.Exec(stmt, now, now); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec(`INSERT INTO codex_tasks(id,project_id,account_id,type,skill_name,action,status,completion_phase,transport,prompt_snapshot,created_at) VALUES(?,'22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111','montage','jianying-montage-draft','montage.execute','running','plaintext_ready','legacy_exec','prompt',?)`, taskID, now); err != nil {
				t.Fatal(err)
			}
			digest, err := hashPlaintextWorkspace(t.Context(), registered.workspace)
			if err != nil {
				t.Fatal(err)
			}
			repo := store.NewMontageRepository(db)
			attempt, err := repo.CompleteAndBegin(t.Context(), store.CompleteRegistration{TaskID: taskID, ManifestPath: registered.manifestPath, WorkspacePath: registered.workspace, Result: store.TaskResultWrite{Status: domain.TaskCompleted, Summary: "plaintext", AssistantContent: "plaintext", EventKind: "plaintext_ready"}, Artifacts: []store.TaskArtifact{{Kind: "plaintext_workspace", Path: registered.workspace, Filename: "workspace", MIMEType: "inode/directory", SHA256: digest}}})
			if err != nil {
				t.Fatal(err)
			}
			f := cityfixture.New(t)
			mediaPath := f.Add(t, "clip", "City_Aerial")
			plan := map[string]any{"version": 2, "project_id": "console-" + taskID, "catalog_path": f.Path, "account": "Account", "source": "video-production-console", "clips": []map[string]any{{"asset_id": "clip", "path": mediaPath, "category": "City_Aerial", "source_in": 2, "source_out": 6}}, "opening_motion_review": map[string]any{"passed": true, "checked_until": 4, "evidence": []any{"fixture"}}}
			status := "reserved"
			if kind == "retry-cancelled" {
				status = "cancelled"
			}
			// The receipt uses the same typed shape as the planner's real evidence.
			plan["opening_motion_review"] = map[string]any{"passed": true, "checked_until": 4, "evidence": []map[string]any{{"asset_id": "clip", "project_id": "console-" + taskID, "sample_times": []float64{2.4, 4, 5.6}, "mean_pixel_differences": []float64{.1, .1}}}}
			raw, _ := json.Marshal(plan)
			f.Exec(t, "INSERT INTO projects VALUES(?,?,?,1,?)", "console-"+taskID, "Account", status, string(raw))
			if kind != "retry-cancelled" {
				f.Exec(t, "INSERT INTO reservations VALUES(?,?)", "console-"+taskID, "clip")
			}
			receipt := filepath.Join(filepath.Dir(registered.manifestPath), "citylib_reservation.json")
			writeJSONFixture(t, receipt, plan)
			if kind == "failed" {
				if err := os.WriteFile(filepath.Join(registered.workspace, "draft_content.json"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "commit-warning" {
				f.Exec(t, `CREATE TRIGGER reject_uses BEFORE INSERT ON uses BEGIN SELECT RAISE(ABORT,'fixture commit error'); END`)
			}
			coordinator := &Coordinator{ctx: context.Background(), repo: repo, tasks: store.NewTaskRepository(db), runtime: TrustedRuntime{JianyingRoot: registered.root}}
			coordinator.process(registrationJob{Attempt: attempt})
			got, err := repo.Latest(t.Context(), taskID)
			if err != nil {
				t.Fatal(err)
			}
			if got.ErrorMessage != nil {
				t.Logf("registration error: %s", *got.ErrorMessage)
			}
			switch kind {
			case "failed":
				if got.State != domain.RegistrationFailed || f.Count(t, "SELECT COUNT(*) FROM uses") != 0 || f.Count(t, "SELECT COUNT(*) FROM reservations") != 0 {
					t.Fatalf("failed state=%s", got.State)
				}
			case "commit-warning":
				if got.State != domain.RegistrationSucceeded || f.Count(t, "SELECT COUNT(*) FROM uses") != 0 || f.Count(t, "SELECT COUNT(*) FROM reservations") != 1 {
					t.Fatalf("ledger error changed registration=%s", got.State)
				}
				var warnings int
				if err := db.QueryRow("SELECT COUNT(*) FROM task_events WHERE task_id=? AND level='warning'", taskID).Scan(&warnings); err != nil {
					t.Fatal(err)
				}
				if warnings == 0 {
					t.Fatal("missing usage warning")
				}
			default:
				if got.State != domain.RegistrationSucceeded || f.Count(t, "SELECT COUNT(*) FROM uses") != 1 || f.Count(t, "SELECT COUNT(*) FROM reservations") != 0 {
					t.Fatalf("success state=%s", got.State)
				}
			}
		})
	}
}
