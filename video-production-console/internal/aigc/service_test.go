package aigc

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"video-production-console/internal/domain"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type runtimeStub struct{ value consoleSettings.Runtime }

func (r runtimeStub) Runtime(context.Context) (consoleSettings.Runtime, error) { return r.value, nil }

func fixture(t *testing.T) (*Service, *sql.DB, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := New(db, root, runtimeStub{})
	s.Execute = func(context.Context, string, ...string) ([]byte, []byte, error) {
		t.Error("unexpected external command")
		return nil, nil, errors.New("blocked")
	}
	s.HTTPClient = &http.Client{Transport: rejectTransport{}}
	merchant, err := s.merchants.Create(context.Background(), store.Merchant{Name: "测试商户"})
	if err != nil {
		t.Fatal(err)
	}
	return s, db, merchant.ID
}

type rejectTransport struct{}

func (rejectTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("external requests forbidden in test")
}

func material(t *testing.T, s *Service, merchantID, kind, name string) store.MerchantMaterial {
	t.Helper()
	root := filepath.Join(s.dataRoot, "merchants", merchantID, "materials")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("fake media"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := s.merchants.CreateMaterial(context.Background(), store.MerchantMaterial{MerchantID: merchantID, Kind: kind, Filename: name, Path: path, LicenseScope: "全平台"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func waitJob(t *testing.T, s *Service, id string) store.AIGCJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := s.GetJob(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status == "completed" || j.Status == "failed" {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return store.AIGCJob{}
}

func TestExtractFramesFakeExecutableAndRollback(t *testing.T) {
	s, _, merchantID := fixture(t)
	source := material(t, s, merchantID, "video", "商品.mp4")
	dir := t.TempDir()
	code := `package main
import("os";"fmt")
func main(){ if len(os.Args)>1 && os.Args[1]=="-v" {fmt.Print("20");return};if err:=os.WriteFile(os.Args[len(os.Args)-1],[]byte{255,216,255,217},0600);err!=nil {os.Exit(1)} }
`
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "fake.exe")
	if out, err := exec.Command("go", "build", "-o", binary, path).CombinedOutput(); err != nil {
		t.Fatalf("fake build: %v %s", err, out)
	}
	s.runtime = runtimeStub{consoleSettings.Runtime{PublicSettings: domain.PublicSettings{FFmpegPath: binary, FFprobePath: binary}}}
	var timestamps []string
	s.Execute = func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		if args[0] != "-v" {
			timestamps = append(timestamps, args[2])
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 30*time.Second {
				t.Error("missing frame timeout")
			}
		}
		return executeCommand(ctx, name, args...)
	}
	job, outputs, err := s.ExtractFrames(context.Background(), merchantID, source.ID, 3)
	if err != nil || job.Status != "completed" || len(outputs) != 3 {
		t.Fatalf("%+v %+v %v", job, outputs, err)
	}
	if strings.Join(timestamps, ",") != "1.000,10.000,19.000" {
		t.Fatalf("timestamps: %v", timestamps)
	}
	for _, m := range outputs {
		if m.ParentMaterialID != source.ID || m.Kind != "image" || m.LicenseScope != "全平台" {
			t.Fatalf("%+v", m)
		}
	}
	calls := 0
	s.Execute = func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		if args[0] == "-v" {
			return nil, nil, errors.New("no duration")
		}
		calls++
		if calls == 2 {
			_ = os.WriteFile(args[len(args)-1], []byte("partial"), 0600)
			return nil, []byte("bad frame"), errors.New("exit 1")
		}
		return executeCommand(ctx, name, args...)
	}
	job, outputs, err = s.ExtractFrames(context.Background(), merchantID, source.ID, 3)
	if err == nil || job.Status != "failed" || len(outputs) != 0 {
		t.Fatalf("rollback: %+v %v", job, err)
	}
	all, _ := s.merchants.ListMaterials(context.Background(), merchantID)
	if len(all) != 4 {
		t.Fatalf("partial material remains: %d", len(all))
	}
	files, _ := os.ReadDir(filepath.Dir(source.Path))
	if len(files) != 4 {
		t.Fatalf("partial file remains: %d", len(files))
	}
}

func TestRestyleMultipartPartialCostAndRedaction(t *testing.T) {
	s, _, merchantID := fixture(t)
	a := material(t, s, merchantID, "image", "a.jpg")
	b := material(t, s, merchantID, "image", "b.jpg")
	order, err := s.merchants.CreateOrder(context.Background(), store.Order{MerchantID: merchantID, Title: "订单"})
	if err != nil {
		t.Fatal(err)
	}
	key := "sk-fake-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/edits" || r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("request mismatch")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			http.Error(w, "invalid", 400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		for k, v := range map[string]string{"model": "fake-model", "n": "1", "size": "1024x1536", "response_format": "b64_json"} {
			if r.FormValue(k) != v {
				t.Errorf("%s = %q", k, r.FormValue(k))
			}
		}
		if prompt := r.FormValue("prompt"); !strings.Contains(prompt, "保持画面中商品的形状、颜色、包装、文字和数量完全不变") || !strings.Contains(prompt, "保留商标") || !strings.Contains(prompt, "暖光") {
			t.Error(prompt)
		}
		f, header, err := r.FormFile("image")
		if err != nil {
			t.Error(err)
			return
		}
		f.Close()
		if header.Filename == "b.jpg" {
			http.Error(w, "provider says "+key, 500)
			return
		}
		fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString([]byte("fake png")))
	}))
	defer server.Close()
	s.HTTPClient = server.Client()
	s.runtime = runtimeStub{consoleSettings.Runtime{PublicSettings: domain.PublicSettings{ImageBaseURL: server.URL + "/v1", ImageModel: "fake-model"}, ImageAPIKey: key}}
	job, err := s.Restyle(context.Background(), merchantID, []string{a.ID, b.ID}, "暖光", "保留商标", JobOptions{OrderID: order.ID, UnitCostCents: 25})
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, s, job.ID)
	if job.Status != "completed" || len(job.OutputMaterialIDs) != 1 || strings.Contains(job.Error, key) || !strings.Contains(job.Error, b.ID) {
		t.Fatalf("%+v", job)
	}
	output, err := s.merchants.GetMaterial(context.Background(), job.OutputMaterialIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output.Path)
	if err != nil || string(raw) != "fake png" || output.ParentMaterialID != a.ID {
		t.Fatalf("%+v %s %v", output, raw, err)
	}
	costs, err := s.merchants.ListCosts(context.Background(), order.ID)
	if err != nil || len(costs) != 1 || costs[0].AmountCents != 25 || costs[0].Minutes != 0 || costs[0].Note != "AIGC 重绘 1 张" {
		t.Fatalf("%+v %v", costs, err)
	}
	job, err = s.Restyle(context.Background(), merchantID, []string{b.ID}, "暖光", "保留商标")
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, s, job.ID)
	if job.Status != "failed" || strings.Contains(job.Error, key) {
		t.Fatalf("%+v", job)
	}
}

