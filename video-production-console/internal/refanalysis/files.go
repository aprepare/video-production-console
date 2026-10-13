package refanalysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"video-production-console/internal/store"
)

var ErrFrameNotFound = errors.New("关键帧不存在")

// MediaLocator 返回已缓存的本地视频路径；尚未缓存时返回空字符串。
type MediaLocator interface {
	LocalVideo(ctx context.Context, referenceID string) (string, error)
}

type FileMediaLocator struct{ Runtime RuntimeProvider }

func (l FileMediaLocator) LocalVideo(ctx context.Context, referenceID string) (string, error) {
	if !validID(referenceID) {
		return "", ErrInvalidInput
	}
	if l.Runtime == nil {
		return "", ErrNotConfigured
	}
	rt, err := l.Runtime.Runtime(ctx)
	if err != nil {
		return "", errors.New("无法读取视频缓存设置")
	}
	path, err := checkedPath(rt.DataRoot, referenceID, "source.mp4")
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", errors.New("无法读取原视频缓存")
	}
	if !info.Mode().IsRegular() {
		return "", nil
	}
	return path, nil
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func validID(value string) bool { return idPattern.MatchString(value) }

// checkedPath 同时检查词法路径和真实路径，写入前也检查尚未创建的祖先。
// DataRoot 可有平台别名；references 以下不接受重定向目录或文件链接。
func checkedPath(dataRoot string, parts ...string) (string, error) {
	if !filepath.IsAbs(dataRoot) {
		return "", ErrUnsafePath
	}
	dataRoot, err := resolveExistingPath(dataRoot)
	if err != nil {
		return "", ErrUnsafePath
	}
	root := filepath.Join(dataRoot, "references")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, `/\:`) {
			return "", ErrUnsafePath
		}
	}
	path := filepath.Join(append([]string{root}, parts...)...)
	resolved, err := resolveExistingPath(path)
	if err != nil {
		return "", ErrUnsafePath
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == "." || !filepath.IsLocal(relative) {
		return "", ErrUnsafePath
	}
	same := filepath.Clean(path) == filepath.Clean(resolved)
	if filepath.Separator == '\\' {
		same = strings.EqualFold(filepath.Clean(path), filepath.Clean(resolved))
	}
	if !same {
		return "", ErrUnsafePath
	}
	return resolved, nil
}

func resolveExistingPath(path string) (string, error) {
	var tail []string
	for {
		_, err := os.Lstat(path)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return "", err
			}
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		tail = append(tail, filepath.Base(path))
		path = parent
	}
}

func analysisPath(dataRoot string, a store.ReferenceAnalysis, filename string) (string, error) {
	if !validID(a.ReferenceID) || !validID(a.ID) {
		return "", ErrUnsafePath
	}
	parts := []string{a.ReferenceID, "analysis", a.ID}
	if filename != "" {
		parts = append(parts, filename)
	}
	return checkedPath(dataRoot, parts...)
}

func validFrames(frames []Frame, count int) bool {
	if count < 1 || count > maxFrames || len(frames) != count {
		return false
	}
	for i, f := range frames {
		if f.Index != i+1 || f.Shot < 1 || f.TimeS < 0 || math.IsNaN(f.TimeS) || math.IsInf(f.TimeS, 0) {
			return false
		}
		if i > 0 && frames[i-1].TimeS >= f.TimeS {
			return false
		}
	}
	return true
}

func loadFrames(root string, a store.ReferenceAnalysis) ([]Frame, error) {
	path, err := analysisPath(root, a, "frames.json")
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("关键帧清单读取失败")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return nil, errors.New("关键帧清单读取失败")
	}
	var frames []Frame
	if json.Unmarshal(raw, &frames) != nil || !validFrames(frames, a.FrameCount) {
		return nil, errors.New("关键帧清单格式无效")
	}
	for i := range frames {
		frames[i].URL = fmt.Sprintf("/api/reference-analyses/%s/frames/%d", a.ID, frames[i].Index)
	}
	return frames, nil
}

func removeAnalysisDirectory(root string, a store.ReferenceAnalysis) error {
	directory, err := analysisPath(root, a, "")
	if err != nil {
		return err
	}
	if err := os.RemoveAll(directory); err != nil {
		return errors.New("分析帧目录删除失败")
	}
	return nil
}
