package catalogbuilder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"video-production-console/internal/mediacatalog"
)

type MergeSummary struct {
	ImportedSources int
	SkippedSources  int
	ImportedShots   int
	CopiedKeyframes int
	Warnings        []string
}

func MergePack(ctx context.Context, hostRoot, zipPath string) (MergeSummary, error) {
	if strings.TrimSpace(hostRoot) == "" || !filepath.IsAbs(hostRoot) {
		return MergeSummary{}, fmt.Errorf("%w: host media_root must be an absolute path", errInvalidConfig)
	}
	if strings.TrimSpace(zipPath) == "" || !filepath.IsAbs(zipPath) {
		return MergeSummary{}, fmt.Errorf("%w: pack path must be an absolute path", errInvalidPack)
	}
	extractDir := filepath.Join(filepath.Dir(zipPath), ".catalog-merge-"+filepath.Base(zipPath))
	if err := os.RemoveAll(extractDir); err != nil {
		return MergeSummary{}, fmt.Errorf("prepare merge workspace: %w", err)
	}
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return MergeSummary{}, fmt.Errorf("create merge workspace: %w", err)
	}
	defer os.RemoveAll(extractDir)
	manifest, err := extractPack(zipPath, extractDir)
	if err != nil {
		return MergeSummary{}, err
	}
	host, err := mediacatalog.Open(hostRoot)
	if err != nil {
		return MergeSummary{}, fmt.Errorf("%w: open host catalog", errMergeFailed)
	}
	defer host.Close()
	remote, err := mediacatalog.Open(extractDir)
	if err != nil {
		return MergeSummary{}, fmt.Errorf("%w: open pack catalog", errInvalidPack)
	}
	defer remote.Close()
	if err := rejectIdentityConflict(ctx, host, manifest); err != nil {
		return MergeSummary{}, err
	}
	sources, err := remote.ListSources(ctx)
	if err != nil {
		return MergeSummary{}, err
	}
	summary := MergeSummary{Warnings: []string{}}
	for _, source := range sources {
		imported, skipped, shots, frames, warn, err := mergeSource(ctx, host, remote, extractDir, hostRoot, source)
		if err != nil {
			return summary, err
		}
		if imported {
			summary.ImportedSources++
		}
		if skipped {
			summary.SkippedSources++
		}
		summary.ImportedShots += shots
		summary.CopiedKeyframes += frames
		if warn != "" {
			summary.Warnings = append(summary.Warnings, warn)
		}
	}
	return summary, nil
}

func rejectIdentityConflict(ctx context.Context, host *mediacatalog.Repository, manifest PackManifest) error {
	identity, _, _, err := inspectCatalog(ctx, host)
	if err != nil {
		return err
	}
	if identity.EmbeddingModel == "" && identity.AnalysisVersion == "" {
		return nil
	}
	if manifest.EmbeddingModel != "" && identity.EmbeddingModel != "" &&
		(manifest.EmbeddingModel != identity.EmbeddingModel || manifest.EmbeddingDimension != identity.EmbeddingDimension) {
		return fmt.Errorf("%w: embedding model or dimension does not match the host catalog", errModelMismatch)
	}
	if manifest.AnalysisVersion != "" && identity.AnalysisVersion != "" && manifest.AnalysisVersion != identity.AnalysisVersion {
		return fmt.Errorf("%w: analysis_version does not match the host catalog", errModelMismatch)
	}
	return nil
}