func TestPrepareConfirmResumeAndLineage(t *testing.T) {
	s, db, merchantID := fixture(t)
	a := material(t, s, merchantID, "image", "a.png")
	job, err := s.PrepareImageToVideo(context.Background(), merchantID, []string{a.ID}, "镜头推进", 5, "9:16")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "awaiting_confirm" {
		t.Fatal(job.Status)
	}
	var p videoParams
	if err := json.Unmarshal([]byte(job.ParamsJSON), &p); err != nil {
		t.Fatal(err)
	}
	var shots []shot
	raw, err := os.ReadFile(p.ShotsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &shots); err != nil {
		t.Fatal(err)
	}
	if len(shots) != 1 || shots[0].Name != "shot-01" || shots[0].Audio != nil || shots[0].Duration != 5 || filepath.IsAbs(shots[0].Image) {
		t.Fatalf("%s", raw)
	}
	copied := filepath.Join(filepath.Dir(p.ShotsPath), shots[0].Image)
	if copied == a.Path {
		t.Fatal("source not copied")
	}
	if _, err := os.Stat(copied); err != nil {
		t.Fatal(err)
	}
	s.HypitScriptsDir = filepath.Join(t.TempDir(), "fake-scripts")
	var calls atomic.Int32
	s.Execute = func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		calls.Add(1)
		if name != "python" || len(args) != 5 || args[0] != filepath.Join(s.HypitScriptsDir, "runninghub_batch.py") || args[1] != "--shots" || args[2] != p.ShotsPath || args[3] != "--output-dir" || args[4] != p.OutputDir {
			t.Errorf("command: %s %v", name, args)
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 2*time.Hour {
			t.Error("missing timeout")
		}
		return nil, nil, os.WriteFile(filepath.Join(p.OutputDir, "shot-01.mp4"), []byte("fake mp4"), 0600)
	}
	if _, err = s.ConfirmImageToVideo(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmImageToVideo(context.Background(), job.ID); !errors.Is(err, store.ErrAIGCJobConflict) {
		t.Fatalf("duplicate confirm: %v", err)
	}
	job = waitJob(t, s, job.ID)
	if job.Status != "completed" || len(job.OutputMaterialIDs) != 1 || calls.Load() != 1 {
		t.Fatalf("%+v calls=%d", job, calls.Load())
	}
	output, err := s.merchants.GetMaterial(context.Background(), job.OutputMaterialIDs[0])
	if err != nil || output.Kind != "video" || output.ParentMaterialID != a.ID {
		t.Fatalf("%+v %v", output, err)
	}
	chain, err := s.Lineage(context.Background(), output.ID)
	if err != nil || len(chain) != 2 || chain[0].ID != a.ID || chain[1].Step != "image_to_video" {
		t.Fatalf("%+v %v", chain, err)
	}
	if _, err := db.Exec(`UPDATE merchant_materials SET parent_material_id=? WHERE id=?`, output.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	chain, err = s.Lineage(context.Background(), output.ID)
	if err != nil || len(chain) != 2 {
		t.Fatalf("cycle: %+v %v", chain, err)
	}
	if err = s.jobs.UpdateJobStatus(context.Background(), job.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, _ = s.GetJob(context.Background(), job.ID)
	if job.Status != "failed" || job.Error != "控制台重启时中断，请重新发起" || calls.Load() != 1 {
		t.Fatalf("%+v", job)
	}
}
