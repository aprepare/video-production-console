package remixlab

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/store"
)

type remixFakeProtector struct{}

func (remixFakeProtector) Protect(value []byte) ([]byte, error) {
	return append([]byte("opaque:"), value...), nil
}

func (remixFakeProtector) Unprotect(value []byte) ([]byte, error) {
	return bytes.TrimPrefix(value, []byte("opaque:")), nil
}

type stubRuntime struct {
	view RuntimeView
	err  error
}

func (s stubRuntime) Runtime(context.Context) (RuntimeView, error) {
	return s.view, s.err
}

func fastStubRunner(_ context.Context, opts openaicompat.Options) error {
	dir := filepath.Dir(opts.OutputLastMessage)
	return os.WriteFile(filepath.Join(dir, "continuous_script.txt"), []byte("stub-script"), 0o644)
}

func waitExperimentTerminal(t *testing.T, svc *Service, id string) Experiment {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		exp, err := svc.GetExperiment(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		switch exp.Status {
		case "completed", "partial", "failed":
			return exp
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for experiment %s terminal status", id)
	return Experiment{}
}

func TestCreateExperimentUsesPresetKeyAndMasksDefaults(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)
	prot := remixFakeProtector{}
	cipher, _ := prot.Protect([]byte("sk-preset"))
	if err := repo.PutPresetJSON(t.Context(), `{"slots":[{"base_url":"https://api.deepseek.com","model":"deepseek-v4-pro","run_count":2,"api_key_ciphertext":"`+base64.StdEncoding.EncodeToString(cipher)+`"}]}`); err != nil {
		t.Fatal(err)
	}
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixBaseURL: "http://console.example/v1", RemixModel: "grok-4.6-fast", RemixAPIKey: "sk-runtime", DataRoot: t.TempDir()}}, prot, t.TempDir(), fastStubRunner, nil, nil)
	idx := 0
	exp, err := svc.CreateExperiment(t.Context(), "对标原文一二三", []SlotInput{
		{Model: "deepseek-v4-pro", BaseURL: "https://api.deepseek.com", RunCount: 2, PresetIndex: &idx},
	})
	if err != nil {
		t.Fatal(err)
	}
	if exp.PromptStamp != openaicompat.RewritePromptStampStable {
		t.Fatalf("stamp=%q", exp.PromptStamp)
	}
	if len(exp.Runs) != 2 || exp.Runs[0].ID == exp.Runs[1].ID {
		t.Fatalf("runs=%+v", exp.Runs)
	}
	def, err := svc.Defaults(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if def.RemixBaseURL != "http://console.example/v1" || !def.RemixAPIKeyConfigured {
		t.Fatalf("%+v", def)
	}
	if len(def.Presets) != 1 || !def.Presets[0].APIKeyConfigured || def.Presets[0].Model != "deepseek-v4-pro" {
		t.Fatalf("%+v", def.Presets)
	}
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, "sk-preset") || strings.Contains(body, "sk-runtime") || strings.Contains(body, "opaque:") {
		t.Fatalf("defaults leaked secret material: %s", body)
	}
	expRaw, err := json.Marshal(exp)
	if err != nil {
		t.Fatal(err)
	}
	expBody := string(expRaw)
	if strings.Contains(expBody, "sk-preset") || strings.Contains(expBody, "sk-runtime") || strings.Contains(expBody, "opaque:") {
		t.Fatalf("experiment leaked secret material: %s", expBody)
	}
	_ = waitExperimentTerminal(t, svc, exp.ID)
}

func TestCreateExperimentRejectsBadCounts(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime", DataRoot: t.TempDir()}}, remixFakeProtector{}, t.TempDir(), fastStubRunner, nil, nil)

	_, err = svc.CreateExperiment(t.Context(), "", []SlotInput{{Model: "m", RunCount: 1}})
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("empty source: err=%v", err)
	}

	slots := make([]SlotInput, 5)
	for i := range slots {
		slots[i] = SlotInput{Model: "m", RunCount: 1}
	}
	_, err = svc.CreateExperiment(t.Context(), "原文", slots)
	if !errors.Is(err, ErrInvalidSlots) {
		t.Fatalf("five slots: err=%v", err)
	}

	_, err = svc.CreateExperiment(t.Context(), "原文", []SlotInput{{Model: "m", RunCount: 4}})
	if !errors.Is(err, ErrInvalidRunCount) {
		t.Fatalf("run_count=4: err=%v", err)
	}
}

