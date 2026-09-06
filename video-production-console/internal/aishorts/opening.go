package aishorts

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
)

// OpeningTimelinePlanner obtains real narration times before paid video submission.
type OpeningTimelinePlanner interface {
	OpeningTimeline(context.Context, Runtime, *Short, string) ([][2]float64, error)
}

func (a *DraftAssembler) OpeningTimeline(ctx context.Context, rt Runtime, short *Short, dir string) ([][2]float64, error) {
	nar, err := a.prepareNarration(ctx, rt, short, dir, joinNarrations(short.Shots), func(string) {})
	if err != nil {
		return nil, err
	}
	if len(nar.CharTimes) > 0 {
		return timesFromCharMap(short.Shots, nar.CharTimes, nar.Words, nar.DurationS), nil
	}
	return alignShotsToWords(short.Shots, nar.Words, nar.DurationS), nil
}

func (s *Service) prepareOpeningTimeline(ctx context.Context, rt Runtime, id string) error {
	short, err := s.store.Get(id)
	if err != nil {
		return err
	}
	if !short.IsExplainer() || short.VisualSettings == nil || short.VisualSettings.OpeningVideoSeconds == 0 {
		return nil
	}
	times := make([][2]float64, len(short.Shots))
	valid := len(times) > 0
	for i, shot := range short.Shots {
		times[i] = [2]float64{shot.StartS, shot.EndS}
		if shot.EndS <= shot.StartS || math.IsNaN(shot.EndS) || math.IsInf(shot.EndS, 0) || (i > 0 && math.Abs(shot.StartS-times[i-1][1]) > 0.05) {
			valid = false
		}
	}
	if !valid {
		planner, ok := s.assemble.(OpeningTimelinePlanner)
		if !ok {
			return errors.New("开场视频需要先取得配音时间轴")
		}
		times, err = planner.OpeningTimeline(ctx, rt, short, s.store.AssetDir(id))
		if err != nil {
			return err
		}
	}
	if len(times) != len(short.Shots) {
		return errors.New("开场配音时间轴与分镜数量不一致")
	}
	_, err = s.store.Update(id, func(x *Short) error {
		for i, t := range times {
			if t[1] <= t[0] || math.IsNaN(t[0]) || math.IsNaN(t[1]) || math.IsInf(t[1], 0) {
				return errors.New("开场配音时间轴无效")
			}
			x.Shots[i].StartS, x.Shots[i].EndS = t[0], t[1]
			x.Shots[i].Hero = x.NeedsVideo(x.Shots[i])
			if x.Shots[i].Hero {
				duration := math.Min(t[1], float64(x.VisualSettings.OpeningVideoSeconds)) - t[0]
				switch {
				case duration <= 6:
					x.Shots[i].Seconds = 6
				case duration <= 10:
					x.Shots[i].Seconds = 10
				case duration <= 15:
					x.Shots[i].Seconds = 15
				default:
					return fmt.Errorf("第%d镜超过15秒，请增加分镜后再生成开场视频", i+1)
				}
			}
		}
		return nil
	})
	return err
}

func videoModelFor(short *Short, rt Runtime) string {
	if short.VisualSettings != nil && strings.TrimSpace(short.VisualSettings.VideoModel) != "" {
		return strings.TrimSpace(short.VisualSettings.VideoModel)
	}
	return rt.Models.Video
}

func explainerJobShots(short *Short, shot Shot, timing [2]float64) []jobShot {
	item := jobShot{Image: shot.ImagePath, CameraMove: shot.CameraMove, Annotation: shot.Annotation, StartS: timing[0], EndS: timing[1], VideoVolume: 0, Speaker: SpeakerNarrator}
	if !short.NeedsVideo(shot) {
		return []jobShot{item}
	}
	item.Image, item.Video = "", shot.VideoPath
	limit := float64(short.VisualSettings.OpeningVideoSeconds)
	if item.EndS <= limit {
		return []jobShot{item}
	}
	tail := item
	tail.StartS, tail.Image, tail.Video, tail.Annotation = limit, shot.ImagePath, "", ""
	item.EndS = limit
	return []jobShot{item, tail}
}
