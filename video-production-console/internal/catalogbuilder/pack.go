package catalogbuilder

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"video-production-console/internal/mediacatalog"
)

const (
	packFormatName     = "catalog-pack-v1"
	packManifestName   = "pack-manifest.json"
	packCatalogName    = "catalog.db"
	packKeyframePrefix = "derived/keyframes/"
)

// PackManifest is the only metadata file inside a catalog pack.
type PackManifest struct {
	Format             string `json:"format"`
	AnalysisVersion    string `json:"analysis_version"`
	VisionModel        string `json:"vision_model,omitempty"`
	EmbeddingModel     string `json:"embedding_model,omitempty"`
	EmbeddingDimension int    `json:"embedding_dimension,omitempty"`
	SourceCount        int    `json:"source_count"`
	ShotCount          int    `json:"shot_count"`
	CreatedAt          string `json:"created_at"`
}

type catalogIdentity struct {
	AnalysisVersion    string
	EmbeddingModel     string
	EmbeddingDimension int
}

func ExportPack(ctx context.Context, mediaRoot, zipPath string, visionModel string) error {
	if strings.TrimSpace(zipPath) == "" || !filepath.IsAbs(zipPath) {
		return fmt.Errorf("%w: pack path must be absolute", errInvalidPack)
	}
	repo, err := mediacatalog.Open(mediaRoot)
	if err != nil {
		return fmt.Errorf("%w: open catalog", errInvalidPack)
	}
	defer repo.Close()
	identity, sources, shots, err := inspectCatalog(ctx, repo)
	if err != nil {
		return err
	}
	work := filepath.Join(filepath.Dir(zipPath), ".catalog-pack-"+filepath.Base(zipPath))
	if err := os.RemoveAll(work); err != nil {
		return fmt.Errorf("prepare pack workspace: %w", err)
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return fmt.Errorf("create pack workspace: %w", err)
	}
	defer os.RemoveAll(work)
	if err := repo.VacuumInto(filepath.Join(work, packCatalogName)); err != nil {
		return err
	}
	if err := copyKeyframeTree(repo.Root(), work); err != nil {
		return err
	}
	manifest := PackManifest{
		Format:             packFormatName,
		AnalysisVersion:    identity.AnalysisVersion,
		VisionModel:        strings.TrimSpace(visionModel),
		EmbeddingModel:     identity.EmbeddingModel,
		EmbeddingDimension: identity.EmbeddingDimension,
		SourceCount:        sources,
		ShotCount:          shots,
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
	}
	if manifest.AnalysisVersion == "" {
		manifest.AnalysisVersion = mediacatalog.AnalysisVersion
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pack manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(work, packManifestName), append(payload, '\n'), 0o600); err != nil {
		return fmt.Errorf("write pack manifest: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(zipPath), 0o755); err != nil {
		return fmt.Errorf("create pack directory: %w", err)
	}
	return zipDir(work, zipPath)
}

func inspectCatalog(ctx context.Context, repo *mediacatalog.Repository) (catalogIdentity, int, int, error) {
	sources, err := repo.ListSources(ctx)
	if err != nil {
		return catalogIdentity{}, 0, 0, err
	}
	identity := catalogIdentity{}
	shots := 0
	for _, source := range sources {
		sourceShots, err := repo.ShotsBySource(ctx, source.ID)
		if err != nil {
			return catalogIdentity{}, 0, 0, err
		}
		shots += len(sourceShots)
		for _, shot := range sourceShots {
			if shot.AnalysisVersion != "" && identity.AnalysisVersion == "" {
				identity.AnalysisVersion = shot.AnalysisVersion
			}
			embedding, err := repo.ShotEmbedding(ctx, shot.ID)
			if err != nil {
				return catalogIdentity{}, 0, 0, err
			}
			if embedding.Dimension == 0 || embedding.Model == "" {
				continue
			}
			if identity.EmbeddingModel == "" {
				identity.EmbeddingModel = embedding.Model
				identity.EmbeddingDimension = embedding.Dimension
				if identity.AnalysisVersion == "" {
					identity.AnalysisVersion = embedding.AnalysisVersion
				}
				continue
			}
			if embedding.Model != identity.EmbeddingModel || embedding.Dimension != identity.EmbeddingDimension {
				return catalogIdentity{}, 0, 0, fmt.Errorf("%w: catalog mixes embedding models", errModelMismatch)
			}
		}
	}
	return identity, len(sources), shots, nil
}

func copyKeyframeTree(srcRoot, destRoot string) error {
	src := filepath.Join(srcRoot, "derived", "keyframes")
	info, err := os.Stat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.IsDir() {
		return fmt.Errorf("inspect keyframes: %w", err)
	}
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(destRoot, filepath.FromSlash(rel)), 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if err := validatePackName(rel); err != nil {
			return err
		}
		return copyFile(path, filepath.Join(destRoot, filepath.FromSlash(rel)))
	})
}

