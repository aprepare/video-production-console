package setup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"video-production-console/internal/domain"
	"video-production-console/internal/jianyingdraft"
	"video-production-console/internal/localpath"
	"video-production-console/internal/settings"
)

var ErrInvalidDraftRoot = errors.New("该路径不在本次检测结果中，请重新检测并选择剪映草稿目录。")

type Detection struct {
	jianyingdraft.Detection
	ConfiguredRoot    string `json:"configured_root"`
	ConfiguredMatches bool   `json:"configured_matches"`
}

type ApplyResult struct {
	JianyingRoot          string `json:"jianying_root"`
	RestartRequired       bool   `json:"restart_required"`
	MachineProfileUpdated bool   `json:"machine_profile_updated"`
}

func (s *Service) Detect(ctx context.Context) (Detection, error) {
	view, err := s.settings.Get(ctx)
	if err != nil {
		return Detection{}, errors.New("配置读取失败，请稍后重试。")
	}
	detected, err := s.detect()
	if err != nil {
		return Detection{}, errors.New("剪映检测失败，请确认已安装剪映后重试。")
	}
	if detected.Candidates == nil {
		detected.Candidates = []jianyingdraft.Candidate{}
	}
	result := Detection{Detection: detected, ConfiguredRoot: view.Public.JianyingRoot}
	for _, candidate := range detected.Candidates {
		if matchingPath(result.ConfiguredRoot, candidate.DraftRoot) {
			result.ConfiguredMatches = true
			break
		}
	}
	return result, nil
}

func (s *Service) ApplyJianying(ctx context.Context, root string) (ApplyResult, error) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	detected, err := s.Detect(ctx)
	if err != nil {
		return ApplyResult{}, err
	}
	for _, candidate := range detected.Candidates {
		if matchingPath(root, candidate.DraftRoot) {
			return s.applyJianying(ctx, candidate.DraftRoot)
		}
	}
	return ApplyResult{}, ErrInvalidDraftRoot
}

// AutoDetectJianying 仅处理尚未设置目录的机器；歧义留给用户从候选中选择。
func (s *Service) AutoDetectJianying(ctx context.Context) (bool, error) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	view, err := s.settings.Get(ctx)
	if err != nil {
		return false, errors.New("配置读取失败，请稍后重试。")
	}
	if strings.TrimSpace(view.Public.JianyingRoot) != "" {
		return false, nil
	}
	detected, err := s.detect()
	if err != nil {
		return false, errors.New("剪映检测失败，请在设置中重新检测。")
	}
	root, count := "", 0
	for _, candidate := range detected.Candidates {
		if candidate.HasRootMeta {
			root = candidate.DraftRoot
			count++
		}
	}
	if count != 1 {
		return false, nil
	}
	_, err = s.applyJianying(ctx, root)
	return err == nil, err
}

func (s *Service) applyJianying(ctx context.Context, root string) (ApplyResult, error) {
	view, err := s.settings.Get(ctx)
	if err != nil {
		return ApplyResult{}, errors.New("配置读取失败，请稍后重试。")
	}
	root = resolvedPath(root)
	// 先读改并写好临时文件，避免损坏的档案导致只保存了一半配置。
	staged, err := prepareProfileUpdate(view.Public, root)
	if err != nil {
		return ApplyResult{}, err
	}
	if staged != nil {
		defer os.Remove(staged.temp)
	}
	view.Public.JianyingRoot = root
	updated, err := s.settings.Update(ctx, view.Public, nil)
	if err != nil {
		return ApplyResult{}, errors.New(settings.UserMessage(err))
	}
	result := ApplyResult{JianyingRoot: updated.Public.JianyingRoot, RestartRequired: updated.RestartRequired}
	if staged != nil {
		if err := replaceProfileFile(staged.temp, staged.path); err != nil {
			return ApplyResult{}, errors.New("剪映目录已保存，但机器档案同步失败，请检查文件权限后重新应用。")
		}
		result.MachineProfileUpdated = true
	}
	return result, nil
}

func matchingPath(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	left, right = resolvedPath(left), resolvedPath(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func resolvedPath(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if resolved, err := localpath.Resolve(path); err == nil {
		return filepath.Clean(resolved)
	}
	return path
}

type profileUpdate struct{ path, temp string }

func prepareProfileUpdate(public domain.PublicSettings, draftRoot string) (*profileUpdate, error) {
	if strings.TrimSpace(public.MachineProfilePath) == "" || strings.TrimSpace(public.DataRoot) == "" {
		return nil, nil
	}
	root, err := localpath.Resolve(public.DataRoot)
	if err != nil {
		return nil, errors.New("数据目录无法读取，请检查系统设置后重试。")
	}
	path, err := localpath.Resolve(public.MachineProfilePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("机器档案位置无法确认，请检查文件权限后重试。")
	}
	// 解析目录联接后再检查相对路径，不能把写入导向 DataRoot 之外。
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("机器档案读取失败，请检查文件权限后重试。")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("机器档案不是有效文件，请检查系统设置。")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return nil, errors.New("机器档案读取失败或过大，请检查文件后重试。")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, errors.New("机器档案格式无效，请修复文件后重新应用。")
	}
	encodedRoot, _ := json.Marshal(draftRoot)
	fields["jianying_root"] = encodedRoot
	encoded, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return nil, errors.New("机器档案无法编码，请修复文件后重试。")
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".setup-profile-*.tmp")
	if err != nil {
		return nil, errors.New("机器档案目录无法写入，请检查文件权限后重试。")
	}
	tempPath := temp.Name()
	success := false
	defer func() {
		_ = temp.Close()
		if !success {
			_ = os.Remove(tempPath)
		}
	}()
	if err = temp.Chmod(info.Mode().Perm()); err == nil {
		_, err = temp.Write(append(encoded, '\n'))
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, errors.New("机器档案临时文件写入失败，请检查磁盘和权限后重试。")
	}
	success = true
	return &profileUpdate{path: path, temp: tempPath}, nil
}
