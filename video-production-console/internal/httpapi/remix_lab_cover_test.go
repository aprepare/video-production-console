package httpapi

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/remixlab"
	"video-production-console/internal/store"
)

func coverHTTPEnv(t *testing.T) (http.Handler, *remixlab.Service, *store.RemixLabRepository, remixlab.Experiment, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := store.NewRemixLabRepository(db)
	svc := remixlab.NewService(repo, remixLabStubRuntime{view: remixlab.RuntimeView{
		CoverSkillRoot: filepath.Join(root, "skill"), CoverOutputDir: filepath.Join(root, "library"),
	}}, remixLabHTTPFakeProtector{}, filepath.Join(root, "data"), nil, nil, nil)
	exp, err := svc.ImportDraft(t.Context(), remixlab.DraftInput{AccountID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", PackageInput: remixlab.PackageInput{
		ContinuousScript: "保住家里的钱。", ShortTitles: []string{"存款别乱动"}, CoverTitles: []string{"原来第一行", "原来第二行"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return NewRemixLabHandler(svc, nil), svc, repo, exp, root
}

func coverRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func TestRemixLabCoverHTTPValidation(t *testing.T) {
	handler, _, _, exp, _ := coverHTTPEnv(t)
	url := "/api/remix-lab/runs/" + exp.Runs[0].ID + "/cover"
	for _, body := range []string{`{`, `{}`, `{"line1":"！！！","line2":"家底"}`, `{"line1":"一二三四五六七八九十一二三","line2":"家底"}`, `{"line1":"存 款","line2":"家底"}`, `{"line1":"存款","line2":"💰"}`} {
		if res := coverRequest(handler, http.MethodPost, url, body); res.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status=%d body=%s", body, res.Code, res.Body.String())
		}
	}
	if res := coverRequest(handler, http.MethodGet, url, ""); res.Code != http.StatusNotFound {
		t.Fatalf("missing cover: %d %s", res.Code, res.Body.String())
	}
	valid := `{"line1":"守住家底","line2":"存款别乱动"}`
	if res := coverRequest(handler, http.MethodPost, url, valid); res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "这个账号还没有封面模板，请先在技能里为它生成一张参考封面") {
		t.Fatalf("no template: %d %s", res.Code, res.Body.String())
	}
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		if res := coverRequest(handler, method, "/api/remix-lab/runs/missing/cover", valid); res.Code != http.StatusNotFound {
			t.Fatalf("missing run: %d %s", res.Code, res.Body.String())
		}
	}
}

func TestRemixLabCoverHTTPRoundTrip(t *testing.T) {
	if _, err := os.Stat("C:/Windows/Fonts/msyhbd.ttc"); err != nil {
		t.Skip("封面字体测试需要 Windows 微软雅黑 Bold TTC")
	}
	for _, production := range []bool{false, true} {
		t.Run(map[bool]string{false: "experiment-account", true: "production-account"}[production], func(t *testing.T) {
			handler, svc, repo, exp, root := coverHTTPEnv(t)
			runID := exp.Runs[0].ID
			accountID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			if production {
				accountID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
				if err := repo.UpsertProduction(t.Context(), store.RemixLabProductionRecord{RunID: runID, ExperimentID: exp.ID, AccountID: accountID, Status: "completed", Step: "done", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
			}
			dir := filepath.Join(root, "skill", "accounts", "测试账号", "cover-style")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			profile, _ := json.Marshal(map[string]string{"console_id": accountID})
			if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "profile.json"), profile, 0o600); err != nil {
				t.Fatal(err)
			}
			img := image.NewNRGBA(image.Rect(0, 0, 640, 960))
			draw.Draw(img, img.Bounds(), image.NewUniform(color.NRGBA{10, 10, 10, 255}), image.Point{}, draw.Src)
			var encoded bytes.Buffer
			if err := png.Encode(&encoded, img); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "cover-reference.png"), encoded.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			url := "/api/remix-lab/runs/" + runID + "/cover"
			res := coverRequest(handler, http.MethodPost, url, `{"line1":"  守住，家底！ ","line2":" 存款别乱动。 "}`)
			if res.Code != http.StatusOK {
				t.Fatalf("render: %d %s", res.Code, res.Body.String())
			}
			var result struct {
				Path, URL, Account string
				Width, Height      int
			}
			if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !filepath.IsAbs(result.Path) || filepath.Dir(result.Path) != filepath.Join(root, "library") || result.URL != url || result.Account != "测试账号" || result.Width != 640 || result.Height != 960 {
				t.Fatalf("result=%+v", result)
			}
			library, err := os.ReadFile(result.Path)
			if err != nil {
				t.Fatal(err)
			}
			got := coverRequest(handler, http.MethodGet, url, "")
			if got.Code != http.StatusOK || got.Header().Get("Content-Type") != "image/png" || !bytes.Equal(got.Body.Bytes(), library) {
				t.Fatalf("GET cover mismatch: status=%d", got.Code)
			}
			run, err := repo.GetRun(t.Context(), runID)
			if err != nil {
				t.Fatal(err)
			}
			disk, err := os.ReadFile(filepath.Join(run.OutputDir, "publishing_package.json"))
			if err != nil {
				t.Fatal(err)
			}
			for name, raw := range map[string][]byte{"database": []byte(run.PackageJSON), "disk": disk, "GET": coverRequest(handler, http.MethodGet, "/api/remix-lab/runs/"+runID+"/package", "").Body.Bytes()} {
				var pkg remixlab.PackageInput
				if err := json.Unmarshal(raw, &pkg); err != nil || pkg.CoverPath != result.Path || !reflect.DeepEqual(pkg.CoverTitles, []string{"守住家底", "存款别乱动"}) {
					t.Fatalf("%s package=%s err=%v", name, raw, err)
				}
			}
			if err := svc.UpdateRunPackage(t.Context(), runID, remixlab.PackageInput{ContinuousScript: "改过的正文。", ShortTitles: []string{"新短标题"}, CoverTitles: []string{"守住家底", "存款别乱动"}}); err != nil {
				t.Fatal(err)
			}
			run, err = repo.GetRun(t.Context(), runID)
			if err != nil {
				t.Fatal(err)
			}
			var edited remixlab.PackageInput
			if err := json.Unmarshal([]byte(run.PackageJSON), &edited); err != nil || edited.CoverPath != result.Path || edited.ContinuousScript != "改过的正文。" {
				t.Fatalf("edit lost cover: %s err=%v", run.PackageJSON, err)
			}
		})
	}
}
