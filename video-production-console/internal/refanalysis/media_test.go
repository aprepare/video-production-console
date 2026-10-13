package refanalysis

import (
	"context"
	"image/jpeg"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestShowinfoParsingAndAdjacentCutMerging(t *testing.T) {
	stderr := `[Parsed_showinfo_1] n:0 pts:10 pts_time:1.0 pos:100
[Parsed_showinfo_1] n:1 pts:12 pts_time:1.2 pos:200
[Parsed_showinfo_1] n:2 pts:15 pts_time:1.55 pos:300
[Parsed_showinfo_1] n:3 pts:21 pts_time:2.1e+00 pos:400
[Parsed_showinfo_1] n:4 pts:25 pts_time:2.5 pos:500
[Parsed_showinfo_1] n:5 pts:50 pts_time:5.0 pos:600
noise pts_time:NaN
noise pts_time:-1
noise pts_time:invalid`
	cuts := parseSceneTimes(stderr)
	shots := shotsFromCuts(5, cuts)
	want := []Shot{{Index: 1, StartS: 0, EndS: 1}, {Index: 2, StartS: 1, EndS: 2.1}, {Index: 3, StartS: 2.1, EndS: 5}}
	if !reflect.DeepEqual(shots, want) {
		t.Fatalf("shots = %+v, want %+v", shots, want)
	}
}

func TestShotsFallbackAndShortestAdjacentMerge(t *testing.T) {
	shots := shotsFromCuts(12, nil)
	if len(shots) != 4 || shots[0].EndS != 3 || shots[3].StartS != 9 || shots[3].EndS != 12 {
		t.Fatalf("均匀回退: %+v", shots)
	}
	var cuts []float64
	end := 0.0
	for i := 0; i < 61; i++ {
		d := 2.0
		if i == 1 || i == 2 {
			d = 1
		}
		end += d
		if i < 60 {
			cuts = append(cuts, end)
		}
	}
	shots = shotsFromCuts(end, cuts)
	if len(shots) != 60 || shots[1].StartS != 2 || shots[1].EndS != 4 {
		t.Fatalf("没有合并最短的相邻镜头: %+v", shots)
	}
	last := 0.0
	for i, shot := range shots {
		if shot.Index != i+1 || shot.StartS != last || shot.EndS <= shot.StartS {
			t.Fatalf("镜头覆盖不连续: %+v", shot)
		}
		last = shot.EndS
	}
	if last != end {
		t.Fatal("遗漏结尾")
	}
	if got := shotsFromCuts(600, nil); len(got) != 60 || got[59].EndS != 600 {
		t.Fatal("均匀回退没有遵守上限")
	}
}

func TestProbeReadsVideoMetadata(t *testing.T) {
	info, err := parseProbe([]byte(`{"streams":[{"width":1920,"height":1080,"avg_frame_rate":"30000/1001","r_frame_rate":"30/1","duration":"7.5"}],"format":{"duration":"8.2"}}`))
	if err != nil || info.Width != 1920 || info.Height != 1080 || info.DurationS != 8.2 || math.Abs(info.FPS-29.97002997) > 0.0001 {
		t.Fatalf("probe = %+v, %v", info, err)
	}
	if _, err := parseProbe([]byte(`{"streams":[],"format":{"duration":"NaN"}}`)); err == nil {
		t.Fatal("接受了损坏的媒体元数据")
	}
}

func TestKeyframeSamplingCapAndOpeningPriority(t *testing.T) {
	var shots []Shot
	for i := 0; i < 60; i++ {
		shots = append(shots, Shot{Index: i + 1, StartS: float64(i), EndS: float64(i + 1)})
	}
	frames := sampleFrames(shots)
	if len(frames) != 24 || frames[0].TimeS != 0.3 || frames[23].TimeS != 59.5 {
		t.Fatalf("帧上限、开头或结尾错误: %+v", frames)
	}
	for i := 0; i < 6; i++ {
		if frames[i+1].TimeS != float64(i)+0.5 {
			t.Fatal("没有优先保留前 6 秒的所有候选帧")
		}
	}
	for i, frame := range frames {
		if frame.Index != i+1 || frame.Shot != int(frame.TimeS)+1 {
			t.Fatalf("帧编号或镜头归属: %+v", frame)
		}
		if i > 0 && frame.TimeS <= frames[i-1].TimeS {
			t.Fatal("帧顺序错误")
		}
	}
	for i := range shots {
		shots[i].StartS /= 10
		shots[i].EndS /= 10
	}
	frames = sampleFrames(shots)
	if len(frames) != 24 {
		t.Fatal("开头过密时超过帧数上限")
	}
	hook := false
	for _, f := range frames {
		hook = hook || f.TimeS == 0.3
	}
	if !hook {
		t.Fatal("开头过密时丢失 0.3 秒钩子帧")
	}
	frames = sampleFrames([]Shot{{Index: 1, StartS: 0, EndS: 0.2}})
	if len(frames) != 1 || frames[0].TimeS >= 0.2 {
		t.Fatalf("超短视频采样越界: %+v", frames)
	}
}

func TestFFmpegShotsAndKeyframesIntegration(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("PATH 中没有 ffmpeg")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("PATH 中没有 ffprobe")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	dir := t.TempDir()
	video := filepath.Join(dir, "three-scenes.mp4")
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=10:duration=1,lutrgb=r=0:g=0:b=0",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=10:duration=1,lutrgb=r=255:g=255:b=255",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=10:duration=1",
		"-filter_complex", "[0:v][1:v][2:v]concat=n=3:v=1:a=0,format=yuv420p[v]", "-map", "[v]", "-c:v", "mpeg4", "-q:v", "2", video)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成 testsrc 视频: %v %s", err, out)
	}
	info, shots, err := Shots(ctx, ffmpeg, ffprobe, video)
	if err != nil || len(shots) != 3 || math.Abs(shots[0].EndS-1) > 0.11 || math.Abs(shots[1].EndS-2) > 0.11 {
		t.Fatalf("切镜识别: %+v %v", shots, err)
	}
	if info.Width != 320 || info.Height != 240 || math.Abs(info.DurationS-3) > 0.11 || info.FPS != 10 {
		t.Fatalf("视频元数据: %+v", info)
	}
	frames, err := Keyframes(ctx, ffmpeg, video, shots, filepath.Join(dir, "frames"))
	if err != nil || len(frames) != 4 {
		t.Fatalf("抽帧: %d %v", len(frames), err)
	}
	for _, frame := range frames {
		f, err := os.Open(filepath.Join(dir, "frames", frameFilename(frame.Index)))
		if err != nil {
			t.Fatal(err)
		}
		config, err := jpeg.DecodeConfig(f)
		f.Close()
		if err != nil || config.Width > 768 || config.Height > 768 || len(frame.JPEG) == 0 {
			t.Fatalf("无效 JPEG: %+v %v", config, err)
		}
	}
}
