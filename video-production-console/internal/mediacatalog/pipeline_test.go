package mediacatalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// newPipelineRunner returns a fake runner that answers ffprobe with metadata,
// scene detection with two cuts, and keyframe extraction by writing a real
// small JPEG at the requested .part destination.
func newPipelineRunner(t *testing.T) *fakeCommandRunner {
	t.Helper()
	runner := &fakeCommandRunner{}
	runner.setHandler(func(_ context.Context, binary string, args []string) ([]byte, []byte, error) {
		switch {
		case binary == "fake-ffprobe":
			return []byte(fakeProbeJSON), nil, nil
		case hasArg(args, "-frames:v"):
			writeTestJPEG(t, args[len(args)-1], 320, 180)
			return nil, nil, nil
		default:
			return nil, []byte(fakeSceneStderr), nil
		}
	})
	return runner
}

func newPipelineFixture(t *testing.T, runner *fakeCommandRunner) (string, *Repository, *Pipeline) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "originals", "movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	writeOriginal(t, root, "movies", "clip.mp4", "fake movie bytes")
	if _, err := NewIndexer(repo).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	ff, err := NewFFmpeg(FFmpegConfig{FFmpegPath: "fake-ffmpeg", FFprobePath: "fake-ffprobe", Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := NewPipeline(repo, ff)
	if err != nil {
		t.Fatal(err)
	}
	return root, repo, pipeline
}

func pendingProbeSource(t *testing.T, repo *Repository) Source {
	t.Helper()
	sources, err := repo.ListSources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected exactly one source, got %d", len(sources))
	}
	return sources[0]
}

