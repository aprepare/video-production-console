package remixlab

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/assets"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

func TestAdoptWritesContinuousScriptWithoutTouchingSource(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	dataRoot := t.TempDir()
	assetSvc := assets.NewService(dataRoot)
	projects := store.NewProjectRepository(db)

	accountID := uuid.NewString()
	projectID := uuid.NewString()
	now := time.Now().UTC()
	if _, err := store.NewAccountRepository(db).CreateWithBackground(context.Background(), domain.Account{
		ID: accountID, Name: "adopt-account", Color: "#fff", Status: "active", CreatedAt: now, UpdatedAt: now,
	}, store.NewBackground{
		ID: uuid.NewString(), Path: filepath.Join(dataRoot, "bg.png"), Filename: "bg.png", MIMEType: "image/png", Size: 1, SHA256: "sha",
	}); err != nil {
		t.Fatal(err)
	}
	if err := projects.CreateProject(context.Background(), domain.Project{
		ID: projectID, AccountID: accountID, Title: "风景项目", Stage: domain.StageScript, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	const oldSource = "旧同行原文不要动"
	savedSource, err := assetSvc.SaveProjectAsset(projectID, domain.AssetSourceScript, "source.txt", strings.NewReader(oldSource))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projects.AddAsset(context.Background(), &domain.Asset{
		ID: uuid.NewString(), ProjectID: &projectID, Type: domain.AssetSourceScript,
		Path: savedSource.Path, Filename: "source.txt", MIMEType: savedSource.MIMEType,
		Size: savedSource.Size, SHA256: savedSource.SHA256, Status: "active", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	const oldContinuous = "旧连续文案"
	savedOld, err := assetSvc.SaveProjectAsset(projectID, domain.AssetContinuousScript, "continuous-script.txt", strings.NewReader(oldContinuous))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projects.AddAsset(context.Background(), &domain.Asset{
		ID: uuid.NewString(), ProjectID: &projectID, Type: domain.AssetContinuousScript,
		Path: savedOld.Path, Filename: "continuous-script.txt", MIMEType: savedOld.MIMEType,
		Size: savedOld.Size, SHA256: savedOld.SHA256, Status: "active", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	repo := store.NewRemixLabRepository(db)
	const adoptedScript = "进化台采用稿正文"
	runner := func(_ context.Context, opts openaicompat.Options) error {
		dir := filepath.Dir(opts.OutputLastMessage)
		return os.WriteFile(filepath.Join(dir, "continuous_script.txt"), []byte(adoptedScript), 0o644)
	}
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, dataRoot, runner, assetSvc, projects)

	exp, err := svc.CreateExperiment(t.Context(), "采用测试原文", []SlotInput{{Model: "m", RunCount: 1}})
	if err != nil {
		t.Fatal(err)
	}
	got := waitExperimentTerminal(t, svc, exp.ID)
	if got.Status != "completed" || len(got.Runs) != 1 {
		t.Fatalf("exp=%+v", got)
	}
	runID := got.Runs[0].ID

	if err := svc.Adopt(t.Context(), runID, projectID); err != nil {
		t.Fatal(err)
	}

	current, err := store.NewAssetRepository(db).CurrentByProject(t.Context(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	var sourcePath, continuousPath string
	for _, v := range current {
		switch v.Type {
		case domain.AssetSourceScript:
			sourcePath = v.Path
		case domain.AssetContinuousScript:
			continuousPath = v.Path
		}
	}
	if sourcePath == "" || continuousPath == "" {
		t.Fatalf("missing assets: %+v", current)
	}
	sourceBody, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(sourceBody) != oldSource {
		t.Fatalf("source_script overwritten: %q", sourceBody)
	}
	continuousBody, err := os.ReadFile(continuousPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(continuousBody) != adoptedScript {
		t.Fatalf("continuous_script=%q want %q", continuousBody, adoptedScript)
	}

	run, err := repo.GetRun(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.AdoptedProjectID != projectID {
		t.Fatalf("adopted_project_id=%q", run.AdoptedProjectID)
	}
}

func TestAdoptRejectsNonCompletedRun(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dataRoot := t.TempDir()
	repo := store.NewRemixLabRepository(db)
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, dataRoot, fastStubRunner, assets.NewService(dataRoot), store.NewProjectRepository(db))

	now := time.Now().UTC()
	expID := uuid.NewString()
	slotID := uuid.NewString()
	runID := uuid.NewString()
	if err := repo.CreateExperiment(t.Context(), store.RemixLabExperimentRecord{
		ID: expID, Title: "t", SourceText: "s", PromptStamp: "stamp", Status: "running", CreatedAt: now, UpdatedAt: now,
	}, []store.RemixLabSlotRecord{{ID: slotID, ExperimentID: expID, Model: "m", RunCount: 1}}, []store.RemixLabRunRecord{
		{ID: runID, ExperimentID: expID, SlotID: slotID, RunIndex: 1, Status: "queued"},
	}); err != nil {
		t.Fatal(err)
	}

	err = svc.Adopt(t.Context(), runID, uuid.NewString())
	if !errors.Is(err, ErrRunNotAdoptable) {
		t.Fatalf("err=%v", err)
	}
}

func TestAdoptNilDepsReturnsError(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewService(store.NewRemixLabRepository(db), stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), fastStubRunner, nil, nil)
	err = svc.Adopt(t.Context(), uuid.NewString(), uuid.NewString())
	if !errors.Is(err, ErrAdoptUnavailable) {
		t.Fatalf("err=%v", err)
	}
}
