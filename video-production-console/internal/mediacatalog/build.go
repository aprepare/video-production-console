package mediacatalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrFFmpegRequired is returned after ingest when videos still need probe
	// and FFmpeg/FFprobe are not configured.
	ErrFFmpegRequired = errors.New("ffmpeg_not_configured")
	// ErrVisionRequired is returned when shots have keyframes but vision or
	// embedding settings are missing.
	ErrVisionRequired = errors.New("vision_not_configured")
	// ErrAnalysisAllFailed is returned when a finished analysis pass produced
	// only failures. Callers must not treat that as a successful ready library.
	ErrAnalysisAllFailed = errors.New("analysis_all_failed")
)

// HostedBuildConfig is the tool/model bundle for a local catalog build.
// Secrets are injected by the caller and never written into catalog.db.
type HostedBuildConfig struct {
	FFmpegPath          string
	FFprobePath         string
	VisionBaseURL       string
	VisionModel         string
	VisionAPIKey        string
	EmbeddingBaseURL    string
	EmbeddingModel      string
	EmbeddingAPIKey     string
	AnalysisConcurrency int
	OnPhase             func(phase string)
}

// HostedBuildSummary is the phase-by-phase result of RunHostedBuild.
type HostedBuildSummary struct {
	Phase    string
	Index    IndexSummary
	Pipeline PipelineSummary
	Analysis AnalysisSummary
}

// RunHostedBuild scans media_root, probes movies, and analyzes pending shots
// on an already-open catalog. It is shared by the console media library and
// catalog-builder so both entry points produce the same searchable catalog.db.
func RunHostedBuild(ctx context.Context, repo *Repository, cfg HostedBuildConfig) (HostedBuildSummary, error) {
	if repo == nil {
		return HostedBuildSummary{}, fmt.Errorf("%w: catalog repository is required", ErrInvalidValue)
	}
	setPhase := func(phase string) {
		if cfg.OnPhase != nil {
			cfg.OnPhase(phase)
		}
	}

	summary := HostedBuildSummary{Phase: PhaseIngest}
	setPhase(PhaseIngest)
	index, err := NewIndexer(repo).Run(ctx)
	summary.Index = index
	if err != nil {
		return summary, fmt.Errorf("ingest failed: %w", err)
	}

	pendingProbe, err := repo.SourcesWithIncompleteProbe(ctx)
	if err != nil {
		return summary, fmt.Errorf("list probe jobs: %w", err)
	}
	if len(pendingProbe) > 0 {
		if strings.TrimSpace(cfg.FFmpegPath) == "" || strings.TrimSpace(cfg.FFprobePath) == "" {
			return summary, ErrFFmpegRequired
		}
		summary.Phase = PhaseProbe
		setPhase(PhaseProbe)
		ffmpeg, err := NewFFmpeg(FFmpegConfig{FFmpegPath: cfg.FFmpegPath, FFprobePath: cfg.FFprobePath})
		if err != nil {
			return summary, ErrFFmpegRequired
		}
		pipeline, err := NewPipeline(repo, ffmpeg)
		if err != nil {
			return summary, ErrFFmpegRequired
		}
		pipe, err := pipeline.Run(ctx)
		summary.Pipeline = pipe
		if err != nil {
			return summary, fmt.Errorf("probe failed: %w", err)
		}
	}

	needAnalysis, err := hasAnalyzablePendingShots(ctx, repo)
	if err != nil {
		return summary, fmt.Errorf("list analysis jobs: %w", err)
	}
	if needAnalysis {
		if !hostedVisionConfigured(cfg) {
			return summary, ErrVisionRequired
		}
		summary.Phase = PhaseAnalysis
		setPhase(PhaseAnalysis)
		vision, err := NewHTTPVisionAnalyzer(VisionConfig{
			BaseURL: cfg.VisionBaseURL,
			Model:   cfg.VisionModel,
			APIKey:  cfg.VisionAPIKey,
		})
		if err != nil {
			return summary, ErrVisionRequired
		}
		embedder, err := NewHTTPEmbedder(EmbedderConfig{
			BaseURL: cfg.EmbeddingBaseURL,
			Model:   cfg.EmbeddingModel,
			APIKey:  cfg.EmbeddingAPIKey,
		})
		if err != nil {
			return summary, ErrVisionRequired
		}
		runner, err := NewAnalysisRunnerWithConcurrency(repo, vision, embedder, cfg.EmbeddingModel, cfg.AnalysisConcurrency)
		if err != nil {
			return summary, ErrVisionRequired
		}
		analysis, err := runner.Run(ctx)
		summary.Analysis = analysis
		if err != nil {
			return summary, fmt.Errorf("analysis failed: %w", err)
		}
		if analysis.AnalyzedShots == 0 && analysis.FailedShots > 0 {
			return summary, fmt.Errorf("%w: %d shots failed and none completed", ErrAnalysisAllFailed, analysis.FailedShots)
		}
	}

	summary.Phase = "ready"
	setPhase("ready")
	return summary, nil
}

func hostedVisionConfigured(cfg HostedBuildConfig) bool {
	return strings.TrimSpace(cfg.VisionBaseURL) != "" &&
		strings.TrimSpace(cfg.VisionModel) != "" &&
		strings.TrimSpace(cfg.EmbeddingBaseURL) != "" &&
		strings.TrimSpace(cfg.EmbeddingModel) != ""
}

func hasAnalyzablePendingShots(ctx context.Context, repo *Repository) (bool, error) {
	shots, err := repo.ShotsPendingAnalysis(ctx, AnalysisVersion)
	if err != nil {
		return false, err
	}
	for _, shot := range shots {
		frames, err := repo.KeyframesByShot(ctx, shot.ID)
		if err != nil {
			return false, err
		}
		if len(frames) > 0 {
			return true, nil
		}
	}
	return false, nil
}
