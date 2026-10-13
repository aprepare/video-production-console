package montageplan

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const cityMotionThreshold = 1.5 / 255

// Review only the source windows actually visible during the first ten seconds.
func reviewCityOpening(ctx context.Context, ffmpeg string, plan *CityLibraryReservation) (CityOpeningMotionReview, error) {
	if strings.TrimSpace(ffmpeg) == "" {
		ffmpeg = "ffmpeg"
	}
	review := CityOpeningMotionReview{Passed: true, CheckedUntil: math.Min(10, plan.Duration), Evidence: []CityOpeningMotionEvidence{}}
	for _, c := range plan.Clips {
		if c.TimelineIn >= review.CheckedUntil {
			break
		}
		e := CityOpeningMotionEvidence{ProjectID: plan.ProjectID, AssetID: c.AssetID, Path: c.Path}
		span := math.Min(c.Duration, review.CheckedUntil-c.TimelineIn)
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		var frames [][]byte
		var probeErr error
		for _, fraction := range []float64{.1, .5, .9} {
			at := c.SourceIn + span*fraction
			e.SampleTimes = append(e.SampleTimes, at)
			cmd := exec.CommandContext(probeCtx, ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-ss", strconv.FormatFloat(at, 'f', 6, 64), "-i", c.Path, "-an", "-sn", "-frames:v", "1", "-vf", "scale=64:36,format=gray", "-f", "rawvideo", "pipe:1")
			cmd.WaitDelay = 5 * time.Second
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			frame, err := cmd.Output()
			if err != nil {
				probeErr = fmt.Errorf("抽帧 %.6fs: %w (%s)", at, err, strings.TrimSpace(stderr.String()))
				break
			}
			if len(frame) != 64*36 {
				probeErr = fmt.Errorf("抽帧 %.6fs 得到 %d 字节，预期 2304", at, len(frame))
				break
			}
			frames = append(frames, frame)
		}
		cancel()
		if probeErr != nil {
			e.Error = probeErr.Error()
			review.Passed = false
			review.Evidence = append(review.Evidence, e)
			return review, fmt.Errorf("开头运动检测失败（素材 %s）: %w", c.AssetID, probeErr)
		}
		for i := 1; i < len(frames); i++ {
			difference := 0.0
			for j, pixel := range frames[i] {
				difference += math.Abs(float64(pixel) - float64(frames[i-1][j]))
			}
			e.MeanPixelDifferences = append(e.MeanPixelDifferences, difference/float64(len(frames[i]))/255)
		}
		e.Static = e.MeanPixelDifferences[0] < cityMotionThreshold && e.MeanPixelDifferences[1] < cityMotionThreshold
		if e.Static {
			review.Passed = false
		}
		review.Evidence = append(review.Evidence, e)
	}
	if len(review.Evidence) == 0 {
		return review, fmt.Errorf("开头运动检测没有可检查的镜头")
	}
	return review, nil
}
