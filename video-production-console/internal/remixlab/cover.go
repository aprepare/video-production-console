package remixlab

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"video-production-console/internal/cover"
	"video-production-console/internal/store"
)

type CoverView struct {
	Path    string `json:"path"`
	URL     string `json:"url"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Account string `json:"account"`
}

func (s *Service) GenerateCover(ctx context.Context, runID, line1, line2 string) (CoverView, error) {
	var titles [2]string
	for i, title := range []string{line1, line2} {
		var err error
		titles[i], err = cover.NormalizeTitle(title)
		if err != nil {
			return CoverView{}, err
		}
	}
	// 与手工编辑发布包串行，避免生成封面时覆盖刚保存的正文。
	s.coverMu.Lock()
	defer s.coverMu.Unlock()
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return CoverView{}, err
	}
	if run.Status != "completed" || strings.TrimSpace(run.OutputDir) == "" {
		return CoverView{}, ErrRunNotReworkable
	}
	exp, _, _, err := s.repo.GetExperiment(ctx, run.ExperimentID)
	if err != nil {
		return CoverView{}, err
	}
	accountID := exp.ProduceAccountID
	production, err := s.repo.GetProduction(ctx, run.ID)
	if err == nil && strings.TrimSpace(production.AccountID) != "" {
		accountID = production.AccountID
	} else if err != nil && !errors.Is(err, store.ErrRemixLabNotFound) {
		return CoverView{}, err
	}
	rt, err := s.runtime.Runtime(ctx)
	if err != nil {
		return CoverView{}, err
	}
	accountName, err := s.repo.AccountName(ctx, accountID)
	if err != nil {
		return CoverView{}, err
	}
	style, err := cover.ResolveAccountStyle(rt.CoverSkillRoot, accountID, accountName)
	if err != nil {
		return CoverView{}, err
	}
	output := filepath.Join(run.OutputDir, "cover.png")
	result, err := cover.Render(cover.RenderRequest{
		ReferencePath: style.ReferencePath, Titles: titles, Colors: style.Colors,
		SeparatorLines: style.SeparatorLines, TitleBox: style.TitleBox, OutputPath: output,
	})
	if err != nil {
		return CoverView{}, err
	}
	libraryDir := strings.TrimSpace(rt.CoverOutputDir)
	if libraryDir == "" {
		libraryDir = cover.DefaultOutputDir()
	}
	path, err := cover.SaveToLibrary(output, libraryDir, s.now().In(time.Local).Format("20060102"), style.Account, titles[0])
	if err != nil {
		return CoverView{}, err
	}
	packageJSON, err := packageWithCover([]byte(run.PackageJSON), path, titles)
	if err != nil {
		return CoverView{}, err
	}
	// 磁盘仍保留模型正文及其他审计字段，只补封面信息；数据库保留操作员定稿。
	packagePath := filepath.Join(run.OutputDir, "publishing_package.json")
	diskJSON, err := os.ReadFile(packagePath)
	if os.IsNotExist(err) {
		diskJSON = []byte(run.PackageJSON)
	} else if err != nil {
		return CoverView{}, err
	}
	diskJSON, err = packageWithCover(diskJSON, path, titles)
	if err != nil {
		return CoverView{}, err
	}
	if err := writeCoverPackage(packagePath, diskJSON); err != nil {
		return CoverView{}, err
	}
	if err := s.repo.UpdateRunPackage(ctx, runID, run.ContinuousScript, run.TitlesJSON, string(packageJSON)); err != nil {
		return CoverView{}, err
	}
	return CoverView{Path: path, URL: "/api/remix-lab/runs/" + run.ID + "/cover", Width: result.Width, Height: result.Height, Account: style.Account}, nil
}

func packageWithCover(raw []byte, path string, titles [2]string) ([]byte, error) {
	pkg := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &pkg); err != nil {
			return nil, err
		}
	}
	if pkg == nil {
		pkg = map[string]json.RawMessage{}
	}
	pkg["cover_path"], _ = json.Marshal(path)
	pkg["cover_titles"], _ = json.Marshal(titles)
	return json.MarshalIndent(pkg, "", "  ")
}

func writeCoverPackage(path string, raw []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".cover-package-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(append(raw, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}

func (s *Service) RunCoverPath(ctx context.Context, runID string) (string, error) {
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(run.OutputDir) == "" {
		return "", os.ErrNotExist
	}
	return filepath.Join(run.OutputDir, "cover.png"), nil
}

func (s *Service) RunPackage(ctx context.Context, runID string) (json.RawMessage, error) {
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(run.PackageJSON) == "" {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(run.PackageJSON), nil
}