func mergeSource(ctx context.Context, host, remote *mediacatalog.Repository, packRoot, hostRoot string, remoteSource mediacatalog.Source) (imported, skipped bool, shots, frames int, warning string, err error) {
	if _, statErr := os.Stat(filepath.Join(hostRoot, filepath.FromSlash(remoteSource.RelativePath))); statErr != nil {
		warning = "original_missing"
	}
	remoteShots, err := remote.ShotsBySource(ctx, remoteSource.ID)
	if err != nil {
		return false, false, 0, 0, warning, err
	}
	existing, err := host.SourceBySHA256(ctx, remoteSource.SHA256)
	if err != nil && !errors.Is(err, mediacatalog.ErrSourceNotFound) {
		return false, false, 0, 0, warning, err
	}
	if err == nil {
		localShots, listErr := host.ShotsBySource(ctx, existing.ID)
		if listErr != nil {
			return false, false, 0, 0, warning, listErr
		}
		if shouldSkipSource(existing, localShots) {
			return false, true, 0, 0, warning, nil
		}
		shots, frames, err = adoptRemoteDetail(ctx, host, remote, packRoot, hostRoot, existing, remoteSource, remoteShots)
		return err == nil && (shots > 0 || frames > 0), false, shots, frames, warning, err
	}
	local := remoteSource
	local.ID = ""
	stored, _, err := host.UpsertSource(ctx, local)
	if err != nil {
		return false, false, 0, 0, warning, err
	}
	shots, frames, err = adoptRemoteDetail(ctx, host, remote, packRoot, hostRoot, stored, remoteSource, remoteShots)
	if err != nil {
		return false, false, shots, frames, warning, err
	}
	return true, false, shots, frames, warning, nil
}

func shouldSkipSource(source mediacatalog.Source, shots []mediacatalog.Shot) bool {
	if len(shots) == 0 {
		return false
	}
	if source.Kind == mediacatalog.SourceKindImage {
		for _, shot := range shots {
			if shot.AnalysisStatus == mediacatalog.AnalysisCompleted {
				return true
			}
		}
		return false
	}
	return true
}

func adoptRemoteDetail(ctx context.Context, host, remote *mediacatalog.Repository, packRoot, hostRoot string, local, remoteSource mediacatalog.Source, remoteShots []mediacatalog.Shot) (int, int, error) {
	if local.Kind != mediacatalog.SourceKindImage && remoteSource.DurationMS > 0 && remoteSource.Width > 0 && remoteSource.Height > 0 {
		if err := host.UpdateSourceProbe(ctx, local.ID, mediacatalog.Probe{
			DurationMS: remoteSource.DurationMS,
			Width:      remoteSource.Width,
			Height:     remoteSource.Height,
			FPS:        remoteSource.FPS,
		}); err != nil {
			return 0, 0, err
		}
	}
	if err := host.UpdateSourceStatus(ctx, local.ID, remoteSource.Status, remoteSource.ErrorCode); err != nil {
		return 0, 0, err
	}
	if err := copyRights(ctx, host, remote, local.ID, remoteSource.ID); err != nil {
		return 0, 0, err
	}
	importedShots := 0
	copiedFrames := 0
	for _, remoteShot := range remoteShots {
		localShot, created, err := ensureLocalShot(ctx, host, local, remoteShot)
		if err != nil {
			return importedShots, copiedFrames, err
		}
		if created {
			importedShots++
		}
		if err := copyShotPayload(ctx, host, remote, localShot.ID, remoteShot); err != nil {
			return importedShots, copiedFrames, err
		}
		n, err := copyShotKeyframes(ctx, host, remote, packRoot, hostRoot, localShot.ID, remoteShot.ID)
		if err != nil {
			return importedShots, copiedFrames, err
		}
		copiedFrames += n
	}
	if err := markMergedJobs(ctx, host, local); err != nil {
		return importedShots, copiedFrames, err
	}
	return importedShots, copiedFrames, nil
}

func ensureLocalShot(ctx context.Context, host *mediacatalog.Repository, local mediacatalog.Source, remoteShot mediacatalog.Shot) (mediacatalog.Shot, bool, error) {
	if local.Kind == mediacatalog.SourceKindImage {
		shot, err := host.EnsureImageShot(ctx, local.ID)
		return shot, false, err
	}
	existing, err := host.ShotsBySource(ctx, local.ID)
	if err != nil {
		return mediacatalog.Shot{}, false, err
	}
	for _, shot := range existing {
		if shot.Ordinal == remoteShot.Ordinal {
			return shot, false, nil
		}
	}
	remoteShot.ID = ""
	remoteShot.SourceID = local.ID
	shot, err := host.InsertShot(ctx, remoteShot)
	if err != nil {
		return mediacatalog.Shot{}, false, err
	}
	return shot, true, nil
}

