package remixlab

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/store"
)

func TestAuditFailedEnvelopeWithDraftDoesNotComplete(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	runner := func(_ context.Context, opts openaicompat.Options) error {
		if err := os.WriteFile(filepath.Join(filepath.Dir(opts.OutputLastMessage), "continuous_script.txt"), []byte("保留下来的待审稿。"), 0600); err != nil {
			return err
		}
		return os.WriteFile(opts.OutputLastMessage, []byte(`{"status":"failed","summary":"事实核查未完成"}`), 0600)
	}
	svc := NewService(store.NewRemixLabRepository(db), stubRuntime{view: RuntimeView{RemixAPIKey: "test-key"}}, remixFakeProtector{}, t.TempDir(), runner, nil, nil)
	exp, err := svc.CreateExperiment(t.Context(), "测试原文", []SlotInput{{Model: "model", RunCount: 1}})
	if err != nil {
		t.Fatal(err)
	}
	run := waitExperimentTerminal(t, svc, exp.ID).Runs[0]
	if run.Status != "failed" || !strings.Contains(run.ErrorMessage, "事实核查") {
		t.Fatalf("false completion: %+v", run)
	}
	if run.ContinuousScript != "保留下来的待审稿。" {
		t.Fatalf("recoverable draft lost: %q", run.ContinuousScript)
	}
}