func TestPipelineProbesCutsShotsAndExtractsVerifiedKeyframes(t *testing.T) {
	ctx := context.Background()
	runner := newPipelineRunner(t)
	root, repo, pipeline := newPipelineFixture(t, runner)

	summary, err := pipeline.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ProcessedSources != 1 || summary.Shots != 3 || summary.Keyframes != 9 || summary.FailedSources != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	source := pendingProbeSource(t, repo)
	if source.Status != SourceStatusReady {
		t.Fatalf("source must be ready, got %q (%s)", source.Status, source.ErrorCode)
	}
	if source.DurationMS != 20500 || source.Width != 1920 || source.Height != 1080 || source.FPS <= 29 {
		t.Fatalf("probe metadata was not persisted: %+v", source)
	}

	job, err := repo.JobBySourcePhase(ctx, source.ID, PhaseProbe)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != JobCompleted || job.CompletedUnits != 3 {
		t.Fatalf("probe job must complete with the shot count, got %+v", job)
	}

	shots, err := repo.ShotsBySource(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantWindows := []SceneBoundary{{0, 4200}, {4200, 9000}, {9000, 20500}}
	if len(shots) != len(wantWindows) {
		t.Fatalf("expected %d shots, got %d", len(wantWindows), len(shots))
	}
	for i, shot := range shots {
		if shot.SourceInMS != wantWindows[i].InMS || shot.SourceOutMS != wantWindows[i].OutMS {
			t.Fatalf("shot %d window mismatch: got [%d,%d) want %v", i, shot.SourceInMS, shot.SourceOutMS, wantWindows[i])
		}
		keyframes, err := repo.KeyframesByShot(ctx, shot.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(keyframes) != 3 {
			t.Fatalf("shot %d expected 3 keyframes, got %d", i, len(keyframes))
		}
		for j, keyframe := range keyframes {
			wantPrefix := fmt.Sprintf("derived/keyframes/%s/%s/", source.SHA256, shot.ID)
			if !strings.HasPrefix(keyframe.RelativePath, wantPrefix) {
				t.Fatalf("keyframe path %q missing prefix %q", keyframe.RelativePath, wantPrefix)
			}
			if keyframe.Width != 320 || keyframe.Height != 180 {
				t.Fatalf("keyframe dims mismatch: %dx%d", keyframe.Width, keyframe.Height)
			}
			wantAt := shot.SourceInMS + int64([]float64{0.2, 0.5, 0.8}[j]*float64(shot.DurationMS))
			if keyframe.AtMS != wantAt {
				t.Fatalf("keyframe %d at_ms %d, want %d", j, keyframe.AtMS, wantAt)
			}
			absolute, err := repo.ResolvePath(keyframe.RelativePath)
			if err != nil {
				t.Fatalf("keyframe file must exist inside the media root: %v", err)
			}
			data, err := os.ReadFile(absolute)
			if err != nil {
				t.Fatal(err)
			}
			if sha256Hex(data) != keyframe.SHA256 {
				t.Fatal("stored keyframe SHA-256 must match the file on disk")
			}
		}
	}

	// The atomic rename must not leave any job-specific .part files behind.
	var partFiles []string
	err = filepath.WalkDir(filepath.Join(root, "derived"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".part") {
			partFiles = append(partFiles, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(partFiles) > 0 {
		t.Fatalf("leftover .part files: %v", partFiles)
	}
}

func TestPipelineSkipsCompletedProbePhases(t *testing.T) {
	ctx := context.Background()
	runner := newPipelineRunner(t)
	_, _, pipeline := newPipelineFixture(t, runner)

	if _, err := pipeline.Run(ctx); err != nil {
		t.Fatal(err)
	}
	callsAfterFirstRun := runner.callCount()

	summary, err := pipeline.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ProcessedSources != 0 || summary.Shots != 0 || summary.Keyframes != 0 {
		t.Fatalf("completed phases must be skipped, got %+v", summary)
	}
	if runner.callCount() != callsAfterFirstRun {
		t.Fatalf("re-run executed %d extra commands", runner.callCount()-callsAfterFirstRun)
	}
}

func TestPipelineFailureMarksJobRetryableAndRecovers(t *testing.T) {
	ctx := context.Background()
	runner := &fakeCommandRunner{}
	runner.setHandler(func(_ context.Context, _ string, _ []string) ([]byte, []byte, error) {
		return nil, []byte("boom"), errors.New("simulated ffprobe crash")
	})
	_, repo, pipeline := newPipelineFixture(t, runner)

	summary, err := pipeline.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FailedSources != 1 || summary.ProcessedSources != 0 {
		t.Fatalf("unexpected summary after failure: %+v", summary)
	}
	source := pendingProbeSource(t, repo)
	if source.Status != SourceStatusFailed || source.ErrorCode != "probe_failed" {
		t.Fatalf("source must be failed with probe_failed, got %q/%q", source.Status, source.ErrorCode)
	}
	job, err := repo.JobBySourcePhase(ctx, source.ID, PhaseProbe)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != JobFailed || job.ErrorCode != "probe_failed" {
		t.Fatalf("job must be failed and retryable, got %+v", job)
	}

	// Swap in a working toolchain: the failed (non-completed) job is retried.
	working := newPipelineRunner(t)
	runner.setHandler(func(ctx context.Context, binary string, args []string) ([]byte, []byte, error) {
		return working.handle(ctx, binary, args)
	})
	summary, err = pipeline.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ProcessedSources != 1 || summary.Shots != 3 {
		t.Fatalf("retry must fully process the source, got %+v", summary)
	}
	source = pendingProbeSource(t, repo)
	if source.Status != SourceStatusReady || source.ErrorCode != "" {
		t.Fatalf("source must recover to ready, got %q/%q", source.Status, source.ErrorCode)
	}
	job, err = repo.JobBySourcePhase(ctx, source.ID, PhaseProbe)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != JobCompleted || job.ErrorCode != "" {
		t.Fatalf("job must complete cleanly after retry, got %+v", job)
	}
}

func TestPipelineResumeSkipsShotsThatAlreadyHaveKeyframes(t *testing.T) {
	ctx := context.Background()
	var extracts atomic.Int64
	runner := &fakeCommandRunner{}
	runner.setHandler(func(_ context.Context, binary string, args []string) ([]byte, []byte, error) {
		switch {
		case binary == "fake-ffprobe":
			return []byte(fakeProbeJSON), nil, nil
		case hasArg(args, "-frames:v"):
			if extracts.Add(1) > 3 {
				return nil, []byte("disk full"), errors.New("simulated extraction failure")
			}
			writeTestJPEG(t, args[len(args)-1], 320, 180)
			return nil, nil, nil
		default:
			return nil, []byte(fakeSceneStderr), nil
		}
	})
	_, repo, pipeline := newPipelineFixture(t, runner)

	summary, err := pipeline.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FailedSources != 1 {
		t.Fatalf("first run must fail mid-extraction, got %+v", summary)
	}
	source := pendingProbeSource(t, repo)
	if source.Status != SourceStatusFailed {
		t.Fatalf("source must not be left half-completed, got %q", source.Status)
	}

	// Recover with an always-working extractor: shot 1's keyframes already
	// exist, so only shots 2 and 3 extract again (6 more calls).
	extractsBeforeResume := extracts.Load()
	runner.setHandler(func(_ context.Context, binary string, args []string) ([]byte, []byte, error) {
		switch {
		case binary == "fake-ffprobe":
			return []byte(fakeProbeJSON), nil, nil
		case hasArg(args, "-frames:v"):
			extracts.Add(1)
			writeTestJPEG(t, args[len(args)-1], 320, 180)
			return nil, nil, nil
		default:
			return nil, []byte(fakeSceneStderr), nil
		}
	})
	summary, err = pipeline.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ProcessedSources != 1 || summary.Shots != 3 {
		t.Fatalf("resume must complete the source, got %+v", summary)
	}
	if extracts.Load()-extractsBeforeResume != 6 {
		t.Fatalf("resume must only extract missing shots, got %d new extractions", extracts.Load()-extractsBeforeResume)
	}
	shots, err := repo.ShotsBySource(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, shot := range shots {
		keyframes, err := repo.KeyframesByShot(ctx, shot.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(keyframes) != 3 {
			t.Fatalf("shot %s expected 3 keyframes after resume, got %d", shot.ID, len(keyframes))
		}
	}
}

func TestPipelineRejectsOversizedKeyframes(t *testing.T) {
	ctx := context.Background()
	runner := &fakeCommandRunner{}
	runner.setHandler(func(_ context.Context, binary string, args []string) ([]byte, []byte, error) {
		switch {
		case binary == "fake-ffprobe":
			return []byte(fakeProbeJSON), nil, nil
		case hasArg(args, "-frames:v"):
			writeTestJPEG(t, args[len(args)-1], 900, 500) // violates the 512px cap
			return nil, nil, nil
		default:
			return nil, []byte(fakeSceneStderr), nil
		}
	})
	_, repo, pipeline := newPipelineFixture(t, runner)

	summary, err := pipeline.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FailedSources != 1 {
		t.Fatalf("oversized keyframes must fail the source, got %+v", summary)
	}
	source := pendingProbeSource(t, repo)
	shots, err := repo.ShotsBySource(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, shot := range shots {
		keyframes, err := repo.KeyframesByShot(ctx, shot.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(keyframes) != 0 {
			t.Fatal("no keyframe row may be recorded for an invalid frame")
		}
	}
}

func TestPipelineConstructionRequiresConfiguredFFmpeg(t *testing.T) {
	repo := newTestRepository(t)
	if _, err := NewPipeline(repo, nil); !errors.Is(err, ErrFFmpegNotConfigured) {
		t.Fatalf("expected ErrFFmpegNotConfigured, got %v", err)
	}
	if _, err := NewFFmpeg(FFmpegConfig{}); !strings.Contains(fmt.Sprint(err), "ffmpeg_not_configured") {
		t.Fatalf("preflight must return ffmpeg_not_configured, got %v", err)
	}
}
