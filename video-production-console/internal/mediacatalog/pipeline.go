package mediacatalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"os"
	"path/filepath"
)

// Pipeline consumes pending probe jobs: ffprobe metadata, scene cuts (time
// windows only, no physical cutting), and 2-3 low-res keyframes per shot.
// The original movie bytes and audio never leave the machine.
type Pipeline struct {
	repo   *Repository
	ffmpeg *FFmpeg
}

// NewPipeline requires an already-configured FFmpeg wrapper; construction via
// NewFFmpeg guarantees the binary paths were injected by the caller.
func NewPipeline(repo *Repository, ffmpeg *FFmpeg) (*Pipeline, error) {
	if repo == nil {
		return nil, fmt.Errorf("%w: pipeline requires a repository", ErrInvalidValue)
	}
	if ffmpeg == nil {
		return nil, ErrFFmpegNotConfigured
	}
	return &Pipeline{repo: repo, ffmpeg: ffmpeg}, nil
}

type PipelineSummary struct {
	ProcessedSources int
	FailedSources    int
	Shots            int
	Keyframes        int
}

// Run processes every source whose probe job has not completed. Failures mark
// the job failed (retryable) and the source failed; they never leave a
// half-finished completed state.
func (p *Pipeline) Run(ctx context.Context) (PipelineSummary, error) {
	summary := PipelineSummary{}
	sources, err := p.repo.SourcesWithIncompleteProbe(ctx)
	if err != nil {
		return summary, err
	}
	for _, source := range sources {
		job, err := p.repo.JobBySourcePhase(ctx, source.ID, PhaseProbe)
		if err != nil {
			return summary, err
		}
		if job.Status == JobCompleted {
			continue
		}
		shots, keyframes, err := p.processSource(ctx, source, job)
		if err != nil {
			summary.FailedSources++
			code := pipelineErrorCode(err)
			if failErr := p.repo.FailJob(ctx, job.ID, code, err.Error()); failErr != nil {
				return summary, errors.Join(err, failErr)
			}
			if statusErr := p.repo.UpdateSourceStatus(ctx, source.ID, SourceStatusFailed, code); statusErr != nil {
				return summary, errors.Join(err, statusErr)
			}
			if ctx.Err() != nil {
				return summary, err
			}
			continue
		}
		summary.ProcessedSources++
		summary.Shots += shots
		summary.Keyframes += keyframes
	}
	return summary, nil
}

func pipelineErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrFFmpegNotConfigured):
		return "ffmpeg_not_configured"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "probe_timeout"
	default:
		return "probe_failed"
	}
}

func (p *Pipeline) processSource(ctx context.Context, source Source, job Job) (int, int, error) {
	if err := p.repo.StartJob(ctx, job.ID); err != nil {
		return 0, 0, err
	}
	absPath, err := p.repo.ResolvePath(source.RelativePath)
	if err != nil {
		return 0, 0, err
	}
	probe, err := p.ffmpeg.Probe(ctx, absPath)
	if err != nil {
		return 0, 0, err
	}
	if err := p.repo.UpdateSourceProbe(ctx, source.ID, probe); err != nil {
		return 0, 0, err
	}
	shots, err := p.repo.ShotsBySource(ctx, source.ID)
	if err != nil {
		return 0, 0, err
	}
	if len(shots) == 0 {
		boundaries, err := p.ffmpeg.DetectScenes(ctx, absPath, probe.DurationMS)
		if err != nil {
			return 0, 0, err
		}
		for ordinal, boundary := range boundaries {
			shot, err := p.repo.InsertShot(ctx, Shot{
				SourceID:   source.ID,
				Ordinal:    ordinal,
				SourceInMS: boundary.InMS, SourceOutMS: boundary.OutMS,
			})
			if err != nil {
				return 0, 0, err
			}
			shots = append(shots, shot)
		}
	}
	keyframes := 0
	for _, shot := range shots {
		written, err := p.extractShotKeyframes(ctx, source, shot, absPath, job.ID)
		if err != nil {
			return 0, 0, err
		}
		keyframes += written
	}
	if err := p.repo.UpdateSourceStatus(ctx, source.ID, SourceStatusReady, ""); err != nil {
		return 0, 0, err
	}
	if err := p.repo.CompleteJob(ctx, job.ID, len(shots)); err != nil {
		return 0, 0, err
	}
	return len(shots), keyframes, nil
}

// keyframeOffsets yields the relative sampling positions inside a shot:
// 3 frames at 20%/50%/80%, or 2 frames for very short shots.
func keyframeOffsets(durationMS int64) []float64 {
	if durationMS < 3000 {
		return []float64{0.3, 0.7}
	}
	return []float64{0.2, 0.5, 0.8}
}

