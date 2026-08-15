package catalogbuilder

import (
	"context"
	"errors"
	"fmt"

	"video-production-console/internal/mediacatalog"
)

type BuildSummary struct {
	Phase    string
	Index    mediacatalog.IndexSummary
	Pipeline mediacatalog.PipelineSummary
	Analysis mediacatalog.AnalysisSummary
}

func Build(ctx context.Context, cfg Config) (BuildSummary, error) {
	prepared, err := cfg.Prepare()
	if err != nil {
		return BuildSummary{}, err
	}
	cfg = prepared
	if err := cfg.Validate(); err != nil {
		return BuildSummary{}, err
	}
	repo, err := mediacatalog.Open(cfg.MediaRoot)
	if err != nil {
		return BuildSummary{}, fmt.Errorf("%w: catalog could not be opened", errInvalidConfig)
	}
	defer repo.Close()

	hosted, err := mediacatalog.RunHostedBuild(ctx, repo, mediacatalog.HostedBuildConfig{
		FFmpegPath:          cfg.FFmpegPath,
		FFprobePath:         cfg.FFprobePath,
		VisionBaseURL:       cfg.VisionBaseURL,
		VisionModel:         cfg.VisionModel,
		VisionAPIKey:        cfg.VisionAPIKey,
		EmbeddingBaseURL:    cfg.EmbeddingBaseURL,
		EmbeddingModel:      cfg.EmbeddingModel,
		EmbeddingAPIKey:     cfg.EmbeddingAPIKey,
		AnalysisConcurrency: cfg.AnalysisConcurrency,
	})
	summary := BuildSummary{
		Phase:    hosted.Phase,
		Index:    hosted.Index,
		Pipeline: hosted.Pipeline,
		Analysis: hosted.Analysis,
	}
	if errors.Is(err, mediacatalog.ErrFFmpegRequired) {
		return summary, errFFmpegRequired
	}
	if errors.Is(err, mediacatalog.ErrVisionRequired) {
		return summary, errVisionRequired
	}
	if errors.Is(err, mediacatalog.ErrAnalysisAllFailed) {
		summary.Phase = "failed"
		return summary, fmt.Errorf("%w: analysis finished with 0 ready shots", errBuildFailed)
	}
	if err != nil {
		if summary.Phase == "" {
			summary.Phase = "ingest"
		}
		return summary, fmt.Errorf("%w: %v", errBuildFailed, err)
	}
	if hosted.Index.DiscoveredFiles == 0 {
		return summary, fmt.Errorf("%w: no media under %s; put movies in originals/movies", errNoMedia, movieDir(cfg.MediaRoot))
	}
	summary.Phase = "ready"
	return summary, nil
}