func TestDriveTwoRunsSameModelDoNotShareOutputDir(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)
	dataRoot := t.TempDir()

	var (
		outputsMu sync.Mutex
		outputs   []string
	)

	runner := func(_ context.Context, opts openaicompat.Options) error {
		dir := filepath.Dir(opts.OutputLastMessage)
		outputsMu.Lock()
		outputs = append(outputs, opts.OutputLastMessage)
		outputsMu.Unlock()
		script := "稿" + filepath.Base(dir)
		return os.WriteFile(filepath.Join(dir, "continuous_script.txt"), []byte(script), 0o644)
	}

	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime", RemixBaseURL: "http://x/v1"}}, remixFakeProtector{}, dataRoot, runner, nil, nil)
	exp, err := svc.CreateExperiment(t.Context(), "同一模型两稿原文", []SlotInput{
		{Model: "same-model", RunCount: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := waitExperimentTerminal(t, svc, exp.ID)
	if got.Status != "completed" {
		t.Fatalf("status=%q", got.Status)
	}
	if len(got.Runs) != 2 {
		t.Fatalf("runs=%d", len(got.Runs))
	}
	if got.Runs[0].ContinuousScript == "" || got.Runs[0].ContinuousScript == got.Runs[1].ContinuousScript {
		t.Fatalf("scripts=%q %q", got.Runs[0].ContinuousScript, got.Runs[1].ContinuousScript)
	}
	if got.Runs[0].OutputDir == "" || got.Runs[0].OutputDir == got.Runs[1].OutputDir {
		t.Fatalf("output dirs=%q %q", got.Runs[0].OutputDir, got.Runs[1].OutputDir)
	}
	outputsMu.Lock()
	gotOutputs := append([]string{}, outputs...)
	outputsMu.Unlock()
	if len(gotOutputs) != 2 {
		t.Fatalf("runner calls=%d", len(gotOutputs))
	}
	if gotOutputs[0] == gotOutputs[1] || filepath.Dir(gotOutputs[0]) == filepath.Dir(gotOutputs[1]) {
		t.Fatalf("OutputLastMessage not distinct: %v", gotOutputs)
	}
}

func TestDriveRunsAllQueuedTogether(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)

	var current, peak atomic.Int32
	runner := func(_ context.Context, opts openaicompat.Options) error {
		n := current.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(80 * time.Millisecond)
		current.Add(-1)
		dir := filepath.Dir(opts.OutputLastMessage)
		return os.WriteFile(filepath.Join(dir, "continuous_script.txt"), []byte("ok"), 0o644)
	}

	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)
	exp, err := svc.CreateExperiment(t.Context(), "并发一起跑原文", []SlotInput{
		{Model: "m1", RunCount: 1},
		{Model: "m2", RunCount: 1},
		{Model: "m3", RunCount: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := waitExperimentTerminal(t, svc, exp.ID)
	if got.Status != "completed" {
		t.Fatalf("status=%q", got.Status)
	}
	if peak.Load() != 3 {
		t.Fatalf("peak concurrency=%d want 3", peak.Load())
	}
}

func TestDriveOneFailureLeavesPartial(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)

	const secret = "sk-secret-key"
	runner := func(_ context.Context, opts openaicompat.Options) error {
		dir := filepath.Dir(opts.OutputLastMessage)
		if opts.Model == "a" {
			return errors.New("boom including " + secret)
		}
		return os.WriteFile(filepath.Join(dir, "continuous_script.txt"), []byte("survived"), 0o644)
	}

	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: secret}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)
	exp, err := svc.CreateExperiment(t.Context(), "部分失败原文", []SlotInput{
		{Model: "a", RunCount: 1},
		{Model: "b", RunCount: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := waitExperimentTerminal(t, svc, exp.ID)
	if got.Status != "partial" {
		t.Fatalf("status=%q runs=%+v", got.Status, got.Runs)
	}
	var failed, completed int
	for _, run := range got.Runs {
		switch run.Status {
		case "failed":
			failed++
			if run.ErrorMessage == "" {
				t.Fatal("failed run missing error_message")
			}
			if strings.Contains(run.ErrorMessage, secret) {
				t.Fatalf("error leaked key: %q", run.ErrorMessage)
			}
		case "completed":
			completed++
		}
	}
	if failed != 1 || completed != 1 {
		t.Fatalf("failed=%d completed=%d", failed, completed)
	}
}

func TestPatchCommentPersists(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), fastStubRunner, nil, nil)
	exp, err := svc.CreateExperiment(t.Context(), "批注原文", []SlotInput{{Model: "m", RunCount: 1}})
	if err != nil {
		t.Fatal(err)
	}
	_ = waitExperimentTerminal(t, svc, exp.ID)
	if err := svc.PatchComment(t.Context(), exp.Runs[0].ID, "开头不够狠"); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetExperiment(t.Context(), exp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runs[0].Comment != "开头不够狠" {
		t.Fatalf("comment=%q", got.Runs[0].Comment)
	}
}

func TestPatchCommentDoesNotClobberCompletedRun(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), fastStubRunner, nil, nil)
	exp, err := svc.CreateExperiment(t.Context(), "批注不覆盖原文", []SlotInput{{Model: "m", RunCount: 1}})
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitExperimentTerminal(t, svc, exp.ID)
	if terminal.Status != "completed" || terminal.Runs[0].ContinuousScript == "" {
		t.Fatalf("precondition failed: %+v", terminal.Runs[0])
	}
	scriptBefore := terminal.Runs[0].ContinuousScript
	if err := svc.PatchComment(t.Context(), terminal.Runs[0].ID, "边跑边批注"); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetExperiment(t.Context(), exp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runs[0].Status != "completed" || got.Runs[0].ContinuousScript != scriptBefore {
		t.Fatalf("clobbered: status=%q script=%q want completed/%q", got.Runs[0].Status, got.Runs[0].ContinuousScript, scriptBefore)
	}
	if got.Status != "completed" {
		t.Fatalf("experiment status=%q", got.Status)
	}
	if got.Runs[0].Comment != "边跑边批注" {
		t.Fatalf("comment=%q", got.Runs[0].Comment)
	}
}

func TestCreateExperimentEmptyPresetCipherFallsBackToRuntime(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)
	if err := repo.PutPresetJSON(t.Context(), `{"slots":[{"base_url":"https://api.example.com","model":"deepseek-v4-pro","run_count":1,"api_key_ciphertext":""}]}`); err != nil {
		t.Fatal(err)
	}
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixBaseURL: "https://api.example.com", RemixModel: "deepseek-v4-pro", RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), fastStubRunner, nil, nil)
	idx := 0
	exp, err := svc.CreateExperiment(t.Context(), "空预设密文回退", []SlotInput{
		{Model: "deepseek-v4-pro", BaseURL: "https://api.example.com", RunCount: 1, PresetIndex: &idx},
	})
	if err != nil {
		t.Fatalf("want success with runtime fallback, got %v", err)
	}
	_ = waitExperimentTerminal(t, svc, exp.ID)
}

func TestCreateExperimentSavePresetFailureStillDrives(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), fastStubRunner, nil, nil)
	svc.putPresetJSON = func(context.Context, string) error {
		return errors.New("preset write failed")
	}
	exp, err := svc.CreateExperiment(t.Context(), "预设保存失败仍驱动", []SlotInput{{Model: "m", RunCount: 1}})
	if err != nil {
		t.Fatalf("create should succeed despite preset error, got %v", err)
	}
	got := waitExperimentTerminal(t, svc, exp.ID)
	if got.Status != "completed" {
		t.Fatalf("drive did not finish: status=%q", got.Status)
	}
}