func copyShotPayload(ctx context.Context, host, remote *mediacatalog.Repository, localShotID string, remoteShot mediacatalog.Shot) error {
	if remoteShot.AnalysisStatus == mediacatalog.AnalysisCompleted && strings.TrimSpace(remoteShot.Summary) != "" {
		analysis := mediacatalog.ShotAnalysis{
			Summary:     remoteShot.Summary,
			Mood:        remoteShot.Mood,
			Setting:     remoteShot.Setting,
			PeopleCount: remoteShot.PeopleCount,
			MotionLevel: remoteShot.MotionLevel,
			HasText:     remoteShot.HasText,
		}
		tags, err := remote.TagsByShot(ctx, remoteShot.ID)
		if err != nil {
			return err
		}
		analysis.Tags = make([]mediacatalog.TagScore, 0, len(tags))
		copied := make([]mediacatalog.Tag, 0, len(tags))
		for _, tag := range tags {
			analysis.Tags = append(analysis.Tags, mediacatalog.TagScore{Value: tag.Value, Confidence: tag.Confidence})
			tag.ShotID = localShotID
			copied = append(copied, tag)
		}
		if err := host.SetShotAnalysis(ctx, localShotID, analysis, remoteShot.AnalysisVersion); err != nil {
			return err
		}
		if err := host.UpsertTags(ctx, localShotID, copied); err != nil {
			return err
		}
	}
	embedding, err := remote.ShotEmbedding(ctx, remoteShot.ID)
	if err != nil {
		return err
	}
	if embedding.Dimension == 0 || embedding.Model == "" {
		return nil
	}
	return host.SetShotEmbedding(ctx, localShotID, embedding.Model, embedding.Vector, embedding.AnalysisVersion)
}

func copyShotKeyframes(ctx context.Context, host, remote *mediacatalog.Repository, packRoot, hostRoot, localShotID, remoteShotID string) (int, error) {
	existing, err := host.KeyframesByShot(ctx, localShotID)
	if err != nil {
		return 0, err
	}
	if len(existing) > 0 {
		return 0, nil
	}
	frames, err := remote.KeyframesByShot(ctx, remoteShotID)
	if err != nil {
		return 0, err
	}
	copied := 0
	for _, frame := range frames {
		src, err := containedPath(packRoot, frame.RelativePath)
		if err != nil {
			return copied, err
		}
		dest, err := containedPath(hostRoot, frame.RelativePath)
		if err != nil {
			return copied, err
		}
		if err := copyFile(src, dest); err != nil {
			return copied, fmt.Errorf("copy keyframe: %w", err)
		}
		frame.ID = ""
		frame.ShotID = localShotID
		if _, err := host.InsertKeyframe(ctx, frame); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, nil
}

func copyRights(ctx context.Context, host, remote *mediacatalog.Repository, localID, remoteID string) error {
	rights, err := remote.RightsBySource(ctx, remoteID)
	if errors.Is(err, mediacatalog.ErrRightsNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	rights.SourceID = localID
	return host.UpsertRights(ctx, rights)
}

func markMergedJobs(ctx context.Context, host *mediacatalog.Repository, local mediacatalog.Source) error {
	if err := completePhase(ctx, host, local.ID, mediacatalog.PhaseIngest); err != nil {
		return err
	}
	if local.Kind == mediacatalog.SourceKindImage {
		return completePhase(ctx, host, local.ID, mediacatalog.PhaseImageShot)
	}
	return completePhase(ctx, host, local.ID, mediacatalog.PhaseProbe)
}

func completePhase(ctx context.Context, repo *mediacatalog.Repository, sourceID, phase string) error {
	job, err := repo.EnsureJob(ctx, sourceID, phase, 1)
	if err != nil {
		return err
	}
	if job.Status == mediacatalog.JobCompleted {
		return nil
	}
	return repo.CompleteJob(ctx, job.ID, 1)
}
