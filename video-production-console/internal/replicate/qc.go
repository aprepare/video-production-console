package replicate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"video-production-console/internal/videoqc"
)

func (s *Service) StartQC(ctx context.Context, id, model string) (videoqc.Check, error) {
	s.mu.Lock()
	p, err := s.repo.Get(ctx, id)
	if err == nil {
		err = s.available(id)
	}
	qc := s.opt.QC
	s.mu.Unlock()
	if err != nil {
		return videoqc.Check{}, notFound(err)
	}
	if p.Status != "stitched" {
		return videoqc.Check{}, invalid("请先拼接成片")
	}
	if qc == nil {
		return videoqc.Check{}, invalid("视频质检服务尚未配置")
	}
	check, err := qc.Start(ctx, videoqc.Input{Source: "replicate", SourceID: id, Model: model})
	if errors.Is(err, videoqc.ErrNotConfigured) {
		return check, invalid("还没配置看图模型，请到设置里配置")
	}
	if errors.Is(err, videoqc.ErrInvalidInput) {
		return check, invalid("质检参数不正确")
	}
	if errors.Is(err, videoqc.ErrMediaNotReady) {
		return check, invalid("成片尚未就绪或已变化，请重新拼接")
	}
	return check, err
}
func (s *Service) QCSource(ctx context.Context, source, id string) (videoqc.ReplicateMedia, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if source == "replicate_take" {
		t, e := s.repo.GetTake(ctx, id)
		if e != nil {
			return videoqc.ReplicateMedia{}, notFound(e)
		}
		sh, e := s.repo.GetShot(ctx, t.ShotID)
		if e != nil {
			return videoqc.ReplicateMedia{}, e
		}
		p, e := s.repo.Get(ctx, sh.ProjectID)
		if e != nil {
			return videoqc.ReplicateMedia{}, e
		}
		if t.Status != "succeeded" {
			return videoqc.ReplicateMedia{}, invalid("版本尚未生成")
		}
		f, e := s.open(t.Path)
		return videoqc.ReplicateMedia{File: f, Title: p.Title, Product: p.Product}, e
	}
	if source != "replicate" {
		return videoqc.ReplicateMedia{}, invalid("质检来源无效")
	}
	p, e := s.repo.Get(ctx, id)
	if e != nil {
		return videoqc.ReplicateMedia{}, notFound(e)
	}
	if p.Status != "stitched" {
		return videoqc.ReplicateMedia{}, invalid("请先拼接成片")
	}
	manifest, e := s.open(filepath.Join(id, "timeline.json"))
	if e != nil {
		return videoqc.ReplicateMedia{}, e
	}
	raw, e := io.ReadAll(io.LimitReader(manifest, 1<<20))
	manifest.Close()
	if e != nil {
		return videoqc.ReplicateMedia{}, e
	}
	var spans []ShotSpan
	if json.Unmarshal(raw, &spans) != nil || len(spans) == 0 {
		return videoqc.ReplicateMedia{}, invalid("成片镜头清单无效")
	}
	ranges := []videoqc.ShotRange{}
	for _, span := range spans {
		ranges = append(ranges, videoqc.ShotRange{Position: span.Position, StartS: span.StartS, EndS: span.EndS})
	}
	f, e := s.open(p.FinalPath)
	if e != nil {
		return videoqc.ReplicateMedia{}, e
	}
	stamp := p.UpdatedAt
	return videoqc.ReplicateMedia{File: f, Title: p.Title, Product: p.Product, Spans: ranges, RecordQC: func(ctx context.Context, qcid string) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		p, e := s.repo.Get(ctx, id)
		if e != nil {
			return e
		}
		if p.Status != "stitched" || !p.UpdatedAt.Equal(stamp) {
			return invalid("成片已变化，请重新提交质检")
		}
		p.QCID = qcid
		return s.repo.SaveProject(ctx, p)
	}}, nil
}
