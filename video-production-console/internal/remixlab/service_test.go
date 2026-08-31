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

func TestDeleteExperimentRemovesRowsAndDir(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)
	dataRoot := t.TempDir()
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime", RemixBaseURL: "http://x/v1"}}, remixFakeProtector{}, dataRoot, fastStubRunner, nil, nil)
	exp, err := svc.CreateExperiment(t.Context(), "删除实验原文", []SlotInput{{Model: "m", RunCount: 1}})
	if err != nil {
		t.Fatal(err)
	}
	got := waitExperimentTerminal(t, svc, exp.ID)
	expDir := filepath.Join(dataRoot, "remix-lab", exp.ID)
	if _, err := os.Stat(expDir); err != nil {
		t.Fatalf("expected output dir %s: %v", expDir, err)
	}
	if got.Runs[0].OutputDir == "" {
		t.Fatalf("missing output dir on run: %+v", got.Runs[0])
	}
	if err := svc.DeleteExperiment(t.Context(), exp.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetExperiment(t.Context(), exp.ID); !errors.Is(err, store.ErrRemixLabNotFound) {
		t.Fatalf("get after delete err=%v", err)
	}
	list, err := svc.ListExperiments(t.Context())
	if err != nil || len(list) != 0 {
		t.Fatalf("list after delete=%v err=%v", list, err)
	}
	if _, err := os.Stat(expDir); !os.IsNotExist(err) {
		t.Fatalf("output dir still present: %v", err)
	}
	if err := svc.DeleteExperiment(t.Context(), exp.ID); !errors.Is(err, store.ErrRemixLabNotFound) {
		t.Fatalf("second delete err=%v", err)
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

// 写手调用失败时 Run 把真实原因写进 last.json 并返回 nil；跑批层必须把
// 信封里的 summary 透传成 error_message，不能只报「找不到成稿文件」。
func TestExecuteRunSurfacesEnvelopeFailureSummary(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)
	runner := func(_ context.Context, opts openaicompat.Options) error {
		envelope := `{"status":"failed","summary":"chat completions status 403: insufficient_quota"}`
		return os.WriteFile(opts.OutputLastMessage, []byte(envelope), 0o644)
	}
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime", RemixBaseURL: "http://x/v1"}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)
	exp, err := svc.CreateExperiment(t.Context(), "配额不足原文", []SlotInput{{Model: "m", RunCount: 1}})
	if err != nil {
		t.Fatal(err)
	}
	got := waitExperimentTerminal(t, svc, exp.ID)
	if got.Runs[0].Status != "failed" {
		t.Fatalf("run status = %s", got.Runs[0].Status)
	}
	if !strings.Contains(got.Runs[0].ErrorMessage, "insufficient_quota") {
		t.Fatalf("error message should carry the envelope summary, got %q", got.Runs[0].ErrorMessage)
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

func TestCreateExperimentCrossesPromptsAndModels(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := store.NewRemixLabRepository(db)
	var manifests []string
	var mu sync.Mutex
	runner := func(_ context.Context, opts openaicompat.Options) error {
		dir := filepath.Dir(opts.OutputLastMessage)
		raw, err := os.ReadFile(filepath.Join(dir, "task_manifest.json"))
		if err != nil {
			return err
		}
		mu.Lock()
		manifests = append(manifests, string(raw))
		mu.Unlock()
		return os.WriteFile(filepath.Join(dir, "continuous_script.txt"), []byte("cross"), 0o644)
	}
	svc := NewService(repo, stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)
	exp, err := svc.CreateExperimentWithPrompts(t.Context(), "交叉原文", []SlotInput{
		{Model: "m1", RunCount: 1},
		{Model: "m2", RunCount: 1},
	}, []string{"elder_stable", "bone_flesh"})
	if err != nil {
		t.Fatal(err)
	}
	if exp.PromptStamp != "交叉试验" {
		t.Fatalf("stamp=%q", exp.PromptStamp)
	}
	if len(exp.Runs) != 4 {
		t.Fatalf("runs=%d want 4", len(exp.Runs))
	}
	got := waitExperimentTerminal(t, svc, exp.ID)
	if got.Status != "completed" {
		t.Fatalf("status=%q", got.Status)
	}
	ids := map[string]int{}
	for _, run := range got.Runs {
		ids[run.PromptID]++
		if run.PromptName == "" {
			t.Fatalf("missing prompt name: %+v", run)
		}
	}
	if ids["elder_stable"] != 2 || ids["bone_flesh"] != 2 {
		t.Fatalf("prompt counts=%v", ids)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(manifests) != 4 {
		t.Fatalf("manifests=%d", len(manifests))
	}
	sawOverride := false
	for _, raw := range manifests {
		if strings.Contains(raw, "remix_system_prompt") && strings.Contains(raw, "骨肉分离") {
			sawOverride = true
		}
	}
	if !sawOverride {
		t.Fatalf("expected bone_flesh system override in manifests: %v", manifests)
	}
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
