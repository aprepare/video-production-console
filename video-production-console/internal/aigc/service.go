// Package aigc implements traceable merchant media generation.
package aigc

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

var (
	ErrInvalidInput          = errors.New("输入参数不正确")
	ErrImageAPINotConfigured = errors.New("生图接口尚未配置完整")
	ErrUnsafePath            = errors.New("素材路径不在允许的存储目录内")
)

// RuntimeProvider reads current settings without persisting secrets in jobs.
type RuntimeProvider interface {
	Runtime(context.Context) (consoleSettings.Runtime, error)
}

// HTTPClient is injectable so tests never contact a real image service.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// CommandExecutor runs one command with a deadline and separate diagnostic streams.
type CommandExecutor func(context.Context, string, ...string) ([]byte, []byte, error)

// JobOptions associates generated material with an optional merchant order.
type JobOptions struct {
	OrderID       string `json:"order_id,omitempty"`
	UnitCostCents int64  `json:"unit_cost_cents"`
}

// Service manages jobs; configure injectable dependencies before serving requests.
type Service struct {
	db              *sql.DB
	dataRoot        string
	runtime         RuntimeProvider
	jobs            *store.AIGCJobRepository
	merchants       *store.MerchantRepository
	HTTPClient      HTTPClient
	Execute         CommandExecutor
	HypitScriptsDir string
}

// New creates a service using live settings and production adapters.
func New(db *sql.DB, dataRoot string, runtime RuntimeProvider) *Service {
	return &Service{db: db, dataRoot: dataRoot, runtime: runtime, jobs: store.NewAIGCJobRepository(db), merchants: store.NewMerchantRepository(db), HTTPClient: &http.Client{Timeout: 5 * time.Minute}, Execute: executeCommand, HypitScriptsDir: "C:/Users/prepare/.agents/skills/hypit/scripts"}
}

func executeCommand(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout := &tailWriter{limit: 64 << 10}
	stderr := &tailWriter{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	return stdout.data, stderr.data, err
}

type tailWriter struct {
	data  []byte
	limit int
}

func (w *tailWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.data = append(w.data, p...)
	if len(w.data) > w.limit {
		w.data = w.data[len(w.data)-w.limit:]
	}
	return n, nil
}

func (s *Service) settings(ctx context.Context) (consoleSettings.Runtime, error) {
	if s.runtime == nil {
		return consoleSettings.Runtime{}, nil
	}
	return s.runtime.Runtime(ctx)
}

func validID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

func (s *Service) inputs(ctx context.Context, merchantID string, ids []string, kind string, max int) ([]store.MerchantMaterial, error) {
	if !validID(merchantID) || len(ids) == 0 || (max > 0 && len(ids) > max) {
		return nil, ErrInvalidInput
	}
	if _, err := s.merchants.Get(ctx, merchantID); err != nil {
		return nil, err
	}
	result := make([]store.MerchantMaterial, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if !validID(id) || seen[id] {
			return nil, ErrInvalidInput
		}
		seen[id] = true
		m, err := s.merchants.GetMaterial(ctx, id)
		if err != nil {
			return nil, err
		}
		if m.MerchantID != merchantID || m.Kind != kind {
			return nil, ErrInvalidInput
		}
		path, err := s.safeMaterialPath(merchantID, m.Path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, ErrUnsafePath
		}
		m.Path = path
		result = append(result, m)
	}
	return result, nil
}

func (s *Service) options(ctx context.Context, merchantID string, options []JobOptions) (JobOptions, error) {
	var opt JobOptions
	if len(options) > 0 {
		opt = options[0]
	}
	if opt.UnitCostCents < 0 {
		return opt, ErrInvalidInput
	}
	if opt.OrderID != "" {
		if !validID(opt.OrderID) {
			return opt, ErrInvalidInput
		}
		order, err := s.merchants.GetOrder(ctx, opt.OrderID)
		if err != nil {
			return opt, err
		}
		if order.MerchantID != merchantID {
			return opt, ErrInvalidInput
		}
	}
	return opt, nil
}

func (s *Service) createJob(ctx context.Context, id, merchantID, kind, status string, ids []string, params any, orderID string) (store.AIGCJob, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return store.AIGCJob{}, err
	}
	return s.jobs.CreateJob(ctx, store.AIGCJob{ID: id, MerchantID: merchantID, Kind: kind, Status: status, InputMaterialIDs: ids, ParamsJSON: string(raw), OrderID: orderID})
}

