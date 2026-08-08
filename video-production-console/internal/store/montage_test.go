package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

func displayReconcileFixture(t *testing.T, displayName string) (*MontageRepository, *AssetRepository, domain.AssetVersion, string, string) {
	t.Helper()
	repo, assets, accountID, projectID, taskID := montageFixture(t)
	root := t.TempDir()
	target := filepath.Join(root, taskID)
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	taskRoot := t.TempDir()
	output := filepath.Join(taskRoot, "output")
	workspace := filepath.Join(output, "workspace", taskID)
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(taskRoot, "task_manifest.json")
	data, err := json.Marshal(map[string]any{
		"schema_version": "2.0",
		"skill":          "jianying-montage-draft",
		"action":         domain.ActionMontageExecute,
		"task_id":        taskID,
		"job_id":         taskID,
		"output_dir":     output,
		"non_secret_settings": map[string]any{
			"draft_display_name": displayName,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`UPDATE codex_tasks SET status=?,completion_phase=?,manifest_path=?,finished_at=? WHERE id=?`, domain.TaskCompleted, domain.CompletionRegistered, manifest, time.Now().UTC(), taskID); err != nil {
		t.Fatal(err)
	}
	version, err := assets.AddVersion(context.Background(), AddAssetVersion{
		ProjectID: &projectID, AccountID: accountID, Type: domain.AssetMixDraft,
		StorageKind: domain.StorageDirectory, Path: target, Filename: taskID,
		MIMEType: "inode/directory", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceTaskID: &taskID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`INSERT INTO montage_registration_attempts(id,task_id,manifest_path,workspace_path,state,attempt,registered_path,receipt_path,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, uuid.NewString(), taskID, manifest, workspace, domain.RegistrationSucceeded, 1, target, filepath.Join(output, "registration", "registration-result.json"), time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return repo, assets, version, root, manifest
}

func TestDraftsNeedingDisplayNameReturnsOnlyValidReadyCompletedUUIDDraft(t *testing.T) {
	displayName := "财富觉醒02_存款大搬家_b66205"
	repo, _, version, root, _ := displayReconcileFixture(t, displayName)
	candidates, err := repo.DraftsNeedingDisplayName(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates=%#v, want one", candidates)
	}
	got := candidates[0]
	if got.AssetVersionID != version.ID || got.AssetID != version.AssetID || got.TaskID != *version.SourceTaskID || got.DisplayName != displayName || got.RegisteredPath != version.Path {
		t.Fatalf("candidate=%#v", got)
	}
}

func TestDraftsNeedingDisplayNameSkipsActiveInvalidAndUnknownDrafts(t *testing.T) {
	displayName := "财富觉醒02_存款大搬家_b66205"
	t.Run("active", func(t *testing.T) {
		repo, _, version, root, manifest := displayReconcileFixture(t, displayName)
		workspace := filepath.Join(t.TempDir(), "workspace")
		if err := os.Mkdir(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.db.Exec(`INSERT INTO montage_registration_attempts(id,task_id,manifest_path,workspace_path,state,attempt,started_at) VALUES(?,?,?,?,?,?,?)`, uuid.NewString(), *version.SourceTaskID, manifest, workspace, domain.RegistrationQueued, 99, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		got, err := repo.DraftsNeedingDisplayName(context.Background(), root)
		if err != nil || len(got) != 0 {
			t.Fatalf("active candidates=%#v err=%v", got, err)
		}
	})
	t.Run("invalid manifest", func(t *testing.T) {
		repo, _, _, root, manifest := displayReconcileFixture(t, displayName)
		if err := os.WriteFile(manifest, []byte(`{"task_id":"wrong"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := repo.DraftsNeedingDisplayName(context.Background(), root)
		if err != nil || len(got) != 0 {
			t.Fatalf("invalid candidates=%#v err=%v", got, err)
		}
	})
	t.Run("unknown source", func(t *testing.T) {
		repo, assets, version, root, _ := displayReconcileFixture(t, displayName)
		if _, err := repo.db.Exec(`UPDATE asset_versions SET source_task_id=NULL WHERE id=?`, version.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := assets.Version(context.Background(), version.ID); err != nil {
			t.Fatal(err)
		}
		got, err := repo.DraftsNeedingDisplayName(context.Background(), root)
		if err != nil || len(got) != 0 {
			t.Fatalf("unknown candidates=%#v err=%v", got, err)
		}
	})
}

func TestDraftsNeedingDisplayNameRequiresMatchingSucceededRegistrationPath(t *testing.T) {
	displayName := "财富觉醒02_存款大搬家_b66205"
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *MontageRepository, domain.AssetVersion)
	}{
		{"missing attempt", func(t *testing.T, repo *MontageRepository, version domain.AssetVersion) {
			if _, err := repo.db.Exec(`DELETE FROM montage_registration_attempts WHERE task_id=?`, *version.SourceTaskID); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong registered path", func(t *testing.T, repo *MontageRepository, version domain.AssetVersion) {
			if _, err := repo.db.Exec(`UPDATE montage_registration_attempts SET registered_path=? WHERE task_id=?`, filepath.Join(filepath.Dir(version.Path), "wrong"), *version.SourceTaskID); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, _, version, root, _ := displayReconcileFixture(t, displayName)
			test.mutate(t, repo, version)
			got, err := repo.DraftsNeedingDisplayName(context.Background(), root)
			if err != nil || len(got) != 0 {
				t.Fatalf("candidates=%#v err=%v", got, err)
			}
		})
	}
}

func TestCompleteDraftDisplayReconcileUpdatesOnlyFilenameAndHash(t *testing.T) {
	displayName := "财富觉醒02_存款大搬家_b66205"
	repo, assets, before, root, _ := displayReconcileFixture(t, displayName)
	candidates, err := repo.DraftsNeedingDisplayName(context.Background(), root)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates=%#v err=%v", candidates, err)
	}
	afterHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := repo.CompleteDraftDisplayReconcile(context.Background(), domain.DraftDisplayReconcileSuccess{
		Candidate: candidates[0], SHA256: afterHash, DraftID: "verified-legacy-draft-id",
	}); err != nil {
		t.Fatal(err)
	}
	after, err := assets.Version(context.Background(), before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ID != before.ID || after.AssetID != before.AssetID || after.Path != before.Path || after.State != before.State || after.SourceTaskID == nil || *after.SourceTaskID != *before.SourceTaskID {
		t.Fatalf("identity changed: before=%#v after=%#v", before, after)
	}
	if after.Filename != displayName || after.SHA256 != afterHash {
		t.Fatalf("metadata not reconciled: %#v", after)
	}
	var draftID string
	if err := repo.db.QueryRow(`SELECT draft_id FROM montage_registration_attempts WHERE task_id=? AND registered_path=?`, *before.SourceTaskID, before.Path).Scan(&draftID); err != nil || draftID != "verified-legacy-draft-id" {
		t.Fatalf("persisted reconciled draft ID=%q err=%v", draftID, err)
	}
	if err := repo.CompleteDraftDisplayReconcile(context.Background(), domain.DraftDisplayReconcileSuccess{Candidate: candidates[0], SHA256: afterHash, DraftID: draftID}); err != nil {
		t.Fatalf("idempotent reconcile: %v", err)
	}
}

func TestCompleteDraftDisplayReconcileRejectsConflictingPersistedDraftID(t *testing.T) {
	repo, assets, before, root, _ := displayReconcileFixture(t, "可信旧草稿")
	candidates, err := repo.DraftsNeedingDisplayName(context.Background(), root)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	if _, err := repo.db.Exec(`UPDATE montage_registration_attempts SET draft_id='different-draft-id' WHERE task_id=?`, *before.SourceTaskID); err != nil {
		t.Fatal(err)
	}
	err = repo.CompleteDraftDisplayReconcile(context.Background(), domain.DraftDisplayReconcileSuccess{Candidate: candidates[0], SHA256: strings.Repeat("b", 64), DraftID: "verified-legacy-draft-id"})
	if !errors.Is(err, ErrRegistrationInputInvalid) {
		t.Fatalf("conflicting draft ID error=%v", err)
	}
	after, readErr := assets.Version(context.Background(), before.ID)
	if readErr != nil || after.Filename != before.Filename || after.SHA256 != before.SHA256 {
		t.Fatalf("asset changed on conflict: %+v err=%v", after, readErr)
	}
}

func TestCompleteAndBeginRollsBackPlaintextWhenAttemptCannotBeQueued(t *testing.T) {
	repo, _, _, _, taskID := montageFixture(t)
	manifest, workspace := retainedRegistrationPaths(t)
	_, err := repo.CompleteAndBegin(context.Background(), CompleteRegistration{TaskID: taskID, ManifestPath: manifest, WorkspacePath: workspace, Result: TaskResultWrite{Status: domain.TaskCompleted, Summary: "plaintext", AssistantContent: "plaintext", EventKind: "plaintext_ready"}, Artifacts: []TaskArtifact{{Kind: "plaintext_workspace", Path: workspace}}})
	if err == nil {
		t.Fatal("invalid artifact unexpectedly committed")
	}
	for _, table := range []string{"montage_registration_attempts", "task_artifacts", "task_messages", "task_events"} {
		var count int
		if err := repo.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE task_id=?`, taskID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d orphan rows", table, count)
		}
	}
}

func TestCompleteAndBeginReconcilesCommittedUnknownOutcome(t *testing.T) {
	repo, _, _, _, taskID := montageFixture(t)
	manifest, workspace := retainedRegistrationPaths(t)
	repo.commit = func(ctx context.Context, conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
			return err
		}
		return errors.New("transport lost after commit")
	}
	attempt, err := repo.CompleteAndBegin(context.Background(), CompleteRegistration{TaskID: taskID, ManifestPath: manifest, WorkspacePath: workspace, Result: TaskResultWrite{Status: domain.TaskCompleted, Summary: "plaintext", AssistantContent: "plaintext", EventKind: "plaintext_ready"}, Artifacts: []TaskArtifact{{Kind: "plaintext_workspace", Path: workspace, Filename: "workspace", MIMEType: "inode/directory", SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}}})
	if err != nil {
		t.Fatal(err)
	}
	if attempt.ID == "" || attempt.State != domain.RegistrationQueued {
		t.Fatalf("attempt=%#v", attempt)
	}
	var artifacts, attempts int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM task_artifacts WHERE task_id=?`, taskID).Scan(&artifacts); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM montage_registration_attempts WHERE task_id=?`, taskID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if artifacts != 1 || attempts != 1 {
		t.Fatalf("artifacts=%d attempts=%d", artifacts, attempts)
	}
}

func TestBeginRetryReconcilesCommittedUnknownOutcome(t *testing.T) {
	repo, _, _, _, taskID := montageFixture(t)
	manifest, workspace := retainedRegistrationPaths(t)
	first, err := repo.Begin(context.Background(), BeginRegistration{TaskID: taskID, ManifestPath: manifest, WorkspacePath: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Fail(context.Background(), first.ID, "failed", "failed"); err != nil {
		t.Fatal(err)
	}
	repo.commit = committedThenUnknown
	retry, err := repo.BeginRetry(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if retry.State != domain.RegistrationQueued || retry.Attempt != 2 {
		t.Fatalf("retry=%#v", retry)
	}
}

func TestRecoverReconcilesCommittedUnknownOutcome(t *testing.T) {
	repo, _, _, _, taskID := montageFixture(t)
	manifest, workspace := retainedRegistrationPaths(t)
	attempt, err := repo.Begin(context.Background(), BeginRegistration{TaskID: taskID, ManifestPath: manifest, WorkspacePath: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRunning(context.Background(), attempt.ID); err != nil {
		t.Fatal(err)
	}
	repo.commit = committedThenUnknown
	if _, err := repo.RecoverActive(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Attempt(context.Background(), attempt.ID)
	if err != nil || got.State != domain.RegistrationInterrupted {
		t.Fatalf("attempt=%#v err=%v", got, err)
	}
}

func committedThenUnknown(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return err
	}
	return errors.New("transport lost after commit")
}

func montageFixture(t *testing.T) (*MontageRepository, *AssetRepository, string, string, string) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "montage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	accountID, projectID, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, accountID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,'p','mixing',?,?)`, projectID, accountID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO codex_tasks(id,project_id,account_id,type,skill_name,action,status,completion_phase,transport,prompt_snapshot,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, taskID, projectID, accountID, "montage", "jianying-montage-draft", domain.ActionMontageExecute, domain.TaskRunning, domain.CompletionPlaintextReady, "legacy_exec", "prompt", now); err != nil {
		t.Fatal(err)
	}
	return NewMontageRepository(db), NewAssetRepository(db), accountID, projectID, taskID
}

func retainedRegistrationPaths(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	manifest := filepath.Join(root, "task_manifest.json")
	if err := os.WriteFile(manifest, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	return manifest, workspace
}

func TestMontageBeginRetryUsesRetainedPathsAndFailedState(t *testing.T) {
	repo, _, _, _, taskID := montageFixture(t)
	manifest, workspace := retainedRegistrationPaths(t)
	first, err := repo.Begin(context.Background(), BeginRegistration{TaskID: taskID, ManifestPath: manifest, WorkspacePath: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Fail(context.Background(), first.ID, "failed", "failed"); err != nil {
		t.Fatal(err)
	}
	retry, err := repo.BeginRetry(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Attempt != 2 || retry.ManifestPath != manifest || retry.WorkspacePath != workspace || retry.State != domain.RegistrationQueued {
		t.Fatalf("retry=%#v", retry)
	}
	if _, err := repo.BeginRetry(context.Background(), taskID); err == nil {
		t.Fatal("queued attempt must not be retryable")
	}
}

func TestMontageBeginRetryRejectsMissingRetainedWorkspace(t *testing.T) {
	repo, _, _, _, taskID := montageFixture(t)
	manifest, workspace := retainedRegistrationPaths(t)
	first, err := repo.Begin(context.Background(), BeginRegistration{TaskID: taskID, ManifestPath: manifest, WorkspacePath: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Fail(context.Background(), first.ID, "failed", "failed"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.BeginRetry(context.Background(), taskID); err == nil {
		t.Fatal("missing workspace was accepted")
	}
}

func TestMontageRecoverInterruptsRunningAndReturnsQueued(t *testing.T) {
	repo, _, _, _, taskID := montageFixture(t)
	manifest, workspace := retainedRegistrationPaths(t)
	running, err := repo.Begin(context.Background(), BeginRegistration{TaskID: taskID, ManifestPath: manifest, WorkspacePath: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRunning(context.Background(), running.ID); err != nil {
		t.Fatal(err)
	}
	recovery, err := repo.RecoverActive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery.Interrupted) != 1 || recovery.Interrupted[0].ID != running.ID || len(recovery.Queued) != 0 {
		t.Fatalf("recovery=%#v", recovery)
	}
	latest, err := repo.Latest(context.Background(), taskID)
	if err != nil || latest.State != domain.RegistrationInterrupted {
		t.Fatalf("latest=%#v err=%v", latest, err)
	}
	queued, err := repo.BeginRetry(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err = repo.RecoverActive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery.Queued) != 1 || recovery.Queued[0].ID != queued.ID || len(recovery.Interrupted) != 0 {
		t.Fatalf("queued recovery=%#v", recovery)
	}
}

func preparedMontageSuccess(t *testing.T) (*MontageRepository, string, RegistrationSuccess) {
	t.Helper()
	repo, _, _, projectID, taskID := montageFixture(t)
	manifest, workspace := retainedRegistrationPaths(t)
	attempt, err := repo.Begin(context.Background(), BeginRegistration{TaskID: taskID, ManifestPath: manifest, WorkspacePath: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRunning(context.Background(), attempt.ID); err != nil {
		t.Fatal(err)
	}
	workspaceHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if _, err := repo.db.Exec(`INSERT INTO task_artifacts(id,task_id,kind,path,filename,mime_type,size,sha256,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, uuid.NewString(), taskID, "plaintext_workspace", workspace, "workspace", "inode/directory", 0, workspaceHash, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return repo, projectID, RegistrationSuccess{
		AttemptID:       attempt.ID,
		RegisteredPath:  filepath.Join(t.TempDir(), taskID),
		ReceiptPath:     filepath.Join(workspace, "receipt.json"),
		DraftID:         "verified-draft-id",
		SHA256:          "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		WorkspaceSHA256: workspaceHash,
		Filename:        taskID,
	}
}

func TestMontageSucceedSetsReadyAtWhenAdvancingToReview(t *testing.T) {
	repo, projectID, success := preparedMontageSuccess(t)
	before := time.Now().UTC()
	if err := repo.Succeed(context.Background(), success); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC()
	var stage domain.ProjectStage
	var readyAt *time.Time
	if err := repo.db.QueryRow(`SELECT stage,ready_at FROM projects WHERE id=?`, projectID).Scan(&stage, &readyAt); err != nil {
		t.Fatal(err)
	}
	if stage != domain.StageReview {
		t.Fatalf("stage=%s, want review", stage)
	}
	if readyAt == nil || readyAt.Before(before) || readyAt.After(after) {
		t.Fatalf("ready_at=%v, want timestamp in [%v,%v]", readyAt, before, after)
	}
}

func TestMontageSucceedPreservesExistingReadyAt(t *testing.T) {
	repo, projectID, success := preparedMontageSuccess(t)
	original := time.Date(2026, time.August, 1, 2, 3, 4, 0, time.UTC)
	if _, err := repo.db.Exec(`UPDATE projects SET ready_at=? WHERE id=?`, original, projectID); err != nil {
		t.Fatal(err)
	}
	if err := repo.Succeed(context.Background(), success); err != nil {
		t.Fatal(err)
	}
	var readyAt time.Time
	if err := repo.db.QueryRow(`SELECT ready_at FROM projects WHERE id=?`, projectID).Scan(&readyAt); err != nil {
		t.Fatal(err)
	}
	if !readyAt.Equal(original) {
		t.Fatalf("ready_at=%v, want preserved %v", readyAt, original)
	}
}

func TestMontageSucceedDoesNotSetReadyAtWithoutReviewTransition(t *testing.T) {
	for _, stage := range []domain.ProjectStage{
		domain.StageReview,
		domain.StagePublished,
		domain.StageArchived,
	} {
		t.Run(string(stage), func(t *testing.T) {
			repo, projectID, success := preparedMontageSuccess(t)
			if _, err := repo.db.Exec(`UPDATE projects SET stage=?,ready_at=NULL WHERE id=?`, stage, projectID); err != nil {
				t.Fatal(err)
			}
			if err := repo.Succeed(context.Background(), success); err != nil {
				t.Fatal(err)
			}
			var gotStage domain.ProjectStage
			var readyAt *time.Time
			if err := repo.db.QueryRow(`SELECT stage,ready_at FROM projects WHERE id=?`, projectID).Scan(&gotStage, &readyAt); err != nil {
				t.Fatal(err)
			}
			if gotStage != stage {
				t.Fatalf("stage=%s, want unchanged %s", gotStage, stage)
			}
			if readyAt != nil {
				t.Fatalf("ready_at=%v, want nil without review transition", readyAt)
			}
		})
	}
}

func TestAuditMixDraftsStalesUnregisteredMontageAsset(t *testing.T) {
	repo, assets, accountID, projectID, taskID := montageFixture(t)
	trustedRoot := t.TempDir()
	draft := filepath.Join(trustedRoot, taskID)
	if err := os.Mkdir(draft, 0o700); err != nil {
		t.Fatal(err)
	}
	version, err := assets.AddVersion(context.Background(), AddAssetVersion{ProjectID: &projectID, AccountID: accountID, Type: domain.AssetMixDraft, StorageKind: domain.StorageDirectory, Path: draft, Filename: taskID, MIMEType: "inode/directory", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SourceTaskID: &taskID})
	if err != nil {
		t.Fatal(err)
	}
	report, err := repo.AuditMixDrafts(context.Background(), trustedRoot)
	if err != nil {
		t.Fatal(err)
	}
	if report.Inspected != 1 || report.Staled != 1 || len(report.Findings) != 1 {
		t.Fatalf("report=%#v", report)
	}
	got, err := assets.Version(context.Background(), version.ID)
	if err != nil || got.State != domain.AssetStale {
		t.Fatalf("version=%#v err=%v", got, err)
	}
}

func TestAuditMixDraftsStalesSucceededAttemptOutsideTrustedRoot(t *testing.T) {
	repo, assets, _, projectID, taskID := montageFixture(t)
	manifest, workspace := retainedRegistrationPaths(t)
	attempt, err := repo.Begin(context.Background(), BeginRegistration{TaskID: taskID, ManifestPath: manifest, WorkspacePath: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRunning(context.Background(), attempt.ID); err != nil {
		t.Fatal(err)
	}
	workspaceHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if _, err := repo.db.Exec(`INSERT INTO task_artifacts(id,task_id,kind,path,filename,mime_type,size,sha256,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, uuid.NewString(), taskID, "plaintext_workspace", workspace, "workspace", "inode/directory", 0, workspaceHash, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), taskID)
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	hash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := repo.Succeed(context.Background(), RegistrationSuccess{AttemptID: attempt.ID, RegisteredPath: outside, ReceiptPath: filepath.Join(workspace, "receipt.json"), DraftID: "verified-draft-id", SHA256: hash, WorkspaceSHA256: workspaceHash, Filename: taskID}); err != nil {
		t.Fatal(err)
	}
	report, err := repo.AuditMixDrafts(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if report.Staled != 1 || len(report.Findings) != 1 {
		t.Fatalf("report=%#v", report)
	}
	versions, err := assets.CurrentByProject(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range versions {
		if version.Type == domain.AssetMixDraft && version.State != domain.AssetStale {
			t.Fatalf("mix draft remained ready: %#v", version)
		}
	}
}

func TestAuditMixDraftsStalesMissingAndNonMontageSources(t *testing.T) {
	repo, assets, accountID, projectID, taskID := montageFixture(t)
	trustedRoot := t.TempDir()
	withoutSource := filepath.Join(trustedRoot, "without-source")
	nonMontage := filepath.Join(trustedRoot, "non-montage")
	if err := os.Mkdir(withoutSource, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(nonMontage, 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := assets.AddVersion(context.Background(), AddAssetVersion{ProjectID: &projectID, AccountID: accountID, Type: domain.AssetMixDraft, StorageKind: domain.StorageDirectory, Path: withoutSource, Filename: "without-source", MIMEType: "inode/directory", SHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`UPDATE codex_tasks SET action=? WHERE id=?`, domain.ActionMontagePlan, taskID); err != nil {
		t.Fatal(err)
	}
	second, err := assets.AddVersion(context.Background(), AddAssetVersion{LogicalAssetID: first.AssetID, ProjectID: &projectID, AccountID: accountID, Type: domain.AssetMixDraft, StorageKind: domain.StorageDirectory, Path: nonMontage, Filename: "non-montage", MIMEType: "inode/directory", SHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", SourceTaskID: &taskID})
	if err != nil {
		t.Fatal(err)
	}
	report, err := repo.AuditMixDrafts(context.Background(), trustedRoot)
	if err != nil {
		t.Fatal(err)
	}
	if report.Inspected != 2 || report.Staled != 2 || len(report.Findings) != 2 {
		t.Fatalf("report=%#v", report)
	}
	for _, id := range []string{first.ID, second.ID} {
		version, err := assets.Version(context.Background(), id)
		if err != nil || version.State != domain.AssetStale {
			t.Fatalf("version=%#v err=%v", version, err)
		}
	}
}

func TestAuditMixDraftsStalesDanglingSourceTask(t *testing.T) {
	repo, assets, accountID, projectID, _ := montageFixture(t)
	trustedRoot := t.TempDir()
	draft := filepath.Join(trustedRoot, "dangling-source")
	if err := os.Mkdir(draft, 0o700); err != nil {
		t.Fatal(err)
	}
	version, err := assets.AddVersion(context.Background(), AddAssetVersion{ProjectID: &projectID, AccountID: accountID, Type: domain.AssetMixDraft, StorageKind: domain.StorageDirectory, Path: draft, Filename: "dangling-source", MIMEType: "inode/directory", SHA256: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := repo.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=OFF`); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	missingTaskID := uuid.NewString()
	if _, err := conn.ExecContext(context.Background(), `UPDATE asset_versions SET source_task_id=? WHERE id=?`, missingTaskID, version.ID); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := repo.AuditMixDrafts(context.Background(), trustedRoot)
	if err != nil {
		t.Fatal(err)
	}
	if report.Staled != 1 || len(report.Findings) != 1 || report.Findings[0].TaskID == nil || *report.Findings[0].TaskID != missingTaskID {
		t.Fatalf("report=%#v", report)
	}
}
