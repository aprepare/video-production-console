package replicate

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	_ "golang.org/x/image/webp"
	"video-production-console/internal/imageproject"
	"video-production-console/internal/store"
)

const MaxImageBytes int64 = 20 << 20
const maxVideoBytes int64 = 300 << 20

func imageExt(data []byte) (string, error) {
	if int64(len(data)) > MaxImageBytes {
		return "", &Error{"invalid_replicate", "图片不能超过20MB"}
	}
	cfg, format, e := image.DecodeConfig(bytes.NewReader(data))
	if e != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 100000000 {
		return "", invalid("图片无效或尺寸过大")
	}
	switch format {
	case "png":
		return ".png", nil
	case "jpeg":
		return ".jpg", nil
	case "webp":
		return ".webp", nil
	}
	return "", invalid("仅支持PNG、JPG、WebP图片")
}
func (s *Service) open(path string) (*os.File, error) {
	if path == "" || filepath.IsAbs(path) {
		return nil, invalid("文件路径无效")
	}
	parts := strings.FieldsFunc(filepath.ToSlash(path), func(r rune) bool { return r == '/' })
	for i, part := range parts {
		if part == "." || part == ".." {
			return nil, invalid("文件路径无效")
		}
		info, e := s.root.Lstat(filepath.Join(parts[:i+1]...))
		if e != nil {
			return nil, &Error{"replicate_not_found", "文件尚未生成或已删除"}
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, invalid("文件路径不能是符号链接")
		}
	}
	f, e := s.root.Open(path)
	if e != nil {
		return nil, &Error{"replicate_not_found", "文件尚未生成或已删除"}
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, invalid("不是有效文件")
	}
	return f, nil
}
func (s *Service) write(path string, data []byte) error {
	if e := s.root.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	tmp := path + "." + uuid.NewString() + ".tmp"
	f, e := s.root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(data)
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e == nil {
		e = s.root.Rename(tmp, path)
	}
	if e != nil {
		_ = s.root.Remove(tmp)
	}
	return e
}
func (s *Service) saveFrame(ctx context.Context, sh store.ReplicateShot, data []byte, source string) error {
	ext, e := imageExt(data)
	if e != nil {
		return e
	}
	path := filepath.Join(sh.ProjectID, "shots", sh.ID, "first"+ext)
	if e = s.write(path, data); e != nil {
		return e
	}
	old := sh.FirstFramePath
	sh.FirstFramePath = path
	sh.FirstFrameSource = source
	sh.FirstFrameStatus = "ready"
	sh.FirstFrameError = ""
	if e = s.repo.SaveShot(ctx, sh); e != nil {
		return e
	}
	if old != "" && old != path {
		_ = s.root.Remove(old)
	}
	return nil
}
func (s *Service) UploadFirstFrame(ctx context.Context, id, filename string, reader io.Reader) (Project, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".webp" {
		return Project{}, invalid("仅支持PNG、JPG、WebP图片")
	}
	data, e := io.ReadAll(io.LimitReader(reader, MaxImageBytes+1))
	if e != nil {
		return Project{}, invalid("图片读取失败")
	}
	actual, e := imageExt(data)
	if e != nil {
		return Project{}, e
	}
	if ext == ".jpeg" {
		ext = ".jpg"
	}
	if actual != ext {
		return Project{}, invalid("图片格式与文件扩展名不一致")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, e := s.repo.GetShot(ctx, id)
	if e != nil {
		return Project{}, notFound(e)
	}
	if _, e = s.project(ctx, sh.ProjectID); e != nil {
		return Project{}, e
	}
	if s.jobs["image:"+id] != nil {
		return Project{}, invalid("首帧正在生成，请稍后替换")
	}
	if e = s.saveFrame(ctx, sh, data, "uploaded"); e != nil {
		return Project{}, e
	}
	return s.get(ctx, sh.ProjectID)
}
func (s *Service) UseReferenceFrame(ctx context.Context, id string, index int) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, e := s.repo.GetShot(ctx, id)
	if e != nil {
		return Project{}, notFound(e)
	}
	p, e := s.project(ctx, sh.ProjectID)
	if e != nil {
		return Project{}, e
	}
	if index < 1 || p.AnalysisID == "" || s.opt.ReferenceFrame == nil {
		return Project{}, invalid("没有可用的拆解关键帧")
	}
	if s.jobs["image:"+id] != nil {
		return Project{}, invalid("首帧正在生成，请稍后替换")
	}
	f, e := s.opt.ReferenceFrame(ctx, p.AnalysisID, index)
	if e != nil {
		return Project{}, invalid("拆解关键帧不存在")
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, MaxImageBytes+1))
	if e != nil {
		return Project{}, invalid("关键帧读取失败")
	}
	if e = s.saveFrame(ctx, sh, data, "reference_frame"); e != nil {
		return Project{}, e
	}
	return s.get(ctx, p.ID)
}
func (s *Service) OpenFirstFrame(ctx context.Context, id string) (*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, e := s.repo.GetShot(ctx, id)
	if e != nil {
		return nil, notFound(e)
	}
	if !validID(sh.ProjectID) || !validID(sh.ID) {
		return nil, invalid("文件路径无效")
	}
	expected := filepath.Join(sh.ProjectID, "shots", sh.ID, "first")
	if sh.FirstFramePath != expected+".png" && sh.FirstFramePath != expected+".jpg" && sh.FirstFramePath != expected+".webp" {
		return nil, &Error{"replicate_not_found", "首帧尚未生成"}
	}
	return s.open(sh.FirstFramePath)
}
func (s *Service) OpenTake(ctx context.Context, id string) (*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, e := s.repo.GetTake(ctx, id)
	if e != nil {
		return nil, notFound(e)
	}
	sh, e := s.repo.GetShot(ctx, t.ShotID)
	if e != nil {
		return nil, notFound(e)
	}
	if t.Status != "succeeded" || !validID(id) || !validID(sh.ProjectID) || t.Path != filepath.Join(sh.ProjectID, "takes", id+".mp4") {
		return nil, &Error{"replicate_not_found", "视频尚未生成"}
	}
	return s.open(t.Path)
}
func (s *Service) OpenFinal(ctx context.Context, id string) (*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.repo.Get(ctx, id)
	if e != nil {
		return nil, notFound(e)
	}
	if p.Status != "stitched" || !validID(id) || p.FinalPath != filepath.Join(id, "final.mp4") {
		return nil, &Error{"replicate_not_found", "成片尚未拼接"}
	}
	return s.open(p.FinalPath)
}
func (s *Service) GenerateFirstFrames(ctx context.Context, id string, ids []string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.project(ctx, id)
	if e != nil {
		return Project{}, e
	}
	shots, e := s.selectedShots(p, ids)
	if e != nil {
		return Project{}, e
	}
	rt, e := s.runtime.Runtime(ctx)
	if e != nil {
		return Project{}, e
	}
	if rt.ImageAPIKey == "" || rt.ImageBaseURL == "" {
		return Project{}, invalid("还没配置生图服务，请到设置填写")
	}
	for _, sh := range shots {
		if s.jobs["image:"+sh.ID] != nil {
			return Project{}, invalid("首帧正在生成，请勿重复提交")
		}
		if strings.TrimSpace(sh.FirstFramePrompt) == "" {
			return Project{}, invalid(fmt.Sprintf("镜头%d缺少首帧提示词", sh.Position))
		}
	}
	for _, sh := range shots {
		sh.FirstFrameStatus = "generating"
		sh.FirstFrameError = ""
		if e = s.repo.SaveShot(ctx, sh); e != nil {
			return Project{}, e
		}
		sh := sh
		s.launch("image:"+sh.ID, p.ID, sh.ID, "image", func(ctx context.Context) error { return s.runImage(ctx, p, sh) })
	}
	return s.get(ctx, id)
}
func (s *Service) imageSlot(ctx context.Context) (func(), error) {
	for {
		rt, e := s.runtime.Runtime(ctx)
		if e != nil {
			return nil, e
		}
		limit := max(1, min(6, rt.MaxImageConcurrency))
		s.mu.Lock()
		if s.imageActive < limit {
			s.imageActive++
			s.mu.Unlock()
			return func() { s.mu.Lock(); s.imageActive--; close(s.changed); s.changed = make(chan struct{}); s.mu.Unlock() }, nil
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}
func (s *Service) runImage(ctx context.Context, p store.ReplicateProject, sh store.ReplicateShot) error {
	release, e := s.imageSlot(ctx)
	if e != nil {
		return e
	}
	defer release()
	rt, e := s.runtime.Runtime(ctx)
	if e != nil {
		return e
	}
	model := p.ImageModel
	if model == "" {
		model = rt.ImageModel
	}
	if model == "" {
		model = "grok-imagine-image-2.0"
	}
	prompt := sh.FirstFramePrompt + "。画面风格：" + p.StyleLock + "。画面比例：" + p.Ratio + "。不要出现文字、水印"
	if sh.Keep != "" {
		prompt += "。商品外观保持：" + sh.Keep
	}
	results, e := s.opt.Images.GenerateMany(ctx, imageproject.GenerateRequest{BaseURL: rt.ImageBaseURL, APIKey: rt.ImageAPIKey, Model: model, Prompt: prompt, Ratio: p.Ratio, Count: 1, Stream: rt.ImageStream})
	if e != nil {
		return invalid("首帧生成失败：" + imageproject.ImageErrorMessage(e, model))
	}
	if len(results) != 1 || results[0].Error != nil {
		return invalid("首帧生成没有返回有效图片")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e = ctx.Err(); e != nil {
		return e
	}
	fresh, e := s.repo.GetShot(ctx, sh.ID)
	if e != nil {
		return e
	}
	return s.saveFrame(ctx, fresh, results[0].Bytes, "generated")
}
func (s *Service) readImage(sh store.ReplicateShot) ([]byte, string, error) {
	f, e := s.open(sh.FirstFramePath)
	if e != nil {
		return nil, "", e
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, MaxImageBytes+1))
	if e != nil {
		return nil, "", e
	}
	if _, e = imageExt(data); e != nil {
		return nil, "", e
	}
	return data, http.DetectContentType(data), nil
}
