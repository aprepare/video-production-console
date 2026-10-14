package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

)

func aiCoverTemplate(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "skill", "accounts", "测试账号", "cover-style")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	profile := `{"console_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","cover_prompt":"cover-style/prompt-template.txt"}`
	files := map[string]string{
		filepath.Join(root, "skill", "accounts", "测试账号", "profile.json"): profile,
		filepath.Join(dir, "prompt-template.txt"):                          "只换标题。第一行：{{TITLE1}}；第二行：TITLE2",
	}
	for path, text := range files {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 90, 160))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cover-reference.png"), encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitAICover(t *testing.T, handler http.Handler, url, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		res := coverRequest(handler, http.MethodGet, url, "")
		var job map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &job)
		if job["status"] == want {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("status never became %s: %s", want, res.Body.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRemixLabAICoverRunsInBackgroundAndPublishes(t *testing.T) {
	handler, svc, repo, exp, root := coverHTTPEnv(t)
	aiCoverTemplate(t, root)
	url := "/api/remix-lab/runs/" + exp.Runs[0].ID + "/ai-cover"
	if res := coverRequest(handler, http.MethodGet, url, ""); !strings.Contains(res.Body.String(), `"none"`) {
		t.Fatalf("before any job: %s", res.Body.String())
	}
	if res := coverRequest(handler, http.MethodPost, url, `{"line1":"守住家底","line2":"存款别乱动"}`); res.Code != http.StatusServiceUnavailable {
		t.Fatalf("no runner: %d %s", res.Code, res.Body.String())
	}

	release := make(chan struct{})
	var calls atomic.Int32
	var gotPrompt string
	svc.SetAICoverRunner(func(_ context.Context, jobsPath, outDir string) error {
		calls.Add(1)
		var jobs []struct {
			Name, Model, Image, Prompt string
			Params                     map[string]any
		}
		raw, _ := os.ReadFile(jobsPath)
		if err := json.Unmarshal(raw, &jobs); err != nil || len(jobs) != 1 {
			return errors.New("bad jobs")
		}
		gotPrompt = jobs[0].Prompt
		if jobs[0].Model != "qwen_unlimited" || !strings.HasSuffix(jobs[0].Image, "cover-reference.png") || jobs[0].Params["original_ratio"] != true {
			return errors.New("bad job fields")
		}
		<-release
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return err
		}
		var encoded bytes.Buffer
		_ = png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 72, 128)))
		return os.WriteFile(filepath.Join(outDir, jobs[0].Name+".png"), encoded.Bytes(), 0o600)
	})
	res := coverRequest(handler, http.MethodPost, url, `{"line1":"守住，家底！","line2":"存款别乱动"}`)
	if res.Code != http.StatusAccepted || !strings.Contains(res.Body.String(), `"running"`) {
		t.Fatalf("start: %d %s", res.Code, res.Body.String())
	}
	// 出图中再点一次不应再提交一单（平台会再扣币）。
	if again := coverRequest(handler, http.MethodPost, url, `{"line1":"别的标题","line2":"存款别乱动"}`); again.Code != http.StatusAccepted || !strings.Contains(again.Body.String(), "守住家底") {
		t.Fatalf("duplicate start: %d %s", again.Code, again.Body.String())
	}
	close(release)
	job := waitAICover(t, handler, url, "done")
	if calls.Load() != 1 || gotPrompt != "只换标题。第一行：守住家底；第二行：存款别乱动" {
		t.Fatalf("calls=%d prompt=%q", calls.Load(), gotPrompt)
	}
	view, _ := job["cover"].(map[string]any)
	path, _ := view["path"].(string)
	if filepath.Dir(path) != filepath.Join(root, "library") || view["width"] != float64(72) {
		t.Fatalf("cover view = %v", view)
	}
	run, err := repo.GetRun(t.Context(), exp.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(run.PackageJSON, `"cover_path"`) || !strings.Contains(run.PackageJSON, "守住家底") {
		t.Fatalf("package = %s", run.PackageJSON)
	}
	if got := coverRequest(handler, http.MethodGet, "/api/remix-lab/runs/"+exp.Runs[0].ID+"/cover", ""); got.Code != http.StatusOK {
		t.Fatalf("cover file: %d", got.Code)
	}
}

func TestRemixLabAICoverReportsPlatformFailures(t *testing.T) {
	cases := map[string]struct {
		task string
		want string
	}{
		"failed":    {`{"taskId":"1","failed":"NSFW 拦截"}`, "RunningHub 出图失败：NSFW 拦截"},
		"ambiguous": {`{"submitting":1791947439.7}`, "平台可能已经扣费"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			handler, svc, _, exp, root := coverHTTPEnv(t)
			aiCoverTemplate(t, root)
			svc.SetAICoverRunner(func(_ context.Context, _, outDir string) error {
				if err := os.MkdirAll(outDir, 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(outDir, "cover.task.json"), []byte(tc.task), 0o600)
			})
			url := "/api/remix-lab/runs/" + exp.Runs[0].ID + "/ai-cover"
			if res := coverRequest(handler, http.MethodPost, url, `{"line1":"守住家底","line2":"存款别乱动"}`); res.Code != http.StatusAccepted {
				t.Fatalf("start: %d %s", res.Code, res.Body.String())
			}
			job := waitAICover(t, handler, url, "failed")
			if msg, _ := job["error"].(string); !strings.Contains(msg, tc.want) {
				t.Fatalf("error = %q", msg)
			}
		})
	}
}

