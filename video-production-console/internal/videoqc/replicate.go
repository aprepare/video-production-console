package videoqc

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"video-production-console/internal/store"
)

type ShotRange struct {
	Position     int
	StartS, EndS float64
}
type ReplicateMedia struct {
	File     *os.File
	Title    string
	Product  string
	Spans    []ShotRange
	RecordQC func(context.Context, string) error
}

func shotAt(spans []ShotRange, t float64) int {
	for i, s := range spans {
		if t >= s.StartS && (t < s.EndS || i == len(spans)-1 && t <= s.EndS) {
			return s.Position
		}
	}
	return 0
}
func (s *Service) startReplicate(ctx context.Context, in Input) (Check, error) {
	if !validID(in.SourceID) || s.options.ReplicateSource == nil {
		return Check{}, ErrInvalidInput
	}
	ctx, done, e := s.begin(ctx)
	if e != nil {
		return Check{}, e
	}
	defer done()
	rt, cfg, e := s.prepare(ctx, in)
	if e != nil {
		return Check{}, e
	}
	media, e := s.options.ReplicateSource(ctx, in.Source, in.SourceID)
	if e != nil {
		return Check{}, ErrMediaNotReady
	}
	if media.File == nil {
		return Check{}, ErrMediaNotReady
	}
	defer media.File.Close()
	if in.Product == "" {
		in.Product = media.Product
	}
	if e = validateInput(in); e != nil {
		return Check{}, e
	}
	root, e := openFiles(rt.DataRoot, true)
	if e != nil {
		return Check{}, e
	}
	defer root.Close()
	id := uuid.NewString()
	if e = root.Mkdir(id, 0700); e != nil {
		return Check{}, ErrUnsafePath
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = root.RemoveAll(id)
		}
	}()
	rel := filepath.Join(id, "source.mp4")
	f, e := root.OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return Check{}, ErrUnsafePath
	}
	n, e := io.Copy(f, io.LimitReader(media.File, MaxUploadBytes+1))
	ce := f.Close()
	if e != nil || ce != nil {
		return Check{}, errors.New("审核视频复制失败")
	}
	if n > MaxUploadBytes {
		return Check{}, ErrTooLarge
	}
	if n == 0 {
		return Check{}, ErrMediaNotReady
	}
	if e = ctx.Err(); e != nil {
		return Check{}, e
	}
	a, e := s.enqueue(ctx, store.VideoQCCheck{ID: id, Source: in.Source, SourceID: in.SourceID, Title: sanitizeTitle(media.Title), Product: in.Product, Model: cfg.Model}, rt, cfg, filepath.Join(root.Name(), rel), append([]ShotRange(nil), media.Spans...))
	if e != nil {
		return Check{}, e
	}
	accepted = true
	if media.RecordQC != nil {
		if e = media.RecordQC(ctx, id); e != nil {
			_ = s.Delete(context.Background(), id)
			return Check{}, e
		}
	}
	return a, nil
}