func zipDir(root, zipPath string) error {
	file, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("create pack zip: %w", err)
	}
	defer file.Close()
	writer := zip.NewWriter(file)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if err := validatePackName(name); err != nil {
			return err
		}
		entryWriter, err := writer.Create(name)
		if err != nil {
			return err
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(entryWriter, source)
		_ = source.Close()
		return copyErr
	})
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}
	return err
}

func extractPack(zipPath, dest string) (PackManifest, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return PackManifest{}, fmt.Errorf("%w: open pack zip", errInvalidPack)
	}
	defer reader.Close()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return PackManifest{}, fmt.Errorf("create pack extract directory: %w", err)
	}
	var manifest PackManifest
	foundManifest := false
	foundCatalog := false
	for _, file := range reader.File {
		name := filepath.ToSlash(file.Name)
		if strings.HasSuffix(name, "/") {
			if err := validatePackName(strings.TrimSuffix(name, "/")); err != nil && name != "derived/" && name != "derived/keyframes/" {
				return PackManifest{}, err
			}
			continue
		}
		if err := validatePackName(name); err != nil {
			return PackManifest{}, err
		}
		if file.Mode()&os.ModeSymlink != 0 {
			return PackManifest{}, fmt.Errorf("%w: pack entry %q is a symlink", errInvalidPack, name)
		}
		target, err := containedPath(dest, name)
		if err != nil {
			return PackManifest{}, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return PackManifest{}, err
		}
		if err := writeZipFile(file, target); err != nil {
			return PackManifest{}, err
		}
		switch name {
		case packManifestName:
			foundManifest = true
		case packCatalogName:
			foundCatalog = true
		}
	}
	if !foundManifest || !foundCatalog {
		return PackManifest{}, fmt.Errorf("%w: pack is missing catalog.db or pack-manifest.json", errInvalidPack)
	}
	data, err := os.ReadFile(filepath.Join(dest, packManifestName))
	if err != nil {
		return PackManifest{}, fmt.Errorf("%w: read pack manifest", errInvalidPack)
	}
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Format != packFormatName {
		return PackManifest{}, fmt.Errorf("%w: pack manifest is not %s", errInvalidPack, packFormatName)
	}
	return manifest, nil
}

func writeZipFile(file *zip.File, dest string) error {
	source, err := file.Open()
	if err != nil {
		return fmt.Errorf("open pack entry: %w", err)
	}
	defer source.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create pack entry: %w", err)
	}
	_, copyErr := io.Copy(out, source)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func validatePackName(name string) error {
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return fmt.Errorf("%w: pack entry %q is not a safe relative path", errInvalidPack, name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%w: pack entry %q is not a safe relative path", errInvalidPack, name)
		}
	}
	if strings.HasPrefix(name, "originals/") {
		return fmt.Errorf("%w: pack must not contain originals", errInvalidPack)
	}
	switch {
	case name == packManifestName, name == packCatalogName:
		return nil
	case name == "derived" || name == "derived/keyframes" || strings.HasPrefix(name, packKeyframePrefix):
		return nil
	default:
		return fmt.Errorf("%w: pack entry %q is not allowed", errInvalidPack, name)
	}
}

func containedPath(root, relative string) (string, error) {
	if err := mediacatalog.ValidateRelativePath(relative); err != nil {
		return "", fmt.Errorf("%w: %v", errInvalidPack, err)
	}
	dest := filepath.Clean(filepath.Join(root, filepath.FromSlash(relative)))
	rel, err := filepath.Rel(root, dest)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: pack entry escapes the extract directory", errInvalidPack)
	}
	return dest, nil
}

func copyFile(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