// GetJob returns a persisted job snapshot.
func (s *Service) GetJob(ctx context.Context, id string) (store.AIGCJob, error) {
	return s.jobs.GetJob(ctx, id)
}

// ListJobs returns the merchant's jobs, newest first.
func (s *Service) ListJobs(ctx context.Context, id string) ([]store.AIGCJob, error) {
	return s.jobs.ListJobs(ctx, id)
}

// Resume marks interrupted work failed without submitting any external task.
func (s *Service) Resume(ctx context.Context) error {
	jobs, err := s.jobs.Running(ctx)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := s.jobs.UpdateJobStatus(ctx, job.ID, "failed", "控制台重启时中断，请重新发起"); err != nil {
			return err
		}
	}
	return nil
}

// LineageEntry describes one material in source-to-current order.
type LineageEntry struct {
	ID        string    `json:"id"`
	Filename  string    `json:"filename"`
	Kind      string    `json:"kind"`
	Step      string    `json:"step"`
	CreatedAt time.Time `json:"created_at"`
}

// Lineage follows at most ten distinct parent materials and rejects cross-merchant links.
func (s *Service) Lineage(ctx context.Context, id string) ([]LineageEntry, error) {
	result := []LineageEntry{}
	seen := map[string]bool{}
	merchantID := ""
	for id != "" && len(result) < 10 && !seen[id] {
		seen[id] = true
		m, err := s.merchants.GetMaterial(ctx, id)
		if err != nil {
			return nil, err
		}
		if merchantID != "" && merchantID != m.MerchantID {
			return nil, ErrInvalidInput
		}
		merchantID = m.MerchantID
		var provenance struct {
			Step string `json:"step"`
		}
		_ = json.Unmarshal([]byte(m.ProvenanceJSON), &provenance)
		result = append(result, LineageEntry{m.ID, m.Filename, m.Kind, provenance.Step, m.CreatedAt})
		id = m.ParentMaterialID
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}

func safePath(root, path string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", ErrUnsafePath
	}
	current := root
	for _, part := range append([]string{""}, strings.Split(rel, string(filepath.Separator))...) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", ErrUnsafePath
		}
	}
	return target, nil
}

var tokenPattern = regexp.MustCompile(`[A-Za-z0-9]{32,}`)

func redact(message string, keys ...string) string {
	for _, key := range keys {
		if key != "" {
			message = strings.ReplaceAll(message, key, "[redacted]")
		}
	}
	return tokenPattern.ReplaceAllString(message, "[redacted]")
}

func sourceStem(m store.MerchantMaterial) string {
	name := filepath.Base(strings.ReplaceAll(m.Filename, `\`, "/"))
	return strings.TrimSuffix(name, filepath.Ext(name))
}

func (s *Service) materialPath(merchantID, id, filename string) (string, error) {
	root := filepath.Join(s.dataRoot, "merchants", merchantID, "materials")
	path, err := s.safeMaterialPath(merchantID, filepath.Join(root, id+"_"+filename))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Service) safeMaterialPath(merchantID, path string) (string, error) {
	root := filepath.Join(s.dataRoot, "merchants")
	if _, err := safePath(root, path); err != nil {
		return "", err
	}
	return safePath(filepath.Join(root, merchantID, "materials"), path)
}

func (s *Service) recordMaterial(ctx context.Context, source store.MerchantMaterial, id, filename, path, kind, step string, params any, model string) (store.MerchantMaterial, error) {
	f, err := os.Open(path)
	if err != nil {
		return store.MerchantMaterial{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return store.MerchantMaterial{}, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return store.MerchantMaterial{}, errors.New("生成文件为空或格式不正确")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return store.MerchantMaterial{}, err
	}
	return s.merchants.CreateDerivedMaterial(ctx, store.MerchantMaterial{ID: id, MerchantID: source.MerchantID, Filename: filename, Path: path, Kind: kind, Size: info.Size(), SHA256: fmt.Sprintf("%x", hash.Sum(nil))}, source.ID, step, params, model)
}

func (s *Service) finish(ctx context.Context, jobID string, ids []string, failures []string) error {
	if err := s.jobs.SetJobOutputs(ctx, jobID, ids); err != nil {
		return err
	}
	status := "completed"
	if len(ids) == 0 {
		status = "failed"
	}
	return s.jobs.UpdateJobStatus(ctx, jobID, status, strings.Join(failures, "；"))
}