// extractShotKeyframes writes each keyframe to a job-specific .part file,
// validates dimensions and SHA-256, atomically renames it into place, and only
// then records the row. Shots that already have keyframes are skipped so
// interrupted runs resume without duplicating work.
func (p *Pipeline) extractShotKeyframes(ctx context.Context, source Source, shot Shot, absPath, jobID string) (int, error) {
	existing, err := p.repo.KeyframesByShot(ctx, shot.ID)
	if err != nil {
		return 0, err
	}
	if len(existing) > 0 {
		return 0, nil
	}
	relativeDir := filepath.ToSlash(filepath.Join("derived", "keyframes", source.SHA256, shot.ID))
	absoluteDir := filepath.Join(p.repo.Root(), filepath.FromSlash(relativeDir))
	if err := os.MkdirAll(absoluteDir, 0o755); err != nil {
		return 0, fmt.Errorf("create keyframe directory: %w", err)
	}
	written := 0
	for index, offset := range keyframeOffsets(shot.DurationMS) {
		atMS := shot.SourceInMS + int64(offset*float64(shot.DurationMS))
		fileName := fmt.Sprintf("%02d.jpg", index+1)
		finalPath := filepath.Join(absoluteDir, fileName)
		partPath := finalPath + "." + jobID + ".part"
		if err := p.writeVerifiedKeyframe(ctx, absPath, atMS, partPath, finalPath, shot.ID, index, relativeDir+"/"+fileName); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

func (p *Pipeline) writeVerifiedKeyframe(ctx context.Context, sourcePath string, atMS int64, partPath, finalPath, shotID string, ordinal int, relativePath string) error {
	defer func() { _ = os.Remove(partPath) }()
	if err := p.ffmpeg.ExtractKeyframe(ctx, sourcePath, atMS, partPath); err != nil {
		return err
	}
	width, height, digest, err := inspectKeyframeJPEG(partPath)
	if err != nil {
		return err
	}
	// Windows cannot rename over an existing file; a stale final file can
	// only come from an interrupted run that never reached the DB row.
	_ = os.Remove(finalPath)
	if err := os.Rename(partPath, finalPath); err != nil {
		return fmt.Errorf("finalize keyframe: %w", err)
	}
	if _, err := p.repo.InsertKeyframe(ctx, Keyframe{
		ShotID: shotID, Ordinal: ordinal, RelativePath: relativePath,
		AtMS: atMS, Width: width, Height: height, SHA256: digest,
	}); err != nil {
		_ = os.Remove(finalPath)
		return err
	}
	return nil
}

func inspectKeyframeJPEG(path string) (int, int, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, "", fmt.Errorf("read keyframe: %w", err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "jpeg" {
		return 0, 0, "", fmt.Errorf("%w: keyframe is not a decodable JPEG", ErrInvalidValue)
	}
	if config.Width > KeyframeMaxEdge || config.Height > KeyframeMaxEdge {
		return 0, 0, "", fmt.Errorf("%w: keyframe %dx%d exceeds the %dpx edge cap", ErrInvalidValue, config.Width, config.Height, KeyframeMaxEdge)
	}
	sum := sha256.Sum256(data)
	return config.Width, config.Height, hex.EncodeToString(sum[:]), nil
}

// SourcesWithIncompleteProbe lists sources whose probe job exists and has not
// completed; interrupted or failed runs stay visible until they finish.
func (r *Repository) SourcesWithIncompleteProbe(ctx context.Context) ([]Source, error) {
	rows, err := r.db.QueryContext(ctx, sourceSelect+
		` WHERE id IN (SELECT source_id FROM media_jobs WHERE phase=? AND status<>'completed') ORDER BY relative_path`, PhaseProbe)
	if err != nil {
		return nil, fmt.Errorf("list sources pending probe: %w", err)
	}
	defer rows.Close()
	sources := []Source{}
	for rows.Next() {
		source, err := scanSource(rows)
		if err != nil {
			return nil, fmt.Errorf("read pending probe source: %w", err)
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

// UpdateSourceProbe persists ffprobe metadata for one source.
func (r *Repository) UpdateSourceProbe(ctx context.Context, id string, probe Probe) error {
	if probe.DurationMS <= 0 || probe.Width <= 0 || probe.Height <= 0 || probe.FPS < 0 {
		return fmt.Errorf("%w: probe metadata requires positive duration and dimensions", ErrInvalidValue)
	}
	return r.withImmediateTx(ctx, func(conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx,
			`UPDATE media_sources SET width=?, height=?, duration_ms=?, fps=?, updated_at=? WHERE id=?`,
			probe.Width, probe.Height, probe.DurationMS, probe.FPS, formatTime(r.now().UTC()), id)
		if err != nil {
			return fmt.Errorf("update source probe metadata: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrSourceNotFound
		}
		return nil
	})
}
